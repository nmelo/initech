// Tests for the WebSocket PTY streaming endpoint (GET /ws/pane/{name}).
package web

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeSubscriber implements PaneSubscriber for testing.
type fakeSubscriber struct {
	mu    sync.Mutex
	chans map[string]map[string]chan []byte // paneName -> subID -> channel
}

func newFakeSubscriber() *fakeSubscriber {
	return &fakeSubscriber{chans: make(map[string]map[string]chan []byte)}
}

func (f *fakeSubscriber) SubscribePane(paneName, subID string) (chan []byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if paneName == "missing" {
		return nil, false
	}
	if f.chans[paneName] == nil {
		f.chans[paneName] = make(map[string]chan []byte)
	}
	ch := make(chan []byte, 64)
	f.chans[paneName][subID] = ch
	return ch, true
}

func (f *fakeSubscriber) UnsubscribePane(paneName, subID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if subs, ok := f.chans[paneName]; ok {
		delete(subs, subID)
	}
}

// subscriberCount returns the total number of active subscriptions across all panes.
func (f *fakeSubscriber) subscriberCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, subs := range f.chans {
		n += len(subs)
	}
	return n
}

// sendToAll sends data to all subscribers of the named pane.
func (f *fakeSubscriber) sendToAll(paneName string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.chans[paneName] {
		ch <- data
	}
}

func TestWS_ReceivesBytes(t *testing.T) {
	lister := &fakeLister{ok: true}
	sub := newFakeSubscriber()
	srv := NewServer(0, lister, sub, nil, nil)

	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, ts.URL+"/ws/pane/eng1", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Send bytes through the fake subscriber.
	sub.sendToAll("eng1", []byte("hello PTY"))

	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageBinary {
		t.Errorf("message type = %v, want Binary", typ)
	}
	if string(data) != "hello PTY" {
		t.Errorf("data = %q, want %q", data, "hello PTY")
	}
}

func TestWS_UnknownPane_404(t *testing.T) {
	lister := &fakeLister{ok: true}
	sub := newFakeSubscriber()
	srv := NewServer(0, lister, sub, nil, nil)

	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, ts.URL+"/ws/pane/missing", nil)
	if err == nil {
		t.Fatal("expected error for missing pane")
	}
	if resp != nil && resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestWS_NoSubscriber_501(t *testing.T) {
	lister := &fakeLister{ok: true}
	srv := NewServer(0, lister, nil, nil, nil)

	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, ts.URL+"/ws/pane/eng1", nil)
	if err == nil {
		t.Fatal("expected error when subscriber is nil")
	}
	if resp != nil && resp.StatusCode != 501 {
		t.Errorf("status = %d, want 501", resp.StatusCode)
	}
}

func TestWS_DisconnectCleansUp(t *testing.T) {
	lister := &fakeLister{ok: true}
	sub := newFakeSubscriber()
	srv := NewServer(0, lister, sub, nil, nil)

	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, ts.URL+"/ws/pane/eng1", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Verify subscription exists.
	if sub.subscriberCount() != 1 {
		t.Fatalf("expected 1 subscriber, got %d", sub.subscriberCount())
	}

	// Close the connection.
	conn.Close(websocket.StatusNormalClosure, "done")

	// Wait for the server handler to clean up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sub.subscriberCount() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("subscriber not cleaned up; count = %d", sub.subscriberCount())
}

func TestWS_MultipleConnections(t *testing.T) {
	lister := &fakeLister{ok: true}
	sub := newFakeSubscriber()
	srv := NewServer(0, lister, sub, nil, nil)

	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn1, _, err := websocket.Dial(ctx, ts.URL+"/ws/pane/eng1", nil)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer conn1.CloseNow()

	conn2, _, err := websocket.Dial(ctx, ts.URL+"/ws/pane/eng1", nil)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer conn2.CloseNow()

	if sub.subscriberCount() != 2 {
		t.Fatalf("expected 2 subscribers, got %d", sub.subscriberCount())
	}

	sub.sendToAll("eng1", []byte("broadcast"))

	for i, conn := range []*websocket.Conn{conn1, conn2} {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("conn%d read: %v", i+1, err)
		}
		if string(data) != "broadcast" {
			t.Errorf("conn%d data = %q, want %q", i+1, data, "broadcast")
		}
	}
}

func TestWS_ChannelClosed_ServerCloses(t *testing.T) {
	lister := &fakeLister{ok: true}
	sub := newFakeSubscriber()
	srv := NewServer(0, lister, sub, nil, nil)

	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, ts.URL+"/ws/pane/eng1", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Close the subscriber channel (simulates pane teardown).
	sub.mu.Lock()
	for _, ch := range sub.chans["eng1"] {
		close(ch)
	}
	sub.mu.Unlock()

	// Next read should fail because the server closes the connection.
	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected error after channel closed")
	}
}
