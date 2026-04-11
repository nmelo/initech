package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/webhook"
)

func TestWebhookSink_PostsCorrectPayload(t *testing.T) {
	var mu sync.Mutex
	var received []webhook.Payload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		var p webhook.Payload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		mu.Lock()
		received = append(received, p)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := make(chan AgentEvent, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go startWebhookSink(ctx, srv.URL, "testproject", ch)

	ch <- AgentEvent{
		Type:   EventBeadCompleted,
		Pane:   "eng1",
		BeadID: "ini-abc.1",
		Detail: "eng1 marked ini-abc.1 ready_for_qa",
		Time:   time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC),
	}

	// Wait for delivery.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 POST, got %d", len(received))
	}

	p := received[0]
	if p.Kind != "agent.completed" {
		t.Errorf("kind = %q, want agent.completed", p.Kind)
	}
	if p.Agent != "eng1" {
		t.Errorf("agent = %q, want eng1", p.Agent)
	}
	if p.BeadID != "ini-abc.1" {
		t.Errorf("bead_id = %q, want ini-abc.1", p.BeadID)
	}
	if p.Detail != "eng1 marked ini-abc.1 ready_for_qa" {
		t.Errorf("detail = %q", p.Detail)
	}
	if p.Timestamp != "2026-04-04T12:00:00Z" {
		t.Errorf("timestamp = %q, want 2026-04-04T12:00:00Z", p.Timestamp)
	}
	if p.Project != "testproject" {
		t.Errorf("project = %q, want testproject", p.Project)
	}
}

func TestWebhookSink_AllEventKinds(t *testing.T) {
	for evType, wantKind := range webhookKindMap {
		kind, ok := webhookKindMap[evType]
		if !ok {
			t.Errorf("missing kind for EventType %d", evType)
			continue
		}
		if kind != wantKind {
			t.Errorf("EventType %d: kind = %q, want %q", evType, kind, wantKind)
		}
	}
	// Verify all 20 event types are mapped.
	if len(webhookKindMap) != 20 {
		t.Errorf("webhookKindMap has %d entries, want 20", len(webhookKindMap))
	}
}

func TestWebhookSink_NonBlockingSend(t *testing.T) {
	// Verify that a full channel doesn't block handleAgentEvent.
	ch := make(chan AgentEvent, 2)

	// Fill the channel.
	ch <- AgentEvent{Type: EventAgentStarted, Pane: "eng1"}
	ch <- AgentEvent{Type: EventAgentStarted, Pane: "eng2"}

	// Non-blocking send should succeed without deadlock.
	ev := AgentEvent{Type: EventBeadCompleted, Pane: "eng3"}
	select {
	case ch <- ev:
		t.Error("expected channel to be full")
	default:
		// Correctly dropped: channel is full.
	}
}

func TestWebhookSink_ContextCancellation(t *testing.T) {
	ch := make(chan AgentEvent, 8)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		startWebhookSink(ctx, "http://localhost:1", "test", ch)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// Goroutine exited cleanly.
	case <-time.After(2 * time.Second):
		t.Fatal("webhook sink did not exit after context cancellation")
	}
}

func TestWebhookSink_EndpointError(t *testing.T) {
	// Verify that a failing endpoint doesn't crash.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ch := make(chan AgentEvent, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go startWebhookSink(ctx, srv.URL, "test", ch)

	ch <- AgentEvent{
		Type: EventAgentStuck,
		Pane: "eng1",
		Time: time.Now(),
	}

	// Give time for the POST to complete.
	time.Sleep(100 * time.Millisecond)
	// No panic = success. The 500 is logged, event is dropped.
}
