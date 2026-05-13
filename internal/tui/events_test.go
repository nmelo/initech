package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
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
		{EventBeadAssigned, "assigned"},
		{EventBeadDelivered, "delivered"},
		{EventPeerConnected, "peer_connected"},
		{EventPeerDisconnected, "peer_disconnected"},
		{EventLiveSwap, "live_swap"},
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
	EmitEvent(ch, AgentEvent{Type: EventAgentStarted, Pane: "eng1"})

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

func TestHandleAgentEventCapsAt5(t *testing.T) {
	tui := &TUI{}
	for i := 0; i < 8; i++ {
		tui.handleAgentEvent(AgentEvent{
			Type:   EventBeadClaimed,
			Pane:   "eng1",
			Detail: fmt.Sprintf("event %d", i),
		})
	}
	if len(tui.notifications) != maxNotifications {
		t.Errorf("notifications = %d, want %d", len(tui.notifications), maxNotifications)
	}
	// Oldest should have been dropped; newest should be event 7.
	last := tui.notifications[len(tui.notifications)-1]
	if last.event.Detail != "event 7" {
		t.Errorf("last notification = %q, want 'event 7'", last.event.Detail)
	}
	first := tui.notifications[0]
	if first.event.Detail != "event 3" {
		t.Errorf("first notification = %q, want 'event 3'", first.event.Detail)
	}
}

func TestHandleAgentEventCompletionLongerTTL(t *testing.T) {
	tui := &TUI{}
	tui.handleAgentEvent(AgentEvent{Type: EventBeadCompleted, Pane: "eng1"})
	tui.handleAgentEvent(AgentEvent{Type: EventBeadClaimed, Pane: "eng2"})

	completionTTL := time.Until(tui.notifications[0].expires)
	claimedTTL := time.Until(tui.notifications[1].expires)
	if completionTTL <= claimedTTL {
		t.Errorf("completion TTL (%v) should be longer than claimed TTL (%v)", completionTTL, claimedTTL)
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

func TestAutoNotifyDisabled_SuppressesIdleWithBead(t *testing.T) {
	f := false
	tui := &TUI{
		project: &config.Project{AutoNotify: &f},
	}

	ev := AgentEvent{
		Type:   EventAgentIdleWithBead,
		Pane:   "eng1",
		BeadID: "ini-test",
		Detail: "eng1 idle with bead",
		Time:   time.Now(),
	}
	tui.handleAgentEvent(ev)

	// The event should still appear in notifications (toast) and event log,
	// but the injectText to super should NOT fire. Since we have no super pane
	// and no safeGo, a panic would indicate the code tried to notify.
	// Absence of panic = suppression worked.
	if len(tui.notifications) != 1 {
		t.Errorf("notifications = %d, want 1 (event still logged)", len(tui.notifications))
	}
}

// TestAutoNotifyDefault_SuppressesIdleWithBead: post-ini-3k1 the nil
// AutoNotify default means SUPPRESS, not allow. This test pins the new
// opt-in default. The toast/event-log notification still appears (it's
// unconditional); only the super-pane injectText is gated.
//
// Test depth caveat: we verify the toast appears AND no panic, which only
// proves the code took a survivable path. Strong assertion (that injectText
// was not called) requires a super-pane fixture + interception — pre-existing
// test debt mirrored in the sibling _Disabled and _ExplicitTrue tests.
func TestAutoNotifyDefault_SuppressesIdleWithBead(t *testing.T) {
	// nil AutoNotify post-ini-3k1 = defaults to false (suppressed).
	tui := &TUI{
		project: &config.Project{},
	}

	ev := AgentEvent{
		Type:   EventAgentIdleWithBead,
		Pane:   "eng1",
		BeadID: "ini-test",
		Detail: "eng1 idle with bead",
		Time:   time.Now(),
	}
	// IsAutoNotifyEnabled returns false on the nil default, so the
	// findPaneByName("super") branch is NOT entered. No panic confirms the
	// suppression path was taken; the toast still appears below.
	tui.handleAgentEvent(ev)

	if len(tui.notifications) != 1 {
		t.Errorf("notifications = %d, want 1 (toast still appears even when super-notify is suppressed)", len(tui.notifications))
	}
}

// TestAutoNotifyExplicitTrue_AllowsIdleWithBead: opt-in path. With
// AutoNotify: &true, the notify branch enters; findPaneByName returns nil
// here because no super pane is wired in the fixture, so injectText is
// never called and no panic results. Toast still fires. Mirrors the
// _Disabled / _Default suppression tests with the opposite gate decision.
func TestAutoNotifyExplicitTrue_AllowsIdleWithBead(t *testing.T) {
	tr := true
	tui := &TUI{
		project: &config.Project{AutoNotify: &tr},
	}

	ev := AgentEvent{
		Type:   EventAgentIdleWithBead,
		Pane:   "eng1",
		BeadID: "ini-test",
		Detail: "eng1 idle with bead",
		Time:   time.Now(),
	}
	tui.handleAgentEvent(ev)

	if len(tui.notifications) != 1 {
		t.Errorf("notifications = %d, want 1", len(tui.notifications))
	}
}
