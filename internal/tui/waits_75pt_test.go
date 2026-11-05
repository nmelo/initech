package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/progress"
)

// ini-75pt in the poll: a waiting bead's stated wait (its newest WAIT
// comment) is read at most once a minute, an edge older than it is not
// judged, and a wait with several targets is over only when all are. No
// assertion here waits on a clock: whether a line went out is read from
// paceOverSent, set on the main loop before the send.

var c0 = time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC)

func blocks(to string, at time.Time) progress.Dependency {
	return progress.Dependency{DependsOn: to, Type: "blocks", CreatedAt: at}
}

// waitComments makes bdBeadComments answer from m, counting reads per bead.
func useWaitComments(t *testing.T, m map[string][]progress.Comment, fail *atomic.Bool, reads map[string]*atomic.Int32) {
	t.Helper()
	bdBeadComments = func(ctx context.Context, dir, id string) ([]progress.Comment, error) {
		if c, ok := reads[id]; ok {
			c.Add(1)
		}
		if fail != nil && fail.Load() {
			return nil, errors.New("bd unreachable")
		}
		return m[id], nil
	}
}

func sentFor(tui *TUI, id string) bool {
	for k := range tui.paceOverSent {
		if strings.HasPrefix(k, id+"|") {
			return true
		}
	}
	return false
}

// AC1, the fed0 shape: an edge from an earlier wait closes, but the bead's
// stated wait is newer and names nothing initech can judge: no line.
func TestWaits75_TheFed0ShapeSendsNothing(t *testing.T) {
	r := &waitsRig{beads: []bdBead{
		{ID: "ini-fed0", Status: "waiting", Dependencies: []progress.Dependency{blocks("ini-nxyf", c0)}},
		{ID: "ini-nxyf", Status: "closed", ClosedAt: c0.Add(time.Hour)},
	}}
	tui, _ := waitsTUI(t, r)
	useWaitComments(t, map[string][]progress.Comment{"ini-fed0": {
		{Text: "WAIT OVER: ◆ ini-nxyf landed", CreatedAt: c0.Add(4 * time.Minute)},
		{Text: "WAIT (operator via theguy): shape is changing to a five-box rig board", CreatedAt: c0.Add(5 * time.Minute)},
	}}, nil, nil)
	tui.pollBeadsOnce(c0.Add(2 * time.Hour))
	if sentFor(tui, "ini-fed0") || tui.paceWaits["ini-fed0"].Over {
		t.Fatalf("the fed0 shape went over: %+v", tui.paceWaits["ini-fed0"])
	}
}

// AC1, the fed0 twin: the stated wait names a bead; its close sends once.
func TestWaits75_TheFed0TwinSendsOnce(t *testing.T) {
	r := &waitsRig{beads: []bdBead{
		{ID: "ini-fed0", Status: "waiting", Dependencies: []progress.Dependency{blocks("ini-nxyf", c0)}},
		{ID: "ini-nxyf", Status: "closed"},
		{ID: "ini-xyz", Status: "in_progress"},
	}}
	tui, rec := waitsTUI(t, r)
	useWaitComments(t, map[string][]progress.Comment{"ini-fed0": {{Text: "WAIT: ini-xyz board shape", CreatedAt: c0.Add(5 * time.Minute)}}}, nil, nil)
	tui.pollBeadsOnce(c0.Add(time.Hour))
	if sentFor(tui, "ini-fed0") {
		t.Fatal("sent while ini-xyz is open")
	}
	r.beads[2].Status = "closed"
	tui.pollBeadsOnce(c0.Add(2 * time.Hour))
	got := rec.waitFor(1)
	if len(got) != 1 || !strings.HasSuffix(got[0], "☾ ini-fed0 waits on ini-xyz: ini-xyz closed; move it on") {
		t.Fatalf("lines %q, want one naming ini-xyz", got)
	}
}

// AC1, the pt3j shape: two edges, the first closed: no line; the twin, both
// closed: one line naming the one that closed last.
func TestWaits75_ThePt3jShapeAndTwin(t *testing.T) {
	r := &waitsRig{beads: []bdBead{
		{ID: "ini-pt3j", Status: "waiting", Dependencies: []progress.Dependency{blocks("ini-t0y1", c0), blocks("ini-8rkg", c0.Add(time.Minute))}},
		{ID: "ini-t0y1", Status: "closed", ClosedAt: c0.Add(10 * time.Minute)},
		{ID: "ini-8rkg", Status: "in_progress"},
	}}
	tui, rec := waitsTUI(t, r)
	tui.pollBeadsOnce(c0.Add(time.Hour))
	if sentFor(tui, "ini-pt3j") {
		t.Fatal("sent with ini-8rkg open")
	}
	r.beads[2].Status, r.beads[2].ClosedAt = "closed", c0.Add(40*time.Minute)
	tui.pollBeadsOnce(c0.Add(2 * time.Hour))
	got := rec.waitFor(1)
	if len(got) != 1 || !strings.HasSuffix(got[0], "☾ ini-pt3j waits on ini-8rkg: ini-8rkg closed; move it on") {
		t.Fatalf("lines %q, want one naming ini-8rkg", got)
	}
}

