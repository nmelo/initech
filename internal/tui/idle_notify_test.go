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

// TestUpdateActivity_IdleWithBead_EdgeFires verifies that a running->idle
// transition on a pane with a beadID emits EventAgentIdleWithBead.
func TestUpdateActivity_IdleWithBead_EdgeFires(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:           "eng1",
		alive:          true,
		activity:       StateRunning,
		beadID:         "ini-abc",
		eventCh:        ch,
		lastOutputTime: time.Now().Add(-5 * time.Second), // past idle threshold
		lastIdleNotify: time.Time{},                      // never notified
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

// TestUpdateActivity_IdleWithBead_NoBead verifies no event is emitted when the
// pane has no bead assigned.
func TestUpdateActivity_IdleWithBead_NoBead(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:           "eng1",
		alive:          true,
		activity:       StateRunning,
		beadID:         "", // no bead
		eventCh:        ch,
		lastOutputTime: time.Now().Add(-5 * time.Second),
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
// a second event when the pane goes idle again within 60 seconds.
func TestUpdateActivity_IdleWithBead_Cooldown(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:           "eng1",
		alive:          true,
		activity:       StateRunning,
		beadID:         "ini-abc",
		eventCh:        ch,
		lastOutputTime: time.Now().Add(-5 * time.Second),
		lastIdleNotify: time.Now(), // notified just now — cooldown active
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
		name:           "eng1",
		alive:          true,
		activity:       StateRunning,
		beadID:         "ini-abc",
		eventCh:        ch,
		lastOutputTime: time.Now().Add(-5 * time.Second),
		lastIdleNotify: time.Now().Add(-2 * idleNotifyCooldown), // well past cooldown
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

// TestUpdateActivity_IdleWithBead_NoEdge verifies no event when pane was
// already idle (idle->idle, not running->idle).
func TestUpdateActivity_IdleWithBead_NoEdge(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:           "eng1",
		alive:          true,
		activity:       StateIdle, // was already idle
		beadID:         "ini-abc",
		eventCh:        ch,
		lastOutputTime: time.Now().Add(-5 * time.Second),
	}

	p.updateActivity()

	evs := drainEvents(ch)
	for _, ev := range evs {
		if ev.Type == EventAgentIdleWithBead {
			t.Error("unexpected EventAgentIdleWithBead: no running->idle edge (was already idle)")
		}
	}
}

// TestUpdateActivity_IdleWithBead_PrevActivityTracked verifies that consecutive
// updateActivity calls correctly detect the running->idle edge.
func TestUpdateActivity_IdleWithBead_PrevActivityTracked(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:           "eng1",
		alive:          true,
		activity:       StateRunning,
		beadID:         "ini-abc",
		eventCh:        ch,
		lastOutputTime: time.Now(), // still active
	}

	// First call: should stay running (output recent).
	p.updateActivity()
	if p.activity != StateRunning {
		t.Fatalf("expected StateRunning, got %v", p.activity)
	}

	// Simulate output stopping.
	p.mu.Lock()
	p.lastOutputTime = time.Now().Add(-5 * time.Second)
	p.mu.Unlock()

	// Second call: running->idle edge, event should fire.
	p.updateActivity()
	if p.activity != StateIdle {
		t.Fatalf("expected StateIdle, got %v", p.activity)
	}
	evs := drainEvents(ch)
	found := false
	for _, ev := range evs {
		if ev.Type == EventAgentIdleWithBead {
			found = true
		}
	}
	if !found {
		t.Error("expected EventAgentIdleWithBead on running->idle edge")
	}
}
