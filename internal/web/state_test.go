package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
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
				{Name: "eng1", Activity: "running", Alive: true, Visible: true, Order: 0, Cols: 120, Rows: 40},
				{Name: "qa1", Activity: "idle", Alive: true, Visible: true, BeadID: "ini-abc", Order: 1, Cols: 120, Rows: 40},
			},
		},
		ok: true,
	}
	srv := NewServer(0, lister, nil, sp, nil, nil, nil, nil)
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

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Type != "state" {
		t.Errorf("type = %q, want state", msg.Type)
	}
	if msg.State == nil {
		t.Fatal("state is nil")
	}
	if msg.State.Layout.Mode != "grid" || msg.State.Layout.Cols != 2 {
		t.Errorf("layout = %+v", msg.State.Layout)
	}
	if len(msg.State.Panes) != 2 {
		t.Fatalf("panes = %d, want 2", len(msg.State.Panes))
	}
	if msg.State.Panes[0].Name != "eng1" || msg.State.Panes[1].BeadID != "ini-abc" {
		t.Errorf("panes = %+v", msg.State.Panes)
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
	srv := NewServer(0, lister, nil, sp, nil, nil, nil, nil)
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

	var msg wsMessage
	json.Unmarshal(data, &msg)
	if msg.Type != "state" || msg.State == nil {
		t.Fatalf("expected state message, got type=%q", msg.Type)
	}
	if msg.State.Panes[0].Activity != "idle" {
		t.Errorf("expected activity=idle after change, got %q", msg.State.Panes[0].Activity)
	}
}

func TestStateWS_NoProviderReturns501(t *testing.T) {
	lister := &fakeLister{ok: true}
	srv := NewServer(0, lister, nil, nil, nil, nil, nil, nil)
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
	srv := NewServer(0, lister, nil, sp, nil, nil, nil, nil)
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

// fakeEventProvider implements EventProvider for testing.
type fakeEventProvider struct {
	mu   sync.Mutex
	subs map[string]chan AgentEventInfo
}

func newFakeEventProvider() *fakeEventProvider {
	return &fakeEventProvider{subs: make(map[string]chan AgentEventInfo)}
}

func (f *fakeEventProvider) SubscribeEvents(id string) chan AgentEventInfo {
	ch := make(chan AgentEventInfo, 8)
	f.mu.Lock()
	f.subs[id] = ch
	f.mu.Unlock()
	return ch
}

func (f *fakeEventProvider) UnsubscribeEvents(id string) {
	f.mu.Lock()
	ch, ok := f.subs[id]
	if ok {
		delete(f.subs, id)
	}
	f.mu.Unlock()
	if ok {
		close(ch)
	}
}

func (f *fakeEventProvider) broadcast(ev AgentEventInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.subs {
		ch <- ev
	}
}

func TestStateWS_ReceivesEvents(t *testing.T) {
	lister := &fakeLister{ok: true}
	sp := &fakeStateProvider{
		snap: StateSnapshot{
			Layout: LayoutInfo{Mode: "grid", Cols: 2, Rows: 2, Focused: "eng1"},
			Panes:  []PaneState{{Name: "eng1", Activity: "running", Alive: true, Visible: true, Order: 0}},
		},
		ok: true,
	}
	ep := newFakeEventProvider()
	srv := NewServer(0, lister, nil, sp, ep, nil, nil, nil)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws/state", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Read initial state message.
	conn.Read(ctx)

	// Push an event.
	ep.broadcast(AgentEventInfo{
		Kind:   "bead_completed",
		Pane:   "eng1",
		BeadID: "ini-abc.1",
		Detail: "eng1 marked ini-abc.1 ready_for_qa",
		Time:   "2026-04-03T12:00:00Z",
	})

	// Read event message.
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read event: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Type != "event" {
		t.Fatalf("type = %q, want event", msg.Type)
	}
	if msg.Event == nil {
		t.Fatal("event is nil")
	}
	if msg.Event.Kind != "bead_completed" {
		t.Errorf("kind = %q, want bead_completed", msg.Event.Kind)
	}
	if msg.Event.Pane != "eng1" {
		t.Errorf("pane = %q, want eng1", msg.Event.Pane)
	}
	if msg.Event.BeadID != "ini-abc.1" {
		t.Errorf("bead_id = %q, want ini-abc.1", msg.Event.BeadID)
	}
}

func TestStateWS_EventWithoutProvider(t *testing.T) {
	// No event provider: should still work (state only, no events).
	lister := &fakeLister{ok: true}
	sp := &fakeStateProvider{
		snap: StateSnapshot{
			Layout: LayoutInfo{Mode: "focus", Cols: 1, Rows: 1, Focused: "eng1"},
			Panes:  []PaneState{{Name: "eng1", Activity: "idle", Alive: true, Visible: true, Order: 0}},
		},
		ok: true,
	}
	srv := NewServer(0, lister, nil, sp, nil, nil, nil, nil)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws/state", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	// Read initial state. Should work without event provider.
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var msg wsMessage
	json.Unmarshal(data, &msg)
	if msg.Type != "state" {
		t.Errorf("type = %q, want state", msg.Type)
	}
}

func TestStateWS_LiveModeFields(t *testing.T) {
	lister := &fakeLister{ok: true}
	sp := &fakeStateProvider{
		snap: StateSnapshot{
			Layout: LayoutInfo{Mode: "live", Cols: 2, Rows: 2, Focused: "super"},
			Panes: []PaneState{
				{Name: "super", Activity: "running", Alive: true, Visible: true, Pinned: true, Order: 0, Cols: 120, Rows: 40},
				{Name: "eng1", Activity: "running", Alive: true, Visible: true, Order: 1, Cols: 120, Rows: 40},
				{Name: "pm", Activity: "idle", Alive: true, Visible: true, Order: 2, Cols: 120, Rows: 40},
				{Name: "eng2", Activity: "running", Alive: true, Visible: true, Order: 3, Cols: 120, Rows: 40},
			},
			Live: &LiveInfo{
				Pinned: map[string]int{"super": 0},
				Slots:  []string{"super", "eng1", "pm", "eng2"},
			},
		},
		ok: true,
	}
	srv := NewServer(0, lister, nil, sp, nil, nil, nil, nil)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws"+ts.URL[4:]+"/ws/state", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.State.Layout.Mode != "live" {
		t.Errorf("mode = %q, want live", msg.State.Layout.Mode)
	}
	if msg.State.Live == nil {
		t.Fatal("live info is nil")
	}
	if len(msg.State.Live.Slots) != 4 {
		t.Errorf("slots = %d, want 4", len(msg.State.Live.Slots))
	}
	if slot, ok := msg.State.Live.Pinned["super"]; !ok || slot != 0 {
		t.Errorf("pinned[super] = %v, %v", slot, ok)
	}
	if !msg.State.Panes[0].Pinned {
		t.Error("super should be pinned")
	}
	if msg.State.Panes[1].Pinned {
		t.Error("eng1 should not be pinned")
	}
}

