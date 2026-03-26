// QA tests for ini-oiw: Activity detection v2 (PTY byte recency).
// Verifies that the new ptyIdleTimeout constant and lastOutputTime field drive
// activity state, and that render() calls updateActivity() each frame so the
// overlay reflects live PTY recency rather than stale manual assignments.
package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ptyIdleTimeout constant must be 2 seconds exactly.
func TestPtyIdleTimeout_Is2Seconds(t *testing.T) {
	if ptyIdleTimeout != 2*time.Second {
		t.Errorf("ptyIdleTimeout = %v, want 2s", ptyIdleTimeout)
	}
}

// render() must call updateActivity() per pane each frame, overriding any
// manually-set p.activity value. A pane with zero lastOutputTime must render
// as idle even if p.activity was previously set to StateRunning.
func TestRender_UpdateActivity_OverridesManualRunning(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.layoutState.Overlay = true
	// Manually set running state — render() must override this via updateActivity().
	tui.panes[0].activity = StateRunning
	// lastOutputTime is zero → updateActivity() returns StateIdle.
	tui.render()

	sw, _ := s.Size()
	panelW := 4 + 4 + 1 + 7 + 2
	px := sw - panelW - 1
	dotX := px + 2
	dotY := 2

	mainc, _, style, _ := s.GetContent(dotX, dotY)
	if mainc != '\u25cb' {
		t.Errorf("dot = %q (%U), want ○ (U+25CB) — render must override stale StateRunning", mainc, mainc)
	}
	fg, _, _ := style.Decompose()
	if fg != tcell.ColorGray {
		t.Errorf("dot color = %v, want Gray (idle with no PTY output)", fg)
	}
}

// render() derives running state from lastOutputTime: a pane with output
// within ptyIdleTimeout renders as running (green filled dot).
func TestRender_UpdateActivity_RecentOutputYieldsRunning(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.layoutState.Overlay = true
	tui.panes[0].lastOutputTime = time.Now() // simulate active PTY output
	tui.render()

	sw, _ := s.Size()
	panelW := 4 + 4 + 1 + 7 + 2
	px := sw - panelW - 1
	dotX := px + 2
	dotY := 2

	mainc, _, style, _ := s.GetContent(dotX, dotY)
	if mainc != '\u25cf' {
		t.Errorf("dot = %q (%U), want ● (U+25CF) — recent PTY output must yield running", mainc, mainc)
	}
	fg, _, _ := style.Decompose()
	if fg != tcell.ColorGreen {
		t.Errorf("dot color = %v, want Green (active PTY output)", fg)
	}
}

// render() derives idle state from stale lastOutputTime: output older than
// ptyIdleTimeout renders as idle (gray hollow dot).
func TestRender_UpdateActivity_StaleOutputYieldsIdle(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.layoutState.Overlay = true
	tui.panes[0].lastOutputTime = time.Now().Add(-(ptyIdleTimeout + time.Second))
	tui.render()

	sw, _ := s.Size()
	panelW := 4 + 4 + 1 + 7 + 2
	px := sw - panelW - 1
	dotX := px + 2
	dotY := 2

	mainc, _, style, _ := s.GetContent(dotX, dotY)
	if mainc != '\u25cb' {
		t.Errorf("dot = %q (%U), want ○ (U+25CB) — stale PTY output must yield idle", mainc, mainc)
	}
	fg, _, _ := style.Decompose()
	if fg != tcell.ColorGray {
		t.Errorf("dot color = %v, want Gray (stale PTY output)", fg)
	}
}

// Dead pane (alive=false) must always render as idle regardless of lastOutputTime.
func TestRender_UpdateActivity_DeadPaneIsIdle(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.layoutState.Overlay = true
	tui.panes[0].alive = false
	tui.panes[0].lastOutputTime = time.Now() // recent, but pane is dead
	tui.render()

	sw, _ := s.Size()
	panelW := 4 + 4 + 1 + 7 + 2
	px := sw - panelW - 1
	dotX := px + 2
	dotY := 2

	mainc, _, style, _ := s.GetContent(dotX, dotY)
	if mainc != '\u25cb' {
		t.Errorf("dead pane dot = %q (%U), want ○ (U+25CB)", mainc, mainc)
	}
	fg, _, _ := style.Decompose()
	if fg != tcell.ColorGray {
		t.Errorf("dead pane dot color = %v, want Gray", fg)
	}
}
