package watch

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Store holds every watch of a rig, in memory and in one file. Window 1 owns
// the only one, and every call is made on its main goroutine, so the Store
// has no lock. Every change is written whole before the call returns (see
// file.go); what a failed write does is per call, below.
type Store struct {
	path   string
	loaded time.Time // when this window 1 read the file: an ending older than this is late
	nextID int
	ws     []*Watch
	write  func(path string, data []byte) error

	// dirty: memory holds a change the file does not. pending: endings not
	// yet on disk, which are not owed until they are (no line goes out for
	// an ending a crash could lose).
	dirty   bool
	pending map[string]bool

	// The trouble of a run of failing writes: when it began, and whether the
	// pass has logged it and told super.
	troubleSince time.Time
	logged, told bool
	broken       error // the file could not be read nor set aside: nothing may be written over it
}

// Trouble is what a pass found about writing the file.
type Trouble struct {
	Err       error  // the write failed; nil when the file holds everything
	File      string // the file's path
	Log       bool   // the first pass of this run of failures: log it, once
	TellSuper bool   // writes have failed for WriteTroubleTellSuper: tell the rig's super, once
}

// Add sets a new waiting watch for w.Owner and returns its id. It refuses,
// with one line and nothing set: no owner, no kind, no reason, a reason over
// MaxWhy characters or with a line break, no deadline or one not after now,
// an owner already at MaxWaitingPerOwner waiting watches, or a duplicate of
// the owner's own waiting watch (same kind and params), named. A failed write
// is a refusal too: the watch is not kept.
func (s *Store) Add(w Watch, now time.Time) (string, error) {
	switch {
	case s.broken != nil:
		return "", fmt.Errorf("watches cannot be stored: %v", s.broken)
	case w.Owner == "":
		return "", errors.New("a watch needs its owner")
	case w.Kind == "":
		return "", errors.New("a watch needs a condition")
	case strings.TrimSpace(w.Why) == "":
		return "", errors.New("a watch needs a reason (--why), the line its owner gets")
	case strings.ContainsAny(w.Why, "\r\n"):
		return "", errors.New("the reason is one line: it holds a line break")
	case utf8.RuneCountInString(w.Why) > MaxWhy:
		return "", fmt.Errorf("the reason is %d characters, over %d", utf8.RuneCountInString(w.Why), MaxWhy)
	case w.Deadline.IsZero():
		return "", errors.New("a watch needs a deadline (--until)")
	case !w.Deadline.After(now):
		return "", fmt.Errorf("the deadline %s is not in the future", w.Deadline.Format(time.RFC3339))
	}
	waiting := 0
	for _, o := range s.ws {
		if o.Owner != w.Owner || o.State != Waiting {
			continue
		}
		waiting++
		if o.Kind == w.Kind && maps.Equal(o.Params, w.Params) {
			return "", fmt.Errorf("%s already waits for this (%s)", o.ID, o.Why)
		}
	}
	if waiting >= MaxWaitingPerOwner {
		return "", fmt.Errorf("%s already has %d waiting watches, the most one agent may hold", w.Owner, MaxWaitingPerOwner)
	}
	nw := copyWatch(w)
	nw.ID = fmt.Sprintf("w%d", s.nextID)
	nw.State, nw.Set = Waiting, now
	nw.Ended, nw.Detail, nw.Late, nw.Delivery = time.Time{}, "", false, Delivery{}
	s.ws = append(s.ws, &nw)
	s.nextID++
	dirty := s.dirty
	if err := s.flush(); err != nil {
		s.ws = s.ws[:len(s.ws)-1]
		s.nextID--
		s.dirty = dirty
		return "", fmt.Errorf("watch not set: %s could not be written: %v", filepath.Base(s.path), err)
	}
	return nw.ID, nil
}

// Cancel cancels owner's own waiting watch id. Anyone else's, an ended one,
// or an unknown id is refused. A failed write undoes it: the watch still
// waits.
func (s *Store) Cancel(id, owner string, now time.Time) error {
	w := s.find(id)
	switch {
	case w == nil:
		return fmt.Errorf("no watch %s", id)
	case w.Owner != owner:
		return fmt.Errorf("%s is %s's watch; an agent cancels only its own", id, w.Owner)
	case w.State != Waiting:
		return fmt.Errorf("%s has already ended (%s)", id, w.State)
	}
	before, dirty := *w, s.dirty
	w.State, w.Ended = Cancelled, now
	if err := s.flush(); err != nil {
		*w = before
		s.dirty = dirty
		return fmt.Errorf("%s not cancelled: %s could not be written: %v", id, filepath.Base(s.path), err)
	}
	return nil
}

// End ends waiting watch id as Fired or Missed, once: a second End is an
// error and never overwrites the first. detail is what was seen; evidenceAt
// is the evidence's own time (for a miss, the deadline). An ending whose
// evidence predates this store's load happened while window 1 was down and is
// marked Late. If the write fails, the ending stays in memory, is not owed,
// and the next Pass writes it.
func (s *Store) End(id string, state State, detail string, evidenceAt, now time.Time) error {
	w := s.find(id)
	switch {
	case w == nil:
		return fmt.Errorf("no watch %s", id)
	case state != Fired && state != Missed:
		return fmt.Errorf("a watch ends as fired or missed here, not %q", state)
	case w.State != Waiting:
		return fmt.Errorf("%s has already ended (%s)", id, w.State)
	}
	w.State, w.Ended, w.Detail = state, now, detail
	w.Late = evidenceAt.Before(s.loaded)
	s.pending[id] = true
	if err := s.flush(); err != nil {
		s.noteFailure(now)
	}
	return nil
}

