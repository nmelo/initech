package appserve

import (
	"context"
	"sort"
	"time"
)

// WaitingEvery is how often the service reads each running rig's beads
// waiting on Nelson: one bd call per rig per minute.
const WaitingEvery = time.Minute

// WaitingBead is a bead waiting on Nelson, as bd list gives it.
type WaitingBead struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Assignee string `json:"assignee"`
}

// WaitingReader is a backend that can read a rig's beads in waiting_on_nelson;
// cmd implements it with one bd list.
type WaitingReader interface {
	Waiting(r Rig) ([]WaitingBead, error)
}

// WaitingEvent is the data of a waiting_item event: a bead that entered
// waiting_on_nelson. The title is for the app's list; a push never carries
// it. Pane (the bead's assignee) and Text (its title) repeat what an app
// built for footer items reads.
type WaitingEvent struct {
	Rig   string `json:"rig"`
	Bead  string `json:"bead"`
	Title string `json:"title"`
	Pane  string `json:"pane"`
	Text  string `json:"text"`
}

// RunWaiting reads each running rig's beads waiting on Nelson every
// WaitingEvery until ctx ends; the first read runs at once.
func (s *Server) RunWaiting(ctx context.Context) {
	wr, ok := s.cfg.Backend.(WaitingReader)
	if !ok {
		return
	}
	for {
		s.readWaiting(wr)
		select {
		case <-ctx.Done():
			return
		case <-s.cfg.After(WaitingEvery):
		}
	}
}

// readWaiting replaces the waiting beads of every running rig and publishes a
// waiting_item for each bead that was not waiting at the last read. A rig read
// for the first time sets its baseline and publishes nothing, so a restart
// does not push what was already waiting. A rig whose read fails keeps its
// last answer, so a bead does not leave and come back over one failed read.
func (s *Server) readWaiting(wr WaitingReader) {
	s.mu.Lock()
	rigs := append([]Rig(nil), s.rigs...)
	s.mu.Unlock()
	next := map[string][]WaitingBead{}
	var entered []WaitingEvent
	for _, rg := range rigs {
		beads, err := wr.Waiting(rg)
		s.mu.Lock()
		prev, known := s.waiting[rg.Root]
		s.mu.Unlock()
		if err != nil {
			if known {
				next[rg.Root] = prev
			}
			continue
		}
		sort.Slice(beads, func(i, j int) bool { return beads[i].ID < beads[j].ID })
		if beads == nil {
			beads = []WaitingBead{}
		}
		next[rg.Root] = beads
		if !known {
			continue
		}
		was := map[string]bool{}
		for _, b := range prev {
			was[b.ID] = true
		}
		for _, b := range beads {
			if !was[b.ID] {
				entered = append(entered, WaitingEvent{Rig: rg.Name, Bead: b.ID, Title: b.Title, Pane: b.Assignee, Text: b.Title})
			}
		}
	}
	s.mu.Lock()
	s.waiting = next
	s.mu.Unlock()
	for _, e := range entered {
		s.feed.Publish("waiting_item", e)
	}
	s.publishBlockers()
}

// Pusher turns the feed into pushes: one per bead entering waiting_on_nelson
// (each waiting_item), one per stuck spell of a pane (its stuck event, the
// spell closing on a pane_state out of stuck). It reads the feed like any
// subscriber, so the feed never waits on Apple.
type Pusher struct {
	feed  *Feed
	send  func(Payload)
	from  int64              // the last event before the pusher was made
	stuck map[[2]string]bool // (rig, pane) in a stuck spell already pushed
}

// NewPusher is a pusher from feed to send, for every event published after
// it is made: taken here, not when Run starts, so nothing published in
// between is missed.
func NewPusher(feed *Feed, send func(Payload)) *Pusher {
	return &Pusher{feed: feed, send: send, from: feed.Last(), stuck: map[[2]string]bool{}}
}

// Run pushes from the feed's events until ctx ends.
func (p *Pusher) Run(ctx context.Context) {
	last := p.from
	for {
		events, wake, _ := p.feed.Since(last)
		for _, e := range events {
			p.handle(e)
			last = e.ID
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}

// handle is one feed event.
func (p *Pusher) handle(e Event) {
	switch d := e.Data.(type) {
	case WaitingEvent:
		p.send(NewBeadPayload(d.Rig, d.Bead))
	case StuckEvent:
		k := [2]string{d.Rig, d.Pane}
		if p.stuck[k] {
			return
		}
		p.stuck[k] = true
		p.send(NewPayload(d.Rig, d.Pane, "stuck"))
	case PaneEvent:
		if stuckReason(PaneState{State: d.To, Banner: d.Banner}) == "" {
			delete(p.stuck, [2]string{d.Rig, d.Pane})
		}
	}
}
