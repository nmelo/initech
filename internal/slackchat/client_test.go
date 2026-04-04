package slackchat

import (
	"log/slog"
	"testing"

	"github.com/slack-go/slack/socketmode"
)

func TestHandleEvent_ConnectionLifecycle(t *testing.T) {
	c := &Client{logger: slog.Default()}

	// Connection lifecycle events should not panic.
	c.handleEvent(socketmode.Event{Type: socketmode.EventTypeConnecting})
	c.handleEvent(socketmode.Event{Type: socketmode.EventTypeConnected})
	c.handleEvent(socketmode.Event{Type: socketmode.EventTypeConnectionError})
}

func TestHandleEventsAPI_WrongPayload(t *testing.T) {
	c := &Client{logger: slog.Default()}

	// Non-EventsAPIEvent data should be silently ignored (returns before Ack).
	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: "not an events api payload",
	}
	c.handleEventsAPI(evt)
}

func TestNewClient(t *testing.T) {
	c := NewClient("xapp-test", "xoxb-test", nil, nil)
	if c.api == nil {
		t.Fatal("api client should not be nil")
	}
	if c.sm == nil {
		t.Fatal("socketmode client should not be nil")
	}
	if c.logger == nil {
		t.Fatal("logger should default to slog.Default()")
	}
}
