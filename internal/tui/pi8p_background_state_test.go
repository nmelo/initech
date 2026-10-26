package tui

import (
	"testing"
	"time"
)

// ini-pi8p: a quiet pane whose footer shows background work reads
// background; it never overrides running, waiting or suspended; and nothing
// that is not a footer with work makes it. The fixtures are real Claude Code
// 2.1.310 Haiku screens.

// pi8pTick paints the screen, sets the stored state the detectors would
// have set, and runs one maintenance tick at now.
func pi8pTick(t *testing.T, tu *TUI, p *Pane, fixture string, stored ActivityState, now time.Time) {
	t.Helper()
	if fixture == "" {
		nma5Paint(p, "")
	} else {
		lvxwPaint(p, lvxwFixture(t, fixture))
	}
	p.mu.Lock()
	p.activity = stored
	p.mu.Unlock()
	tu.modalMaintenance(now)
}

func pi8pPane() (*TUI, *Pane) {
	p := newEmuPane("eng1", 160, 45)
	return &TUI{panes: toPaneViews([]*Pane{p})}, p
}

// Case (a): an agent landing a commit in a background shell reads
// background, its age advancing, and idle once the shell ends.
func TestBackgroundState_LandingACommitInABackgroundShell(t *testing.T) {
	tu, p := pi8pPane()
	t0 := time.Date(2026, 10, 26, 22, 0, 0, 0, time.UTC)
	pi8pTick(t, tu, p, "haiku310_landing_shell.txt", StateIdle, t0)
	if got := p.Activity(); got != StateBackground {
		t.Fatalf("shell running, pane quiet: %v, want background", got)
	}
	pi8pTick(t, tu, p, "haiku310_landing_shell.txt", StateIdle, t0.Add(30*time.Second))
	if b := paneBusy(p, t0.Add(30*time.Second)); b == nil || b.SinceSec != 30 || b.Shells != 1 {
		t.Errorf("after 30s: busy %+v, want 1 shell for 30s", b)
	}
	pi8pTick(t, tu, p, "haiku310_landed.txt", StateIdle, t0.Add(45*time.Second))
	if got := p.Activity(); got != StateIdle {
		t.Errorf("shell ended: %v, want idle", got)
	}
}

// Case (b): an agent compacted with nothing running reads idle throughout.
func TestBackgroundState_ACompactedAgentStaysIdle(t *testing.T) {
	tu, p := pi8pPane()
	t0 := time.Now()
	for i, f := range []string{"haiku310_compacting.txt", "haiku310_compacted.txt"} {
		pi8pTick(t, tu, p, f, StateIdle, t0.Add(time.Duration(i)*time.Second))
		if got := p.Activity(); got != StateIdle {
			t.Errorf("%s: %v, want idle", f, got)
		}
	}
}

// background never overrides running, waiting or suspended.
func TestBackgroundState_NeverOverridesAStrongerState(t *testing.T) {
	cases := []struct {
		fixture string
		stored  ActivityState
	}{
		{"haiku310_running_shell.txt", StateRunning},
		{"haiku310_dialog_shell.txt", StateWaitingInput},
		{"haiku310_landing_shell.txt", StateSuspended},
	}
	for _, c := range cases {
		tu, p := pi8pPane()
		// Work first seen while quiet, then the stronger state.
		pi8pTick(t, tu, p, "haiku310_landing_shell.txt", StateIdle, time.Now())
		pi8pTick(t, tu, p, c.fixture, c.stored, time.Now())
		if got := p.Activity(); got != c.stored {
			t.Errorf("%s stored %v: reads %v", c.fixture, c.stored, got)
		}
	}
}

// Feed it nothing: an empty screen, a shell, a footer with no work stay idle.
func TestBackgroundState_NothingButWorkMakesIt(t *testing.T) {
	for _, f := range []string{"", "haiku310_footer_idle.txt", "haiku310_branch_idle_w40.txt", "haiku310_write_prompt.txt"} {
		tu, p := pi8pPane()
		pi8pTick(t, tu, p, f, StateIdle, time.Now())
		if got := p.Activity(); got != StateIdle {
			t.Errorf("%q: %v, want idle", f, got)
		}
	}
	tu, p := pi8pPane()
	nma5Paint(p, "sh-3.2$ echo '⏸ manual mode on · 1 shell'\r\n⏸ manual mode on · 1 shell\r\nsh-3.2$ ")
	p.mu.Lock()
	p.activity = StateIdle
	p.mu.Unlock()
	tu.modalMaintenance(time.Now())
	if got := p.Activity(); got != StateIdle {
		t.Errorf("shell pane printing a mode line: %v, want idle", got)
	}
}
