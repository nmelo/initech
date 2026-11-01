package appserve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// FeedKeep is how long the feed keeps an event for a subscriber that
// reconnects.
const FeedKeep = 10 * time.Minute

// FeedKeepalive is how often an idle feed stream sends a comment line, so
// a dead connection is noticed.
const FeedKeepalive = 15 * time.Second

// Event is one change on the feed.
type Event struct {
	ID   int64     `json:"id"`
	At   time.Time `json:"at"`
	Type string    `json:"type"` // pane_state, stuck, waiting_item, transcript, blockers
	Data any       `json:"data"`
}

// Feed is the service's change feed: every event in order, kept FeedKeep
// for replay.
type Feed struct {
	now       func() time.Time
	keepalive time.Duration

	mu      sync.Mutex
	next    int64
	events  []Event
	changed chan struct{} // closed and replaced on every publish
}

// NewFeed is an empty feed on clock now.
func NewFeed(now func() time.Time) *Feed {
	return &Feed{now: now, keepalive: FeedKeepalive, next: 1, changed: make(chan struct{})}
}

// Publish adds an event and wakes every subscriber.
func (f *Feed) Publish(typ string, data any) Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := Event{ID: f.next, At: f.now(), Type: typ, Data: data}
	f.next++
	f.events = append(f.events, e)
	f.dropOld()
	close(f.changed)
	f.changed = make(chan struct{})
	return e
}

// dropOld forgets events older than FeedKeep. f.mu is held.
func (f *Feed) dropOld() {
	cut := f.now().Add(-FeedKeep)
	i := 0
	for i < len(f.events) && f.events[i].At.Before(cut) {
		i++
	}
	if i > 0 {
		f.events = append([]Event(nil), f.events[i:]...)
	}
}

// Since is every kept event after id, a channel closed on the next publish,
// and whether events after id were already forgotten (the subscriber missed
// some and must reload).
func (f *Feed) Since(id int64) (events []Event, wake <-chan struct{}, lost bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropOld()
	first := f.next // the id the oldest kept event has, or the next one
	if len(f.events) > 0 {
		first = f.events[0].ID
	}
	lost = id > 0 && id < first-1
	for _, e := range f.events {
		if e.ID > id {
			events = append(events, e)
		}
	}
	return events, f.changed, lost
}

// Last is the id of the newest event, 0 for none.
func (f *Feed) Last() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.next - 1
}

// serveFeed streams the feed as server-sent events from the id the
// subscriber last saw (Last-Event-ID, or ?since=), replaying what it missed
// in the last FeedKeep. A subscriber with no id starts from now. One whose
// id is older than what is kept gets a reset event first. authorized is asked
// before anything is written and on every wake and keepalive: the stream ends
// once its token is no longer the current one, so revoking a device also ends
// what it has open.
func serveFeed(f *Feed, w http.ResponseWriter, r *http.Request, authorized func() bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	last := f.Last()
	from := r.Header.Get("Last-Event-ID")
	if from == "" {
		from = r.URL.Query().Get("since")
	}
	if from != "" {
		n, err := strconv.ParseInt(from, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "bad event id", http.StatusBadRequest)
			return
		}
		last = n
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	keep := time.NewTicker(f.keepalive)
	defer keep.Stop()
	for {
		if !authorized() {
			return
		}
		events, wake, lost := f.Since(last)
		if lost {
			fmt.Fprintf(w, "event: reset\ndata: {\"reason\":\"events older than %s were dropped; reload\"}\n\n", FeedKeep)
		}
		for _, e := range events {
			data, _ := json.Marshal(e.Data)
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, data)
			last = e.ID
		}
		if lost || len(events) > 0 {
			fl.Flush()
		}
		if lost && len(events) == 0 {
			last = f.Last()
		}
		select {
		case <-r.Context().Done():
			return
		case <-wake:
		case <-keep.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}
