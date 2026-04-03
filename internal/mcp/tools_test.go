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
	addErr       error    // if set, AddAgent returns this error
	removeErr    error    // if set, RemoveAgent returns this error
	scheduleErr  error    // if set, ScheduleSend returns this error
	scheduleLog  []string // records "schedule:eng1:5m" etc.
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

func (h *fakePaneHost) AddAgent(name string) error {
	if h.addErr != nil {
		return h.addErr
	}
	if _, ok := h.panes[name]; ok {
		return fmt.Errorf("agent %q already exists", name)
	}
	h.panes[name] = &fakePaneHandle{name: name}
	h.lifecycleLog = append(h.lifecycleLog, "add:"+name)
	return nil
}

func (h *fakePaneHost) RemoveAgent(name string) error {
	if h.removeErr != nil {
		return h.removeErr
	}
	if _, ok := h.panes[name]; !ok {
		return fmt.Errorf("agent %q not found", name)
	}
	if len(h.panes) == 1 {
		return fmt.Errorf("cannot remove last agent")
	}
	delete(h.panes, name)
	h.lifecycleLog = append(h.lifecycleLog, "remove:"+name)
	return nil
}

func (h *fakePaneHost) ScheduleSend(agent, message, delay string) (string, error) {
	if h.scheduleErr != nil {
		return "", h.scheduleErr
	}
	id := fmt.Sprintf("at-%d", len(h.scheduleLog))
	h.scheduleLog = append(h.scheduleLog, fmt.Sprintf("schedule:%s:%s", agent, delay))
	return id, nil
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

// Add tool tests.

func TestHandleAdd_Success(t *testing.T) {
	pane := &fakePaneHandle{name: "eng1"}
	host := newFakeHost(pane)

	_, out, err := handleAdd(host, AddInput{Role: "eng3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != "added" {
		t.Errorf("status = %q, want added", out.Status)
	}
	if out.Agent != "eng3" {
		t.Errorf("agent = %q, want eng3", out.Agent)
	}
	if len(host.lifecycleLog) != 1 || host.lifecycleLog[0] != "add:eng3" {
		t.Errorf("lifecycle log = %v", host.lifecycleLog)
	}
}

func TestHandleAdd_MissingRole(t *testing.T) {
	host := newFakeHost()
	_, _, err := handleAdd(host, AddInput{})
	if err == nil {
		t.Fatal("expected error for missing role")
	}
}

func TestHandleAdd_AlreadyExists(t *testing.T) {
	pane := &fakePaneHandle{name: "eng1"}
	host := newFakeHost(pane)

	_, _, err := handleAdd(host, AddInput{Role: "eng1"})
	if err == nil {
		t.Fatal("expected error for duplicate agent")
	}
	if got := err.Error(); got != `agent "eng1" already exists` {
		t.Errorf("error = %q", got)
	}
}

func TestHandleAdd_HostError(t *testing.T) {
	host := newFakeHost()
	host.addErr = fmt.Errorf("workspace not found")

	_, _, err := handleAdd(host, AddInput{Role: "eng3"})
	if err == nil {
		t.Fatal("expected error from host")
	}
	if err.Error() != "workspace not found" {
		t.Errorf("error = %q", err.Error())
	}
}

// Remove tool tests.

func TestHandleRemove_Success(t *testing.T) {
	host := newFakeHost(
		&fakePaneHandle{name: "eng1"},
		&fakePaneHandle{name: "eng2"},
	)

	_, out, err := handleRemove(host, RemoveInput{Agent: "eng1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != "removed" {
		t.Errorf("status = %q, want removed", out.Status)
	}
}

func TestHandleRemove_MissingAgent(t *testing.T) {
	host := newFakeHost()
	_, _, err := handleRemove(host, RemoveInput{})
	if err == nil {
		t.Fatal("expected error for missing agent")
	}
}

func TestHandleRemove_NotFound(t *testing.T) {
	host := newFakeHost(&fakePaneHandle{name: "eng1"})

	_, _, err := handleRemove(host, RemoveInput{Agent: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if got := err.Error(); got != `agent "nonexistent" not found` {
		t.Errorf("error = %q", got)
	}
}

func TestHandleRemove_LastPane(t *testing.T) {
	host := newFakeHost(&fakePaneHandle{name: "eng1"})

	_, _, err := handleRemove(host, RemoveInput{Agent: "eng1"})
	if err == nil {
		t.Fatal("expected error for removing last agent")
	}
	if got := err.Error(); got != "cannot remove last agent" {
		t.Errorf("error = %q", got)
	}
}

func TestHandleRemove_HostError(t *testing.T) {
	host := newFakeHost(&fakePaneHandle{name: "eng1"}, &fakePaneHandle{name: "eng2"})
	host.removeErr = fmt.Errorf("removal blocked")

	_, _, err := handleRemove(host, RemoveInput{Agent: "eng1"})
	if err == nil {
		t.Fatal("expected error from host")
	}
}

// At (schedule) tool tests.

func TestHandleAt_Success(t *testing.T) {
	host := newFakeHost(&fakePaneHandle{name: "eng1"})

	_, out, err := handleAt(host, AtInput{Agent: "eng1", Message: "make test", Delay: "5m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Status != "scheduled" {
		t.Errorf("status = %q, want scheduled", out.Status)
	}
	if out.TimerID != "at-0" {
		t.Errorf("timer_id = %q, want at-0", out.TimerID)
	}
	if len(host.scheduleLog) != 1 || host.scheduleLog[0] != "schedule:eng1:5m" {
		t.Errorf("schedule log = %v", host.scheduleLog)
	}
}

func TestHandleAt_MissingAgent(t *testing.T) {
	host := newFakeHost()
	_, _, err := handleAt(host, AtInput{Message: "hello", Delay: "5m"})
	if err == nil {
		t.Fatal("expected error for missing agent")
	}
}

func TestHandleAt_MissingMessage(t *testing.T) {
	host := newFakeHost(&fakePaneHandle{name: "eng1"})
	_, _, err := handleAt(host, AtInput{Agent: "eng1", Delay: "5m"})
	if err == nil {
		t.Fatal("expected error for missing message")
	}
}

func TestHandleAt_MissingDelay(t *testing.T) {
	host := newFakeHost(&fakePaneHandle{name: "eng1"})
	_, _, err := handleAt(host, AtInput{Agent: "eng1", Message: "hello"})
	if err == nil {
		t.Fatal("expected error for missing delay")
	}
}

func TestHandleAt_InvalidDelay(t *testing.T) {
	host := newFakeHost(&fakePaneHandle{name: "eng1"})

	_, _, err := handleAt(host, AtInput{Agent: "eng1", Message: "hello", Delay: "not-a-duration"})
	if err == nil {
		t.Fatal("expected error for invalid delay")
	}
	if got := err.Error(); !contains(got, "invalid delay") {
		t.Errorf("error = %q, want 'invalid delay'", got)
	}
}

func TestHandleAt_HostError(t *testing.T) {
	host := newFakeHost(&fakePaneHandle{name: "eng1"})
	host.scheduleErr = fmt.Errorf("timer store not initialized")

	_, _, err := handleAt(host, AtInput{Agent: "eng1", Message: "hello", Delay: "5m"})
	if err == nil {
		t.Fatal("expected error from host")
	}
}

func TestHandleAt_VariousDelayFormats(t *testing.T) {
	host := newFakeHost(&fakePaneHandle{name: "eng1"})

	for _, delay := range []string{"30s", "1h", "100ms", "2h30m"} {
		_, out, err := handleAt(host, AtInput{Agent: "eng1", Message: "test", Delay: delay})
		if err != nil {
			t.Errorf("delay %q: unexpected error: %v", delay, err)
		}
		if out.Status != "scheduled" {
			t.Errorf("delay %q: status = %q", delay, out.Status)
		}
	}
}

// contains is a test helper for substring matching.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
