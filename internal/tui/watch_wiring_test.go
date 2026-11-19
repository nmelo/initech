package tui

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/progress"
	"github.com/nmelo/initech/internal/watch"
)

// ini-20r2: the pass runs from the real entry point, the bead poll, and from
// a timer at the nearest deadline. bd and the timer are stubbed.

// wiredTUI is a window-1 TUI with a real store in a temp folder.
func wiredTUI(t *testing.T, now time.Time) (*TUI, string) {
	t.Helper()
	tu := newTestTUI()
	path := filepath.Join(t.TempDir(), "watches.yaml")
	s, note := watch.Open(path, now)
	if note != "" {
		t.Fatal(note)
	}
	tu.watches = s
	return tu, path
}

func stubBeads(t *testing.T, beads func() []bdBead) {
	t.Helper()
	orig := bdListBeads
	bdListBeads = func(context.Context, string) ([]bdBead, error) { return beads(), nil }
	t.Cleanup(func() { bdListBeads = orig })
}

// fakeAfter records the timer the pass arms, never firing on its own.
type fakeAfter struct {
	d     time.Duration
	f     func()
	armed int
	stops int
}

func stubAfter(t *testing.T) *fakeAfter {
	t.Helper()
	fa := &fakeAfter{}
	orig := watchAfterFunc
	watchAfterFunc = func(d time.Duration, f func()) func() bool {
		fa.d, fa.f = d, f
		fa.armed++
		return func() bool { fa.stops++; return true }
	}
	t.Cleanup(func() { watchAfterFunc = orig })
	return fa
}

func storedState(t *testing.T, path, id string) watch.State {
	t.Helper()
	s, note := watch.Open(path, wcT0.Add(24*time.Hour))
	if note != "" {
		t.Fatal(note)
	}
	for _, w := range s.Snapshot(wcT0) {
		if w.ID == id {
			return w.State
		}
	}
	t.Fatalf("no %s on disk", id)
	return ""
}

func TestWatchWiring_APollEndsAWatchAndTheEndingIsOnDisk(t *testing.T) {
	stubAfter(t)
	tu, path := wiredTUI(t, wcT0)
	status := "in_progress"
	stubBeads(t, func() []bdBead {
		return []bdBead{{ID: "ini-a", Status: status, UpdatedAt: wcT0.Add(2 * time.Minute)}}
	})
	tu.pollBeadsOnce(wcT0) // the first poll: the bead is known
	id, err := tu.watches.Add(watch.Watch{Owner: "eng4", Kind: watch.KindBeadStatus, Why: "qa", Deadline: wcT0.Add(time.Hour),
		Params: map[string]string{"bead": "ini-a", "status": "ready_for_qa"}}, wcT0)
	if err != nil {
		t.Fatal(err)
	}
	tu.pollBeadsOnce(wcT0.Add(time.Minute))
	if got := storedState(t, path, id); got != watch.Waiting {
		t.Fatalf("ended before the bead moved: %s", got)
	}
	status = "ready_for_qa"
	tu.pollBeadsOnce(wcT0.Add(3 * time.Minute))
	if got := storedState(t, path, id); got != watch.Fired {
		t.Fatalf("on disk %s after the poll that saw the status, want fired", got)
	}
}