// Owed is every ending whose line still has to go out: fired or missed, on
// disk, and neither handed to delivery nor sent to super, in id order.
func (s *Store) Owed() []Watch {
	var out []Watch
	for _, w := range s.ws {
		if w.endingOwed() && !s.pending[w.ID] {
			out = append(out, copyWatch(*w))
		}
	}
	return out
}

// MarkHanded records that id's line was handed to delivery, with its send
// receipt. Only an owed ending can be handed. The write must succeed before
// the line goes out, so a failed write undoes it and returns the error:
// delivery tries again later, and a crash never delivers a line twice.
func (s *Store) MarkHanded(id, receipt string, at time.Time) error {
	return s.markSent(id, at, func(w *Watch) { w.Delivery.Handed, w.Delivery.Receipt = at, receipt })
}

// MarkToSuper records that id's line went to the rig's super instead (its
// owner is gone), on the same terms as MarkHanded.
func (s *Store) MarkToSuper(id string, at time.Time) error {
	return s.markSent(id, at, func(w *Watch) { w.Delivery.ToSuper, w.Delivery.Handed = true, at })
}

// markSent records a line handed out. An ending not yet on disk needs no
// guard here: Owed never lists it, and the write below puts the ending and the
// hand on disk together before this returns, so no line goes out for an
// ending the file does not hold.
func (s *Store) markSent(id string, at time.Time, set func(*Watch)) error {
	w := s.find(id)
	switch {
	case w == nil:
		return fmt.Errorf("no watch %s", id)
	case !w.endingOwed():
		return fmt.Errorf("%s has no line owed", id)
	}
	before, dirty := *w, s.dirty
	set(w)
	if err := s.flush(); err != nil {
		*w = before
		s.dirty = dirty
		return fmt.Errorf("%s not handed: %s could not be written: %v", id, filepath.Base(s.path), err)
	}
	return nil
}

// MarkDelivered records when id's handed line reached its owner. Its hour of
// history starts then. A failed write keeps it in memory for the next pass.
func (s *Store) MarkDelivered(id string, at time.Time) error {
	w := s.find(id)
	if w == nil || w.Delivery.Handed.IsZero() {
		return fmt.Errorf("%s was not handed to delivery", id)
	}
	w.Delivery.Delivered = at
	if err := s.flush(); err != nil {
		s.noteFailure(at)
	}
	return nil
}

// MarkSuperToldHeld records that the owner's super was told id's line is
// held, so it is told once.
func (s *Store) MarkSuperToldHeld(id string, at time.Time) error {
	w := s.find(id)
	if w == nil {
		return fmt.Errorf("no watch %s", id)
	}
	w.Delivery.SuperToldHeld = true
	if err := s.flush(); err != nil {
		s.noteFailure(at)
	}
	return nil
}

// Prune drops the history over an hour old: a cancelled watch an hour after
// its cancel, a fired or missed one an hour after its line was delivered (or
// sent to super), and never one whose line is still owed or not yet
// delivered.
func (s *Store) Prune(now time.Time) {
	kept := s.ws[:0]
	gone := false
	for _, w := range s.ws {
		if from := w.historyFrom(); !from.IsZero() && now.Sub(from) >= History {
			gone = true
			continue
		}
		kept = append(kept, w)
	}
	s.ws = kept
	if gone {
		if err := s.flush(); err != nil {
			s.noteFailure(now)
		}
	}
}

// Pass writes whatever memory holds that the file does not, and reports the
// run of failures: Log on its first pass, TellSuper once when it has lasted
// WriteTroubleTellSuper. Window 1 calls it after each pass over the watches.
func (s *Store) Pass(now time.Time) Trouble {
	if !s.dirty {
		return Trouble{}
	}
	err := s.flush()
	if err == nil {
		return Trouble{}
	}
	s.noteFailure(now)
	t := Trouble{Err: err, File: s.path}
	if !s.logged {
		s.logged, t.Log = true, true
	}
	if !s.told && now.Sub(s.troubleSince) >= WriteTroubleTellSuper {
		s.told, t.TellSuper = true, true
	}
	return t
}

// Snapshot is a copy of every watch, in id order, for listing.
func (s *Store) Snapshot(now time.Time) []Watch {
	out := make([]Watch, 0, len(s.ws))
	for _, w := range s.ws {
		out = append(out, copyWatch(*w))
	}
	return out
}

// WaitingFor is how many waiting watches owner holds.
func (s *Store) WaitingFor(owner string) int {
	n := 0
	for _, w := range s.ws {
		if w.Owner == owner && w.State == Waiting {
			n++
		}
	}
	return n
}

// flush writes the whole store. On success the file holds everything: no
// ending is pending and the trouble is over.
func (s *Store) flush() error {
	if s.broken != nil {
		s.dirty = true
		return s.broken
	}
	data, err := s.marshal()
	if err == nil {
		err = s.write(s.path, data)
	}
	if err != nil {
		s.dirty = true
		return err
	}
	s.dirty = false
	clear(s.pending)
	s.troubleSince, s.logged, s.told = time.Time{}, false, false
	return nil
}

// noteFailure starts a run of failing writes at now, if one is not running.
func (s *Store) noteFailure(now time.Time) {
	if s.troubleSince.IsZero() {
		s.troubleSince = now
	}
}

func (s *Store) find(id string) *Watch {
	for _, w := range s.ws {
		if w.ID == id {
			return w
		}
	}
	return nil
}

// copyWatch is w with its own params map.
func copyWatch(w Watch) Watch {
	if w.Params != nil {
		w.Params = maps.Clone(w.Params)
	}
	return w
}
