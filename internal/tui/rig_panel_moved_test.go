package tui

import (
	"fmt"
	"github.com/nmelo/initech/internal/progress"
	"strings"
	"testing"
	"time"
)

// ini-9z0b: the moved box's rules on a fixed clock.

func movedText(rows []movedRow) string {
	var out []string
	for _, r := range rows {
		pad := strings.Repeat("  ", r.Depth)
		switch {
		case r.More > 0:
			out = append(out, fmt.Sprintf("%s+%d more", pad, r.More))
		case r.Effort && r.ID == "":
			out = append(out, pad+"["+r.Title+"]")
		case r.Effort:
			var flags []string
			for _, f := range r.Flags {
				flags = append(flags, f.Text)
			}
			out = append(out, fmt.Sprintf("%s%s %s %s %s %s", pad, r.ID, r.Title, r.Done, r.Rate, strings.Join(flags, ",")))
		default:
			out = append(out, fmt.Sprintf("%s%s %s %s", pad, r.ID, r.Kind, r.Ago))
		}
	}
	return strings.Join(out, "\n")
}

// busyBeads is m2's shape: an effort with three moved beads, a nested
// effort under it with two, a closed bead, an archived bead, an effort that
// moved nothing, and a loose bead.
func busyBeads() []movedBead {
	return []movedBead{
		{ID: "h.pnxg", Title: "Today", Status: "in_progress", Priority: 1},
		// Listed oldest first, so the box's newest-first order is its own.
		{ID: "h.79", Parent: "h.pnxg", Title: "Alert email", Status: "open", LastMoved: ago(9 * time.Minute), Kind: "filed"},
		{ID: "h.77", Parent: "h.pnxg", Title: "Usage tile", Status: "in_progress", LastMoved: ago(4 * time.Minute), Kind: "claimed"},
		{ID: "h.78", Parent: "h.pnxg", Title: "Detections", Status: "in_qa", LastMoved: ago(time.Minute), Kind: "qa"},
		{ID: "h.old", Parent: "h.pnxg", Title: "Done", Status: "closed", ClosedAt: ago(time.Hour), LastMoved: ago(time.Hour)},
		{ID: "h.arch", Parent: "h.pnxg", Title: "Archived", Status: "in_progress", Archived: true, LastMoved: ago(time.Minute)},
		{ID: "h.76", Parent: "h.pnxg", Title: "Bugs first", Status: "in_progress", Priority: 1},
		{ID: "h.76.2", Parent: "h.76", Title: "Dashboard", Status: "in_progress", LastMoved: ago(6 * time.Minute), Kind: "comment"},
		{ID: "h.76.1", Parent: "h.76", Title: "VMS pull", Status: "in_progress", LastMoved: ago(3 * time.Minute), Kind: "commit"},
		{ID: "h.71", Title: "Release", Status: "in_progress", Priority: 2},
		{ID: "h.71.4", Parent: "h.71", Title: "First cut", Status: "closed", ClosedAt: ago(2 * time.Hour)},
		{ID: "h.dcz9", Title: "video rollout", Status: "waiting", Priority: 2, LastMoved: ago(5 * time.Minute), Kind: "waiting"},
	}
}