// A comment watch through the poll: bd comments is read once while the count
// stays, again when it rises, and the watch fires on the author's comment.
func TestWatchWiring_ACommentWatchReadsCommentsOnlyWhenTheCountRises(t *testing.T) {
	stubAfter(t)
	tu, _ := wiredTUI(t, wcT0)
	count := 1
	stubBeads(t, func() []bdBead { return []bdBead{{ID: "ini-a", Status: "in_progress", CommentCount: count}} })
	var reads int
	var cs []progress.Comment
	orig := bdBeadComments
	bdBeadComments = func(context.Context, string, string) ([]progress.Comment, error) { reads++; return cs, nil }
	t.Cleanup(func() { bdBeadComments = orig })
	tu.pollBeadsOnce(wcT0)
	id, err := tu.watches.Add(watch.Watch{Owner: "eng4", Kind: watch.KindBeadComment, Why: "verdict", Deadline: wcT0.Add(time.Hour),
		Params: map[string]string{"bead": "ini-a", "author": "qa2"}}, wcT0)
	if err != nil {
		t.Fatal(err)
	}
	tu.pollBeadsOnce(wcT0.Add(time.Minute))
	tu.pollBeadsOnce(wcT0.Add(2 * time.Minute))
	if reads != 1 {
		t.Fatalf("%d comment reads with the count unchanged, want 1", reads)
	}
	count, cs = 2, []progress.Comment{{Author: "qa2", Text: "PASS", CreatedAt: wcT0.Add(150 * time.Second)}}
	tu.pollBeadsOnce(wcT0.Add(3 * time.Minute))
	if reads != 2 {
		t.Fatalf("%d comment reads after the count rose, want 2", reads)
	}
	for _, w := range tu.watches.Snapshot(wcT0) {
		if w.ID == id && w.State != watch.Fired {
			t.Fatalf("%s is %s, want fired on qa2's comment", id, w.State)
		}
	}
}

// The timer: armed at the nearest waiting deadline, and its firing ends the
// deadline watch without waiting for a poll.
func TestWatchWiring_TheTimerIsArmedAtTheNearestDeadlineAndEndsIt(t *testing.T) {
	fa := stubAfter(t)
	tu, path := wiredTUI(t, wcT0)
	stubBeads(t, func() []bdBead { return nil })
	near, err := tu.watches.Add(watch.Watch{Owner: "eng4", Kind: watch.KindDeadline, Why: "check back", Deadline: wcT0.Add(5 * time.Minute)}, wcT0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tu.watches.Add(watch.Watch{Owner: "eng1", Kind: watch.KindDeadline, Why: "later", Deadline: wcT0.Add(20 * time.Minute)}, wcT0); err != nil {
		t.Fatal(err)
	}
	tu.pollBeadsOnce(wcT0)
	if fa.armed == 0 || fa.d != 5*time.Minute {
		t.Fatalf("timer armed %d times for %v, want the 5m deadline", fa.armed, fa.d)
	}
	watchNow = func() time.Time { return wcT0.Add(5 * time.Minute) }
	t.Cleanup(func() { watchNow = time.Now })
	fa.f()
	if got := storedState(t, path, near); got != watch.Fired {
		t.Fatalf("the timer left %s %s, want fired", near, got)
	}
	if fa.d != 15*time.Minute {
		t.Fatalf("re-armed for %v, want the next deadline 15m on", fa.d)
	}
}

// Feed it nothing: a window with no store (a second window, serve) polls as
// before and arms nothing.
func TestWatchWiring_NoStoreArmsNothingAndJudgesNothing(t *testing.T) {
	fa := stubAfter(t)
	tu := newTestTUI()
	stubBeads(t, func() []bdBead { return []bdBead{{ID: "ini-a", Status: "closed"}} })
	tu.pollBeadsOnce(wcT0)
	if fa.armed != 0 {
		t.Fatalf("armed a timer with no store")
	}
}

// The deadline timer's pass runs on the main goroutine at any moment, so it
// may overlap a bead poll reading comments on the poller goroutine. The
// comment cache must be touched by one goroutine only (a security review of
// 5e92bea). This test overlaps the two on a real main loop; under -race (the
// rigs' race job) the old code reports a data race on watchComments.
func TestWatchWiring_ATimerPassDuringAPollReadingCommentsDoesNotRace(t *testing.T) {
	stubAfter(t)
	tu, _ := wiredTUI(t, wcT0)
	if _, err := tu.watches.Add(watch.Watch{Owner: "eng4", Kind: watch.KindBeadComment, Why: "verdict", Deadline: wcT0.Add(time.Hour),
		Params: map[string]string{"bead": "ini-a", "author": "qa2"}}, wcT0); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	stubBeads(t, func() []bdBead {
		return []bdBead{{ID: "ini-a", Status: "in_progress", CommentCount: int(count.Add(1))}}
	})
	orig := bdBeadComments
	bdBeadComments = func(context.Context, string, string) ([]progress.Comment, error) {
		return []progress.Comment{{Author: "eng1", CreatedAt: wcT0}}, nil
	}
	t.Cleanup(func() { bdBeadComments = orig })
	watchMainLoop(t, tu)
	const n = 40
	var wg sync.WaitGroup
	var polled atomic.Bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer polled.Store(true)
		for i := 0; i < n; i++ {
			tu.pollBeadsOnce(wcT0.Add(time.Duration(i) * time.Second))
		}
	}()
	// Timer passes for as long as the polls run, so the two overlap.
	for i := 0; !polled.Load(); i++ {
		tu.runOnMain(func() { tu.judgeWatchPass(wcT0.Add(time.Duration(i%n) * time.Second)) })
	}
	wg.Wait()
}

