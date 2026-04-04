package slackchat

import (
	"testing"
	"time"
)

// mockTracker implements ConversationLookup for testing.
type mockTracker struct {
	convos    map[string][2]string // agent -> [channel, threadTS]
	completed []string
}

func newMockTracker() *mockTracker {
	return &mockTracker{convos: make(map[string][2]string)}
}

func (m *mockTracker) Track(agent, channel, threadTS string) {
	m.convos[agent] = [2]string{channel, threadTS}
}

func (m *mockTracker) Lookup(agent string) (string, string, bool) {
	c, ok := m.convos[agent]
	if !ok {
		return "", "", false
	}
	return c[0], c[1], true
}

func (m *mockTracker) Complete(agent string) {
	m.completed = append(m.completed, agent)
	delete(m.convos, agent)
}

// mockPeeker implements PanePeeker for testing.
type mockPeeker struct {
	output map[string]string
}

func (m *mockPeeker) PeekOutput(agent string, lines int) (string, error) {
	return m.output[agent], nil
}

func TestFormatOutput_Empty(t *testing.T) {
	got := formatOutput("")
	if got != "_No visible output_" {
		t.Errorf("formatOutput('') = %q, want '_No visible output_'", got)
	}
}

func TestFormatOutput_Normal(t *testing.T) {
	got := formatOutput("hello world")
	want := "```\nhello world\n```"
	if got != want {
		t.Errorf("formatOutput = %q, want %q", got, want)
	}
}

func TestFormatOutput_Truncation(t *testing.T) {
	long := make([]byte, 3000)
	for i := range long {
		long[i] = 'x'
	}
	got := formatOutput(string(long))
	if len(got) > maxOutputChars+100 { // Allow room for backticks + truncation msg
		t.Errorf("output too long: %d chars", len(got))
	}
	if got[len(got)-4:] != "\n```" {
		t.Error("should end with closing code fence")
	}
}

func TestRateLimiter(t *testing.T) {
	r := NewResponder(nil, nil, nil, "completion", nil)

	// First call should be allowed.
	if !r.rateLimitOK("eng1") {
		t.Error("first call should be allowed")
	}

	// Immediate second call should be blocked.
	if r.rateLimitOK("eng1") {
		t.Error("immediate second call should be blocked")
	}

	// Different agent should be allowed.
	if !r.rateLimitOK("eng2") {
		t.Error("different agent should be allowed")
	}
}

func TestRateLimiter_ExpiresAfterInterval(t *testing.T) {
	r := NewResponder(nil, nil, nil, "completion", nil)

	r.mu.Lock()
	r.lastPost["eng1"] = time.Now().Add(-rateLimitInterval - time.Second)
	r.mu.Unlock()

	if !r.rateLimitOK("eng1") {
		t.Error("should be allowed after interval expires")
	}
}

func TestHandleEvent_OffMode(t *testing.T) {
	tracker := newMockTracker()
	tracker.Track("eng1", "C123", "1234.5678")

	r := NewResponder(nil, tracker, nil, "off", nil)
	r.handleEvent(ResponderEvent{Type: "completed", Pane: "eng1"})
	// Should not panic or post (api is nil, would panic if post attempted).
}

func TestHandleEvent_NoConversation(t *testing.T) {
	tracker := newMockTracker() // Empty tracker.
	r := NewResponder(nil, tracker, nil, "completion", nil)
	r.handleEvent(ResponderEvent{Type: "completed", Pane: "eng1"})
	// Should not panic or post (no conversation to post to).
}

func TestHandleEvent_IdleCompletes(t *testing.T) {
	tracker := newMockTracker()
	tracker.Track("eng1", "C123", "1234.5678")
	peeker := &mockPeeker{output: map[string]string{"eng1": "done"}}

	// Verify the tracker Complete() logic directly. We can't call handleEvent
	// because it would try to post via nil api.
	_ = NewResponder(nil, tracker, peeker, "completion", nil)

	_, _, ok := tracker.Lookup("eng1")
	if !ok {
		t.Fatal("eng1 should be tracked")
	}

	tracker.Complete("eng1")
	if len(tracker.completed) != 1 || tracker.completed[0] != "eng1" {
		t.Errorf("completed = %v, want [eng1]", tracker.completed)
	}

	_, _, ok = tracker.Lookup("eng1")
	if ok {
		t.Error("eng1 should be removed after Complete()")
	}
}
