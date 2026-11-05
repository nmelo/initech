package progress

import (
	"strings"
	"testing"
	"time"
)

func idleRule() Rule {
	r := NewRule(time.Hour, []string{"in_progress", "ready_for_qa", "in_qa", "qa_passed"})
	r.IdleAfter = 20 * time.Minute
	return r
}

// ini-9dv AC1: the reading itself. Idle 20m on an in_progress bead reads;
// 19m does not; a bead that moved a minute ago still reads (the pane is
// quiet, not the bead); a waiting bead, a bead with no owner, a running owner
// and an unknown idle time never read.
func TestIdleOwner_ThePaneIsQuietNotTheBead(t *testing.T) {
	rule := idleRule()
	moved := paceNow.Add(-time.Minute)
	for _, c := range []struct {
		name  string
		b     Bead
		state string
		want  bool
	}{
		{"idle 20m", Bead{ID: "a", Status: "in_progress", Assignee: "eng1", UpdatedAt: moved, OwnerIdle: 20 * time.Minute}, "idle", true},
		{"pending 25m", Bead{ID: "a", Status: "in_progress", Assignee: "eng1", UpdatedAt: moved, OwnerIdle: 25 * time.Minute}, "pending", true},
		{"idle 19m", Bead{ID: "a", Status: "in_progress", Assignee: "eng1", UpdatedAt: moved, OwnerIdle: 19*time.Minute + 59*time.Second}, "idle", false},
		{"waiting bead", Bead{ID: "a", Status: "waiting", Assignee: "eng1", OwnerIdle: time.Hour}, "idle", false},
		{"in_qa bead", Bead{ID: "a", Status: "in_qa", Assignee: "qa1", OwnerIdle: time.Hour}, "idle", false},
		{"no owner", Bead{ID: "a", Status: "in_progress", OwnerIdle: time.Hour}, "idle", false},
		{"running owner", Bead{ID: "a", Status: "in_progress", Assignee: "eng1", OwnerIdle: time.Hour}, "running", false},
		{"background owner", Bead{ID: "a", Status: "in_progress", Assignee: "eng1", OwnerIdle: time.Hour}, "background", false},
		{"owner on a dialog", Bead{ID: "a", Status: "in_progress", Assignee: "eng1", OwnerIdle: time.Hour}, "waiting", false},
		{"no pane here", Bead{ID: "a", Status: "in_progress", Assignee: "eng1", OwnerIdle: time.Hour}, "", false},
		{"idle time not known", Bead{ID: "a", Status: "in_progress", Assignee: "eng1"}, "idle", false},
	} {
		if got := IdleOwner(c.b, c.state, rule); got != c.want {
			t.Errorf("%s: IdleOwner = %v, want %v", c.name, got, c.want)
		}
	}
	if IdleOwner(Bead{Status: "in_progress", Assignee: "eng1", OwnerIdle: time.Hour}, "idle", NewRule(time.Hour, nil)) {
		t.Error("a zero IdleAfter read idle-owner")
	}
}

// AC1: the three readings each count on their own. A bead quiet past
// stuck_after with an idle owner is stuck AND idle-owner; one that moved a
// minute ago with an owner idle 20m is idle-owner and not stuck; a waiting
// bead is never idle-owner. The board line, the report and its JSON agree.
func TestIdleOwner_CountsBesideStuckAndWaiting(t *testing.T) {
	in := Input{Rig: "demo", Prefix: "idl.", Hours: 6, Now: paceNow, Rule: idleRule(), PanesKnown: true,
		Panes: map[string]string{"eng1": "idle", "eng2": "idle", "eng3": "idle", "eng4": "running"},
		Beads: []Bead{
			{ID: "idl.1", Title: "fresh, owner quiet", Status: "in_progress", Assignee: "eng1", UpdatedAt: paceNow.Add(-time.Minute), OwnerIdle: 23 * time.Minute},
			{ID: "idl.2", Title: "stuck and quiet", Status: "in_progress", Assignee: "eng2", UpdatedAt: paceNow.Add(-3 * time.Hour), OwnerIdle: 3 * time.Hour},
			{ID: "idl.3", Title: "parked", Status: "waiting", Assignee: "eng3", UpdatedAt: paceNow.Add(-3 * time.Hour), OwnerIdle: 3 * time.Hour},
			{ID: "idl.4", Title: "at work", Status: "in_progress", Assignee: "eng4", UpdatedAt: paceNow.Add(-3 * time.Hour), OwnerIdle: 3 * time.Hour},
		}}
	r := Build(in)
	if strings.Join(r.StuckIDs, ",") != "idl.2" {
		t.Errorf("stuck %v, want idl.2 only (idle-owner does not make stuck)", r.StuckIDs)
	}
	var ids []string
	for _, row := range r.IdleOwner {
		ids = append(ids, row.ID+" "+row.Owner+" "+Minutes(time.Duration(row.IdleMin)*time.Minute))
	}
	if strings.Join(ids, ",") != "idl.1 eng1 23m,idl.2 eng2 180m" {
		t.Errorf("idle owner %v, want idl.1 (moved a minute ago) and idl.2 (also stuck); never the waiting idl.3", ids)
	}
	if len(r.Waiting) != 1 || r.Waiting[0].ID != "idl.3" {
		t.Errorf("waiting %+v, want idl.3", r.Waiting)
	}
	p := PaceOver(in.Beads, "idl", in.Panes, in.Rule, in.Now, HeaderWindow)
	if !strings.HasSuffix(p.Line(), "· 1 stuck · 1 waiting · 2 idle") {
		t.Errorf("line %q, want the idle count beside stuck and waiting", p.Line())
	}
	out := RenderReport(r)
	for _, want := range []string{"stuck 1, blocked 0, waiting 1, owner idle 2", "owner idle 20m or more:", "  idl.1 eng1: idle 23m", "  idl.2 eng2: idle 180m"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table lacks %q:\n%s", want, out)
		}
	}
	if r.IdleAfter != "20m" {
		t.Errorf("idle_after %q", r.IdleAfter)
	}
}

// AC4, feed it nothing: no initech answering (no panes), nothing idle, and an
// empty list read no idle owner and print no heading.
func TestIdleOwner_NothingKnownReadsNothing(t *testing.T) {
	b := Bead{ID: "idl.1", Status: "in_progress", Assignee: "eng1", UpdatedAt: paceNow, OwnerIdle: time.Hour}
	for _, in := range []Input{
		{Prefix: "idl.", Now: paceNow, Rule: idleRule(), Beads: []Bead{b}, Panes: map[string]string{"eng1": "idle"}, PanesKnown: false},
		{Prefix: "idl.", Now: paceNow, Rule: idleRule(), Beads: []Bead{b}, Panes: map[string]string{}, PanesKnown: true},
		{Prefix: "idl.", Now: paceNow, Rule: idleRule()},
	} {
		r := Build(in)
		if len(r.IdleOwner) != 0 || strings.Contains(RenderReport(r), "owner idle") {
			t.Errorf("panes %v known %v: idle owner %+v", in.Panes, in.PanesKnown, r.IdleOwner)
		}
	}
	// Positive twin: the same bead with its owner's pane known idle reads.
	r := Build(Input{Prefix: "idl.", Now: paceNow, Rule: idleRule(), Beads: []Bead{b}, Panes: map[string]string{"eng1": "idle"}, PanesKnown: true})
	if len(r.IdleOwner) != 1 {
		t.Errorf("the twin did not read: %+v", r.IdleOwner)
	}
}
