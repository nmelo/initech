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
	c := NewClient("xapp-test", "xoxb-test", nil, nil, nil)
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

func TestIsAuthorized_EmptyList(t *testing.T) {
	c := NewClient("xapp-test", "xoxb-test", nil, nil, nil)
	if !c.isAuthorized("U12345") {
		t.Error("empty allowed list should allow all users")
	}
}

func TestIsAuthorized_AllowedUser(t *testing.T) {
	c := NewClient("xapp-test", "xoxb-test", nil, []string{"U12345", "U67890"}, nil)
	if !c.isAuthorized("U12345") {
		t.Error("allowed user should be authorized")
	}
	if !c.isAuthorized("U67890") {
		t.Error("allowed user should be authorized")
	}
}

func TestIsAuthorized_DeniedUser(t *testing.T) {
	c := NewClient("xapp-test", "xoxb-test", nil, []string{"U12345"}, nil)
	if c.isAuthorized("UOTHER") {
		t.Error("non-allowed user should be denied")
	}
}
