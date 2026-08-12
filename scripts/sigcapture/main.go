// Signature-capture harness for the attention system (ini-2x8 family).
//
// Run: go run ./scripts/sigcapture/ -agent claude
//      go run ./scripts/sigcapture/ -agent codex -prompt "Run the shell command: date +%s"
//
// WHAT THIS IS FOR
//
// Attention detection is not something initech computes; it is something the
// agent TELLS us, or fails to. That makes every detection rule a claim about
// another program's observable behaviour at a specific version -- and the one
// rule this feature is built on is that such claims get MEASURED before they
// get coded. Patterns invented from memory are the recorded failure mode; the
// interaction nobody prototypes is the one that ships broken.
//
// This tool takes the measurement. It spawns a REAL agent under a PTY, drives
// it into a REAL blocking dialog, and records every face of that moment at
// once:
//
//   - the raw PTY bytes, so escape-level signals are visible
//   - OSC 777 / OSC 9 / window-title sequences, with byte offsets and timings
//   - the RENDERED screen, through the same x/vt emulator the TUI uses
//   - Notification hook payloads, when -agent claude installs the hook
//
// It is also the reproduction step for ini-2x8.7, which is blocked on a codex
// per-command approval specimen that could not be provoked on 2026-08-12.
//
// WHY IT RENDERS THROUGH x/vt RATHER THAN GREPPING THE BYTE STREAM
//
// Codex positions each WORD separately, so its dialog phrases are NOT
// contiguous in the raw PTY stream: "Do you trust", "trust the contents",
// "Press enter to continue" and "Yes, continue" all fail a raw-byte substring
// test while all of them pass against rendered rows. Two capture attempts were
// lost to that before it was spotted. Reading through the emulator means what
// this tool prints is what tier-2 detection actually sees -- and a transcript
// taken from it can be pasted into a fixture without silently encoding a
// layout the detector will never encounter.
//
// SAFETY PROPERTY -- READ THIS BEFORE ADDING A FLAG
//
// A capture must never make itself convenient at the cost of the operator's
// real configuration or credentials. Specifically:
//
//   - codex directory trust is granted with an IN-MEMORY `-c` override
//     (`-c projects."<dir>".trust_level="trusted"`). Never write
//     ~/.codex/config.toml, and never copy auth into a scratch CODEX_HOME.
//   - the codex hook-review gate is answered "Continue without trusting
//     (hooks won't run)" -- option 3, least privilege. NOT "Trust all", which
//     would grant standing trust to hooks in the operator's own environment
//     purely to get past a prompt.
//   - Claude settings are written ONLY into the throwaway project directory
//     this tool creates. ~/.claude is never touched.
//   - a private CLAUDE_CONFIG_DIR was tried and rejected: it drops the child
//     into unauthenticated first-run onboarding, which never reaches a dialog.
//     Inheriting the real config keeps auth done; the forced approval comes
//     from project-local settings instead.
//
// Everything the tool creates lives under -out (default ./.tmp/sigcapture).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

const (
	emuCols = 120
	emuRows = 40
)

func main() {
	agent := flag.String("agent", "claude", "agent to capture: claude | codex")
	prompt := flag.String("prompt", "", "prompt to send (default: one that provokes an approval dialog)")
	hold := flag.Duration("hold", 90*time.Second, "how long to hold the session open")
	outDir := flag.String("out", ".tmp/sigcapture", "directory for the capture artifacts")
	flag.Parse()

	if *prompt == "" {
		*prompt = "Run the shell command: date +%s"
	}
	if err := run(*agent, *prompt, *hold, *outDir); err != nil {
		fmt.Fprintln(os.Stderr, "sigcapture:", err)
		os.Exit(1)
	}
}

