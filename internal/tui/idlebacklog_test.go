package tui

import (
	"testing"
)

func TestPaneSetIdleWithBacklog(t *testing.T) {
	p := &Pane{}
	if p.IdleWithBacklog() {
		t.Error("IdleWithBacklog should be false initially")
	}
	if p.BacklogCount() != 0 {
		t.Errorf("BacklogCount = %d, want 0 initially", p.BacklogCount())
	}

	p.SetIdleWithBacklog(3)
	if !p.IdleWithBacklog() {
		t.Error("IdleWithBacklog should be true after SetIdleWithBacklog")
	}
	if p.BacklogCount() != 3 {
		t.Errorf("BacklogCount = %d, want 3", p.BacklogCount())
	}
}

func TestPaneClearIdleWithBacklog(t *testing.T) {
	p := &Pane{}
	p.SetIdleWithBacklog(5)
	p.ClearIdleWithBacklog()
	if p.IdleWithBacklog() {
		t.Error("IdleWithBacklog should be false after ClearIdleWithBacklog")
	}
	if p.BacklogCount() != 0 {
		t.Errorf("BacklogCount = %d, want 0 after clear", p.BacklogCount())
	}
}

func TestAgentInfoIdleWithBacklog(t *testing.T) {
	a := AgentInfo{
		Name:            "eng1",
		Status:          "idle (3 ready)",
		Activity:        StateIdle,
		Visible:         true,
		IdleWithBacklog: true,
		BacklogCount:    3,
	}
	if !a.IdleWithBacklog {
		t.Error("AgentInfo.IdleWithBacklog should be true")
	}
	if a.BacklogCount != 3 {
		t.Errorf("AgentInfo.BacklogCount = %d, want 3", a.BacklogCount)
	}
}

func TestOverlayDotColorIdleWithBacklog(t *testing.T) {
	// Simulate the dot color logic from renderOverlay.
	// Idle + IdleWithBacklog -> yellow dot.
	// Idle + no backlog -> gray dot.
	// Running -> green dot.
	tests := []struct {
		activity        ActivityState
		idleWithBacklog bool
		wantYellow      bool
		wantGreen       bool
	}{
		{StateIdle, true, true, false},
		{StateIdle, false, false, false},
		{StateRunning, false, false, true},
	}

	for _, tt := range tests {
		a := AgentInfo{Activity: tt.activity, IdleWithBacklog: tt.idleWithBacklog}
		isYellow := a.Activity == StateIdle && a.IdleWithBacklog
		isGreen := a.Activity == StateRunning
		if isYellow != tt.wantYellow {
			t.Errorf("activity=%v idleWithBacklog=%v: isYellow=%v, want %v",
				tt.activity, tt.idleWithBacklog, isYellow, tt.wantYellow)
		}
		if isGreen != tt.wantGreen {
			t.Errorf("activity=%v: isGreen=%v, want %v",
				tt.activity, isGreen, tt.wantGreen)
		}
	}
}

func TestHandleAgentEvent_ClearsIdleWithBacklogOnClaim(t *testing.T) {
	p := &Pane{name: "eng1", alive: true}
	p.SetIdleWithBacklog(2)

	tui := &TUI{
		panes:       toPaneViews([]*Pane{p}),
		agentEvents: make(chan AgentEvent, 8),
	}
	tui.handleAgentEvent(AgentEvent{
		Type: EventBeadClaimed,
		Pane: "eng1",
	})

	if p.IdleWithBacklog() {
		t.Error("IdleWithBacklog should be cleared when bead is claimed")
	}
	if p.BacklogCount() != 0 {
		t.Errorf("BacklogCount = %d, want 0 after claim", p.BacklogCount())
	}
}

func TestHandleAgentEvent_NoEffectForOtherEvents(t *testing.T) {
	p := &Pane{name: "eng1", alive: true}
	p.SetIdleWithBacklog(2)

	tui := &TUI{
		panes:       toPaneViews([]*Pane{p}),
		agentEvents: make(chan AgentEvent, 8),
	}
	tui.handleAgentEvent(AgentEvent{
		Type: EventBeadCompleted,
		Pane: "eng1",
	})

	// EventBeadCompleted does not clear the flag (only EventBeadClaimed does).
	if !p.IdleWithBacklog() {
		t.Error("IdleWithBacklog should remain set for non-claim events")
	}
}
