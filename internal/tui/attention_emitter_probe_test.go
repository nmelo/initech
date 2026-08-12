//go:build !windows

package tui

// attention_emitter_probe_test.go is the emission guardrail Nelson's decision
// asked for, and it is the deliverable as much as the detector is (ini-2x8.2).
//
// Tier-1 detection is not something initech computes. It is something Claude
// Code TELLS us, by writing an OSC 777 notify sequence into the PTY when a
// blocking dialog opens. That makes detection inherit the emitter's behaviour --
// the last-hop rule. A Claude upgrade, or a notification setting, that stops the
// emission would silently kill this feature: no error, no crash, just an
// attention system that never fires again, which is indistinguishable from the
// bug it was built to fix.
//
// So the measured emission is pinned two ways:
//
//   - TestAttentionOSC_FixtureMatchesTheMeasuredEmission runs always, in
//     `make test`, against the bytes captured from Claude Code 2.1.229. It
//     guards OUR parsing: if someone loosens the handler until it no longer
//     recognises the real sequence, this fails.
//   - TestAttentionOSC_LiveClaudeStillEmits is gated behind INITECH_OSC_PROBE=1
//     because it spawns a real Claude Code under a PTY and drives it into a real
//     dialog. THAT is the one that catches an upgrade going quiet. Run it when
//     bumping the pinned Claude version, and in the release check.
//
// Fixture provenance: captured 2026-08-12 from Claude Code 2.1.229 on macOS via
// a PTY, project-local settings forcing an approval prompt. Full capture record
// is on ini-2x8.

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// measuredOSC777 is the exact sequence Claude Code 2.1.229 emits when a blocking
// dialog opens. Byte-for-byte from the capture; do not "tidy" it.
const measuredOSC777 = "\x1b]777;notify;Claude Code;Claude needs your permission\x07"

// TestAttentionOSC_FixtureMatchesTheMeasuredEmission feeds the captured bytes
// through a real emulator with the real handler attached, and asserts the pane
// ends up waiting at chime grade.
func TestAttentionOSC_FixtureMatchesTheMeasuredEmission(t *testing.T) {
	p := testPane("super")
	p.attn = &attentionSignal{}
	registerAttentionOSC(p)

	if _, err := p.emu.Write([]byte(measuredOSC777)); err != nil {
		t.Fatalf("emulator write: %v", err)
	}

	p.refreshWaitingState()

	waiting, _, _ := p.WaitingInput()
	if !waiting {
		t.Fatal("the measured OSC 777 emission did not raise WaitingInput -- " +
			"tier-1 detection no longer recognises the sequence Claude Code actually sends")
	}
	if got := p.WaitingTierOf(); got != WaitingTierChime {
		t.Errorf("tier = %v, want WaitingTierChime (the app declaring its own state is chime-grade)", got)
	}
}

// TestAttentionOSC_IgnoresOtherOSCTraffic is the false-positive control. The
// same stream carries window titles and progress reports constantly; if any of
// those raised the state, the chime would fire on ordinary work and a false
// chime is a defect.
func TestAttentionOSC_IgnoresOtherOSCTraffic(t *testing.T) {
	// All measured from the same captures as the notify sequence above.
	noise := []string{
		"\x1b]0;✳ Claude Code\x07",                      // window title
		"\x1b]0;◐ Run Unix timestamp shell command\x07", // title with spinner glyph
		"\x1b]9;4;3;\x07",                               // progress: busy
		"\x1b]9;4;0;\x07",                               // progress: done
		"\x1b]8;id=zaxmda;https://example.com\x07",      // hyperlink
	}
	for _, seq := range noise {
		p := testPane("eng1")
		p.attn = &attentionSignal{}
		registerAttentionOSC(p)

		if _, err := p.emu.Write([]byte(seq)); err != nil {
			t.Fatalf("emulator write: %v", err)
		}
		p.refreshWaitingState()

		if waiting, _, _ := p.WaitingInput(); waiting {
			t.Errorf("ordinary OSC traffic raised WaitingInput: %q", seq)
		}
	}
}