// AC1, the l1r shape: an old edge closes, the stated wait names another
// bead still open: no line; that bead's close sends once.
func TestWaits75_TheL1rShapeAndTwin(t *testing.T) {
	r := &waitsRig{beads: []bdBead{
		{ID: "ini-l1r", Status: "waiting", Dependencies: []progress.Dependency{blocks("ini-r0q.18", c0)}},
		{ID: "ini-r0q.18", Status: "closed"},
		{ID: "ini-zz9", Status: "waiting_on_nelson"},
	}}
	tui, rec := waitsTUI(t, r)
	useWaitComments(t, map[string][]progress.Comment{"ini-l1r": {{Text: "WAIT: ini-zz9 Nelson's word", CreatedAt: c0.Add(30 * time.Minute)}}}, nil, nil)
	tui.pollBeadsOnce(c0.Add(time.Hour))
	if sentFor(tui, "ini-l1r") {
		t.Fatal("the old edge's close sent a line")
	}
	r.beads[2].Status = "closed"
	tui.pollBeadsOnce(c0.Add(2 * time.Hour))
	if got := rec.waitFor(1); len(got) != 1 || !strings.Contains(got[0], "ini-l1r waits on ini-zz9") {
		t.Fatalf("lines %q, want one naming ini-zz9", got)
	}
}

// AC3: comments are read at most once a minute per waiting bead.
func TestWaits75_CommentsAreReadOnceAMinute(t *testing.T) {
	r := &waitsRig{beads: []bdBead{{ID: "ini-w", Status: "waiting"}, {ID: "ini-n", Status: "in_progress"}}}
	tui, _ := waitsTUI(t, r)
	reads := map[string]*atomic.Int32{"ini-w": {}, "ini-n": {}}
	useWaitComments(t, nil, nil, reads)
	for s := 0; s < 150; s += 10 {
		tui.pollBeadsOnce(c0.Add(time.Duration(s) * time.Second))
	}
	if n := reads["ini-w"].Load(); n != 3 {
		t.Fatalf("%d comment reads over 150 s of polls, want 3 (at 0, 60 and 120 s)", n)
	}
	if n := reads["ini-n"].Load(); n != 0 {
		t.Fatalf("a bead not waiting had its comments read %d times", n)
	}
}

// AC3: a failed read keeps the last judgement; a bead never read is not
// judged over, so a failed read cannot send the old false line.
func TestWaits75_AFailedReadKeepsTheLastJudgement(t *testing.T) {
	r := &waitsRig{beads: []bdBead{
		{ID: "ini-fed0", Status: "waiting", Dependencies: []progress.Dependency{blocks("ini-nxyf", c0)}},
		{ID: "ini-nxyf", Status: "closed"},
	}}
	tui, _ := waitsTUI(t, r)
	var fail atomic.Bool
	fail.Store(true)
	useWaitComments(t, map[string][]progress.Comment{"ini-fed0": {{Text: "WAIT (super): a human answer", CreatedAt: c0.Add(5 * time.Minute)}}}, &fail, nil)
	tui.pollBeadsOnce(c0.Add(time.Hour))
	if sentFor(tui, "ini-fed0") || tui.paceWaits["ini-fed0"].Over {
		t.Fatal("a bead whose comments were never read went over")
	}
	fail.Store(false)
	tui.pollBeadsOnce(c0.Add(time.Hour + time.Minute))
	first := tui.paceWaits["ini-fed0"]
	fail.Store(true)
	tui.pollBeadsOnce(c0.Add(time.Hour + 2*time.Minute))
	if got := tui.paceWaits["ini-fed0"]; got != first || got.Over {
		t.Fatalf("after a failed read: %+v, want the last judgement %+v", got, first)
	}
}

// AC3: the line carries the bead id and the target, never comment text.
func TestWaits75_TheLineCarriesNoCommentText(t *testing.T) {
	r := &waitsRig{beads: []bdBead{{ID: "ini-q", Status: "waiting"}, {ID: "ini-t", Status: "closed"}}}
	tui, rec := waitsTUI(t, r)
	useWaitComments(t, map[string][]progress.Comment{"ini-q": {{Text: "WAIT (do-not-quote-me): ini-t private words here", CreatedAt: c0}}}, nil, nil)
	tui.pollBeadsOnce(c0.Add(time.Hour))
	got := rec.waitFor(1)
	if len(got) != 1 || strings.Contains(got[0], "private") || strings.Contains(got[0], "do-not-quote-me") {
		t.Fatalf("lines %q, want one with no comment text", got)
	}
}

// AC4: a moved wait (a newer WAIT comment) gives a new target, so the old
// wait_over_sent record is cleared and the new wait can send once more.
func TestWaits75_AMovedWaitClearsTheRecord(t *testing.T) {
	r := &waitsRig{beads: []bdBead{
		{ID: "ini-m", Status: "waiting", Metadata: []byte(`{"wait_over_sent":"ini-a"}`)},
		{ID: "ini-a", Status: "closed"},
		{ID: "ini-b", Status: "in_progress"},
	}}
	tui, _ := waitsTUI(t, r)
	useWaitComments(t, map[string][]progress.Comment{"ini-m": {
		{Text: "WAIT: ini-a", CreatedAt: c0},
		{Text: "WAIT: ini-b next", CreatedAt: c0.Add(10 * time.Minute)},
	}}, nil, nil)
	tui.pollBeadsOnce(c0.Add(time.Hour))
	if wrote, ok := tui.paceOverWrote["ini-m"]; !ok || wrote != "" {
		t.Fatalf("paceOverWrote = %q %v, want the record cleared for the moved wait", wrote, ok)
	}
	if tui.paceWaits["ini-m"].Target.Name != "ini-b" {
		t.Fatalf("target %+v, want ini-b", tui.paceWaits["ini-m"].Target)
	}
}
