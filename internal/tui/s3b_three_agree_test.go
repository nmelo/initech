package tui

import (
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/progress"
)

// ini-s3b AC5: the pane header, the overlay's effort row and initech
// progress agree on closed, total, stuck and blocked for the same beads.
func TestBlocked_TheThreeSurfacesAgree(t *testing.T) {
	now := time.Now()
	q := now.Add(-90 * time.Minute)
	beads := []bdBead{
		{ID: "ini-x.1", Status: "closed", ClosedAt: now.Add(-time.Hour)},
		{ID: "ini-x.2", Status: "closed", ClosedAt: now.Add(-2 * time.Hour)},
		{ID: "ini-x.3", Status: "in_progress", Assignee: "eng1", UpdatedAt: now.Add(-5 * time.Minute)},
		{ID: "ini-x.4", Status: "in_progress", Assignee: "eng2", UpdatedAt: q},
		{ID: "ini-x.5", Status: "blocked", Assignee: "eng3", UpdatedAt: q},
		{ID: "ini-x.6", Status: "deferred", Assignee: "eng4", UpdatedAt: q},
		{ID: "ini-x.7", Status: "waiting_on_nelson", Assignee: "eng5", UpdatedAt: q},
	}
	tui, s := newTestTUIWithScreen("eng1", "eng2", "eng3", "eng4", "eng5")
	states := map[string]ActivityState{"eng1": StateRunning, "eng2": StateIdle, "eng3": StateIdle, "eng4": StateIdle, "eng5": StateIdle}
	panes := map[string]string{}
	for n, st := range states {
		setPaneState(ysr2Pane(tui, n), st)
		panes[n] = st.String()
	}
	ysr2Pane(tui, "eng1").SetBead("ini-x.3", "")
	tui.notePaceAnswer(beads, true)
	tui.refreshPaceLines(now)

	header := ysr2Pane(tui, "eng1").PaceLine()
	_, rows := effortBlock(tui, overlayFrameRows(tui, s))
	pb := make([]progress.Bead, len(beads))
	for i, b := range beads {
		pb[i] = progress.Bead{ID: b.ID, Status: b.Status, Assignee: b.Assignee, UpdatedAt: b.UpdatedAt, ClosedAt: b.ClosedAt}
	}
	rep := progress.Build(progress.Input{Rig: "r", Prefix: "ini-x.", Hours: 6, Now: now, Beads: pb, Panes: panes, PanesKnown: true})

	line := regexp.MustCompile(`^ini-x (\d+)/(\d+) · [\d.]+/h · (\d+) stuck(?: · (\d+) blocked)?$`)
	read := func(name, l string) [4]int {
		m := line.FindStringSubmatch(l)
		if m == nil {
			t.Fatalf("%s reads %q", name, l)
		}
		var out [4]int
		for i := 0; i < 4; i++ {
			out[i], _ = strconv.Atoi(m[i+1])
		}
		return out
	}
	want := [4]int{2, 6, 1, 2}
	if got := read("the header", header); got != want {
		t.Errorf("the header %q counts %v, want %v", header, got, want)
	}
	if len(rows) != 1 {
		t.Fatalf("overlay rows %q, want one", rows)
	}
	if got := read("the overlay row", rows[0]); got != want {
		t.Errorf("the overlay row %q counts %v, want %v", rows[0], got, want)
	}
	if got := [4]int{rep.Closed, rep.Total, rep.Stuck, rep.Blocked}; got != want {
		t.Errorf("initech progress counts %v, want %v", got, want)
	}
}