// fakePinToggler implements PinToggler for testing.
type fakePinToggler struct {
	pinned map[string]bool
}

func (f *fakePinToggler) TogglePin(name string) (bool, bool) {
	if f.pinned == nil {
		f.pinned = make(map[string]bool)
	}
	if name == "nonexistent" {
		return false, false
	}
	f.pinned[name] = !f.pinned[name]
	return f.pinned[name], true
}

func TestPinEndpoint_TogglePin(t *testing.T) {
	lister := &fakeLister{ok: true}
	pt := &fakePinToggler{}
	srv := NewServer(0, lister, nil, nil, nil, nil, pt, nil)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	// First toggle: should pin.
	resp, err := http.Post(ts.URL+"/api/pin/eng1", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var pr pinResponse
	json.NewDecoder(resp.Body).Decode(&pr)
	resp.Body.Close()
	if pr.Name != "eng1" || !pr.Pinned {
		t.Errorf("got %+v, want {eng1, true}", pr)
	}

	// Second toggle: should unpin.
	resp, err = http.Post(ts.URL+"/api/pin/eng1", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	json.NewDecoder(resp.Body).Decode(&pr)
	resp.Body.Close()
	if pr.Pinned {
		t.Error("second toggle should unpin")
	}
}

func TestPinEndpoint_NotFound(t *testing.T) {
	lister := &fakeLister{ok: true}
	pt := &fakePinToggler{}
	srv := NewServer(0, lister, nil, nil, nil, nil, pt, nil)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/pin/nonexistent", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPinEndpoint_NoPinToggler(t *testing.T) {
	lister := &fakeLister{ok: true}
	srv := NewServer(0, lister, nil, nil, nil, nil, nil, nil)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/pin/eng1", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 501 {
		t.Errorf("status = %d, want 501", resp.StatusCode)
	}
}
