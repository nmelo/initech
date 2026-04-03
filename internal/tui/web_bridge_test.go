// Tests for web bridge adapters (tuiPaneLister, tuiPaneSubscriber).
package tui

import (
	"testing"
	"time"
)

func TestTuiPaneLister_AllPanes(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("qa1"))
	lister := &tuiPaneLister{t: tui}

	panes, ok := lister.AllPanes()
	if !ok {
		t.Fatal("AllPanes returned not-ok")
	}
	if len(panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(panes))
	}
	if panes[0].Name != "eng1" || panes[1].Name != "qa1" {
		t.Errorf("unexpected pane names: %v", panes)
	}
}

func TestTuiPaneSubscriber_SubscribeAndReceive(t *testing.T) {
	p := testPane("eng1")
	tui := newTestTUI(p)
	sub := &tuiPaneSubscriber{t: tui}

	ch, ok := sub.SubscribePane("eng1", "ws-1")
	if !ok || ch == nil {
		t.Fatal("SubscribePane returned not-ok or nil channel")
	}
	defer sub.UnsubscribePane("eng1", "ws-1")

	// Broadcast through the pane directly (simulates readLoop).
	p.broadcastToSubscribers([]byte("hello"))

	select {
	case got := <-ch:
		if string(got) != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast")
	}
}

func TestTuiPaneSubscriber_UnknownPane(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	sub := &tuiPaneSubscriber{t: tui}

	ch, ok := sub.SubscribePane("missing", "ws-1")
	if ok || ch != nil {
		t.Error("expected not-ok and nil channel for unknown pane")
	}
}

func TestTuiPaneSubscriber_Unsubscribe(t *testing.T) {
	p := testPane("eng1")
	tui := newTestTUI(p)
	sub := &tuiPaneSubscriber{t: tui}

	ch, _ := sub.SubscribePane("eng1", "ws-1")
	sub.UnsubscribePane("eng1", "ws-1")

	// Channel should be closed after unsubscribe.
	select {
	case _, open := <-ch:
		if open {
			t.Error("expected channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out; channel not closed")
	}
}

func TestTuiPaneSubscriber_UnsubscribeUnknownPane(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	sub := &tuiPaneSubscriber{t: tui}

	// Should not panic.
	sub.UnsubscribePane("missing", "ws-1")
}

func TestTuiPaneSubscriber_UnsubscribeUnknownID(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	sub := &tuiPaneSubscriber{t: tui}

	// Should not panic.
	sub.UnsubscribePane("eng1", "never-subscribed")
}
