package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/progress"
	"github.com/nmelo/initech/internal/watch"
)

// ini-20r2 (Watches 2B): the conditions. Every test runs on an explicit now;
// bd and the receipts are stubbed.

var wcT0 = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

type fakeWorld struct {
	now      time.Time
	beads    map[string]bdBead
	known    bool
	comments map[string][]progress.Comment
	receipts map[string]ReceiptState
	reads    map[string]int
}

func newFakeWorld(now time.Time) *fakeWorld {
	return &fakeWorld{now: now, beads: map[string]bdBead{}, known: true,
		comments: map[string][]progress.Comment{}, receipts: map[string]ReceiptState{}, reads: map[string]int{}}
}
func (w *fakeWorld) Now() time.Time   { return w.now }
func (w *fakeWorld) BeadsKnown() bool { return w.known }
func (w *fakeWorld) Bead(id string) (bdBead, bool) {
	b, ok := w.beads[id]
	return b, ok
}
func (w *fakeWorld) Comments(id string) ([]progress.Comment, bool) {
	w.reads[id]++
	cs, ok := w.comments[id]
	return cs, ok
}
func (w *fakeWorld) Receipt(pane, id string) ReceiptState {
	if r, ok := w.receipts[pane+"/"+id]; ok {
		return r
	}
	return ReceiptState{Pane: pane, ID: id, State: ReceiptUnknown}
}

type wcEnd struct {
	id     string
	state  watch.State
	detail string
	at     time.Time
}

type fakeWatchStore struct {
	ws   []watch.Watch
	ends []wcEnd
}

func (s *fakeWatchStore) Snapshot(time.Time) []watch.Watch {
	return append([]watch.Watch(nil), s.ws...)
}
func (s *fakeWatchStore) End(id string, st watch.State, detail string, at, _ time.Time) error {
	for _, e := range s.ends {
		if e.id == id {
			return errors.New("ended twice")
		}
	}
	s.ends = append(s.ends, wcEnd{id, st, detail, at})
	return nil
}

func wcWatch(id string, k watch.Kind, params map[string]string, deadline time.Duration) watch.Watch {
	return watch.Watch{ID: id, Owner: "eng4", Kind: k, Params: params, Why: "test",
		Set: wcT0, Deadline: wcT0.Add(deadline), State: watch.Waiting}
}

func wcEndOf(t *testing.T, s *fakeWatchStore, id string) (wcEnd, bool) {
	t.Helper()
	for _, e := range s.ends {
		if e.id == id {
			return e, true
		}
	}
	return wcEnd{}, false
}

// --- bead_status ---

func TestWatchCheck_BeadStatusFiresAtTheBeadsOwnTime(t *testing.T) {
	w := newFakeWorld(wcT0.Add(10 * time.Minute))
	moved := wcT0.Add(4 * time.Minute)
	w.beads["ini-a"] = bdBead{ID: "ini-a", Status: "ready_for_qa", UpdatedAt: moved}
	s := &fakeWatchStore{ws: []watch.Watch{wcWatch("w1", watch.KindBeadStatus, map[string]string{"bead": "ini-a", "status": "ready_for_qa"}, time.Hour)}}
	judgeWatches(s, w, nil)
	e, ok := wcEndOf(t, s, "w1")
	if !ok || e.state != watch.Fired || !e.at.Equal(moved) || e.detail != "ini-a reached ready_for_qa" {
		t.Fatalf("end %+v (ended %v), want fired at the bead's updated_at", e, ok)
	}
}

func TestWatchCheck_BeadStatusMissesAtItsDeadline(t *testing.T) {
	w := newFakeWorld(wcT0.Add(30 * time.Minute))
	w.beads["ini-a"] = bdBead{ID: "ini-a", Status: "in_progress", UpdatedAt: wcT0}
	s := &fakeWatchStore{ws: []watch.Watch{wcWatch("w1", watch.KindBeadStatus, map[string]string{"bead": "ini-a", "status": "ready_for_qa"}, 20*time.Minute)}}
	w.now = wcT0.Add(19 * time.Minute)
	judgeWatches(s, w, nil)
	if len(s.ends) != 0 {
		t.Fatalf("ended before its deadline: %+v", s.ends)
	}
	w.now = wcT0.Add(21 * time.Minute)
	judgeWatches(s, w, nil)
	if e, ok := wcEndOf(t, s, "w1"); !ok || e.state != watch.Missed {
		t.Fatalf("end %+v, want missed past the deadline", e)
	}
}

