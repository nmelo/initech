package tui

import (
	"testing"
	"time"
)

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

// TestUpdateActivity_IdleWithBead_FiresAfterThreshold verifies that the
// idle-with-bead notification fires when silence exceeds the bead threshold,
// not the activity display threshold.
func TestUpdateActivity_IdleWithBead_FiresAfterThreshold(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "eng1",
		alive:                 true,
		activity:              StateRunning,
		beadIDs:               []string{"ini-abc"},
		eventCh:               ch,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-65 * time.Second), // past 60s threshold
		lastIdleNotify:        time.Time{},                       // never notified
	}

	p.updateActivity()

	evs := drainEvents(ch)
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	if evs[0].Type != EventAgentIdleWithBead {
		t.Errorf("expected EventAgentIdleWithBead, got %v", evs[0].Type)
	}
	if evs[0].Pane != "eng1" {
		t.Errorf("event.Pane = %q, want eng1", evs[0].Pane)
	}
	if evs[0].BeadID != "ini-abc" {
		t.Errorf("event.BeadID = %q, want ini-abc", evs[0].BeadID)
	}
}

// TestUpdateActivity_IdleWithBead_NoFireDuringThinkingPause verifies that a
// short pause (10s) does NOT fire the idle-with-bead notification even though
// the activity bar shows idle.
func TestUpdateActivity_IdleWithBead_NoFireDuringThinkingPause(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "eng1",
		alive:                 true,
		activity:              StateRunning,
		beadIDs:               []string{"ini-abc"},
		eventCh:               ch,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-10 * time.Second), // 10s pause — normal thinking
	}

	p.updateActivity()

	// Activity bar should show idle (past 2s ptyIdleTimeout).
	if p.activity != StateIdle {
		t.Errorf("activity = %v, want StateIdle", p.activity)
	}

	// But no bead notification should fire.
	evs := drainEvents(ch)
	for _, ev := range evs {
		if ev.Type == EventAgentIdleWithBead {
			t.Error("unexpected EventAgentIdleWithBead during 10s thinking pause")
		}
	}
}

// TestUpdateActivity_IdleWithBead_NoBead verifies no event is emitted when the
// pane has no bead assigned.
func TestUpdateActivity_IdleWithBead_NoBead(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "eng1",
		alive:                 true,
		activity:              StateRunning,
		beadIDs:               nil, // no bead
		eventCh:               ch,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-65 * time.Second),
	}

	p.updateActivity()

	evs := drainEvents(ch)
	for _, ev := range evs {
		if ev.Type == EventAgentIdleWithBead {
			t.Error("unexpected EventAgentIdleWithBead when no bead is assigned")
		}
	}
}

// TestUpdateActivity_IdleWithBead_Cooldown verifies the cooldown suppresses
// a second event when the pane is still idle within 60 seconds.
func TestUpdateActivity_IdleWithBead_Cooldown(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "eng1",
		alive:                 true,
		activity:              StateIdle,
		beadIDs:               []string{"ini-abc"},
		eventCh:               ch,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-65 * time.Second),
		lastIdleNotify:        time.Now(), // notified just now — cooldown active
	}

	p.updateActivity()

	evs := drainEvents(ch)
	for _, ev := range evs {
		if ev.Type == EventAgentIdleWithBead {
			t.Error("unexpected EventAgentIdleWithBead during cooldown")
		}
	}
}

// TestUpdateActivity_IdleWithBead_CooldownExpired verifies the event fires
// after the cooldown window has elapsed.
func TestUpdateActivity_IdleWithBead_CooldownExpired(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "eng1",
		alive:                 true,
		activity:              StateIdle,
		beadIDs:               []string{"ini-abc"},
		eventCh:               ch,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-65 * time.Second),
		lastIdleNotify:        time.Now().Add(-2 * idleNotifyCooldown), // well past cooldown
	}

	p.updateActivity()

	evs := drainEvents(ch)
	found := false
	for _, ev := range evs {
		if ev.Type == EventAgentIdleWithBead {
			found = true
		}
	}
	if !found {
		t.Error("expected EventAgentIdleWithBead after cooldown expired, none emitted")
	}
}

