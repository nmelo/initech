//go:build !windows

package tui

// d7a7_measure_test.go — MEASUREMENT for ini-d7a7: what does Claude 2.1.233
// actually emit on a blocking dialog?
//
// The standing probe asks a yes/no question ("did OSC 777 with the measured
// dialog text arrive"). That is the right shape for a tripwire and the wrong
// shape for a diagnosis: it cannot distinguish EMISSION GONE from SEQUENCE
// CHANGED from GATING CHANGED, and those have three different fixes.
//
// So this captures EVERY OSC sequence the child emits, not just 777, with the
// screen state at the time, and reports them. It asserts nothing about which
// ones should be there -- an assertion here would encode the belief I am trying
// to test. It is an instrument, and per the bead it is POLLED over the whole
// session rather than sampled at one instant, because a single read of a
// notification channel measures the instant, not the channel (ini-zjhg, where
// exactly that error manufactured a phantom tier-1 gap).
//
// Run: INITECH_D7A7=1 go test ./internal/tui/ -run D7A7 -v -count=1 -timeout 400s

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// anyOSCRe matches ANY OSC sequence, BEL- or ST-terminated. Deliberately wider
// than the product's matcher: the question is what the emitter does now, and a
// matcher built from what it used to do can only ever confirm the old answer.
var anyOSCRe = regexp.MustCompile(`\x1b\]([0-9]+);([^\x07\x1b]*)(?:\x07|\x1b\\)`)

// d7a7Session drives one real Claude to one dialog and returns everything it
// emitted plus the final screen.
func d7a7Session(t *testing.T, label, prompt string) (raw string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/.claude", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Force the approval prompt with the spawn form MEASURED to work today
	// (ini-zjhg, twice): an explicit --settings ask rule plus
	// --permission-mode default. A project-local .claude/settings.json and an
	// argv prompt did NOT reach a dialog on 2.1.233 -- the first run of this
	// measurement captured zero dialogs and therefore said nothing about the
	// emitter, which is the ini-543b rig fault and is why the dialog-reached
	// check below exists.
	settings := dir + "/d7a7-settings.json"
	if err := os.WriteFile(settings,
		[]byte(`{"permissions":{"ask":["Bash"],"defaultMode":"default"}}`), 0o644); err != nil {
		t.Fatalf("settings: %v", err)
	}

	cmd := exec.Command("claude", "--permission-mode", "default", "--settings", settings)
	cmd.Dir = dir
	// agentBaseEnv, not os.Environ: the child's terminal identity must be built
	// exactly the way real panes build it (ini-g0h). The OSC emission was
	// measured to depend on TERM_PROGRAM, so an environment production never
	// produces would measure a different product.
	cmd.Env = append(agentBaseEnv(),
		"CLAUDE_CODE_CHILD_SESSION=",
		"CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1",
	)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("start claude: %v", err)
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	var seen strings.Builder
	trusted := false
	trustRe := regexp.MustCompile(`(?i)trust`)

	chunks := make(chan []byte, 256)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				chunks <- cp
			}
			if err != nil {
				close(chunks)
				return
			}
		}
	}()

	deadline := time.After(120 * time.Second)
collect:
	for {
		select {
		case c, ok := <-chunks:
			if !ok {
				break collect
			}
			seen.Write(c)
			if !trusted && trustRe.MatchString(seen.String()) {
				trusted = true
				_, _ = ptmx.Write([]byte("\r"))
				// Give the composer a moment, then TYPE the request. The argv
				// form did not reach a dialog on this build; the interactive
				// path is the one measured to work.
				go func() {
					time.Sleep(6 * time.Second)
					_, _ = ptmx.Write([]byte(prompt + "\r"))
				}()
			}
		case <-deadline:
			break collect
		}
	}
	return seen.String()
}

// ansiRe strips escape sequences so screen TEXT can be searched.
//
// Without this, a raw substring search for dialog text false-negatives: Claude
// styles the prompt, so "Do you want to proceed" is not contiguous in the byte
// stream. My first run reported DIALOG REACHED=false while the OSC in the very
// same capture said the dialog had opened -- the check disagreed with the
// evidence beside it, and only the contradiction made it visible.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][A-Z0-9]`)

func d7a7ScreenText(raw string) string { return ansiRe.ReplaceAllString(raw, "") }

