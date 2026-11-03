package progress

import (
	"sort"
	"testing"
	"time"
)

// ini-bfu: stuck counts from the bead's last move (status, comment or
// commit), the same move the quiet list reads, on every surface.

// bfuInput is one in-progress bead, ini-q.1, owned by eng1 (idle), whose
// status changed two hours ago; comments and commits are added per test.
func bfuInput() Input {
	return Input{
		Rig: "demo", Prefix: "ini-q.", Hours: 6, Now: paceNow, Rule: testRule,
		Beads: []Bead{
			{ID: "ini-q.1", Title: "a step", Status: "in_progress", Assignee: "eng1", UpdatedAt: paceNow.Add(-2 * time.Hour)},
			{ID: "ini-q.2", Title: "another", Status: "closed", ClosedAt: paceNow.Add(-3 * time.Hour)},
		},
		Comments:     map[string][]Comment{},
		CommitsKnown: true,
		Panes:        map[string]string{"eng1": "idle"},
		PanesKnown:   true,
	}
}

// AC1: a comment inside QuietAfter moves the bead: stuck 0, and the quiet
// list agrees (empty).
func TestBuild_ACommentInsideQuietAfterIsNotStuck(t *testing.T) {
	in := bfuInput()
	in.Comments["ini-q.1"] = []Comment{{Author: "eng1", Text: "AC1 proven", CreatedAt: paceNow.Add(-10 * time.Minute)}}
	r := Build(in)
	if r.Stuck != 0 || len(r.Quiet) != 0 {
		t.Fatalf("a comment 10 minutes old: stuck %d, quiet %v; want 0 and none", r.Stuck, r.Quiet)
	}
}

// AC1: the same for a commit naming the bead inside QuietAfter.
func TestBuild_ACommitInsideQuietAfterIsNotStuck(t *testing.T) {
	in := bfuInput()
	in.Commits = []Commit{{When: paceNow.Add(-5 * time.Minute), Subject: "fix(x): the thing (ini-q.1)"}}
	r := Build(in)
	if r.Stuck != 0 || len(r.Quiet) != 0 {
		t.Fatalf("a commit 5 minutes old: stuck %d, quiet %v; want 0 and none", r.Stuck, r.Quiet)
	}
}

// AC5 positive twin: no move of any kind past QuietAfter and an idle owner
// is stuck 1, and the quiet list has the same bead; a working owner is never
// stuck.
func TestBuild_NoMovePastQuietAfterIsStuckUnlessTheOwnerWorks(t *testing.T) {
	in := bfuInput()
	in.Comments["ini-q.1"] = []Comment{{Author: "eng1", CreatedAt: paceNow.Add(-90 * time.Minute)}}
	r := Build(in)
	if r.Stuck != 1 || len(r.Quiet) != 1 || r.Quiet[0].ID != "ini-q.1" {
		t.Fatalf("quiet 90 minutes, idle owner: stuck %d, quiet %v; want 1 and ini-q.1", r.Stuck, r.Quiet)
	}
	in.Panes["eng1"] = "running"
	if r := Build(in); r.Stuck != 0 {
		t.Errorf("a working owner reads stuck %d", r.Stuck)
	}
}

// AC3: one function. The same beads with the same last moves give the same
// stuck count through PaceOf, PaceOver and the report, and the stuck ids are
// the quiet list's ids with an owner not working.
func TestStuck_OneCountOnEverySurface(t *testing.T) {
	in := bfuInput()
	in.Beads = append(in.Beads,
		Bead{ID: "ini-q.3", Status: "in_progress", Assignee: "eng2", UpdatedAt: paceNow.Add(-3 * time.Hour)},
		Bead{ID: "ini-q.4", Status: "in_qa", Assignee: "qa1", UpdatedAt: paceNow.Add(-4 * time.Hour)},
		Bead{ID: "ini-q.5", Status: "blocked", Assignee: "eng3", UpdatedAt: paceNow.Add(-5 * time.Hour)},
	)
	in.Panes["eng2"], in.Panes["qa1"], in.Panes["eng3"] = "idle", "waiting", "idle"
	in.Comments["ini-q.3"] = []Comment{{Author: "eng2", CreatedAt: paceNow.Add(-2 * time.Minute)}}
	r := Build(in)

	beads := append([]Bead(nil), in.Beads...)
	for i, b := range beads {
		beads[i].Moved, _ = LastMove(b, in.Comments[b.ID], in.Commits)
	}
	over := PaceOver(beads, "ini-q", in.Panes, in.Rule, in.Now, 6*time.Hour)
	of := PaceOf(beads, "ini-q.1", in.Panes, in.Rule, in.Now, 6*time.Hour)
	if r.Stuck != over.Stuck || over.Stuck != of.Stuck || r.Stuck != 2 {
		t.Fatalf("stuck: report %d, PaceOver %d, PaceOf %d; want 2 on all three", r.Stuck, over.Stuck, of.Stuck)
	}
	var stuck, quiet []string
	for _, b := range beads {
		if Stuck(b, in.Panes[b.Assignee], in.Rule, in.Now) {
			stuck = append(stuck, b.ID)
		}
	}
	// The quiet list also lists a blocked bead (a named wait, counted
	// blocked and never stuck), so only the others are compared.
	status := map[string]string{}
	for _, b := range beads {
		status[b.ID] = b.Status
	}
	for _, q := range r.Quiet {
		if q.Wait != WaitWorkingUnrecorded && !blockedStates[status[q.ID]] {
			quiet = append(quiet, q.ID)
		}
	}
	sort.Strings(stuck)
	sort.Strings(quiet)
	if len(stuck) != 2 || stuck[0] != "ini-q.1" || stuck[1] != "ini-q.4" || len(quiet) != 2 || quiet[0] != stuck[0] || quiet[1] != stuck[1] {
		t.Errorf("stuck ids %v, quiet ids %v; want ini-q.1 and ini-q.4 in both", stuck, quiet)
	}
}

// LastMoved is the newer of updated_at and Moved; zero Moved is updated_at.
func TestBead_LastMovedIsTheNewerMove(t *testing.T) {
	b := Bead{UpdatedAt: paceNow.Add(-time.Hour)}
	if !b.LastMoved().Equal(b.UpdatedAt) {
		t.Errorf("no Moved: %v, want updated_at", b.LastMoved())
	}
	b.Moved = paceNow
	if !b.LastMoved().Equal(paceNow) {
		t.Errorf("Moved newer: %v, want Moved", b.LastMoved())
	}
	b.Moved = paceNow.Add(-2 * time.Hour)
	if !b.LastMoved().Equal(b.UpdatedAt) {
		t.Errorf("Moved older: %v, want updated_at", b.LastMoved())
	}
}
