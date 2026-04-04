package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeStateProvider implements StateProvider for testing.
type fakeStateProvider struct {
	snap StateSnapshot
	ok   bool
}

func (f *fakeStateProvider) CurrentState() (StateSnapshot, bool) {
	return f.snap, f.ok
}

func TestStateWS_InitialSnapshot(t *testing.T) {
	lister := &fakeLister{ok: true}
	sp := &fakeStateProvider{
		snap: StateSnapshot{
			Layout: LayoutInfo{Mode: "grid", Cols: 2, Rows: 2, Focused: "eng1"},
			Panes: []PaneState{
				{Name: "eng1", Activity: "running", Alive: true, Visible: true, Order: 0},
				{Name: "qa1", Activity: "idle", Alive: true, Visible: true, BeadID: "ini-abc", Order: 1},
			},
		},
		ok: true,
	}
	srv := NewServer(0, lister, nil, sp, nil)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws/state", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Read initial snapshot.
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Errorf("type = %v, want Text", typ)
	}

	var snap StateSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if snap.Layout.Mode != "grid" || snap.Layout.Cols != 2 {
		t.Errorf("layout = %+v", snap.Layout)
	}
	if len(snap.Panes) != 2 {
		t.Fatalf("panes = %d, want 2", len(snap.Panes))
	}
	if snap.Panes[0].Name != "eng1" || snap.Panes[1].BeadID != "ini-abc" {
		t.Errorf("panes = %+v", snap.Panes)
	}
}

func TestStateWS_PushesOnChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow debounce test in short mode")
	}

	lister := &fakeLister{ok: true}
	sp := &fakeStateProvider{
		snap: StateSnapshot{
			Layout: LayoutInfo{Mode: "grid", Cols: 2, Rows: 2, Focused: "eng1"},
			Panes: []PaneState{
				{Name: "eng1", Activity: "running", Alive: true, Visible: true, Order: 0},
			},
		},
		ok: true,
	}
	srv := NewServer(0, lister, nil, sp, nil)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws/state", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Read initial snapshot.
	conn.Read(ctx)

	// Change state.
	sp.snap.Panes[0].Activity = "idle"

	// Wait for debounce tick (slightly over 500ms).
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read update: %v", err)
	}

	var snap StateSnapshot
	json.Unmarshal(data, &snap)
	if snap.Panes[0].Activity != "idle" {
		t.Errorf("expected activity=idle after change, got %q", snap.Panes[0].Activity)
	}
}

func TestStateWS_NoProviderReturns501(t *testing.T) {
	lister := &fakeLister{ok: true}
	srv := NewServer(0, lister, nil, nil, nil)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws/state", nil)
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	if resp != nil && resp.StatusCode != 501 {
		t.Errorf("status = %d, want 501", resp.StatusCode)
	}
}

func TestStateWS_DebounceSkipsDuplicates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow debounce test in short mode")
	}

	lister := &fakeLister{ok: true}
	sp := &fakeStateProvider{
		snap: StateSnapshot{
			Layout: LayoutInfo{Mode: "focus", Cols: 1, Rows: 1, Focused: "eng1"},
			Panes:  []PaneState{{Name: "eng1", Activity: "running", Alive: true, Visible: true, Order: 0}},
		},
		ok: true,
	}
	srv := NewServer(0, lister, nil, sp, nil)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws/state", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Read initial.
	conn.Read(ctx)

	// Don't change state. Wait 1.5s (3 ticks). Should not receive anything.
	readCtx, readCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer readCancel()

	_, _, err = conn.Read(readCtx)
	if err == nil {
		t.Error("expected timeout (no update sent for unchanged state)")
	}
}