func run(agent, prompt string, hold time.Duration, outDir string) error {
	stamp := time.Now().Format("20060102-150405")
	root, err := filepath.Abs(filepath.Join(outDir, agent+"-"+stamp))
	if err != nil {
		return err
	}
	proj := filepath.Join(root, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		return err
	}

	var cmd *exec.Cmd
	hookLog := filepath.Join(root, "hook-events.jsonl")

	switch agent {
	case "claude":
		if err := writeClaudeSettings(proj, hookLog); err != nil {
			return err
		}
		cmd = exec.Command("claude", prompt)
		cmd.Env = append(os.Environ(),
			"TERM=xterm-256color",
			// An inherited child-session marker disables transcript writing and
			// changes startup behaviour. Clearing it is what makes the captured
			// session a normal one.
			"CLAUDE_CODE_CHILD_SESSION=",
			"CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1",
		)
	case "codex":
		cmd = exec.Command("codex",
			"-C", proj,
			"-a", "untrusted", // force approval for anything outside the trusted set
			"-s", "read-only",
			// In-memory trust grant. See the safety note in the package comment:
			// this exists so the capture never writes the operator's config.
			"-c", fmt.Sprintf("projects.%q.trust_level=%q", proj, "trusted"),
			prompt,
		)
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	default:
		return fmt.Errorf("unknown -agent %q (want claude or codex)", agent)
	}
	cmd.Dir = proj

	fmt.Printf("sigcapture: %s\n", strings.Join(cmd.Args, " "))
	fmt.Printf("artifacts:  %s\n\n", root)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: emuRows, Cols: emuCols})
	if err != nil {
		return fmt.Errorf("start %s under a pty: %w", agent, err)
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// Render through the SAME emulator the TUI uses, so the transcript is what
	// tier-2 detection would actually see.
	emu := vt.NewSafeEmulator(emuCols, emuRows)
	go drain(emu)

	raw, marks := pump(ptmx, emu, hold)

	rawPath := filepath.Join(root, "pty.bin")
	if err := os.WriteFile(rawPath, raw, 0o644); err != nil {
		return err
	}
	report(raw, marks, emu, rawPath, hookLog)
	return nil
}

// pump reads the PTY until hold expires, feeding the emulator, answering
// startup gates, and timestamping notable sequences.
func pump(ptmx *os.File, emu *vt.SafeEmulator, hold time.Duration) ([]byte, []mark) {
	var raw []byte
	var marks []mark
	var gates gateState

	osc777 := 0
	start := time.Now()
	buf := make([]byte, 32*1024)

	for time.Since(start) < hold {
		_ = ptmx.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, err := ptmx.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			raw = append(raw, chunk...)
			_, _ = emu.Write(chunk)

			if c := bytes.Count(raw, []byte("\x1b]777;")); c > osc777 {
				osc777 = c
				marks = append(marks, mark{time.Since(start), fmt.Sprintf("OSC 777 #%d", c), len(raw)})
			}
			if m := gates.answer(ptmx, raw); m != "" {
				marks = append(marks, mark{time.Since(start), m, len(raw)})
			}
		}
		if err != nil && n == 0 {
			continue // read deadline; keep going until hold expires
		}
	}
	return raw, marks
}

// gateState answers the startup gates that stand between a fresh session and
// the dialog worth capturing.
type gateState struct {
	trusted bool
	hooks   bool
}

// answer sends a keystroke for whichever gate is currently up, and reports what
// it did. Triggers are SINGLE WORDS on purpose: codex positions each word
// separately, so a multi-word trigger never matches contiguously in the raw
// stream -- the trap that cost two capture attempts.
func (g *gateState) answer(ptmx *os.File, raw []byte) string {
	low := bytes.ToLower(raw)
	switch {
	case !g.trusted && bytes.Contains(low, []byte("trust")):
		g.trusted = true
		time.Sleep(500 * time.Millisecond)
		_, _ = ptmx.Write([]byte("\r")) // option 1 is preselected on both agents
		return "trust gate -> Enter"
	case g.trusted && !g.hooks && bytes.Contains(low, []byte("hooks")):
		g.hooks = true
		time.Sleep(500 * time.Millisecond)
		// Option 3: "Continue without trusting (hooks won't run)". Least
		// privilege -- see the safety note in the package comment.
		_, _ = ptmx.Write([]byte("3"))
		time.Sleep(300 * time.Millisecond)
		_, _ = ptmx.Write([]byte("\r"))
		return "hooks gate -> option 3 (no trust granted)"
	}
	return ""
}

type mark struct {
	at     time.Duration
	what   string
	offset int
}

