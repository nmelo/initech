package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestAuth_UnauthenticatedAPIPanesRejected is the ini-mfmh regression: an
// unauthenticated GET /api/panes must be rejected with 401, never enumerating
// panes.
func TestAuth_UnauthenticatedAPIPanesRejected(t *testing.T) {
	srv := NewServer(0, &fakeLister{ok: true}, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/panes", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /api/panes = %d, want 401", w.Code)
	}
}

// TestAuth_WrongTokenRejected verifies a wrong token is rejected (constant-time
// compare, no bypass).
func TestAuth_WrongTokenRejected(t *testing.T) {
	srv := NewServer(0, &fakeLister{ok: true}, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/panes", nil)
	req.Header.Set("Authorization", "Bearer not-the-token")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-token /api/panes = %d, want 401", w.Code)
	}
}

// TestAuth_ValidTokenAllowsAPIPanes verifies the correct token (via Bearer
// header) is accepted.
func TestAuth_ValidTokenAllowsAPIPanes(t *testing.T) {
	srv := NewServer(0, &fakeLister{ok: true}, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/panes", nil)
	req.Header.Set("Authorization", "Bearer "+srv.Token())
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("authenticated /api/panes = %d, want 200", w.Code)
	}
}

// TestAuth_ValidTokenViaQueryParam verifies the token can be supplied as a
// ?token= query param (needed for browser WebSocket connections).
func TestAuth_ValidTokenViaQueryParam(t *testing.T) {
	srv := NewServer(0, &fakeLister{ok: true}, nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/panes?token="+srv.Token(), nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("query-token /api/panes = %d, want 200", w.Code)
	}
}

// TestAuth_UnauthenticatedPaneWSRejected_NoPTYWrite is the core RCE guard
// (ini-mfmh): an unauthenticated WebSocket to /ws/pane is rejected at the
// handshake and NEVER reaches WriteToPTY.
func TestAuth_UnauthenticatedPaneWSRejected_NoPTYWrite(t *testing.T) {
	sub := newFakeSubscriber()
	writer := newFakeWriter()
	srv := NewServer(0, &fakeLister{ok: true}, sub, nil, nil, writer, nil, nil)

	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, resp, err := websocket.Dial(ctx, ts.URL+"/ws/pane/eng1", nil)
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("unauthenticated /ws/pane dial succeeded, want rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth /ws/pane handshake status = %v, want 401", resp)
	}
	// The subscriber must never have been wired and no PTY write can occur.
	if got := writer.getWrites("eng1"); len(got) != 0 {
		t.Errorf("WriteToPTY reached without auth: %v (RCE not closed)", got)
	}
}

// TestAuth_AuthenticatedPaneWSReachesWriter verifies an authenticated client
// still works end-to-end: WS input relays to WriteToPTY.
func TestAuth_AuthenticatedPaneWSReachesWriter(t *testing.T) {
	sub := newFakeSubscriber()
	writer := newFakeWriter()
	srv := NewServer(0, &fakeLister{ok: true}, sub, nil, nil, writer, nil, nil)

	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + srv.Token()}},
	}
	conn, _, err := websocket.Dial(ctx, ts.URL+"/ws/pane/eng1", opts)
	if err != nil {
		t.Fatalf("authenticated dial failed: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageBinary, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(writer.getWrites("eng1")) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := writer.getWrites("eng1")
	if len(got) == 0 || string(got[0]) != "hello" {
		t.Errorf("authenticated WS input not relayed to PTY, got %v", got)
	}
}
