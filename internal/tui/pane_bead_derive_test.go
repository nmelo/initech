package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ini-wra.1: a pane's bead comes from bd; initech bead is the override.

func bdAt(id, assignee string, updated time.Time) bdBead {
	return bdBead{ID: id, Title: "title of " + id, Status: "in_progress", Assignee: assignee, UpdatedAt: updated}
}

var t0 = time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC)

func TestDeriveBead_PicksTheRolesInProgressBead(t *testing.T) {
	beads := []bdBead{
		bdAt("ini-a", "eng2", t0),
		bdAt("ini-b", "eng1", t0),
		{ID: "ini-c", Status: "ready_for_qa", Assignee: "eng1", UpdatedAt: t0.Add(time.Hour)},
	}
	if b, ok := deriveBead("eng1", beads, ""); !ok || b.ID != "ini-b" {
		t.Errorf("eng1 derived %q (ok=%v), want ini-b: the in_progress bead assigned to it", b.ID, ok)
	}
	// AC5: a bead assigned to another role is not this pane's; a status other
	// than in_progress is not either; a role with none derives nothing.
	if b, ok := deriveBead("qa1", beads, ""); ok {
		t.Errorf("qa1 holds no bead but derived %q", b.ID)
	}
	if _, ok := deriveBead("eng1", nil, ""); ok {
		t.Error("an empty bd answer derived a bead")
	}
}

// AC2: two in_progress beads for one role: the one the pane last named wins,
// else the newest updated, whatever order bd lists them in.
func TestDeriveBead_TwoBeadsLastNamedThenNewest(t *testing.T) {
	older, newer := bdAt("ini-old", "eng1", t0), bdAt("ini-new", "eng1", t0.Add(time.Minute))
	for _, order := range [][]bdBead{{older, newer}, {newer, older}} {
		if b, _ := deriveBead("eng1", order, ""); b.ID != "ini-new" {
			t.Errorf("no name: derived %q, want the newest updated ini-new", b.ID)
		}
		if b, _ := deriveBead("eng1", order, "ini-old"); b.ID != "ini-old" {
			t.Errorf("last named ini-old: derived %q, want ini-old", b.ID)
		}
		if b, _ := deriveBead("eng1", order, "ini-gone"); b.ID != "ini-new" {
			t.Errorf("last named a bead bd no longer lists: derived %q, want the newest", b.ID)
		}
	}
}

// pollTUI is a window-1 TUI whose bd is the given stub.
func pollTUI(t *testing.T, names []string, bd func(context.Context, string) ([]bdBead, error)) *TUI {
	t.Helper()
	old := bdListBeads
	bdListBeads = bd
	t.Cleanup(func() { bdListBeads = old })
	tui, _ := newTestTUIWithScreen(names...)
	tui.projectRoot = t.TempDir()
	return tui
}

// AC1: a role with one in_progress bead shows it after one poll, with no
// initech bead call: on the pane, in the list reply and in the ribbon look.
// Red at base: nothing derived it, so the pane showed no bead.
func TestBeadPoll_OneInProgressBeadShowsWithNoInitechBeadCall(t *testing.T) {
	tui := pollTUI(t, []string{"eng1", "eng2"}, func(context.Context, string) ([]bdBead, error) {
		return []bdBead{bdAt("ini-wra.1", "eng1", t0)}, nil
	})
	tui.pollBeadsOnce(t0)
	eng1, eng2 := tui.panes[0].(*Pane), tui.panes[1].(*Pane)
	if got := ribbonBeads(eng1); len(got) != 1 || got[0] != "ini-wra.1" {
		t.Fatalf("eng1's list beads %v, want [ini-wra.1]", got)
	}
	if eng1.BeadTitle() != "title of ini-wra.1" {
		t.Errorf("eng1's bead title %q, want bd's", eng1.BeadTitle())
	}
	if look, _ := eng1.chromeLook(false, 1); look.bead != "ini-wra.1" {
		t.Errorf("eng1's ribbon bead %q, want ini-wra.1", look.bead)
	}
	if got := ribbonBeads(eng2); len(got) != 0 {
		t.Errorf("eng2 holds no bead in bd but shows %v", got)
	}
	if id, _, since := eng1.DerivedBeadSince(); id != "ini-wra.1" || !since.Equal(t0) {
		t.Errorf("derived %q since %v, want ini-wra.1 first seen at the poll %v", id, since, t0)
	}
}

// AC3: initech bead <id> overrides the derived bead, the poll never replaces
// it, and --clear returns to the derived bead at once, without a poll.
func TestBeadPoll_OverrideWinsAndClearReturnsToBd(t *testing.T) {
	tui := pollTUI(t, []string{"eng1"}, func(context.Context, string) ([]bdBead, error) {
		return []bdBead{bdAt("ini-bd", "eng1", t0)}, nil
	})
	p := tui.panes[0].(*Pane)
	tui.pollBeadsOnce(t0)
	p.SetBead("ini-manual", "")
	tui.pollBeadsOnce(t0.Add(beadPollInterval))
	if got := p.BeadIDs(); len(got) != 1 || got[0] != "ini-manual" {
		t.Fatalf("after an override and a poll the pane shows %v, want the override ini-manual", got)
	}
	p.SetBead("", "")
	if got := p.BeadIDs(); len(got) != 1 || got[0] != "ini-bd" {
		t.Errorf("after --clear the pane shows %v, want bd's ini-bd at once", got)
	}
}

