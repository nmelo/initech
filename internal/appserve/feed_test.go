package appserve

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// sseEvent is one event read off the feed stream.
type sseEvent struct {
	ID, Type, Data string
}

// subscribe opens the feed at url with lastID ("" for none) and returns its
// events on a channel.
func subscribe(t *testing.T, url, lastID string) <-chan sseEvent {
	t.Helper()
	req, _ := http.NewRequest("GET", url+"/v1/feed", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("feed: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	ch := make(chan sseEvent, 64)
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		var e sseEvent
		for sc.Scan() {
			ln := sc.Text()
			switch {
			case ln == "":
				if e.Type != "" {
					ch <- e
				}
				e = sseEvent{}
			case strings.HasPrefix(ln, "id: "):
				e.ID = ln[4:]
			case strings.HasPrefix(ln, "event: "):
				e.Type = ln[7:]
			case strings.HasPrefix(ln, "data: "):
				e.Data = ln[6:]
			}
		}
	}()
	return ch
}

func next(t *testing.T, ch <-chan sseEvent, within time.Duration) sseEvent {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(within):
		t.Fatalf("no event within %s", within)
		return sseEvent{}
	}
}

// nextPaneEvent is the next event that is not the blockers list, which a
// stuck pane also changes (TestBlockersFeed_EventsOnAChangedList).
func nextPaneEvent(t *testing.T, ch <-chan sseEvent, within time.Duration) sseEvent {
	t.Helper()
	for {
		if e := next(t, ch, within); e.Type != "blockers" {
			return e
		}
	}
}

// lockedClock is a test clock the poll and the feed read while the test
// moves it.
type lockedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *lockedClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *lockedClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// feedServer is a server over b on clk behind a test HTTP server, closed
// at cleanup after the subscribers' streams (registered later, so cancelled
// first): Close waits for open streams.
func feedServer(t *testing.T, b *fakeBackend, clk *lockedClock) (*Server, *httptest.Server) {
	s := New(Config{Backend: b, Digest: shaToken(), HasToken: true, Now: clk.Now})
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return s, hs
}

