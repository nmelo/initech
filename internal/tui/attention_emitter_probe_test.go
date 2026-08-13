//go:build !windows

package tui

// attention_emitter_probe_test.go holds the LIVE emitter guardrail: it spawns a
// real Claude Code, drives it into a real dialog, and fails if no OSC 777
// arrives (ini-2x8.2).
//
// '//go:build !windows' is legitimate here and nowhere else in the attention
// suites: this file drives a PTY via creack/pty. The portable half -- the
// measured sequence and the parsing tests around it -- lives in the UNTAGGED
// attention_osc_test.go, so this one real constraint cannot propagate to files
// that have no reason to carry it (ini-47w).
//
// Gated behind INITECH_OSC_PROBE=1 because it costs a real session. Run it when
// bumping the pinned Claude version, and at release:
//
//	INITECH_OSC_PROBE=1 go test ./internal/tui/ -run TestAttentionOSC_Live -v -count=1 -timeout 300s

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

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
	// agentBaseEnv, NOT os.Environ(): the probe must build its child's terminal
	// identity exactly the way real panes do (ini-g0h), or it measures an
	// environment production never produces. It carries the pinned TERM and
	// TERM_PROGRAM, so nothing terminal-facing is set here -- if this test and
	// buildPaneCmd ever disagree about the identity, the canary is measuring
	// something no operator runs.
	cmd.Env = append(agentBaseEnv(),
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
	// DIALOG-SPECIFIC, not "any OSC 777" (ini-zfm). OSC 777 is Claude's general
	// notification channel: it also carries the idle notification, login
	// success, computer-use exits and more. A probe that accepted any notify
	// would report the emitter healthy on a build that had stopped announcing
	// dialogs entirely and was merely being chatty on a timer.
	oscDialogRe := regexp.MustCompile(`\x1b\]777;notify;[^\x07]*Claude needs your permission\x07`)
	oscAnyRe := regexp.MustCompile(`\x1b\]777;notify;[^\x07]*\x07`)
	trustRe := regexp.MustCompile(`(?i)trust`)
	trusted := false

	// Read on a goroutine and select on a timer rather than using
	// ptmx.SetReadDeadline: on darwin a PTY master returns "file type does not
	// support deadline" (measured, ini-zfm) and the error was being discarded,
	// so Read then blocks with no deadline in effect. With continuous output
	// that is invisible; the moment the session goes QUIET -- which is exactly
	// what a stopped emitter looks like -- the loop parks forever and the test
	// dies on the go test timeout instead of reporting this failure message.
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
collect:
	for {
		select {
		case c, ok := <-chunks:
			if !ok {
				break collect // PTY closed; fall through to the report below.
			}
			seen.Write(c)
			// Clear Claude's first-run directory-trust dialog so the run can
			// reach the permission prompt.
			if !trusted && trustRe.MatchString(seen.String()) {
				trusted = true
				time.Sleep(500 * time.Millisecond)
				_, _ = ptmx.Write([]byte("\r"))
			}
			if m := oscDialogRe.FindString(seen.String()); m != "" {
				t.Logf("live emission observed: %q", m)
				return // Guardrail satisfied.
			}
		case <-deadline:
			break collect
		}
	}

	// Distinguish "the emitter went quiet" from "the emitter is alive but no
	// longer announces dialogs" -- different failures with different owners.
	if others := oscAnyRe.FindAllString(seen.String(), -1); len(others) > 0 {
		t.Fatalf(`CLAUDE CODE EMITTED OSC 777, BUT NEVER FOR THE DIALOG.

Notifications are still flowing, so the channel and the pane's terminal identity
are both fine -- what changed is WHICH events Claude announces, or the wording of
the dialog announcement that tier-1 keys on.

Expected to contain: %q
Observed instead:    %q

If the dialog text was merely reworded, the allowlist in attention_detect.go
(dialogNotifyTexts) needs the new wording ADDED from a capture -- do not widen it
to accept any notification, which is the ini-zfm regression.`,
			measuredOSC777, others)
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
