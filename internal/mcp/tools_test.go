package mcp

import (
	"testing"
)

// fakePaneHandle implements PaneHandle for testing.
type fakePaneHandle struct {
	name    string
	content string
	sent    []sentMessage
}

type sentMessage struct {
	text  string
	enter bool
}

func (f *fakePaneHandle) Name() string { return f.name }

func (f *fakePaneHandle) PeekContent(lines int) string { return f.content }

func (f *fakePaneHandle) SendText(text string, enter bool) {
	f.sent = append(f.sent, sentMessage{text: text, enter: enter})
}

// fakePaneHost implements PaneHost for testing.
type fakePaneHost struct {
	panes      map[string]*fakePaneHandle
	shuttingDown bool
}

func newFakeHost(panes ...*fakePaneHandle) *fakePaneHost {
	h := &fakePaneHost{panes: make(map[string]*fakePaneHandle)}
	for _, p := range panes {
		h.panes[p.name] = p
	}
	return h
}

func (h *fakePaneHost) FindPane(name string) (PaneHandle, bool) {
	if h.shuttingDown {
		return nil, false
	}
	p := h.panes[name]
	if p == nil {
		return nil, true
	}
	return p, true
}

func TestHandlePeek_ValidAgent(t *testing.T) {
	pane := &fakePaneHandle{name: "eng1", content: "hello world\nprompt>"}
	host := newFakeHost(pane)

	_, out, err := handlePeek(host, PeekInput{Agent: "eng1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Content != "hello world\nprompt>" {
		t.Errorf("content = %q, want %q", out.Content, "hello world\nprompt>")
	}
}

func TestHandlePeek_DefaultLines(t *testing.T) {
	// Verify that lines defaults to 50 (passed to PeekContent).
	// The fake always returns the same content, but we verify no error.
	pane := &fakePaneHandle{name: "eng1", content: "output"}
	host := newFakeHost(pane)

	_, out, err := handlePeek(host, PeekInput{Agent: "eng1", Lines: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Content != "output" {
		t.Errorf("content = %q", out.Content)
	}
}

func TestHandlePeek_InvalidAgent(t *testing.T) {
	host := newFakeHost()

	_, _, err := handlePeek(host, PeekInput{Agent: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if got := err.Error(); got != `agent "nonexistent" not found` {
		t.Errorf("error = %q", got)
	}
}

func TestHandlePeek_MissingAgent(t *testing.T) {
	host := newFakeHost()

	_, _, err := handlePeek(host, PeekInput{})
	if err == nil {
		t.Fatal("expected error for missing agent")
	}
}

func TestHandlePeek_ShuttingDown(t *testing.T) {
	host := newFakeHost()
	host.shuttingDown = true

	_, _, err := handlePeek(host, PeekInput{Agent: "eng1"})
	if err == nil {
		t.Fatal("expected error when shutting down")
	}
}

func TestHandleSend_ValidAgent(t *testing.T) {
	pane := &fakePaneHandle{name: "eng1"}
	host := newFakeHost(pane)

	_, out, err := handleSend(host, SendInput{Agent: "eng1", Message: "make test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != "sent" {
		t.Errorf("status = %q, want %q", out.Status, "sent")
	}
	if len(pane.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(pane.sent))
	}
	if pane.sent[0].text != "make test" {
		t.Errorf("text = %q", pane.sent[0].text)
	}
	if !pane.sent[0].enter {
		t.Error("enter should default to true")
	}
}

func TestHandleSend_EnterFalse(t *testing.T) {
	pane := &fakePaneHandle{name: "eng1"}
	host := newFakeHost(pane)

	enterFalse := false
	_, _, err := handleSend(host, SendInput{Agent: "eng1", Message: "partial", Enter: &enterFalse})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pane.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(pane.sent))
	}
	if pane.sent[0].enter {
		t.Error("enter should be false")
	}
}

func TestHandleSend_InvalidAgent(t *testing.T) {
	host := newFakeHost()

	_, _, err := handleSend(host, SendInput{Agent: "nonexistent", Message: "hello"})
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if got := err.Error(); got != `agent "nonexistent" not found` {
		t.Errorf("error = %q", got)
	}
}

func TestHandleSend_MissingAgent(t *testing.T) {
	host := newFakeHost()

	_, _, err := handleSend(host, SendInput{Message: "hello"})
	if err == nil {
		t.Fatal("expected error for missing agent")
	}
}

func TestHandleSend_MissingMessage(t *testing.T) {
	pane := &fakePaneHandle{name: "eng1"}
	host := newFakeHost(pane)

	_, _, err := handleSend(host, SendInput{Agent: "eng1"})
	if err == nil {
		t.Fatal("expected error for missing message")
	}
}

func TestHandleSend_ShuttingDown(t *testing.T) {
	host := newFakeHost()
	host.shuttingDown = true

	_, _, err := handleSend(host, SendInput{Agent: "eng1", Message: "hello"})
	if err == nil {
		t.Fatal("expected error when shutting down")
	}
}