// d7a7ProbeSpawn reproduces the STANDING PROBE's spawn exactly: argv prompt,
// project-local .claude/settings.json, no --permission-mode and no --settings.
func d7a7ProbeSpawn(t *testing.T, prompt string) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(dir+"/.claude", 0o755)
	os.WriteFile(dir+"/.claude/settings.json",
		[]byte(`{"permissions":{"ask":["Bash"],"allow":[],"deny":[]}}`), 0o644)

	cmd := exec.Command("claude", prompt)
	cmd.Dir = dir
	cmd.Env = append(agentBaseEnv(), "CLAUDE_CODE_CHILD_SESSION=",
		"CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("start claude (probe form): %v", err)
	}
	defer func() { ptmx.Close(); cmd.Process.Kill(); cmd.Process.Wait() }()

	var seen strings.Builder
	trusted := false
	trustRe := regexp.MustCompile(`(?i)trust`)
	chunks := make(chan []byte, 256)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				chunks <- cp
			}
			if err != nil {
				close(chunks)
				return
			}
		}
	}()
	deadline := time.After(90 * time.Second)
	for {
		select {
		case c, ok := <-chunks:
			if !ok {
				return seen.String()
			}
			seen.Write(c)
			if !trusted && trustRe.MatchString(seen.String()) {
				trusted = true
				_, _ = ptmx.Write([]byte("\r"))
			}
		case <-deadline:
			return seen.String()
		}
	}
}

// d7a7ReportOSC prints every distinct OSC sequence with its count.
func d7a7ReportOSC(t *testing.T, label, raw string) int {
	t.Helper()
	counts := map[string]int{}
	for _, m := range anyOSCRe.FindAllStringSubmatch(raw, -1) {
		counts[m[1]+";"+m[2]] = counts[m[1]+";"+m[2]] + 1
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	t.Logf("=== %s: %d bytes captured, %d distinct OSC sequences ===", label, len(raw), len(keys))
	for _, k := range keys {
		t.Logf("    OSC %-60q x%d", k, counts[k])
	}
	if len(keys) == 0 {
		t.Logf("    (NO OSC SEQUENCES OF ANY KIND)")
	}
	return len(keys)
}

func TestD7A7_WhatDoesThisClaudeEmit(t *testing.T) {
	if os.Getenv("INITECH_D7A7") != "1" {
		t.Skip("set INITECH_D7A7=1 to measure the live Claude OSC emitter")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}
	v, _ := exec.Command("claude", "--version").Output()
	t.Logf("MEASURING claude version: %s", strings.TrimSpace(string(v)))

	// ARM A: the spawn form measured to work (explicit --settings ask rule,
	// --permission-mode default, request typed interactively).
	raw := d7a7Session(t, "permission-prompt", "Run the shell command: date +%s")
	n := d7a7ReportOSC(t, "ARM A (measured-good spawn)", raw)

	// ARM B: THE STANDING PROBE'S OWN SPAWN FORM -- argv prompt plus a
	// project-local .claude/settings.json. If A reaches a dialog and B does
	// not, the probe's RED is its fixture, not the emitter, and the product is
	// not degraded at all. That is the whole question this bead turns on, so it
	// is measured side by side rather than inferred from one arm.
	rawB := d7a7ProbeSpawn(t, "Run the shell command: date +%s")
	d7a7ReportOSC(t, "ARM B (standing probe spawn)", rawB)
	notifyB := regexp.MustCompile(`\x1b\]777;notify;[^\x07]*\x07`).FindAllString(rawB, -1)
	textB := d7a7ScreenText(rawB)
	t.Logf("ARM B: notify frames=%d  dialog reached=%v", len(notifyB),
		strings.Contains(textB, "Do you want to proceed"))

	// The two facts the fix shape depends on, reported separately so neither
	// hides the other: is the CHANNEL silent, or is the DIALOG TEXT different?
	notify := regexp.MustCompile(`\x1b\]777;notify;[^\x07]*\x07`).FindAllString(raw, -1)
	t.Logf("OSC 777 notify frames: %d", len(notify))
	for _, s := range notify {
		t.Logf("    %q", s)
	}

	// Did the dialog actually open? A session that never reached a dialog
	// cannot speak to what a dialog emits -- the ini-543b rig fault.
	text := d7a7ScreenText(raw)
	dialogOnScreen := strings.Contains(text, "Do you want to proceed") ||
		strings.Contains(strings.ToLower(text), "yes, and don")
	t.Logf("DIALOG REACHED (screen text present): %v", dialogOnScreen)
	// Dump the dialog as 2.1.233 actually renders it, and check it against the
	// SCREEN-SCAN tier's patterns. Tier 2 is the mitigation the bead says to
	// verify rather than assume, and it is pattern-based -- if the wording
	// moved, tier 2 is blind too and the degradation matrix has a second row.
	tail := text
	if i := strings.LastIndex(tail, "proceed"); i > 400 {
		tail = tail[i-400:]
	} else if len(tail) > 1200 {
		tail = tail[len(tail)-1200:]
	}
	t.Logf("SCREEN TEXT TAIL (ANSI stripped):\n%s", tail)
	t.Logf("TIER-2 screen scan recognises this screen as a modal: %v", isModalPrompt(text))

	if !dialogOnScreen {
		t.Logf("WARNING: no dialog text in the capture, so a silent OSC channel here is " +
			"UNINTERPRETABLE -- it may mean the run never got a dialog rather than that " +
			"dialogs no longer announce themselves. Rig fault, not a product finding.")
	}
	fmt.Fprintf(os.Stderr, "d7a7 measurement done: %d distinct OSC, %d notify frames\n", n, len(notify))
}
