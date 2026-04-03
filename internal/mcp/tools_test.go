package mcp

import (
	"fmt"
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
	panes        map[string]*fakePaneHandle
	shuttingDown bool
	lifecycleLog []string // records "restart:eng1", "stop:eng1", etc.
	lifecycleErr error    // if set, lifecycle methods return this error
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

func (h *fakePaneHost) RestartAgent(name string) error {
	if h.lifecycleErr != nil {
		return h.lifecycleErr
	}
	if _, ok := h.panes[name]; !ok {
		return fmt.Errorf("agent %q not found", name)
	}
	h.lifecycleLog = append(h.lifecycleLog, "restart:"+name)
	return nil
}

func (h *fakePaneHost) StopAgent(name string) error {
	if h.lifecycleErr != nil {
		return h.lifecycleErr
	}
	if _, ok := h.panes[name]; !ok {
		return fmt.Errorf("agent %q not found", name)
	}
	h.lifecycleLog = append(h.lifecycleLog, "stop:"+name)
	return nil
}

func (h *fakePaneHost) StartAgent(name string) error {
	if h.lifecycleErr != nil {
		return h.lifecycleErr
	}
	if _, ok := h.panes[name]; !ok {
		return fmt.Errorf("agent %q not found", name)
	}
	h.lifecycleLog = append(h.lifecycleLog, "start:"+name)
	return nil
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

// Lifecycle tool tests (restart, stop, start).

func TestHandleLifecycle_Restart(t *testing.T) {
	pane := &fakePaneHandle{name: "eng1"}
	host := newFakeHost(pane)

	_, out, err := handleLifecycle(host, AgentInput{Agent: "eng1"}, "restart")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != "restarted" {
		t.Errorf("status = %q, want %q", out.Status, "restarted")
	}
	if len(host.lifecycleLog) != 1 || host.lifecycleLog[0] != "restart:eng1" {
		t.Errorf("lifecycle log = %v", host.lifecycleLog)
	}
}

func TestHandleLifecycle_Stop(t *testing.T) {
	pane := &fakePaneHandle{name: "qa1"}
	host := newFakeHost(pane)

	_, out, err := handleLifecycle(host, AgentInput{Agent: "qa1"}, "stop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != "stopped" {
		t.Errorf("status = %q, want %q", out.Status, "stopped")
	}
	if len(host.lifecycleLog) != 1 || host.lifecycleLog[0] != "stop:qa1" {
		t.Errorf("lifecycle log = %v", host.lifecycleLog)
	}
}

func TestHandleLifecycle_Start(t *testing.T) {
	pane := &fakePaneHandle{name: "eng2"}
	host := newFakeHost(pane)

	_, out, err := handleLifecycle(host, AgentInput{Agent: "eng2"}, "start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != "started" {
		t.Errorf("status = %q, want %q", out.Status, "started")
	}
	if len(host.lifecycleLog) != 1 || host.lifecycleLog[0] != "start:eng2" {
		t.Errorf("lifecycle log = %v", host.lifecycleLog)
	}
}

func TestHandleLifecycle_InvalidAgent(t *testing.T) {
	host := newFakeHost()

	for _, action := range []string{"restart", "stop", "start"} {
		_, _, err := handleLifecycle(host, AgentInput{Agent: "nonexistent"}, action)
		if err == nil {
			t.Errorf("%s: expected error for nonexistent agent", action)
		}
	}
}

func TestHandleLifecycle_MissingAgent(t *testing.T) {
	host := newFakeHost()

	for _, action := range []string{"restart", "stop", "start"} {
		_, _, err := handleLifecycle(host, AgentInput{}, action)
		if err == nil {
			t.Errorf("%s: expected error for missing agent", action)
		}
	}
}

func TestHandleLifecycle_HostError(t *testing.T) {
	pane := &fakePaneHandle{name: "eng1"}
	host := newFakeHost(pane)
	host.lifecycleErr = fmt.Errorf("process exited unexpectedly")

	_, _, err := handleLifecycle(host, AgentInput{Agent: "eng1"}, "restart")
	if err == nil {
		t.Fatal("expected error from host")
	}
	if err.Error() != "process exited unexpectedly" {
		t.Errorf("error = %q", err.Error())
	}
}