// watchMainLoop gives the fixture the one main goroutine Run gives a real
// TUI, so runOnMain serializes as it does in production; stopped at cleanup.
// (A copy of suspend_fate_rig_test.go's startMainLoop, which that file's
// build constraint keeps off Windows.)
func watchMainLoop(t *testing.T, tu *TUI) {
	t.Helper()
	tu.ipcCh = make(chan ipcAction, 32)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case op := <-tu.ipcCh:
				op.fn()
				close(op.done)
			case <-tu.quitCh:
				return
			}
		}
	}()
	t.Cleanup(func() {
		tu.requestQuit()
		<-done
	})
}

// lead1 (a): the bead reaches its status 3 s before the deadline, after the
// last poll; the timer's pass at the deadline sees the old poll and must not
// miss; the next poll fires it at the bead's own time.
func TestWatchWiring_ATimerOnAnOldPollDoesNotMissAStatusReachedInTime(t *testing.T) {
	fa := stubAfter(t)
	tu, path := wiredTUI(t, wcT0)
	status, updated := "in_progress", wcT0
	stubBeads(t, func() []bdBead { return []bdBead{{ID: "ini-a", Status: status, UpdatedAt: updated}} })
	tu.pollBeadsOnce(wcT0)
	id, err := tu.watches.Add(watch.Watch{Owner: "eng4", Kind: watch.KindBeadStatus, Why: "qa", Deadline: wcT0.Add(time.Minute),
		Params: map[string]string{"bead": "ini-a", "status": "ready_for_qa"}}, wcT0)
	if err != nil {
		t.Fatal(err)
	}
	tu.pollBeadsOnce(wcT0.Add(50 * time.Second))
	status, updated = "ready_for_qa", wcT0.Add(57*time.Second)
	// A real AfterFunc fires a moment after its duration.
	watchNow = func() time.Time { return wcT0.Add(time.Minute + time.Millisecond) }
	t.Cleanup(func() { watchNow = time.Now })
	fa.f() // the timer at the deadline, on the 50 s poll
	if got := storedState(t, path, id); got != watch.Waiting {
		t.Fatalf("the timer ended it %s on a poll read before the deadline", got)
	}
	tu.pollBeadsOnce(wcT0.Add(65 * time.Second))
	if got := storedState(t, path, id); got != watch.Fired {
		t.Fatalf("after the next poll: %s, want fired (reached at 57 s)", got)
	}
}

// lead1 (b): bd down across the deadline: no miss on the old poll; the
// answered poll after it judges by the evidence time.
func TestWatchWiring_BdDownAcrossTheDeadlineWaitsForAnAnswer(t *testing.T) {
	stubAfter(t)
	tu, path := wiredTUI(t, wcT0)
	up := true
	status, updated := "in_progress", wcT0
	orig := bdListBeads
	bdListBeads = func(context.Context, string) ([]bdBead, error) {
		if !up {
			return nil, context.DeadlineExceeded
		}
		return []bdBead{{ID: "ini-a", Status: status, UpdatedAt: updated}}, nil
	}
	t.Cleanup(func() { bdListBeads = orig })
	tu.pollBeadsOnce(wcT0)
	id, err := tu.watches.Add(watch.Watch{Owner: "eng4", Kind: watch.KindBeadStatus, Why: "qa", Deadline: wcT0.Add(time.Minute),
		Params: map[string]string{"bead": "ini-a", "status": "ready_for_qa"}}, wcT0)
	if err != nil {
		t.Fatal(err)
	}
	up = false
	tu.pollBeadsOnce(wcT0.Add(70 * time.Second))
	if got := storedState(t, path, id); got != watch.Waiting {
		t.Fatalf("bd down: %s, want still waiting", got)
	}
	up, status, updated = true, "ready_for_qa", wcT0.Add(40*time.Second)
	tu.pollBeadsOnce(wcT0.Add(90 * time.Second))
	if got := storedState(t, path, id); got != watch.Fired {
		t.Fatalf("bd back: %s, want fired at the 40 s evidence", got)
	}
}