// TestUpdateActivity_IdleWithBead_FiresOncePerSilence verifies the notification
// fires once when the threshold is crossed, then the idleBeadNotified flag
// prevents re-firing on subsequent ticks.
func TestUpdateActivity_IdleWithBead_FiresOncePerSilence(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "eng1",
		alive:                 true,
		activity:              StateRunning,
		beadIDs:               []string{"ini-abc"},
		eventCh:               ch,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-65 * time.Second),
	}

	// First tick: fires.
	p.updateActivity()
	evs := drainEvents(ch)
	if len(evs) != 1 {
		t.Fatalf("first tick: expected 1 event, got %d", len(evs))
	}

	// Second tick: flag prevents re-fire.
	p.updateActivity()
	evs = drainEvents(ch)
	if len(evs) != 0 {
		t.Errorf("second tick: expected 0 events, got %d", len(evs))
	}
}

// TestUpdateActivity_IdleWithBead_ResetsOnNewOutput verifies the flag resets
// when the pane produces new output, allowing re-notification.
func TestUpdateActivity_IdleWithBead_ResetsOnNewOutput(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "eng1",
		alive:                 true,
		activity:              StateRunning,
		beadIDs:               []string{"ini-abc"},
		eventCh:               ch,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-65 * time.Second),
	}

	// First idle → fires.
	p.updateActivity()
	drainEvents(ch)

	// Simulate output resuming.
	p.mu.Lock()
	p.lastOutputTime = time.Now()
	p.lastIdleNotify = time.Time{} // clear cooldown for test
	p.mu.Unlock()

	p.updateActivity()
	if p.activity != StateRunning {
		t.Fatalf("after output: activity = %v, want StateRunning", p.activity)
	}
	if p.idleBeadNotified {
		t.Error("idleBeadNotified should reset when output resumes")
	}

	// Silence again past threshold → should fire again.
	p.mu.Lock()
	p.lastOutputTime = time.Now().Add(-65 * time.Second)
	p.mu.Unlock()

	p.updateActivity()
	evs := drainEvents(ch)
	found := false
	for _, ev := range evs {
		if ev.Type == EventAgentIdleWithBead {
			found = true
		}
	}
	if !found {
		t.Error("expected EventAgentIdleWithBead after output resumed then stopped again")
	}
}

// TestUpdateActivity_IdleWithBead_DisabledWhenZero verifies that setting the
// threshold to 0 disables idle-with-bead notifications entirely.
func TestUpdateActivity_IdleWithBead_DisabledWhenZero(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "eng1",
		alive:                 true,
		activity:              StateRunning,
		beadIDs:               []string{"ini-abc"},
		eventCh:               ch,
		idleWithBeadThreshold: 0, // disabled
		lastOutputTime:        time.Now().Add(-120 * time.Second),
	}

	p.updateActivity()

	evs := drainEvents(ch)
	for _, ev := range evs {
		if ev.Type == EventAgentIdleWithBead {
			t.Error("unexpected EventAgentIdleWithBead when threshold is 0 (disabled)")
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

// TestUpdateActivity_CodexAgent_IdleAfterLongPause verifies that a Codex agent
// transitions to Idle and fires idle-with-bead after exceeding the bead threshold.
func TestUpdateActivity_CodexAgent_IdleAfterLongPause(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "intern",
		alive:                 true,
		activity:              StateRunning,
		beadIDs:               []string{"ini-abc"},
		agentType:             "codex",
		eventCh:               ch,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-65 * time.Second), // well past bead threshold
	}

	p.updateActivity()

	if p.activity != StateIdle {
		t.Errorf("Codex agent with 65s gap: activity = %v, want StateIdle", p.activity)
	}
	evs := drainEvents(ch)
	found := false
	for _, ev := range evs {
		if ev.Type == EventAgentIdleWithBead {
			found = true
		}
	}
	if !found {
		t.Error("expected EventAgentIdleWithBead for Codex agent past bead threshold")
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

// TestUpdateActivity_CustomThreshold verifies a custom threshold from config.
func TestUpdateActivity_CustomThreshold(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "eng1",
		alive:                 true,
		activity:              StateRunning,
		beadIDs:               []string{"ini-abc"},
		eventCh:               ch,
		idleWithBeadThreshold: 30 * time.Second, // custom 30s
		lastOutputTime:        time.Now().Add(-35 * time.Second),
	}

	p.updateActivity()

	evs := drainEvents(ch)
	found := false
	for _, ev := range evs {
		if ev.Type == EventAgentIdleWithBead {
			found = true
		}
	}
	if !found {
		t.Error("expected EventAgentIdleWithBead with custom 30s threshold")
	}
}