func TestNotifyMessage_ParsesTheMeasuredPayload(t *testing.T) {
	cases := map[string]string{
		"notify;Claude Code;Claude needs your permission":     "Claude needs your permission",
		"notify;Claude Code;Claude is waiting for your input": "Claude is waiting for your input",
		"notify;Claude Code": "",
		"notify;":            "",
	}
	for in, want := range cases {
		if got := notifyMessage(in); got != want {
			t.Errorf("notifyMessage(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAttentionOSC_LiveClaudeStillEmits is the guardrail proper: it spawns a
// REAL Claude Code, drives it into a REAL dialog, and fails if no OSC 777
// arrives. This is what turns "a Claude upgrade silently killed the attention
// system" into a red test.
//
// Gated because it costs a real session. Run when bumping Claude, and at
// release:
//
//	INITECH_OSC_PROBE=1 go test ./internal/tui/ -run TestAttentionOSC_Live -v -count=1 -timeout 300s
func TestAttentionOSC_LiveClaudeStillEmits(t *testing.T) {
	if os.Getenv("INITECH_OSC_PROBE") != "1" {
		t.Skip("set INITECH_OSC_PROBE=1 to probe the live Claude Code emitter")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}

	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/.claude", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Force an approval prompt regardless of the operator's own allowlist,
	// which would otherwise auto-approve the very dialog we came to observe.
	settings := `{"permissions":{"ask":["Bash"],"allow":[],"deny":[]}}`
	if err := os.WriteFile(dir+"/.claude/settings.json", []byte(settings), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	cmd := exec.Command("claude", "Run the shell command: date +%s")
	cmd.Dir = dir
	// agentBaseEnv, NOT os.Environ(): the probe must build its child's
	// environment through the SAME scrub real panes use (ini-g0h), or it tests
	// an environment production never produces -- and would keep failing under
	// a tmux-set TERM_PROGRAM even after the pane path was fixed.
	cmd.Env = append(agentBaseEnv(),
		"TERM=xterm-256color",
		// An inherited child-session marker disables transcript writing and
		// changes startup behaviour; clear it so this is a normal session.
		"CLAUDE_CODE_CHILD_SESSION=",
		"CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1",
	)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("start claude under pty: %v", err)
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	var seen strings.Builder
	oscRe := regexp.MustCompile(`\x1b\]777;notify;[^\x07]*\x07`)
	trustRe := regexp.MustCompile(`(?i)trust`)
	trusted := false

	deadline := time.Now().Add(90 * time.Second)
	buf := make([]byte, 32*1024)
	for time.Now().Before(deadline) {
		_ = ptmx.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := ptmx.Read(buf)
		if n > 0 {
			seen.Write(buf[:n])
			// Clear Claude's first-run directory-trust dialog so the run can
			// reach the permission prompt.
			if !trusted && trustRe.MatchString(seen.String()) {
				trusted = true
				time.Sleep(500 * time.Millisecond)
				_, _ = ptmx.Write([]byte("\r"))
			}
			if m := oscRe.FindString(seen.String()); m != "" {
				t.Logf("live emission observed: %q", m)
				return // Guardrail satisfied.
			}
		}
		if err != nil && n == 0 {
			// Read deadline or EOF; keep going until the overall deadline.
			continue
		}
	}

	t.Fatalf(`CLAUDE CODE NO LONGER EMITS OSC 777 ON A BLOCKING DIALOG.

Tier-1 attention detection depends on this emission (Nelson's decision,
2026-08-12), and detection inherits the emitter's configuration -- the last-hop
rule. If this is a Claude upgrade or a changed notification setting, the
attention system is now SILENT: no chime, no needs-input row, no error. That is
indistinguishable from the bug the feature was built to fix, which is exactly
why this probe exists rather than trusting the emission to stay put.

Take it back for a decision; do not weaken this test.

Expected (measured at Claude Code 2.1.229):
  %q

Observed %d bytes of PTY output containing no such sequence.`,
		measuredOSC777, seen.Len())
}

// compile-time: the probe uses the same emulator type the panes use.
var _ = vt.NewSafeEmulator