// lead1 (c): a deadline watch kept across a restart, in a rig the bead poll
// never runs in, still fires: loading the store arms the timer.
func TestWatchWiring_LoadingTheStoreArmsTheTimer(t *testing.T) {
	fa := stubAfter(t)
	root := t.TempDir()
	path := filepath.Join(root, ".initech", watchesFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	s, _ := watch.Open(path, wcT0)
	if _, err := s.Add(watch.Watch{Owner: "eng4", Kind: watch.KindDeadline, Why: "check", Deadline: wcT0.Add(10 * time.Minute)}, wcT0); err != nil {
		t.Fatal(err)
	}
	tu := newTestTUI()
	orig := watchNow
	watchNow = func() time.Time { return wcT0.Add(time.Minute) }
	t.Cleanup(func() { watchNow = orig })
	tu.loadWatches(root, wcT0.Add(time.Minute))
	if fa.armed == 0 || fa.d != 9*time.Minute {
		t.Fatalf("load armed %d times for %v, want 9m", fa.armed, fa.d)
	}
}

// A comment watch on a bead that gets no new comment still misses: each
// answered poll after the deadline counts as reading the unchanged comments.
func TestWatchWiring_ACommentWatchWithNoNewCommentMissesAfterItsDeadline(t *testing.T) {
	stubAfter(t)
	tu, path := wiredTUI(t, wcT0)
	stubBeads(t, func() []bdBead { return []bdBead{{ID: "ini-a", Status: "in_progress", CommentCount: 1}} })
	orig := bdBeadComments
	bdBeadComments = func(context.Context, string, string) ([]progress.Comment, error) { return nil, nil }
	t.Cleanup(func() { bdBeadComments = orig })
	tu.pollBeadsOnce(wcT0)
	id, err := tu.watches.Add(watch.Watch{Owner: "eng4", Kind: watch.KindBeadComment, Why: "verdict", Deadline: wcT0.Add(time.Minute),
		Params: map[string]string{"bead": "ini-a", "author": "qa2"}}, wcT0)
	if err != nil {
		t.Fatal(err)
	}
	tu.pollBeadsOnce(wcT0.Add(30 * time.Second))
	tu.pollBeadsOnce(wcT0.Add(70 * time.Second))
	if got := storedState(t, path, id); got != watch.Missed {
		t.Fatalf("%s after a poll past the deadline, want missed", got)
	}
}

// eng8's gap: in a rig without bd nothing polls, so window 1 ticks the
// watches itself: a due deadline fires and old history is pruned on a tick,
// with nothing waiting to arm the deadline timer.
func TestWatchWiring_ARigWithoutBdTicksTheWatches(t *testing.T) {
	stubAfter(t)
	tu, path := wiredTUI(t, wcT0)
	id, err := tu.watches.Add(watch.Watch{Owner: "eng4", Kind: watch.KindDeadline, Why: "check", Deadline: wcT0.Add(time.Minute)}, wcT0)
	if err != nil {
		t.Fatal(err)
	}
	// The store is the main goroutine's, as in production: a real main loop,
	// and the test's own store calls go through it.
	watchMainLoop(t, tu)
	ticks := make(chan time.Time)
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() { tu.watchTickLoop(quit, ticks); close(done) }()
	ticks <- wcT0.Add(2 * time.Minute)
	ticks <- wcT0.Add(2 * time.Minute) // the second send returns once the first tick is done
	if got := storedState(t, path, id); got != watch.Fired {
		t.Fatalf("after a tick past its deadline: %s, want fired", got)
	}
	// History is pruned an hour after it starts: for a cancelled watch, its
	// cancel (a fired one counts from its line's delivery, Watches 2C).
	var gone string
	tu.runOnMain(func() {
		gone, err = tu.watches.Add(watch.Watch{Owner: "eng4", Kind: watch.KindDeadline, Why: "never mind", Deadline: wcT0.Add(time.Hour)}, wcT0.Add(2*time.Minute))
		if err == nil {
			err = tu.watches.Cancel(gone, "eng4", wcT0.Add(2*time.Minute))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	ticks <- wcT0.Add(3*time.Minute + watch.History)
	ticks <- wcT0.Add(3*time.Minute + watch.History)
	var listed []watch.Watch
	tu.runOnMain(func() { listed = tu.watches.Snapshot(wcT0) })
	for _, w := range listed {
		if w.ID == gone {
			t.Fatalf("%s still listed an hour after its cancel: the tick did not prune", gone)
		}
	}
	close(quit)
	<-done
}

// A rig with bd, or a second window, starts no watch tick: the bead poll
// already runs the pass and the tick.
func TestWatchWiring_TheWatchTickStartsOnlyInWindowOneWithoutBd(t *testing.T) {
	var started int
	origT := watchTicker
	watchTicker = func(time.Duration) (<-chan time.Time, func()) { started++; return make(chan time.Time), func() {} }
	origH := projectHasBeads
	t.Cleanup(func() { watchTicker, projectHasBeads = origT, origH })
	quit := make(chan struct{})
	defer close(quit)
	tu := newTestTUI()
	projectHasBeads = func(string) bool { return true }
	tu.startWatchTicker(quit)
	if started != 0 {
		t.Fatal("a rig with bd started the watch tick")
	}
	projectHasBeads = func(string) bool { return false }
	tu.startWatchTicker(quit)
	if started != 1 {
		t.Fatalf("a rig without bd started %d watch ticks, want 1", started)
	}
}

// pm's bound through the real poll and store: bd silent 10 minutes past the
// deadline ends the watch 'could not tell'; bd answering later with the
// status reached changes nothing: it ended once.
func TestWatchWiring_BdSilentPastTheBoundEndsOnceAndBdComingBackChangesNothing(t *testing.T) {
	stubAfter(t)
	tu, path := wiredTUI(t, wcT0)
	var told []string
	tu.onWatchDropped = func(id string) { told = append(told, id) }
	up, status := true, "in_progress"
	orig := bdListBeads
	bdListBeads = func(context.Context, string) ([]bdBead, error) {
		if !up {
			return nil, context.DeadlineExceeded
		}
		return []bdBead{{ID: "ini-a", Status: status, UpdatedAt: wcT0.Add(30 * time.Second)}}, nil
	}
	t.Cleanup(func() { bdListBeads = orig })
	tu.pollBeadsOnce(wcT0)
	id, err := tu.watches.Add(watch.Watch{Owner: "eng4", Kind: watch.KindBeadStatus, Why: "qa", Deadline: wcT0.Add(time.Minute),
		Params: map[string]string{"bead": "ini-a", "status": "ready_for_qa"}}, wcT0)
	if err != nil {
		t.Fatal(err)
	}
	up = false
	tu.pollBeadsOnce(wcT0.Add(11*time.Minute + time.Second))
	if got := storedState(t, path, id); got != watch.Missed || len(told) != 1 {
		t.Fatalf("bd silent past the bound: %s, super told %v; want missed, told once", got, told)
	}
	up, status = true, "ready_for_qa"
	tu.pollBeadsOnce(wcT0.Add(12 * time.Minute))
	if got := storedState(t, path, id); got != watch.Missed || len(told) != 1 {
		t.Fatalf("after bd came back: %s, told %v; want it left missed, nothing re-sent", got, told)
	}
}
