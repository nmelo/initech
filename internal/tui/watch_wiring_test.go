package tui

import (
	"context"
	"path/filepath"
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
