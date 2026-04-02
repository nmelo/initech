// QA tests for ini-z7b: Idle-with-backlog yellow dot in overlay.
package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Rendering: idle-with-backlog pane gets a yellow hollow dot in the overlay.
func TestOverlayDot_IdleWithBacklogIsYellow(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.layoutState.Overlay = true

	// Set pane to idle + idle-with-backlog.
	tui.panes[0].(*Pane).activity = StateIdle
	tui.panes[0].(*Pane).SetIdleWithBacklog(3)

	tui.render()

	// panelW = 4 + len("eng1")=4 + 1 + len("idle (3 ready)")=14 + 2 = 25
	// px = 120 - 25 - 1 = 94; dotX = 94+2 = 96; dotY = 2
	sw, _ := s.Size()
	idleStatus := "idle (3 ready)"
	maxNameLen := 4
	statusMaxLen := len(idleStatus)
	if statusMaxLen < 7 {
		statusMaxLen = 7
	}
	panelW := 4 + maxNameLen + 1 + statusMaxLen + 2
	px := sw - panelW - 1
	dotX := px + 2
	dotY := 2

	mainc, style, _ := s.Get(dotX, dotY)

	// Must be hollow dot ○ (idle state, not running).
	if mainc != "\u25cb" {
		t.Errorf("idle+backlog dot = %q (%q), want ○ (U+25CB)", mainc, mainc)
	}
	// Must be yellow color.
	fg, _, _ := style.Decompose()
	if fg != tcell.ColorYellow {
		t.Errorf("idle+backlog dot color = %v, want Yellow", fg)
	}
}

// Rendering: regular idle (no backlog) still shows gray hollow dot.
func TestOverlayDot_RegularIdleIsGray(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.layoutState.Overlay = true
	tui.panes[0].(*Pane).activity = StateIdle
	// IdleWithBacklog not set.

	tui.render()

	sw, _ := s.Size()
	panelW := 4 + 4 + 1 + 7 + 2
	px := sw - panelW - 1
	dotX := px + 2
	dotY := 2

	mainc, style, _ := s.Get(dotX, dotY)
	if mainc != "\u25cb" {
		t.Errorf("regular idle dot = %q, want ○", mainc)
	}
	fg, _, _ := style.Decompose()
	if fg != tcell.ColorGray {
		t.Errorf("regular idle dot color = %v, want Gray", fg)
	}
}

// Status text: "idle (N ready)" appears in overlay for idle-with-backlog pane.
func TestOverlayStatusText_IdleWithBacklog(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.layoutState.Overlay = true
	tui.panes[0].(*Pane).activity = StateIdle
	tui.panes[0].(*Pane).SetIdleWithBacklog(5)

	tui.render()

	// Read row 2 (first agent row) to find status text.
	sw, _ := s.Size()
	var row strings.Builder
	for x := 0; x < sw; x++ {
		c, _, _ := s.Get(x, 2)
		row.WriteString(c)
	}
	if !strings.Contains(row.String(), "5 ready") {
		t.Errorf("overlay row = %q, want contains '5 ready'", row.String())
	}
}

// Bead ID takes priority over idle-with-backlog status text.
func TestOverlayStatusText_BeadPriorityOverBacklog(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.layoutState.Overlay = true
	tui.panes[0].(*Pane).activity = StateIdle
	tui.panes[0].(*Pane).SetIdleWithBacklog(3)
	tui.panes[0].(*Pane).SetBead("ini-abc.1", "")

	tui.render()

	sw, _ := s.Size()
	var row strings.Builder
	for x := 0; x < sw; x++ {
		c, _, _ := s.Get(x, 2)
		row.WriteString(c)
	}
	rowStr := row.String()
	// Bead ID should appear.
	if !strings.Contains(rowStr, "ini-abc.1") {
		t.Errorf("overlay row = %q, want bead ID 'ini-abc.1' taking priority", rowStr)
	}
	// Backlog text should NOT appear when bead is set.
	if strings.Contains(rowStr, "ready") {
		t.Errorf("overlay row = %q, backlog text should not appear when bead is set", rowStr)
	}
}

// SetIdleWithBacklog(0) still sets the flag (edge: zero-count backlog is still a state).
func TestPaneSetIdleWithBacklog_ZeroCount(t *testing.T) {
	p := &Pane{}
	p.SetIdleWithBacklog(0)
	if !p.IdleWithBacklog() {
		t.Error("SetIdleWithBacklog(0) should still set IdleWithBacklog=true")
	}
	if p.BacklogCount() != 0 {
		t.Errorf("BacklogCount = %d, want 0", p.BacklogCount())
	}
}

// ClearIdleWithBacklog is idempotent (calling twice is safe).
func TestPaneClearIdleWithBacklog_Idempotent(t *testing.T) {
	p := &Pane{}
	p.SetIdleWithBacklog(3)
	p.ClearIdleWithBacklog()
	p.ClearIdleWithBacklog() // second call must not panic
	if p.IdleWithBacklog() {
		t.Error("double ClearIdleWithBacklog: IdleWithBacklog should be false")
	}
}

// EventBeadClaimed clears the flag for the correct pane only.
func TestHandleAgentEvent_ClearOnlyTargetPane(t *testing.T) {
	p1 := &Pane{name: "eng1", alive: true}
	p2 := &Pane{name: "eng2", alive: true}
	p1.SetIdleWithBacklog(3)
	p2.SetIdleWithBacklog(2)

	tui := &TUI{
		panes:       toPaneViews([]*Pane{p1, p2}),
		agentEvents: make(chan AgentEvent, 8),
	}
	tui.handleAgentEvent(AgentEvent{Type: EventBeadClaimed, Pane: "eng1"})

	if p1.IdleWithBacklog() {
		t.Error("eng1 IdleWithBacklog should be cleared on claim")
	}
	if !p2.IdleWithBacklog() {
		t.Error("eng2 IdleWithBacklog should remain set (different pane)")
	}
}

// AgentInfo built from renderOverlay has correct IdleWithBacklog/BacklogCount values.
func TestRenderOverlay_AgentInfoPopulated(t *testing.T) {
	// Use a simulation screen wide enough to hold overlay content.
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.screen = s
	tui.layoutState.Overlay = true
	tui.panes[0].(*Pane).SetIdleWithBacklog(7)
	tui.panes[1].(*Pane).lastOutputTime = time.Now() // running: recent PTY output

	// We can't directly inspect AgentInfo (it's local to renderOverlay), but
	// we can verify the rendered output: eng1 should have yellow dot, eng2 green.
	tui.render()

	sw, _ := s.Size()
	// For two panes "eng1" and "eng2", maxNameLen=4.
	// statusMaxLen = max(7, len("idle (7 ready)"))=14
	idleStatus := fmt.Sprintf("idle (%d ready)", 7)
	statusMaxLen := len(idleStatus)
	if statusMaxLen < 7 {
		statusMaxLen = 7
	}
	panelW := 4 + 4 + 1 + statusMaxLen + 2
	px := sw - panelW - 1
	dotX := px + 2

	// eng1: row 2, yellow dot.
	_, style1, _ := s.Get(dotX, 2)
	fg1, _, _ := style1.Decompose()
	if fg1 != tcell.ColorYellow {
		t.Errorf("eng1 (idle+backlog) dot color = %v, want Yellow", fg1)
	}

	// eng2: row 3, green dot.
	_, style2, _ := s.Get(dotX, 3)
	fg2, _, _ := style2.Decompose()
	if fg2 != tcell.ColorGreen {
		t.Errorf("eng2 (running) dot color = %v, want Green", fg2)
	}
}
