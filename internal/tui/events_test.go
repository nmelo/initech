package tui

import (
	"testing"
	"time"
)

func TestEventTypeString(t *testing.T) {
	tests := []struct {
		et   EventType
		want string
	}{
		{EventBeadCompleted, "completed"},
		{EventBeadClaimed, "claimed"},
		{EventBeadFailed, "failed"},
		{EventAgentStalled, "stalled"},
		{EventAgentStuck, "stuck"},
		{EventAgentIdle, "idle"},
		{EventType(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.et.String(); got != tt.want {
			t.Errorf("EventType(%d).String() = %q, want %q", tt.et, got, tt.want)
		}
	}
}

func TestEmitEventNonBlocking(t *testing.T) {
	ch := make(chan AgentEvent, 2)

	// Fill the channel.
	EmitEvent(ch, AgentEvent{Type: EventBeadClaimed, Pane: "eng1"})
	EmitEvent(ch, AgentEvent{Type: EventBeadCompleted, Pane: "eng1"})

	// Third emit should not block (dropped).
	EmitEvent(ch, AgentEvent{Type: EventAgentIdle, Pane: "eng1"})

	if len(ch) != 2 {
		t.Errorf("channel length = %d, want 2 (third should be dropped)", len(ch))
	}
}

func TestEmitEventSetsTime(t *testing.T) {
	ch := make(chan AgentEvent, 1)
	EmitEvent(ch, AgentEvent{Type: EventBeadClaimed, Pane: "eng1"})
	ev := <-ch
	if ev.Time.IsZero() {
		t.Error("EmitEvent should set Time when zero")
	}
}

func TestHandleAgentEventAppendsNotification(t *testing.T) {
	tui := &TUI{
		agentEvents: make(chan AgentEvent, 64),
	}

	ev := AgentEvent{
		Type:   EventBeadCompleted,
		Pane:   "eng1",
		BeadID: "ini-test.1",
		Detail: "eng1 completed ini-test.1",
		Time:   time.Now(),
	}
	tui.handleAgentEvent(ev)

	if len(tui.notifications) != 1 {
		t.Fatalf("notifications = %d, want 1", len(tui.notifications))
	}
	n := tui.notifications[0]
	if n.event.Pane != "eng1" || n.event.BeadID != "ini-test.1" {
		t.Errorf("notification event = %+v, want eng1/ini-test.1", n.event)
	}
	if n.expires.Before(time.Now()) {
		t.Error("notification should not already be expired")
	}
}

func TestPruneNotifications(t *testing.T) {
	tui := &TUI{}
	tui.notifications = []notification{
		{event: AgentEvent{Pane: "a"}, expires: time.Now().Add(-1 * time.Second)}, // expired
		{event: AgentEvent{Pane: "b"}, expires: time.Now().Add(10 * time.Second)}, // alive
		{event: AgentEvent{Pane: "c"}, expires: time.Now().Add(-5 * time.Second)}, // expired
	}
	tui.pruneNotifications()
	if len(tui.notifications) != 1 {
		t.Fatalf("after prune: %d notifications, want 1", len(tui.notifications))
	}
	if tui.notifications[0].event.Pane != "b" {
		t.Errorf("surviving notification = %q, want b", tui.notifications[0].event.Pane)
	}
}