// The condition came true after the deadline: missed, never fired late.
func TestWatchCheck_ATrueConditionAfterTheDeadlineIsMissed(t *testing.T) {
	w := newFakeWorld(wcT0.Add(30 * time.Minute))
	w.beads["ini-a"] = bdBead{ID: "ini-a", Status: "ready_for_qa", UpdatedAt: wcT0.Add(25 * time.Minute)}
	s := &fakeWatchStore{ws: []watch.Watch{wcWatch("w1", watch.KindBeadStatus, map[string]string{"bead": "ini-a", "status": "ready_for_qa"}, 20*time.Minute)}}
	judgeWatches(s, w, nil)
	if e, _ := wcEndOf(t, s, "w1"); e.state != watch.Missed {
		t.Fatalf("end %+v, want missed: true only after the deadline", e)
	}
}

// pm ruling (1): a status already reached is refused at set.
func TestWatchCheck_AStatusAlreadyReachedIsRefused(t *testing.T) {
	w := newFakeWorld(wcT0)
	w.beads["ini-a"] = bdBead{ID: "ini-a", Status: "ready_for_qa"}
	err := watchCheckers[watch.KindBeadStatus].Validate(map[string]string{"bead": "ini-a", "status": "ready_for_qa"}, "eng4", w)
	if err == nil || err.Error() != "ini-a is already ready_for_qa" {
		t.Fatalf("err %v, want the already-reached refusal", err)
	}
	if err := watchCheckers[watch.KindBeadStatus].Validate(map[string]string{"bead": "ini-a", "status": "qa_passed"}, "eng4", w); err != nil {
		t.Fatalf("a status not yet reached was refused: %v", err)
	}
}

func TestWatchCheck_ABeadNotInTheRigIsRefused(t *testing.T) {
	w := newFakeWorld(wcT0)
	err := watchCheckers[watch.KindBeadStatus].Validate(map[string]string{"bead": "ini-zz", "status": "closed"}, "eng4", w)
	if err == nil || !strings.Contains(err.Error(), "ini-zz") {
		t.Fatalf("err %v, want the unknown bead named", err)
	}
}

// --- bead_comment ---

func TestWatchCheck_BeadCommentFiresOnTheAuthorsCommentAfterSet(t *testing.T) {
	w := newFakeWorld(wcT0.Add(10 * time.Minute))
	w.beads["ini-a"] = bdBead{ID: "ini-a", Status: "in_progress"}
	w.comments["ini-a"] = []progress.Comment{
		{Author: "qa2", Text: "old", CreatedAt: wcT0.Add(-time.Hour)}, // before set: not counted
		{Author: "eng1", Text: "x", CreatedAt: wcT0.Add(2 * time.Minute)},
		{Author: "qa2", Text: "PASS", CreatedAt: wcT0.Add(5 * time.Minute)},
	}
	s := &fakeWatchStore{ws: []watch.Watch{wcWatch("w1", watch.KindBeadComment, map[string]string{"bead": "ini-a", "author": "qa2"}, time.Hour)}}
	judgeWatches(s, w, nil)
	e, ok := wcEndOf(t, s, "w1")
	if !ok || e.state != watch.Fired || !e.at.Equal(wcT0.Add(5*time.Minute)) || e.detail != "qa2 commented on ini-a" {
		t.Fatalf("end %+v, want fired at qa2's comment after set", e)
	}
}

// pm ruling (2): a watch on your own comment is refused.
func TestWatchCheck_AWatchOnYourOwnCommentIsRefused(t *testing.T) {
	w := newFakeWorld(wcT0)
	w.beads["ini-a"] = bdBead{ID: "ini-a"}
	err := watchCheckers[watch.KindBeadComment].Validate(map[string]string{"bead": "ini-a", "author": "eng4"}, "eng4", w)
	if err == nil || err.Error() != "a watch cannot wait for your own comment" {
		t.Fatalf("err %v, want the own-comment refusal", err)
	}
}

