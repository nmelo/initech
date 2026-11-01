package appserve

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitingBackend is a fakeBackend whose rigs have beads waiting on Nelson,
// counting the bd reads.
type waitingBackend struct {
	fakeBackend
	beads map[string][]WaitingBead // rig root -> waiting
	fail  map[string]bool
	reads int
}

func (b *waitingBackend) Waiting(r Rig) ([]WaitingBead, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reads++
	if b.fail[r.Root] {
		return nil, errors.New("bd unreachable")
	}
	return append([]WaitingBead(nil), b.beads[r.Root]...), nil
}

func (b *waitingBackend) set(root string, beads ...WaitingBead) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.beads[root] = beads
}

// recorder is a send that keeps every payload.
type recorder struct {
	mu   sync.Mutex
	sent []Payload
}

func (r *recorder) send(p Payload) { r.mu.Lock(); r.sent = append(r.sent, p); r.mu.Unlock() }
func (r *recorder) all() []Payload {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Payload(nil), r.sent...)
}

// pushRig is a server over one running rig, its pusher running into rec.
func pushRig(t *testing.T, panes []PaneState) (*Server, *waitingBackend, *recorder) {
	t.Helper()
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	b := &waitingBackend{beads: map[string][]WaitingBead{}, fail: map[string]bool{}}
	b.rigs = []Rig{{Name: "alpha", Root: "/r/alpha"}}
	b.panes = map[string][]PaneState{"/r/alpha": panes}
	s := New(Config{Backend: b, Digest: shaToken(), HasToken: true, Now: clk.Now})
	s.Poll()
	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	pusher := NewPusher(s.Feed(), rec.send)
	go func() { pusher.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, b, rec
}

// settle waits until rec has n payloads, then a little more to see none extra.
func settle(t *testing.T, rec *recorder, n int) []Payload {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(rec.all()) < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	return rec.all()
}

// An event published after the pusher is made and before its Run starts is
// still pushed.
func TestPusher_MissesNothingBeforeRunStarts(t *testing.T) {
	f := NewFeed(time.Now)
	rec := &recorder{}
	p := NewPusher(f, rec.send)
	f.Publish("stuck", StuckEvent{Rig: "r", Pane: "eng1", Reason: "waiting"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	if got := settle(t, rec, 1); len(got) != 1 {
		t.Errorf("%d pushes, want the one published before Run", len(got))
	}
}

// ini-r0q.16 AC1: a bead entering waiting_on_nelson makes exactly one push,
// its id the body; the next read makes none for it; a second bead makes one;
// a bead leaving and entering again makes one more. The first read is the
// baseline: what already waits when the service starts is not pushed.
func TestPush_OnePerBeadEnteringWaitingOnNelson(t *testing.T) {
	s, b, rec := pushRig(t, nil)
	b.set("/r/alpha", WaitingBead{ID: "ini-old", Title: "already waiting"})
	s.readWaiting(b)
	if got := settle(t, rec, 0); len(got) != 0 {
		t.Fatalf("the baseline read pushed %+v", got)
	}
	b.set("/r/alpha", WaitingBead{ID: "ini-old"}, WaitingBead{ID: "ini-a.1", Title: "Approve the release", Assignee: "super"})
	s.readWaiting(b)
	s.readWaiting(b)
	got := settle(t, rec, 1)
	if len(got) != 1 {
		t.Fatalf("one bead entering over two reads: %d pushes", len(got))
	}
	p := got[0]
	if p.APS.Alert.Title != "initech: alpha" || p.APS.Alert.Body != "ini-a.1" || p.Data.Bead != "ini-a.1" || p.Data.Rig != "alpha" || p.Data.Reason != "nelson" || p.Data.Pane != "" {
		t.Errorf("push = %+v", p)
	}
	b.set("/r/alpha", WaitingBead{ID: "ini-old"}, WaitingBead{ID: "ini-a.1"}, WaitingBead{ID: "ini-a.2"})
	s.readWaiting(b)
	if got := settle(t, rec, 2); len(got) != 2 || got[1].APS.Alert.Body != "ini-a.2" {
		t.Fatalf("a second bead: %+v", got)
	}
	b.set("/r/alpha", WaitingBead{ID: "ini-old"}, WaitingBead{ID: "ini-a.2"})
	s.readWaiting(b)
	if got := settle(t, rec, 2); len(got) != 2 {
		t.Fatalf("a bead leaving pushed: %d", len(got))
	}
	b.set("/r/alpha", WaitingBead{ID: "ini-old"}, WaitingBead{ID: "ini-a.2"}, WaitingBead{ID: "ini-a.1"})
	s.readWaiting(b)
	if got := settle(t, rec, 3); len(got) != 3 || got[2].APS.Alert.Body != "ini-a.1" {
		t.Errorf("re-entry: %+v", got)
	}
}

// AC1: a failed bd read is not a bead leaving: the bead waiting before it is
// still waiting after it and is not pushed again; the waiting_item event
// carries the bead, its title for the app list and the old keys the app reads.
func TestPush_AFailedReadIsNotALeaveAndTheEventCarriesTheBead(t *testing.T) {
	s, b, rec := pushRig(t, nil)
	s.readWaiting(b)
	b.set("/r/alpha", WaitingBead{ID: "ini-b", Title: "Pick the version", Assignee: "pm"})
	s.readWaiting(b)
	b.mu.Lock()
	b.fail["/r/alpha"] = true
	b.mu.Unlock()
	s.readWaiting(b)
	b.mu.Lock()
	b.fail["/r/alpha"] = false
	b.mu.Unlock()
	// A bead that entered while the read was failing is still new after it.
	b.set("/r/alpha", WaitingBead{ID: "ini-b", Title: "Pick the version", Assignee: "pm"}, WaitingBead{ID: "ini-e"})
	s.readWaiting(b)
	got := settle(t, rec, 2)
	if len(got) != 2 || got[1].APS.Alert.Body != "ini-e" {
		t.Errorf("a failed read in between: pushes %+v, want ini-b once and then ini-e", got)
	}
	evs := eventsOf(s, 0, "waiting_item")
	if len(evs) != 2 {
		t.Fatalf("waiting_item events: %d", len(evs))
	}
	raw, _ := json.Marshal(evs[0].Data)
	if string(raw) != `{"rig":"alpha","bead":"ini-b","title":"Pick the version","pane":"pm","text":"Pick the version"}` {
		t.Errorf("event = %s", raw)
	}
}

// AC1: the blockers list's waiting entries are the beads of the bd read; a
// footer item in a super transcript with no bead in the state is no waiting
// entry and no push.
func TestBlockers_WaitingIsTheBdReadNotFooters(t *testing.T) {
	path := writeTranscript(t, []string{tline("assistant", "a1", t0, []map[string]any{{"type": "text", "text": "Done.\n\n- ▶ Approve the release? Recommend yes."}})})
	s, b, rec := pushRig(t, []PaneState{{Name: "super", State: "idle", Banner: "none", SessionFile: path}})
	s.readWaiting(b)
	s.Poll()
	h := s.Handler()
	_, body := get(t, h, "/v1/blockers", "Bearer "+testToken)
	var list []RigBlockers
	if err := json.Unmarshal([]byte(body), &list); err != nil || len(list) != 1 {
		t.Fatalf("list = %s", body)
	}
	if len(list[0].Waiting) != 0 || !list[0].Quiet {
		t.Errorf("a footer item made a waiting entry: %+v", list[0])
	}
	if got := settle(t, rec, 0); len(got) != 0 {
		t.Errorf("a footer item pushed: %+v", got)
	}
	b.set("/r/alpha", WaitingBead{ID: "ini-c", Title: "Choose", Assignee: "super"})
	s.readWaiting(b)
	_, body = get(t, h, "/v1/blockers", "Bearer "+testToken)
	json.Unmarshal([]byte(body), &list)
	if len(list[0].Waiting) != 1 || list[0].Waiting[0] != (WaitingItem{Pane: "super", Text: "Choose", Bead: "ini-c"}) || list[0].Quiet {
		t.Errorf("waiting = %+v", list[0])
	}
	if len(eventsOf(s, 0, "blockers")) == 0 {
		t.Error("the list changed and no blockers event went out")
	}
}

// ini-r0q.16 AC2: a pane going stuck makes one push naming it; its recovery
// makes none; stuck again later makes one; a second stuck event inside one
// spell makes none.
func TestPush_OnePerStuckSpell(t *testing.T) {
	s, b, rec := pushRig(t, []PaneState{{Name: "eng1", State: "running", Banner: "none"}})
	poll := func(state, banner string) {
		b.mu.Lock()
		b.panes["/r/alpha"] = []PaneState{{Name: "eng1", State: state, Banner: banner}}
		b.mu.Unlock()
		s.Poll()
	}
	poll("idle", "permission_prompt")
	poll("waiting", "permission_prompt")
	got := settle(t, rec, 1)
	if len(got) != 1 || got[0].APS.Alert.Body != "eng1" || got[0].Data.Pane != "eng1" || got[0].Data.Reason != "stuck" || got[0].Data.Bead != "" {
		t.Fatalf("stuck: %+v", got)
	}
	s.Feed().Publish("stuck", StuckEvent{Rig: "alpha", Pane: "eng1", Reason: "waiting"})
	if got := settle(t, rec, 1); len(got) != 1 {
		t.Errorf("a second stuck event in one spell pushed: %d", len(got))
	}
	poll("running", "none")
	if got := settle(t, rec, 1); len(got) != 1 {
		t.Errorf("a recovery pushed: %d", len(got))
	}
	poll("pending", "none")
	if got := settle(t, rec, 2); len(got) != 2 || got[1].Data.Pane != "eng1" {
		t.Errorf("stuck again: %+v", got)
	}
}

// ini-r0q.16 AC3: no push carries anything but the rig, the pane or bead id
// and the reason: over every payload a stuck pane and a waiting bead produce,
// the body is the pane name or the bead id, and neither the transcript's text,
// the bead's title, nor the footer's item text appears anywhere.
func TestPush_NoPayloadCarriesText(t *testing.T) {
	secret := []string{"SECRET-TRANSCRIPT-WORDS", "Approve the release Recommend yes", "SECRET-BEAD-TITLE"}
	path := writeTranscript(t, []string{
		tline("user", "u1", t0, secret[0]),
		tline("assistant", "a1", t0, []map[string]any{{"type": "text", "text": "Done.\n\n- ▶ " + secret[1]}}),
	})
	s, b, rec := pushRig(t, []PaneState{{Name: "super", State: "running", Banner: "none", SessionFile: path}})
	s.readWaiting(b)
	b.set("/r/alpha", WaitingBead{ID: "ini-d", Title: secret[2], Assignee: "super"})
	s.readWaiting(b)
	b.mu.Lock()
	b.panes["/r/alpha"] = []PaneState{{Name: "super", State: "waiting", Banner: "permission_prompt", SessionFile: path}}
	b.mu.Unlock()
	s.Poll()
	got := settle(t, rec, 2)
	if len(got) != 2 {
		t.Fatalf("%d pushes, want a bead and a stuck one", len(got))
	}
	bodies := map[string]bool{}
	for _, p := range got {
		raw, _ := json.Marshal(p)
		for _, w := range secret {
			if strings.Contains(string(raw), w) || strings.Contains(string(raw), strings.Fields(w)[0]) {
				t.Errorf("a payload carries %q: %s", w, raw)
			}
		}
		bodies[p.APS.Alert.Body] = true
	}
	if !bodies["ini-d"] || !bodies["super"] {
		t.Errorf("bodies = %v, want the bead id and the pane name", bodies)
	}
}

// AC8 cadence: the bd read runs every 60 s, no faster, one per running rig.
func TestRunWaiting_EveryMinuteOnePerRig(t *testing.T) {
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	b := &waitingBackend{beads: map[string][]WaitingBead{}, fail: map[string]bool{}}
	b.rigs = []Rig{{Name: "a", Root: "/r/a"}, {Name: "b", Root: "/r/b"}, {Name: "gone", Root: "/r/gone"}}
	b.down = map[string]bool{"/r/gone": true}
	ctx, cancel := context.WithCancel(context.Background())
	after, waits := fakeTimer(clk, 5*time.Minute, cancel)
	s := New(Config{Backend: b, Digest: shaToken(), HasToken: true, Now: clk.Now, After: after})
	s.Poll()
	done := make(chan struct{})
	go func() { s.RunWaiting(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunWaiting did not stop")
	}
	for _, d := range *waits {
		if d != time.Minute {
			t.Fatalf("waited %s, want 1m", d)
		}
	}
	if runs := len(*waits); b.reads != 2*runs {
		t.Errorf("%d reads over %d runs, want two a run (the stopped rig not read)", b.reads, runs)
	}
}