// AC4: bd unreachable keeps the last derived bead and marks it stale (never a
// clear); the next answer clears the mark, and a reassignment in bd moves the
// pane's bead at that poll.
func TestBeadPoll_UnreachableKeepsTheBeadMarkedStale(t *testing.T) {
	answer := []bdBead{bdAt("ini-x", "eng1", t0)}
	var fail bool
	tui := pollTUI(t, []string{"eng1"}, func(context.Context, string) ([]bdBead, error) {
		if fail {
			return nil, errors.New("bd: connection refused")
		}
		return answer, nil
	})
	p := tui.panes[0].(*Pane)
	tui.pollBeadsOnce(t0)
	fail = true
	tui.pollBeadsOnce(t0.Add(beadPollInterval))
	if got := p.BeadIDs(); len(got) != 1 || got[0] != "ini-x" {
		t.Fatalf("bd down: the pane shows %v, want the last derived ini-x kept", got)
	}
	if !p.BeadStale() || !beadStaleOf(p) {
		t.Error("bd down: the bead is not marked stale")
	}
	if look, _ := p.chromeLook(false, 1); look.beadTitle != BeadStaleNote {
		t.Errorf("bd down: the ribbon's bead title %q, want %q", look.beadTitle, BeadStaleNote)
	}
	fail = false
	answer = []bdBead{bdAt("ini-y", "eng1", t0.Add(time.Minute))}
	tui.pollBeadsOnce(t0.Add(2 * beadPollInterval))
	if got := p.BeadIDs(); len(got) != 1 || got[0] != "ini-y" || p.BeadStale() {
		t.Errorf("bd back with a reassignment: shows %v stale=%v, want ini-y and no mark", got, p.BeadStale())
	}
	// An override is never stale: it does not come from bd.
	p.SetBead("ini-manual", "")
	fail = true
	tui.pollBeadsOnce(t0.Add(3 * beadPollInterval))
	if p.BeadStale() {
		t.Error("an override was marked stale by a failed bd poll")
	}
}

// A poll applies to a suspended pane like any other (pm's ruling).
func TestBeadPoll_SuspendedPanesArePolledToo(t *testing.T) {
	tui := pollTUI(t, []string{"eng1"}, func(context.Context, string) ([]bdBead, error) {
		return []bdBead{bdAt("ini-s", "eng1", t0)}, nil
	})
	p := tui.panes[0].(*Pane)
	p.mu.Lock()
	p.alive, p.suspended = false, true
	p.mu.Unlock()
	tui.pollBeadsOnce(t0)
	if p.BeadID() != "ini-s" {
		t.Errorf("a suspended pane derived %q, want ini-s", p.BeadID())
	}
}

// AC5 feed it nothing: a project with no .beads directory starts no poller,
// so bd is never called and nothing is marked stale; the twin: a project with
// .beads starts it and calls bd.
func TestBeadPoller_NoBdProjectPollsNothing(t *testing.T) {
	calls := make(chan struct{}, 4)
	tui := pollTUI(t, []string{"eng1"}, func(context.Context, string) ([]bdBead, error) {
		calls <- struct{}{}
		return nil, errors.New("must not be called")
	})
	quit := make(chan struct{})
	defer close(quit)
	tui.startBeadPoller(quit)
	select {
	case <-calls:
		t.Fatal("a project with no .beads called bd")
	case <-time.After(200 * time.Millisecond):
	}
	if tui.panes[0].(*Pane).BeadStale() {
		t.Error("a project with no bd marked a bead stale")
	}

	if err := os.Mkdir(filepath.Join(tui.projectRoot, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldHas := projectHasBeads
	projectHasBeads = func(dir string) bool { // bd on PATH is not a test's to require
		fi, err := os.Stat(filepath.Join(dir, ".beads"))
		return err == nil && fi.IsDir()
	}
	defer func() { projectHasBeads = oldHas }()
	tui.startBeadPoller(quit)
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("a project with .beads never called bd")
	}
}

// A second window does not poll: it shows what window 1 broadcasts.
func TestBeadPoller_SecondWindowDoesNotPoll(t *testing.T) {
	calls := make(chan struct{}, 4)
	tui := pollTUI(t, []string{"eng1"}, func(context.Context, string) ([]bdBead, error) {
		calls <- struct{}{}
		return nil, nil
	})
	os.Mkdir(filepath.Join(tui.projectRoot, ".beads"), 0o755)
	oldHas := projectHasBeads
	projectHasBeads = func(string) bool { return true }
	defer func() { projectHasBeads = oldHas }()
	tui.windowID = WindowPeerName(2)
	quit := make(chan struct{})
	defer close(quit)
	tui.startBeadPoller(quit)
	select {
	case <-calls:
		t.Fatal("a second window called bd")
	case <-time.After(200 * time.Millisecond):
	}
}

// The no-bead marker is the dispatcher's word, so it stands over bd's bead
// until the next registration or clear.
func TestBeadPoll_NoBeadMarkerOutranksTheDerivedBead(t *testing.T) {
	tui := pollTUI(t, []string{"eng1"}, func(context.Context, string) ([]bdBead, error) {
		return []bdBead{bdAt("ini-bd", "eng1", t0)}, nil
	})
	p := tui.panes[0].(*Pane)
	p.SetNoBeadWork("look into the flake")
	tui.pollBeadsOnce(t0)
	if got := p.BeadIDs(); len(got) != 0 || p.NoBeadWork() == "" {
		t.Errorf("no-bead marker set: shows beads %v, marker %q; want the marker alone", got, p.NoBeadWork())
	}
}
