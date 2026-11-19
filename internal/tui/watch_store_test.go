package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/watch"
)

func watchRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

var wt0 = time.Date(2026, 11, 19, 17, 0, 0, 0, time.UTC)

// ini-0xkk: window 1 opens its store at start; no file is an empty store and
// no line.
func TestWatches_Window1LoadsItsStore(t *testing.T) {
	rec := recordInitechLines(t)
	tui := newTestTUI(testPane("super"))
	if tui.watches != nil {
		t.Fatal("a TUI has a store before it loads one")
	}
	tui.loadWatches(watchRoot(t), wt0)
	if tui.watches == nil || len(tui.watches.Snapshot(wt0)) != 0 {
		t.Fatalf("store %v", tui.watches)
	}
	if got := rec.all(); len(got) != 0 {
		t.Errorf("an empty start sent %q", got)
	}
}

// A bad file is set aside and super hears one line naming it.
func TestWatches_ABadFileTellsSuperOnce(t *testing.T) {
	rec := recordInitechLines(t)
	root := watchRoot(t)
	path := filepath.Join(root, ".initech", watchesFile)
	if err := os.WriteFile(path, []byte("version: 7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tui := newTestTUI(testPane("super"))
	tui.loadWatches(root, wt0)
	got := rec.waitFor(1)
	if len(got) != 1 || !strings.Contains(got[0], "watches.yaml") || !strings.Contains(got[0], "set aside") {
		t.Fatalf("lines %q", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("the bad file is still in place")
	}
}

// Reachability: the bead poll runs the tick on the main goroutine, whether or
// not bd answered, so an hour-old cancel leaves the history.
func TestWatches_TheBeadPollRunsTheTick(t *testing.T) {
	origL, origH := bdListBeads, projectHasBeads
	t.Cleanup(func() { bdListBeads, projectHasBeads = origL, origH })
	bdListBeads = func(ctx context.Context, dir string) ([]bdBead, error) { return nil, errors.New("bd down") }
	projectHasBeads = func(string) bool { return false }
	tui := newTestTUI(testPane("super"))
	tui.loadWatches(watchRoot(t), wt0)
	id, err := tui.watches.Add(watch.Watch{Owner: "eng8", Kind: watch.KindDeadline, Why: "x", Deadline: wt0.Add(3 * time.Hour)}, wt0)
	if err != nil {
		t.Fatal(err)
	}
	if err := tui.watches.Cancel(id, "eng8", wt0); err != nil {
		t.Fatal(err)
	}
	tui.pollBeadsOnce(wt0.Add(59 * time.Minute))
	if len(tui.watches.Snapshot(wt0)) != 1 {
		t.Fatal("history pruned before its hour")
	}
	tui.pollBeadsOnce(wt0.Add(61 * time.Minute))
	if n := len(tui.watches.Snapshot(wt0)); n != 0 {
		t.Errorf("the poll did not run the tick: %d watches left", n)
	}
}

// A real failing write (.initech read-only): the ending is held, and super is
// told once after five minutes of ticks, naming the file; once the folder is
// writable again the next tick writes it.
func TestWatches_FailingWritesTellSuperOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a read-only folder is the Unix way to fail a write here")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only folder")
	}
	rec := recordInitechLines(t)
	root := watchRoot(t)
	dir := filepath.Join(root, ".initech")
	tui := newTestTUI(testPane("super"))
	tui.loadWatches(root, wt0)
	id, err := tui.watches.Add(watch.Watch{Owner: "eng8", Kind: watch.KindDeadline, Why: "x", Deadline: wt0.Add(time.Hour)}, wt0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_ = tui.watches.End(id, watch.Fired, "x", wt0, wt0)
	for sec := 0; sec <= 600; sec += 10 {
		tui.watchesTick(wt0.Add(time.Duration(sec) * time.Second))
	}
	// Once is the store's own count (TestStore_PassTellsSuperOnceAfterFiveMinutesOfFailures);
	// here, that the tick turns it into the line.
	if got := rec.waitFor(1); len(got) != 1 || !strings.Contains(got[0], "watches.yaml") {
		t.Fatalf("lines %q, want one naming watches.yaml", got)
	}
	if len(tui.watches.Owed()) != 0 {
		t.Fatal("an ending not on disk is owed")
	}
	_ = os.Chmod(dir, 0o755)
	tui.watchesTick(wt0.Add(11 * time.Minute))
	if len(tui.watches.Owed()) != 1 {
		t.Error("the tick did not write the held ending once it could")
	}
}

// Feed it nothing: no store (a second window, or before the load), the tick
// does nothing.
func TestWatches_NoStoreTheTickDoesNothing(t *testing.T) {
	tui := newTestTUI(testPane("super"))
	tui.watchesTick(wt0)
	if tui.watches != nil {
		t.Error("the tick made a store")
	}
}
