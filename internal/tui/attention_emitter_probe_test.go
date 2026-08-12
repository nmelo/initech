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