// TestMoved_OrderNestingNewestFirstAndTheRigsRow is AC1's shape.
func TestMoved_OrderNestingNewestFirstAndTheRigsRow(t *testing.T) {
	got := movedText(movedRows(busyBeads(), panelNow, 20, "hover"))
	want := strings.Join([]string{
		"h.pnxg Today 1/7 0.3/h ",
		"  h.78 qa 1m",
		"  h.77 claimed 4m",
		"  h.79 filed 9m",
		"  h.76 Bugs first 0/2 0/h ",
		"    h.76.1 commit 3m",
		"    h.76.2 comment 6m",
		"h.71 Release 1/1 0.3/h ",
		"[hover]",
		"  h.dcz9 waiting 5m",
	}, "\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestMoved_TheHeightKeepsEffortRowsAndTheNewestBeads: effort rows first,
// then the newest beads, a +n more under each effort that lost rows.
func TestMoved_TheHeightKeepsEffortRowsAndTheNewestBeads(t *testing.T) {
	got := movedText(movedRows(busyBeads(), panelNow, 8, "hover"))
	// Four effort rows leave four for beads and tails: the newest bead, and
	// a tail under each of the three groups that lost rows.
	want := strings.Join([]string{
		"h.pnxg Today 1/7 0.3/h ",
		"  h.78 qa 1m",
		"  +2 more",
		"  h.76 Bugs first 0/2 0/h ",
		"    +2 more",
		"h.71 Release 1/1 0.3/h ",
		"[hover]",
		"  +1 more",
	}, "\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if n := len(movedRows(busyBeads(), panelNow, 8, "hover")); n > 8+1 {
		t.Fatalf("%d rows for a height of 8", n)
	}
}

// TestMoved_FlagsCountUnderTheEffort: stale (in progress, no move for 20
// minutes), blocked, on Nelson, waiting; waiting alone is not red.
func TestMoved_FlagsCountUnderTheEffort(t *testing.T) {
	beads := []movedBead{
		{ID: "e", Status: "in_progress"},
		{ID: "e.1", Parent: "e", Status: "in_progress", LastMoved: ago(time.Hour)},
		{ID: "e.2", Parent: "e", Status: "in_progress", LastMoved: ago(25 * time.Minute)},
		{ID: "e.3", Parent: "e", Status: "blocked"},
		{ID: "e.4", Parent: "e", Status: "waiting_on_nelson"},
		{ID: "e.5", Parent: "e", Status: "waiting"},
		{ID: "e.6", Parent: "e", Status: "in_progress", LastMoved: ago(5 * time.Minute)},
	}
	r := movedRows(beads, panelNow, 10, "rig")[0]
	var flags []string
	for _, f := range r.Flags {
		flags = append(flags, fmt.Sprintf("%s/%v", f.Text, f.Waiting))
	}
	if strings.Join(flags, ",") != "2 stale/false,1 blocked/false,1 on Nelson/false,1 waiting/true" {
		t.Fatalf("flags %v", flags)
	}
	if r.Done != "0/6" || r.Rate != "0/h" || !r.RateZero {
		t.Fatalf("done %q rate %q zero %v", r.Done, r.Rate, r.RateZero)
	}
}

// TestMoved_FeedItNothing: nothing moved keeps each effort's single row;
// no efforts and nothing moved is an empty box; a closed effort never shows.
func TestMoved_FeedItNothing(t *testing.T) {
	if rows := movedRows(nil, panelNow, 8, "rig"); len(rows) != 0 {
		t.Fatalf("no beads drew %v", rows)
	}
	quiet := []movedBead{
		{ID: "e", Title: "Quiet", Status: "waiting"},
		{ID: "e.1", Parent: "e", Status: "open"},
		{ID: "c", Title: "Shipped", Status: "closed"},
		{ID: "c.1", Parent: "c", Status: "in_progress", LastMoved: ago(time.Minute)},
	}
	got := movedText(movedRows(quiet, panelNow, 8, "rig"))
	if got != "e Quiet 0/1 0/h \n[rig]\n  c.1  1m" {
		t.Fatalf("got\n%s", got)
	}
}

// TestKindFromStatus: what the poll alone can tell, never comment or commit.
func TestKindFromStatus(t *testing.T) {
	for status, want := range map[string]string{
		"waiting_on_nelson": "nelson", "blocked": "blocked", "waiting": "waiting", "in_qa": "qa",
		"qa_passed": "qa", "in_progress": "claimed", "deferred": "",
	} {
		if got := kindFromStatus(movedBead{Status: status}, time.Time{}, panelNow); got != want {
			t.Errorf("%s -> %q, want %q", status, got, want)
		}
	}
	if got := kindFromStatus(movedBead{Status: "open"}, ago(10*time.Minute), panelNow); got != "filed" {
		t.Errorf("an open bead filed 10m ago -> %q", got)
	}
	if got := kindFromStatus(movedBead{Status: "open"}, ago(2*time.Hour), panelNow); got != "" {
		t.Errorf("an old open bead -> %q", got)
	}
}

type fixedKinds map[string]string

func (k fixedKinds) Kind(id string) (string, bool) { v, ok := k[id]; return v, ok }

// TestMovedSnapshot_ParentsArchivedAndKinds: the poll's beads as the box
// reads them: a parent-child link, else a dotted id whose prefix is polled;
// archived from its label; the kind source first, the status otherwise.
func TestMovedSnapshot_ParentsArchivedAndKinds(t *testing.T) {
	tui := newTestTUI()
	tui.paceAnswered = true
	tui.paceBeads = []bdBead{
		{ID: "ini-e", Status: "in_progress"},
		{ID: "ini-e.1", Status: "in_progress"}, // dotted under ini-e
		{ID: "ini-x.9", Status: "open"},        // dotted, but ini-x is not polled
		{ID: "ini-k", Status: "blocked", Dependencies: []progress.Dependency{{DependsOn: "ini-e", Type: "parent-child"}}},
		{ID: "ini-r", Status: "in_progress", Dependencies: []progress.Dependency{{DependsOn: "ini-e", Type: "related"}}},
		{ID: "ini-a", Status: "in_progress", Labels: []string{"molecule", "archived"}},
	}
	byID := map[string]movedBead{}
	for _, m := range tui.movedSnapshot(panelNow) {
		byID[m.ID] = m
	}
	for id, want := range map[string]string{"ini-e.1": "ini-e", "ini-x.9": "", "ini-k": "ini-e", "ini-r": ""} {
		if got := byID[id].Parent; got != want {
			t.Errorf("%s parent %q, want %q", id, got, want)
		}
	}
	if !byID["ini-a"].Archived || byID["ini-e"].Archived {
		t.Error("the archived label is not read")
	}
	if byID["ini-k"].Kind != "blocked" || byID["ini-e.1"].Kind != "claimed" {
		t.Errorf("status kinds %q %q", byID["ini-k"].Kind, byID["ini-e.1"].Kind)
	}
	tui.panelKinds = fixedKinds{"ini-e.1": "comment"}
	for _, m := range tui.movedSnapshot(panelNow) {
		if m.ID == "ini-e.1" && m.Kind != "comment" {
			t.Errorf("the kind source was not used: %q", m.Kind)
		}
	}
	tui.paceAnswered = false
	if tui.movedSnapshot(panelNow) != nil {
		t.Error("an unanswered poll drew beads")
	}
}
