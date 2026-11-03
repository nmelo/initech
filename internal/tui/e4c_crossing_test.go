package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/progress"
)

// crossingRig stands in for bd and git, counting calls by bead. comments and
// commits answer by id; empty means the bead has none.
type crossingRig struct {
	beads      []bdBead
	comments   map[string][]progress.Comment
	commits    map[string][]progress.Commit
	bdErr      error
	bdCalls    map[string]int
	gitCalls   map[string]int
	duringRead func()
}

func (r *crossingRig) calls() (bd, git int) {
	for _, n := range r.bdCalls {
		bd += n
	}
	for _, n := range r.gitCalls {
		git += n
	}
	return bd, git
}

// crossingTUI is a parked fleet (p01..: idle) at stuck_after 15m with a clone,
// over the rig r.
func crossingTUI(t *testing.T, r *crossingRig) *TUI {
	t.Helper()
	r.bdCalls, r.gitCalls = map[string]int{}, map[string]int{}
	origL, origC, origB, origG, origH := bdListBeads, bdStatusCustom, bdBeadComments, gitBeadCommits, projectHasBeads
	t.Cleanup(func() {
		bdListBeads, bdStatusCustom, bdBeadComments, gitBeadCommits, projectHasBeads = origL, origC, origB, origG, origH
	})
	bdListBeads = func(ctx context.Context, dir string) ([]bdBead, error) { return r.beads, nil }
	bdStatusCustom = func(ctx context.Context, dir string) (string, error) {
		return "ready_for_qa,in_qa,qa_passed,waiting", nil
	}
	bdBeadComments = func(ctx context.Context, dir, id string) ([]progress.Comment, error) {
		r.bdCalls[id]++
		if r.duringRead != nil {
			r.duringRead()
		}
		return r.comments[id], r.bdErr
	}
	gitBeadCommits = func(ctx context.Context, clone, id string) ([]progress.Commit, error) {
		r.gitCalls[id]++
		return r.commits[id], nil
	}
	projectHasBeads = func(string) bool { return true }
	tui, _ := ysr3Parked(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "p01", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := e4cProject(t, "15m")
	p.Root, p.Roles = root, []string{"p01"}
	p.Repos = []config.Repo{{URL: "github.com:o/r.git", Name: "r"}}
	tui.project = p
	tui.projectRoot = root
	ysr2Pane(tui, "p01").SetBead("ini-c.1", "")
	return tui
}

// pace polls once at now and refreshes the lines.
func crossingPoll(tui *TUI, now time.Time) string {
	tui.pollBeadsOnce(now)
	tui.paceAt = time.Time{}
	tui.refreshPaceLines(now)
	return strings.Join(tui.paceStuckIDs["p03"], " ")
}

// AC5: a crossing bead costs one bd comments and one git log; a fresh bead and
// a closed one cost nothing; later polls cost nothing until the bead moves.
func TestCrossing_OneReadPerBeadThenNoneUntilItMoves(t *testing.T) {
	now := time.Now()
	r := &crossingRig{beads: []bdBead{
		{ID: "ini-c.1", Status: "in_progress", Assignee: "p01", UpdatedAt: now},
		{ID: "ini-c.2", Status: "in_progress", Assignee: "p03", UpdatedAt: now.Add(-time.Hour)},
		{ID: "ini-c.3", Status: "closed", ClosedAt: now.Add(-time.Hour)},
		{ID: "ini-c.4", Status: "open", UpdatedAt: now.Add(-time.Hour)},
	}}
	tui := crossingTUI(t, r)
	for i := 0; i < 4; i++ {
		if got := crossingPoll(tui, now.Add(time.Duration(i)*beadPollInterval)); got != "ini-c.2" {
			t.Fatalf("poll %d: stuck %q, want ini-c.2", i, got)
		}
	}
	if bd, git := r.calls(); bd != 1 || git != 1 || r.bdCalls["ini-c.2"] != 1 || r.gitCalls["ini-c.2"] != 1 {
		t.Errorf("four polls made bd %v and git %v, want one each for ini-c.2", r.bdCalls, r.gitCalls)
	}
	// The bead moves and ages past the value again: one more read.
	later := now.Add(time.Hour)
	r.beads[1].UpdatedAt = later
	crossingPoll(tui, later)
	if r.bdCalls["ini-c.2"] != 1 {
		t.Errorf("a bead that just moved was read again: %v", r.bdCalls)
	}
	crossingPoll(tui, later.Add(20*time.Minute))
	crossingPoll(tui, later.Add(21*time.Minute))
	if r.bdCalls["ini-c.2"] != 2 || r.gitCalls["ini-c.2"] != 2 {
		t.Errorf("the second crossing: bd %v git %v, want 2 each for ini-c.2", r.bdCalls, r.gitCalls)
	}
}

// AC5, the start floor gone: a move made before this window started counts at
// the first poll, both ways: a comment inside the value is not stuck; one past
// it is stuck at once, not an hour later.
func TestCrossing_AMoveBeforeTheStartReadsTrueAtTheFirstPoll(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		age  time.Duration
		want string
	}{{5 * time.Minute, ""}, {40 * time.Minute, "ini-c.2"}} {
		r := &crossingRig{
			beads:    []bdBead{{ID: "ini-c.2", Status: "in_progress", Assignee: "p03", UpdatedAt: now.Add(-3 * time.Hour), CommentCount: 1}},
			comments: map[string][]progress.Comment{"ini-c.2": {{Author: "eng1", Text: "x", CreatedAt: now.Add(-c.age)}}},
		}
		tui := crossingTUI(t, r)
		if got := crossingPoll(tui, now); got != c.want {
			t.Errorf("a comment %v old: stuck %q at the first poll, want %q", c.age, got, c.want)
		}
	}
}