// Comments are read once per bead per pass, for every watch on it.
func TestWatchCheck_OneCommentsReadPerBeadForManyWatches(t *testing.T) {
	w := newFakeWorld(wcT0.Add(time.Minute))
	w.beads["ini-a"] = bdBead{ID: "ini-a"}
	cw := &cachedCommentsWorld{World: w, cache: map[string]cachedComments{}}
	s := &fakeWatchStore{ws: []watch.Watch{
		wcWatch("w1", watch.KindBeadComment, map[string]string{"bead": "ini-a", "author": "qa2"}, time.Hour),
		wcWatch("w2", watch.KindBeadComment, map[string]string{"bead": "ini-a", "author": "super"}, time.Hour),
		wcWatch("w3", watch.KindBeadComment, map[string]string{"bead": "ini-a", "author": "qa2"}, 2*time.Hour),
	}}
	judgeWatches(s, cw, nil)
	if w.reads["ini-a"] != 1 {
		t.Fatalf("read ini-a's comments %d times, want once", w.reads["ini-a"])
	}
}

// --- deadline ---

func TestWatchCheck_ADeadlineWatchFiresAtItsDeadline(t *testing.T) {
	w := newFakeWorld(wcT0)
	s := &fakeWatchStore{ws: []watch.Watch{wcWatch("w1", watch.KindDeadline, map[string]string{}, 15*time.Minute)}}
	w.now = wcT0.Add(15*time.Minute - time.Second)
	judgeWatches(s, w, nil)
	if len(s.ends) != 0 {
		t.Fatalf("fired before its deadline: %+v", s.ends)
	}
	w.now = wcT0.Add(15 * time.Minute)
	judgeWatches(s, w, nil)
	if e, ok := wcEndOf(t, s, "w1"); !ok || e.state != watch.Fired || !e.at.Equal(wcT0.Add(15*time.Minute)) {
		t.Fatalf("end %+v, want fired at the deadline", e)
	}
}

// --- send_delivered and send_held ---

func TestWatchCheck_SendDeliveredFiresAtTheTypedTime(t *testing.T) {
	w := newFakeWorld(wcT0.Add(5 * time.Minute))
	typed := wcT0.Add(3 * time.Minute)
	w.receipts["eng2/r7"] = ReceiptState{Pane: "eng2", ID: "r7", From: "eng4", State: ReceiptTyped, Since: wcT0, At: typed}
	s := &fakeWatchStore{ws: []watch.Watch{wcWatch("w1", watch.KindSendDelivered, map[string]string{"pane": "eng2", "receipt": "r7"}, time.Hour)}}
	judgeWatches(s, w, nil)
	if e, ok := wcEndOf(t, s, "w1"); !ok || e.state != watch.Fired || !e.at.Equal(typed) || e.detail != "your send to eng2 was delivered" {
		t.Fatalf("end %+v, want fired at the typed time", e)
	}
}

// After a restart a receipt is unknown: never fires, misses at its deadline.
func TestWatchCheck_AnUnknownReceiptMissesAtItsDeadlineOnly(t *testing.T) {
	w := newFakeWorld(wcT0)
	s := &fakeWatchStore{ws: []watch.Watch{wcWatch("w1", watch.KindSendDelivered, map[string]string{"pane": "eng2", "receipt": "r7"}, 10*time.Minute)}}
	w.now = wcT0.Add(9 * time.Minute)
	judgeWatches(s, w, nil)
	if len(s.ends) != 0 {
		t.Fatalf("an unknown receipt ended early: %+v", s.ends)
	}
	w.now = wcT0.Add(11 * time.Minute)
	judgeWatches(s, w, nil)
	e, _ := wcEndOf(t, s, "w1")
	if e.state != watch.Missed || !strings.Contains(e.detail, "unknown since the restart") {
		t.Fatalf("end %+v, want missed naming the unknown delivery", e)
	}
}

func TestWatchCheck_SendHeldFiresWhenStillHeldAfterFor(t *testing.T) {
	w := newFakeWorld(wcT0.Add(20 * time.Minute))
	held := wcT0.Add(time.Minute)
	w.receipts["eng2/r7"] = ReceiptState{Pane: "eng2", ID: "r7", From: "eng4", State: ReceiptHeld, Since: held}
	s := &fakeWatchStore{ws: []watch.Watch{wcWatch("w1", watch.KindSendHeld, map[string]string{"pane": "eng2", "receipt": "r7", "for": "15m"}, time.Hour)}}
	w.now = wcT0.Add(15 * time.Minute)
	judgeWatches(s, w, nil) // held 14 minutes
	if len(s.ends) != 0 {
		t.Fatalf("fired before 15 minutes held: %+v", s.ends)
	}
	w.now = wcT0.Add(20 * time.Minute)
	judgeWatches(s, w, nil)
	if e, ok := wcEndOf(t, s, "w1"); !ok || e.state != watch.Fired || !e.at.Equal(held.Add(15*time.Minute)) {
		t.Fatalf("end %+v, want fired at held+15m", e)
	}
}