// AC4: a pane whose state changes reaches a subscriber within a second of
// the poll that sees it, as a pane_state event with the old and new state;
// one that becomes stuck also gets one stuck event, and only once while it
// stays stuck. The first poll of a rig is the baseline and sends nothing.
func TestFeed_PaneChangeReachesASubscriberWithinASecondOfThePoll(t *testing.T) {
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	b := &fakeBackend{rigs: []Rig{{Name: "alpha", Root: "/r/alpha"}},
		panes: map[string][]PaneState{"/r/alpha": {{Name: "eng1", State: "running", Banner: "none"}}}}
	s, hs := feedServer(t, b, clk)
	s.Poll()
	if s.Feed().Last() != 0 {
		t.Fatalf("the baseline poll published %d events", s.Feed().Last())
	}
	ch := subscribe(t, hs.URL, "")

	b.mu.Lock()
	b.panes["/r/alpha"] = []PaneState{{Name: "eng1", State: "idle", Banner: "permission_prompt"}}
	b.mu.Unlock()
	start := time.Now()
	s.Poll()
	e := nextPaneEvent(t, ch, time.Second)
	if e.Type != "pane_state" {
		t.Fatalf("first event %+v, want pane_state", e)
	}
	var pe PaneEvent
	json.Unmarshal([]byte(e.Data), &pe)
	if pe != (PaneEvent{Rig: "alpha", Pane: "eng1", From: "running", To: "idle", Banner: "permission_prompt"}) {
		t.Errorf("pane_state = %+v", pe)
	}
	e = nextPaneEvent(t, ch, time.Second)
	var se StuckEvent
	json.Unmarshal([]byte(e.Data), &se)
	if e.Type != "stuck" || se != (StuckEvent{Rig: "alpha", Pane: "eng1", Reason: "permission_prompt"}) {
		t.Errorf("second event %+v, want stuck at the permission prompt", e)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("events took %s after the poll", d)
	}

	// Still stuck, now waiting: a state change, no second stuck event.
	b.mu.Lock()
	b.panes["/r/alpha"] = []PaneState{{Name: "eng1", State: "waiting", Banner: "permission_prompt"}}
	b.mu.Unlock()
	s.Poll()
	if e := nextPaneEvent(t, ch, time.Second); e.Type != "pane_state" {
		t.Errorf("got %+v, want pane_state", e)
	}
	s.Poll() // nothing changed
	select {
	case e := <-ch:
		if e.Type != "blockers" {
			t.Errorf("an unchanged poll or a pane still stuck sent %+v", e)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

// AC4, AC7 (feed replay): a subscriber that reconnects with the last id it
// saw gets every event after it from the last ten minutes, in order; one
// whose id is older than what is kept gets a reset first and then what is
// kept; one with no id starts from now.
func TestFeed_ReconnectReplaysTheLastTenMinutes(t *testing.T) {
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	s, hs := feedServer(t, &fakeBackend{}, clk)
	f := s.Feed()
	for i := 0; i < 3; i++ { // ids 1..3, at t0
		f.Publish("pane_state", PaneEvent{Pane: "old"})
	}
	clk.Add(6 * time.Minute)
	for i := 0; i < 3; i++ { // ids 4..6, at t0+6m
		f.Publish("pane_state", PaneEvent{Pane: "new"})
	}

	ch := subscribe(t, hs.URL, "2")
	for want := 3; want <= 6; want++ {
		if e := next(t, ch, time.Second); e.ID != itoa(want) {
			t.Fatalf("replay from 2: got id %s, want %d", e.ID, want)
		}
	}

	clk.Add(5 * time.Minute) // ids 1..3 are now over ten minutes old
	ch = subscribe(t, hs.URL, "2")
	if e := next(t, ch, time.Second); e.Type != "reset" {
		t.Fatalf("reconnect after its events were dropped: %+v, want reset", e)
	}
	if e := next(t, ch, time.Second); e.ID != "4" {
		t.Fatalf("after reset: id %s, want 4 (the oldest kept)", e.ID)
	}

	ch = subscribe(t, hs.URL, "")
	f.Publish("stuck", StuckEvent{Pane: "x"})
	if e := next(t, ch, time.Second); e.ID != "7" || e.Type != "stuck" {
		t.Errorf("a new subscriber: %+v, want only the new event 7", e)
	}
}

// AC8, AC5: the poll runs every 30 seconds, no faster: over ten minutes it
// reads each rig's pane list once per tick and never reads status (bd).
func TestPoll_EveryThirtySecondsAndNeverReadsStatus(t *testing.T) {
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	b := &fakeBackend{rigs: []Rig{{Name: "a", Root: "/r/a"}, {Name: "b", Root: "/r/b"}}}
	ctx, cancel := context.WithCancel(context.Background())
	var waits []time.Duration
	var elapsed time.Duration
	after := func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		elapsed += d
		clk.Add(d)
		ch := make(chan time.Time, 1)
		if elapsed >= 10*time.Minute {
			cancel() // ten minutes passed: stop the loop
			return make(chan time.Time)
		}
		ch <- clk.Now()
		return ch
	}
	s := New(Config{Backend: b, Digest: shaToken(), HasToken: true, Now: clk.Now, After: after})
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	for _, d := range waits {
		if d != 30*time.Second {
			t.Fatalf("the poll waited %s, want 30s", d)
		}
	}
	polls := len(waits) // the first poll runs at once, then one per wait until the last
	if polls != 20 || b.paneCalls != 2*polls || b.statusCalls != 0 {
		t.Errorf("ten minutes: %d polls, %d pane reads, %d status reads; want 20, 40, 0", polls, b.paneCalls, b.statusCalls)
	}
}

// AC6 feed-it-nothing: a poll with no rig, and with every rig down,
// publishes nothing and leaves no rig running.
func TestPoll_NoRigPublishesNothing(t *testing.T) {
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	for _, b := range []*fakeBackend{{}, {rigs: []Rig{{Name: "a", Root: "/r/a"}}, down: map[string]bool{"/r/a": true}}} {
		s := New(Config{Backend: b, Digest: shaToken(), HasToken: true, Now: clk.Now})
		s.Poll()
		s.Poll()
		if s.Feed().Last() != 0 || len(s.running()) != 0 {
			t.Errorf("no rig: %d events, %d running", s.Feed().Last(), len(s.running()))
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func shaToken() [32]byte { return sha256.Sum256([]byte(testToken)) }
