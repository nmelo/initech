package tui

import (
	"testing"
	"time"
)

func TestUpdateActivity_RunningWhenRecentOutput(t *testing.T) {
	p := &Pane{alive: true}
	p.lastOutputTime = time.Now().Add(-500 * time.Millisecond)
	p.updateActivity()
	if p.activity != StateRunning {
		t.Errorf("activity = %v after 500ms gap, want StateRunning (output is recent)", p.activity)
	}
}

func TestUpdateActivity_RunningJustUnderThreshold(t *testing.T) {
	p := &Pane{alive: true}
	p.lastOutputTime = time.Now().Add(-(ptyIdleTimeout - 100*time.Millisecond))
	p.updateActivity()
	if p.activity != StateRunning {
		t.Errorf("activity = %v just under ptyIdleTimeout, want StateRunning", p.activity)
	}
}

func TestUpdateActivity_IdleAfterThreshold(t *testing.T) {
	p := &Pane{alive: true}
	p.lastOutputTime = time.Now().Add(-(ptyIdleTimeout + time.Second))
	p.updateActivity()
	if p.activity != StateIdle {
		t.Errorf("activity = %v after ptyIdleTimeout+1s, want StateIdle", p.activity)
	}
}

func TestUpdateActivity_IdleWhenNoOutputYet(t *testing.T) {
	p := &Pane{alive: true}
	// lastOutputTime is zero value — no output ever received.
	p.updateActivity()
	if p.activity != StateIdle {
		t.Errorf("activity = %v with zero lastOutputTime, want StateIdle", p.activity)
	}
}

func TestUpdateActivity_IdleWhenDead(t *testing.T) {
	// ini-a1e.29: dead panes show StateDead (red filled dot),
	// distinct from StateIdle (gray hollow circle).
	p := &Pane{alive: false}
	// Even with a very recent lastOutputTime, dead pane must be StateDead.
	p.lastOutputTime = time.Now()
	p.updateActivity()
	if p.activity != StateDead {
		t.Errorf("activity = %v for dead pane, want StateDead", p.activity)
	}
}

func TestUpdateActivity_TransitionRunningToIdle(t *testing.T) {
	p := &Pane{alive: true}
	// Simulate active agent: recent output.
	p.lastOutputTime = time.Now().Add(-100 * time.Millisecond)
	p.updateActivity()
	if p.activity != StateRunning {
		t.Fatalf("activity = %v, want StateRunning", p.activity)
	}
	// Simulate idle agent: output is stale.
	p.lastOutputTime = time.Now().Add(-(ptyIdleTimeout + time.Second))
	p.updateActivity()
	if p.activity != StateIdle {
		t.Errorf("activity = %v after stale output, want StateIdle", p.activity)
	}
}

// makeEventCh creates a buffered event channel for test panes.
func makeEventCh() chan AgentEvent {
	return make(chan AgentEvent, 16)
}

// drainEvents returns all events currently in the channel without blocking.
func drainEvents(ch chan AgentEvent) []AgentEvent {
	var evs []AgentEvent
	for {
		select {
		case ev := <-ch:
			evs = append(evs, ev)
		default:
			return evs
		}
	}
}

// TestUpdateActivity_ActivityBarStillShowsIdle verifies the activity bar
// transitions to idle at the ptyIdleTimeout (2s) regardless of the bead threshold.
func TestUpdateActivity_ActivityBarStillShowsIdle(t *testing.T) {
	p := &Pane{
		name:                  "eng1",
		alive:                 true,
		activity:              StateRunning,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-3 * time.Second), // past 2s, under 60s
	}

	p.updateActivity()

	if p.activity != StateIdle {
		t.Errorf("activity = %v, want StateIdle (3s past ptyIdleTimeout)", p.activity)
	}
}

// TestUpdateActivity_CodexAgent_StaysRunningDuringPause verifies that a Codex
// agent with a 5-second output gap (normal inter-tool-call pause) stays Running
// rather than transitioning to Idle.
func TestUpdateActivity_CodexAgent_StaysRunningDuringPause(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "intern",
		alive:                 true,
		activity:              StateRunning,
		beadIDs:               []string{"ini-abc"},
		beadAssignedAt:        time.Now().Add(-2 * time.Hour),
		agentType:             "codex",
		eventCh:               ch,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-5 * time.Second), // 5s gap — within codex threshold
	}

	p.updateActivity()

	if p.activity != StateRunning {
		t.Errorf("Codex agent with 5s gap: activity = %v, want StateRunning", p.activity)
	}
	evs := drainEvents(ch)
	for _, ev := range evs {
		if ev.Type == EventAgentIdleWithBead {
			t.Error("unexpected EventAgentIdleWithBead for Codex agent within threshold")
		}
	}
}

// TestUpdateActivity_OpenCodeAgent_StaysRunningDuringPause verifies OpenCode
// agents also get the extended threshold via IsCodexLikeAgentType.
func TestUpdateActivity_OpenCodeAgent_StaysRunningDuringPause(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "intern",
		alive:                 true,
		activity:              StateRunning,
		beadIDs:               []string{"ini-abc"},
		beadAssignedAt:        time.Now().Add(-2 * time.Hour),
		agentType:             "opencode",
		eventCh:               ch,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-5 * time.Second),
	}

	p.updateActivity()

	if p.activity != StateRunning {
		t.Errorf("OpenCode agent with 5s gap: activity = %v, want StateRunning", p.activity)
	}
}

// TestUpdateActivity_ClaudeCodeAgent_IdleAt2s verifies the original 2s threshold
// still applies to claude-code agents for the activity bar (regression guard).
func TestUpdateActivity_ClaudeCodeAgent_IdleAt2s(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "eng1",
		alive:                 true,
		activity:              StateRunning,
		beadIDs:               []string{"ini-abc"},
		beadAssignedAt:        time.Now().Add(-2 * time.Hour),
		agentType:             "claude-code",
		eventCh:               ch,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-3 * time.Second), // past 2s CC threshold
	}

	p.updateActivity()

	if p.activity != StateIdle {
		t.Errorf("Claude Code agent with 3s gap: activity = %v, want StateIdle", p.activity)
	}
	// Should NOT fire bead notification (only 3s, not 60s).
	evs := drainEvents(ch)
	for _, ev := range evs {
		if ev.Type == EventAgentIdleWithBead {
			t.Error("unexpected bead notification at 3s — should wait for 60s threshold")
		}
	}
}