var (
	osc777Re = regexp.MustCompile(`\x1b\]777;([^\x07]*)\x07`)
	osc9Re   = regexp.MustCompile(`\x1b\]9;([^\x07]*)\x07`)
	titleRe  = regexp.MustCompile(`\x1b\]0;([^\x07]*)\x07`)
)

func report(raw []byte, marks []mark, emu *vt.SafeEmulator, rawPath, hookLog string) {
	fmt.Printf("captured %d bytes -> %s\n", len(raw), rawPath)

	fmt.Println("\n=== timeline ===")
	if len(marks) == 0 {
		fmt.Println("  (nothing notable)")
	}
	for _, m := range marks {
		fmt.Printf("  [%6.2fs] %-36s byte %d\n", m.at.Seconds(), m.what, m.offset)
	}

	fmt.Println("\n=== OSC 777 (notify) ===")
	dumpMatches(osc777Re, raw, "  none -- this agent declares nothing; it is screen-only, therefore list-only")
	fmt.Println("\n=== OSC 9 (progress) ===")
	dumpMatches(osc9Re, raw, "  none")
	fmt.Println("\n=== window titles ===")
	dumpMatches(titleRe, raw, "  none")

	fmt.Println("\n=== rendered screen (what tier-2 detection sees) ===")
	for _, line := range renderedRows(emu) {
		fmt.Println("  " + line)
	}

	if data, err := os.ReadFile(hookLog); err == nil && len(bytes.TrimSpace(data)) > 0 {
		fmt.Println("\n=== Notification hook payloads ===")
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var pretty bytes.Buffer
			if json.Indent(&pretty, []byte(line), "  ", "  ") == nil {
				fmt.Println("  " + pretty.String())
			} else {
				fmt.Println("  " + line)
			}
		}
	}

	fmt.Println("\nRecord what you captured on the bead VERBATIM, with the agent version named.")
	fmt.Println("A signature is evidence only while it is bounded to what it is: a real capture at a named version.")
}

func dumpMatches(re *regexp.Regexp, raw []byte, ifNone string) {
	ms := re.FindAllSubmatchIndex(raw, -1)
	if len(ms) == 0 {
		fmt.Println(ifNone)
		return
	}
	for _, m := range ms {
		fmt.Printf("  byte %6d: %q\n", m[0], string(raw[m[2]:m[3]]))
	}
}

// renderedRows reads the emulator's rows, trailing blanks trimmed.
func renderedRows(emu *vt.SafeEmulator) []string {
	var rows []string
	for y := 0; y < emuRows; y++ {
		rows = append(rows, strings.TrimRight(emu.RowText(y, emuCols), " "))
	}
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

// drain consumes the emulator's response stream so its writer never blocks.
func drain(emu *vt.SafeEmulator) {
	buf := make([]byte, 4096)
	for {
		if _, err := emu.Read(buf); err != nil {
			return
		}
	}
}

// writeClaudeSettings puts a project-local settings.json in the THROWAWAY
// directory only. Two jobs:
//
//   - permissions.ask forces an approval prompt for every Bash call. Without
//     it the operator's own allowlist auto-approves and the dialog we came to
//     capture never renders.
//   - a Notification hook appends its stdin to hookLog, which is how the hook
//     tier's real payload was measured (notification_type distinguishes
//     permission_prompt from idle_prompt).
//
// A pre-existing SessionStart hook is included deliberately: any code that
// later merges into this file has a foreign entry to preserve, so the fixture
// doubles as a base-and-new merge specimen.
func writeClaudeSettings(proj, hookLog string) error {
	dir := filepath.Join(proj, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	settings := map[string]any{
		"permissions": map[string]any{"ask": []string{"Bash"}, "allow": []string{}, "deny": []string{}},
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{
				"matcher": "",
				"hooks":   []any{map[string]any{"type": "command", "command": "true # preexisting-sentinel"}},
			}},
			"Notification": []any{map[string]any{
				"matcher": "",
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": fmt.Sprintf("cat >> %q; echo >> %q", hookLog, hookLog),
				}},
			}},
		},
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "settings.json"), data, 0o644)
}
