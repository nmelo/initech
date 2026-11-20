package progress

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/lifecycle"
)

// ini-ylx9: a parent's last move is the newest move of itself or any
// descendant, so an epic whose children move is never stuck or quiet; and a
// bead in waiting, waiting_for_release or deferred is never listed, whatever
// a rig's status.custom says.

var famNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// famBead is one bead as bd's list JSON gives it, parent included.
func famBead(id, status, parent, owner string, ago time.Duration) map[string]any {
	b := map[string]any{"id": id, "status": status, "assignee": owner, "updated_at": famNow.Add(-ago).Format(time.RFC3339)}
	if parent != "" {
		b["parent"] = parent
	}
	return b
}

// famReport builds the report over beads decoded from bd's JSON, in a management
// rig's shape (status.custom waiting_on_nelson,waiting,waiting_for_release), with
// a 20-minute window and every owner idle.
func famReport(t *testing.T, comments map[string][]Comment, beads ...map[string]any) Report {
	t.Helper()
	raw, err := json.Marshal(beads)
	if err != nil {
		t.Fatal(err)
	}
	var in Input
	if err := json.Unmarshal(raw, &in.Beads); err != nil {
		t.Fatal(err)
	}
	in.Now = famNow
	in.Rule = NewRule(20*time.Minute, lifecycle.Parse("waiting_on_nelson,waiting,waiting_for_release").Working())
	in.Comments = comments
	in.Panes = map[string]string{}
	for _, b := range beads {
		if o, _ := b["assignee"].(string); o != "" {
			in.Panes[o] = "idle"
		}
	}
	in.PanesKnown = true
	return Build(in)
}

// famListed is what the report calls stuck and quiet, sorted.
func famListed(r Report) (stuck, quiet []string) {
	stuck = slices.Clone(r.StuckIDs)
	for _, q := range r.Quiet {
		quiet = append(quiet, q.ID)
	}
	sort.Strings(stuck)
	sort.Strings(quiet)
	return
}

// AC1, red first: an epic whose own move is 30 minutes old and whose child
// moved 5 minutes ago is neither stuck nor quiet.
func TestFamilyStale_AnEpicWithAMovingChildIsNotStuck(t *testing.T) {
	r := famReport(t, nil,
		famBead("ini-e", "in_progress", "", "eng1", 30*time.Minute),
		famBead("ini-e.1", "in_progress", "ini-e", "eng2", 5*time.Minute),
	)
	if stuck, quiet := famListed(r); len(stuck) != 0 || len(quiet) != 0 {
		t.Errorf("stuck %v quiet %v, want none: the epic's child moved 5 minutes ago", stuck, quiet)
	}
}

// AC1, the shape another rig met: the epic is quiet, its children land comments.
// A child's comment is its move, and so the epic's.
func TestFamilyStale_ChildCommentsKeepAQuietEpicOffTheList(t *testing.T) {
	r := famReport(t, map[string][]Comment{"ini-ep7.3": {{Author: "eng4", Text: "DONE: landed", CreatedAt: famNow.Add(-4 * time.Minute)}}},
		famBead("ini-ep7", "in_progress", "", "super", 2*time.Hour),
		famBead("ini-ep7.3", "in_progress", "ini-ep7", "eng4", 2*time.Hour),
	)
	if stuck, quiet := famListed(r); len(stuck) != 0 || len(quiet) != 0 {
		t.Errorf("stuck %v quiet %v, want none: a child commented 4 minutes ago", stuck, quiet)
	}
}

// AC4: a grandchild's move keeps the top epic off the list.
func TestFamilyStale_AGrandchildsMoveCounts(t *testing.T) {
	r := famReport(t, nil,
		famBead("ini-g", "in_progress", "", "eng1", 30*time.Minute),
		famBead("ini-g.1", "open", "ini-g", "", 30*time.Minute),
		famBead("ini-g.1.1", "in_progress", "ini-g.1", "eng2", 5*time.Minute),
	)
	if stuck, quiet := famListed(r); slices.Contains(stuck, "ini-g") || slices.Contains(quiet, "ini-g") {
		t.Errorf("stuck %v quiet %v, want no ini-g: its grandchild moved 5 minutes ago", stuck, quiet)
	}
}

// AC2: a family that is all quiet past the window is still listed, as the
// bead alone would be. The twin of AC1.
func TestFamilyStale_AQuietFamilyIsStillListed(t *testing.T) {
	r := famReport(t, nil,
		famBead("ini-q", "in_progress", "", "eng1", 30*time.Minute),
		famBead("ini-q.1", "closed", "ini-q", "eng2", 40*time.Minute),
	)
	if stuck, quiet := famListed(r); !slices.Equal(stuck, []string{"ini-q"}) || !slices.Equal(quiet, []string{"ini-q"}) {
		t.Errorf("stuck %v quiet %v, want ini-q on both", stuck, quiet)
	}
}

// AC3: in a rig whose status.custom has no qa_passed, waiting_for_release,
// waiting and deferred beads are never stuck or quiet, however old.
func TestFamilyStale_SideStatesAreNeverListed(t *testing.T) {
	var beads []map[string]any
	for i, s := range []string{"waiting_for_release", "waiting", "deferred"} {
		beads = append(beads, famBead(fmt.Sprintf("ini-s%d", i), s, "", "eng1", 3*time.Hour))
	}
	r := famReport(t, nil, beads...)
	if stuck, quiet := famListed(r); len(stuck) != 0 || len(quiet) != 0 {
		t.Errorf("stuck %v quiet %v, want none: side states are never listed", stuck, quiet)
	}
}

// Feed-it-nothing: beads with no parents list what they list today.
func TestFamilyStale_NoParentsListAsToday(t *testing.T) {
	r := famReport(t, nil,
		famBead("ini-a", "in_progress", "", "eng1", 30*time.Minute),
		famBead("ini-b", "in_progress", "", "eng2", 5*time.Minute),
	)
	if stuck, quiet := famListed(r); !slices.Equal(stuck, []string{"ini-a"}) || !slices.Equal(quiet, []string{"ini-a"}) {
		t.Errorf("stuck %v quiet %v, want ini-a only", stuck, quiet)
	}
}

// AC3, the guard itself: even a chain that names them (a caller that built
// its own) never makes a side state stuck.
func TestFamilyStale_SideStatesNeverStuckWhateverTheChain(t *testing.T) {
	rule := NewRule(20*time.Minute, []string{"in_progress", "waiting", "waiting_for_release", "deferred"})
	for _, s := range []string{"waiting", "waiting_for_release", "deferred"} {
		b := Bead{ID: "ini-s", Status: s, UpdatedAt: famNow.Add(-3 * time.Hour)}
		if Stuck(b, "idle", rule, famNow) {
			t.Errorf("%s read stuck", s)
		}
	}
	if !Stuck(Bead{ID: "ini-p", Status: "in_progress", UpdatedAt: famNow.Add(-3 * time.Hour)}, "idle", rule, famNow) {
		t.Error("in_progress, 3h quiet, idle owner: not stuck") // the twin
	}
}