// AC5: a commit alone is a move now: the read finds it and the bead is not
// stuck. A commit that does not name the bead is no move.
func TestCrossing_ACommitAloneIsAMove(t *testing.T) {
	now := time.Now()
	for subject, want := range map[string]string{"fix: the thing (ini-c.2)": "", "fix: the thing (ini-c.22)": "ini-c.2"} {
		r := &crossingRig{
			beads:   []bdBead{{ID: "ini-c.2", Status: "in_progress", Assignee: "p03", UpdatedAt: now.Add(-3 * time.Hour)}},
			commits: map[string][]progress.Commit{"ini-c.2": {{When: now.Add(-5 * time.Minute), Subject: subject}}},
		}
		tui := crossingTUI(t, r)
		if got := crossingPoll(tui, now); got != want {
			t.Errorf("commit %q: stuck %q, want %q", subject, got, want)
		}
	}
}

// A read that found a move gets one more read when that move crosses the value
// in turn, and then stops.
func TestCrossing_AMoveTheReadFoundCrossesOnceMore(t *testing.T) {
	now := time.Now()
	r := &crossingRig{
		beads:    []bdBead{{ID: "ini-c.2", Status: "in_progress", Assignee: "p03", UpdatedAt: now.Add(-3 * time.Hour)}},
		comments: map[string][]progress.Comment{"ini-c.2": {{CreatedAt: now.Add(-5 * time.Minute)}}},
	}
	tui := crossingTUI(t, r)
	if got := crossingPoll(tui, now); got != "" {
		t.Fatalf("stuck %q right after the comment", got)
	}
	later := now.Add(20 * time.Minute)
	if got := crossingPoll(tui, later); got != "ini-c.2" {
		t.Fatalf("stuck %q past the value, want ini-c.2", got)
	}
	crossingPoll(tui, later.Add(time.Minute))
	crossingPoll(tui, later.Add(2*time.Minute))
	if bd, git := r.calls(); bd != 2 || git != 2 {
		t.Errorf("bd %d git %d over two crossings, want 2 each", bd, git)
	}
}

// A bead is not counted while its read is in flight, so it cannot flash stuck
// before the read can say it moved.
func TestCrossing_ABeadIsNotCountedWhileItsReadRuns(t *testing.T) {
	now := time.Now()
	r := &crossingRig{beads: []bdBead{{ID: "ini-c.2", Status: "in_progress", Assignee: "p03", UpdatedAt: now.Add(-time.Hour)}}}
	tui := crossingTUI(t, r)
	saw := "not called"
	r.duringRead = func() {
		tui.paceAt = time.Time{}
		tui.refreshPaceLines(now)
		saw = strings.Join(tui.paceStuckIDs["p03"], " ")
	}
	if got := crossingPoll(tui, now); got != "ini-c.2" {
		t.Fatalf("after the read: stuck %q, want ini-c.2", got)
	}
	if saw != "" {
		t.Errorf("during the read: stuck %q, want none", saw)
	}
}

// A waiting bead crosses too, and a read that finds it moved keeps it out of
// the count; only beads that would be counted are read.
func TestCrossing_AWaitingBeadIsReadAndAWorkingOwnerIsNot(t *testing.T) {
	now := time.Now()
	r := &crossingRig{
		beads: []bdBead{
			{ID: "ini-c.5", Status: "waiting", UpdatedAt: now.Add(-time.Hour)},
			{ID: "ini-c.6", Status: "waiting", UpdatedAt: now.Add(-time.Hour)},
			{ID: "ini-c.7", Status: "blocked", Assignee: "p03", UpdatedAt: now.Add(-time.Hour)},
		},
		comments: map[string][]progress.Comment{"ini-c.6": {{CreatedAt: now.Add(-time.Minute)}}},
	}
	tui := crossingTUI(t, r)
	crossingPoll(tui, now)
	if len(tui.paceRows) != 1 || !strings.Contains(tui.paceRows[0], "1 waiting ini-c.5") {
		t.Errorf("rows %q, want ini-c.5 alone waiting", tui.paceRows)
	}
	if r.bdCalls["ini-c.7"] != 0 {
		t.Errorf("a blocked bead was read: %v", r.bdCalls)
	}
}

// Feed it nothing: bd fails the read: one try, no call per tick, and the bead
// reads by what the poll knows. No repo: no git call. No bd project: no read.
func TestCrossing_AFailedReadIsNotRetriedEachTick(t *testing.T) {
	now := time.Now()
	r := &crossingRig{beads: []bdBead{{ID: "ini-c.2", Status: "in_progress", Assignee: "p03", UpdatedAt: now.Add(-time.Hour)}}, bdErr: errors.New("bd down")}
	tui := crossingTUI(t, r)
	for i := 0; i < 3; i++ {
		if got := crossingPoll(tui, now.Add(time.Duration(i)*beadPollInterval)); got != "ini-c.2" {
			t.Errorf("poll %d: stuck %q", i, got)
		}
	}
	if bd, _ := r.calls(); bd != 1 {
		t.Errorf("a failed read was tried %d times", bd)
	}

	r2 := &crossingRig{beads: r.beads}
	tui2 := crossingTUI(t, r2)
	tui2.project.Repos = nil
	crossingPoll(tui2, now)
	if bd, git := r2.calls(); bd != 1 || git != 0 {
		t.Errorf("no repo: bd %d git %d, want 1 and 0", bd, git)
	}

	r3 := &crossingRig{beads: r.beads}
	tui3 := crossingTUI(t, r3)
	projectHasBeads = func(string) bool { return false }
	crossingPoll(tui3, now)
	if bd, git := r3.calls(); bd != 0 || git != 0 {
		t.Errorf("no bd project: bd %d git %d, want none", bd, git)
	}
}