// pm ruling (3): withdrawn or dropped ends MISSED at once, for both send
// kinds; super is told only when initech dropped it.
func TestWatchCheck_AWithdrawnOrDroppedSendMissesAtOnce(t *testing.T) {
	for _, c := range []struct {
		state, why, detail string
		super              bool
	}{
		{ReceiptWithdrawn, "", "your send to eng2 was withdrawn", false},
		{ReceiptDropped, "held five minutes", "your send to eng2 was dropped: held five minutes", true},
	} {
		for _, k := range []watch.Kind{watch.KindSendDelivered, watch.KindSendHeld} {
			w := newFakeWorld(wcT0.Add(time.Minute))
			w.receipts["eng2/r7"] = ReceiptState{Pane: "eng2", ID: "r7", From: "eng4", State: c.state, Why: c.why, Since: wcT0}
			p := map[string]string{"pane": "eng2", "receipt": "r7", "for": "15m"}
			if k == watch.KindSendDelivered {
				delete(p, "for")
			}
			s := &fakeWatchStore{ws: []watch.Watch{wcWatch("w1", k, p, time.Hour)}}
			var told []string
			judgeWatches(s, w, func(id string) { told = append(told, id) })
			e, _ := wcEndOf(t, s, "w1")
			if e.state != watch.Missed || e.detail != c.detail {
				t.Errorf("%s %s: end %+v, want missed at once with %q", k, c.state, e, c.detail)
			}
			if (len(told) == 1) != c.super {
				t.Errorf("%s %s: super told %v, want %v", k, c.state, told, c.super)
			}
		}
	}
}

// S2's self-firing refusal: a send kind naming the owner's own pane.
func TestWatchCheck_ASendWatchOnYourselfIsRefused(t *testing.T) {
	w := newFakeWorld(wcT0)
	for _, k := range []watch.Kind{watch.KindSendDelivered, watch.KindSendHeld} {
		p := map[string]string{"pane": "eng4", "receipt": "r7", "for": "5m"}
		if k == watch.KindSendDelivered {
			delete(p, "for")
		}
		err := watchCheckers[k].Validate(p, "eng4", w)
		if err == nil || !strings.Contains(err.Error(), "yourself") {
			t.Errorf("%s: err %v, want the self-firing refusal", k, err)
		}
	}
}

func TestWatchCheck_ASendThatIsNotYoursIsRefused(t *testing.T) {
	w := newFakeWorld(wcT0)
	w.receipts["eng2/r7"] = ReceiptState{Pane: "eng2", ID: "r7", From: "eng1", State: ReceiptHeld, Since: wcT0}
	err := watchCheckers[watch.KindSendDelivered].Validate(map[string]string{"pane": "eng2", "receipt": "r7"}, "eng4", w)
	if err == nil || !strings.Contains(err.Error(), "not yours") {
		t.Fatalf("err %v, want refused as not yours", err)
	}
}

// --- feed it nothing ---

func TestWatchCheck_NoWatchesDoesNothingAndReadsNothing(t *testing.T) {
	w := newFakeWorld(wcT0)
	s := &fakeWatchStore{}
	w.now = wcT0
	judgeWatches(s, w, nil)
	if len(s.ends) != 0 || len(w.reads) != 0 {
		t.Fatalf("ends %v reads %v, want none", s.ends, w.reads)
	}
}

func TestWatchCheck_AMissingParamIsRefusedByName(t *testing.T) {
	w := newFakeWorld(wcT0)
	for k, want := range map[watch.Kind]string{
		watch.KindBeadStatus:    "bead",
		watch.KindBeadComment:   "bead",
		watch.KindSendDelivered: "pane",
		watch.KindSendHeld:      "pane",
	} {
		err := watchCheckers[k].Validate(map[string]string{}, "eng4", w)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s with no params: err %v, want %q named", k, err, want)
		}
	}
	if err := watchCheckers[watch.KindDeadline].Validate(map[string]string{"bead": "x"}, "eng4", w); err == nil {
		t.Error("a deadline watch with a param was accepted")
	}
}

// A watch of a kind this build does not know is kept and never judged.
func TestWatchCheck_AnUnknownKindIsNeverJudged(t *testing.T) {
	w := newFakeWorld(wcT0.Add(2 * time.Hour))
	s := &fakeWatchStore{ws: []watch.Watch{wcWatch("w1", watch.Kind("idle"), map[string]string{}, time.Hour)}}
	judgeWatches(s, w, nil)
	if len(s.ends) != 0 {
		t.Fatalf("an unknown kind was judged: %+v", s.ends)
	}
}

