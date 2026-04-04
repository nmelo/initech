package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeLister struct {
	panes []PaneInfo
	ok    bool
}

func (f *fakeLister) AllPanes() ([]PaneInfo, bool) {
	return f.panes, f.ok
}

func TestHandlePanes_ReturnsJSON(t *testing.T) {
	lister := &fakeLister{
		panes: []PaneInfo{
			{Name: "eng1", Activity: "coding", Alive: true, Visible: true},
			{Name: "qa1", Activity: "idle", Alive: false, Visible: false},
		},
		ok: true,
	}
	srv := NewServer(0, lister, nil, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/panes", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}

	var got []PaneInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(got))
	}
	if got[0].Name != "eng1" || got[1].Name != "qa1" {
		t.Errorf("unexpected panes: %+v", got)
	}
}

func TestHandlePanes_ShuttingDown(t *testing.T) {
	lister := &fakeLister{ok: false}
	srv := NewServer(0, lister, nil, nil, nil, nil)

	req := httptest.NewRequest("GET", "/api/panes", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestServesIndex(t *testing.T) {
	lister := &fakeLister{ok: true}
	srv := NewServer(0, lister, nil, nil, nil, nil)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if len(body) == 0 {
		t.Fatal("expected non-empty body for index.html")
	}
	if !strings.Contains(body, "xterm") {
		t.Error("expected index.html to reference xterm.js")
	}
	if !strings.Contains(body, "/ws/pane/") {
		t.Error("expected index.html to reference WebSocket endpoint")
	}
	if !strings.Contains(body, "status-bar") {
		t.Error("expected index.html to contain status bar")
	}
	if !strings.Contains(body, "/ws/state") {
		t.Error("expected index.html to reference state WebSocket endpoint")
	}
}

func TestStartAndShutdown(t *testing.T) {
	lister := &fakeLister{
		panes: []PaneInfo{{Name: "test", Alive: true, Visible: true}},
		ok:    true,
	}
	srv := NewServer(0, lister, nil, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	// Wait for the server to be reachable.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", srv.Addr())
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Hit the API on the real listener.
	resp, err := http.Get("http://" + srv.Addr() + "/api/panes")
	if err != nil {
		t.Fatalf("GET /api/panes: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Shutdown via context cancellation.
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after shutdown")
	}
}

func TestPortConflict(t *testing.T) {
	// Occupy a port on all interfaces (matching the server's 0.0.0.0 bind).
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	lister := &fakeLister{ok: true}
	srv := NewServer(port, lister, nil, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = srv.Start(ctx)
	if err == nil {
		t.Fatal("expected error for port conflict")
	}
}