// --- the world: comments read only when the count rose ---

func TestWatchCheck_CommentsAreReadOnlyWhenTheCountRose(t *testing.T) {
	var reads int
	orig := bdBeadComments
	bdBeadComments = func(context.Context, string, string) ([]progress.Comment, error) {
		reads++
		return nil, nil
	}
	t.Cleanup(func() { bdBeadComments = orig })
	tu := &TUI{}
	watched := map[string]bool{"ini-a": true}
	tu.readWatchComments(watched, []bdBead{{ID: "ini-a", CommentCount: 2}})
	tu.readWatchComments(watched, []bdBead{{ID: "ini-a", CommentCount: 2}})
	if reads != 1 {
		t.Fatalf("read %d times with no new comment, want once (the first)", reads)
	}
	tu.readWatchComments(watched, []bdBead{{ID: "ini-a", CommentCount: 3}})
	if reads != 2 {
		t.Fatalf("read %d times after the count rose, want 2", reads)
	}
	tu.readWatchComments(map[string]bool{}, []bdBead{{ID: "ini-a", CommentCount: 9}})
	if reads != 2 {
		t.Fatalf("read an unwatched bead's comments")
	}
}

// --- the production world and set-time validation (for Watches 2D) ---

func TestWatchWorld_ReadsTheLastBeadPollAndTheCommentCache(t *testing.T) {
	tu := &TUI{}
	tu.noteWatchBeads([]bdBead{{ID: "ini-a", Status: "in_progress"}}, true)
	tu.watchComments = map[string]cachedComments{"ini-a": {count: 1, cs: []progress.Comment{{Author: "qa2"}}, ok: true}}
	w := tu.watchWorld(wcT0)
	if !w.BeadsKnown() || w.Now() != wcT0 {
		t.Fatalf("world known %v now %v", w.BeadsKnown(), w.Now())
	}
	if b, ok := w.Bead("ini-a"); !ok || b.Status != "in_progress" {
		t.Fatalf("bead %+v %v", b, ok)
	}
	if cs, ok := w.Comments("ini-a"); !ok || len(cs) != 1 {
		t.Fatalf("comments %v %v", cs, ok)
	}
	if _, ok := w.Comments("ini-b"); ok {
		t.Fatal("comments of a bead never read came back as read")
	}
	if r := w.Receipt("eng2", "r1"); r.State != ReceiptUnknown {
		t.Fatalf("receipt %+v, want unknown until Watches 2E", r)
	}
}

// An unanswered poll keeps the last answered beads.
func TestWatchWorld_AnUnansweredPollKeepsTheLastBeads(t *testing.T) {
	tu := &TUI{}
	tu.noteWatchBeads([]bdBead{{ID: "ini-a", Status: "in_progress"}}, true)
	tu.noteWatchBeads(nil, false)
	if _, ok := tu.watchWorld(wcT0).Bead("ini-a"); !ok {
		t.Fatal("an unanswered poll dropped the beads")
	}
}

// Feed it nothing: before the first answered poll, a bead id is not refused
// as unknown, and the world says it does not know.
func TestWatchWorld_BeforeTheFirstPollBeadsAreUnknown(t *testing.T) {
	tu := &TUI{}
	w := tu.watchWorld(wcT0)
	if w.BeadsKnown() {
		t.Fatal("known before any poll")
	}
	if err := tu.validateWatch(watch.KindBeadStatus, map[string]string{"bead": "ini-a", "status": "closed"}, "eng4", wcT0); err != nil {
		t.Fatalf("refused before the first poll: %v", err)
	}
}

func TestWatchValidate_RefusesWithTheCheckersLineAndAnUnknownKind(t *testing.T) {
	tu := &TUI{}
	tu.noteWatchBeads([]bdBead{{ID: "ini-a", Status: "ready_for_qa"}}, true)
	if err := tu.validateWatch(watch.KindBeadStatus, map[string]string{"bead": "ini-a", "status": "ready_for_qa"}, "eng4", wcT0); err == nil || err.Error() != "ini-a is already ready_for_qa" {
		t.Fatalf("err %v", err)
	}
	if err := tu.validateWatch(watch.Kind("idle"), map[string]string{}, "eng4", wcT0); err == nil || !strings.Contains(err.Error(), "idle") {
		t.Fatalf("an unknown kind: err %v", err)
	}
}
