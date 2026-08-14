package tui

// membership_inbound_test.go covers ini-la97's re-work: the INBOUND half of
// fleet-scoped membership. la97 round 1 routed membership viewer -> window 1
// and closed every viewer write path, but gave the viewer no way to LEARN what
// window 1 holds -- so a restarted viewer fell back to default role bands while
// window 1 held the real groups (qa1, 2026-08-14).
//
// Both tests below are qa1's own observables, written to fail against round 1.

import (
	"os"
	"path/filepath"
	"testing"
)

// seedFleetMembership writes a layout store holding fleet membership under the
// CANONICAL bare agent keys -- the shape window 1 persists.
func seedFleetMembership(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "grid: 4x2\nmode: grid\norder:\n    - eng1\n    - eng2\n" +
		"groups:\n    - warroom\n    - qa\n" +
		"group_of:\n    eng1: warroom\n    eng2: qa\n"
	if err := os.WriteFile(filepath.Join(root, ".initech", "layout.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("seed layout: %v", err)
	}
}

// TestRestartedViewerHoldsFleetMembership is pm's AC-delta observable 2 and
// qa1's FAIL: restart window 2, arrangement resets, MEMBERSHIP HOLDS.
//
// A restarted viewer loads the store, finds every entry keyed by the canonical
// bare name while its own panes are alias-keyed, and (before this fix) had them
// all filtered out -- then ensureGroups regenerated role DEFAULTS, so the
// viewer showed 'eng' where window 1 held 'warroom'.
func TestRestartedViewerHoldsFleetMembership(t *testing.T) {
	root := t.TempDir()
	seedFleetMembership(t, root)

	viewer := viewerTUI(t, root, "eng1", "eng2")
	viewer.refreshMembershipIfFollower()
	viewer.ensureGroups(false)

	for _, c := range []struct{ agent, want string }{
		{"eng1", "warroom"},
		{"eng2", "qa"},
	} {
		var got string
		for _, p := range viewer.panes {
			if p.Name() == c.agent {
				got = viewer.layoutState.GroupOf[paneKey(p)]
			}
		}
		if got != c.want {
			t.Errorf(`restarted viewer shows %s in band %q, window 1 holds %q.

Membership is fleet-scoped state: it must survive a viewer restart, or the two
windows disagree about the fleet the parity invariant says they share. Round 1
routed membership OUT and gave the viewer no inbound path, so it fell back to
the default role band.`, c.agent, got, c.want)
		}
	}
}

// TestViewerSeesMembershipItNeverChanged is the mid-session half qa1 found:
// the disagreement is not restart-only. An agent this viewer never regrouped
// must still render in the band window 1 holds, from first attach.
func TestViewerSeesMembershipItNeverChanged(t *testing.T) {
	root := t.TempDir()
	seedFleetMembership(t, root)

	viewer := viewerTUI(t, root, "eng1", "eng2")
	viewer.ensureGroups(false) // defaults land first, as they do at startup
	viewer.refreshMembershipIfFollower()

	var got string
	for _, p := range viewer.panes {
		if p.Name() == "eng2" {
			got = viewer.layoutState.GroupOf[paneKey(p)]
		}
	}
	if got != "qa" {
		t.Errorf("viewer shows eng2 in %q, window 1 holds \"qa\" -- an agent this viewer never "+
			"touched already disagrees, so the refresh must win over the role defaults "+
			"regardless of which ran first", got)
	}
}

// TestViewerRefreshAdoptsWindowOnesBandUniverse: membership is not only the
// per-agent map. If the viewer's band list lacks a label window 1 holds, the
// members render in no tier at all -- the ini-i7fr orphan shape.
func TestViewerRefreshAdoptsWindowOnesBandUniverse(t *testing.T) {
	root := t.TempDir()
	seedFleetMembership(t, root)

	viewer := viewerTUI(t, root, "eng1", "eng2")
	viewer.refreshMembershipIfFollower()

	seen := map[string]bool{}
	for _, g := range viewer.layoutState.Groups {
		seen[g] = true
	}
	for _, want := range []string{"warroom", "qa"} {
		if !seen[want] {
			t.Errorf("viewer band universe %v lacks %q, which window 1 holds; its members would "+
				"render in no tier at all", viewer.layoutState.Groups, want)
		}
	}
}

// TestMembershipRefreshIsAuthorityNoOp keeps the never-rereads premise true by
// construction: window 1 owns this state and its in-memory copy IS the truth.
// A refresh on the authority would re-open the two-truths door la97 closes.
func TestMembershipRefreshIsAuthorityNoOp(t *testing.T) {
	root := t.TempDir()
	seedFleetMembership(t, root)

	w1 := authorityTUI(t, root, "eng1", "eng2")
	w1.layoutState.GroupOf = map[string]string{"eng1": "live-edit"}
	w1.refreshMembershipIfFollower()

	if got := w1.layoutState.GroupOf["eng1"]; got != "live-edit" {
		t.Errorf("the authority re-read the store and clobbered its own in-memory truth "+
			"(eng1 = %q, want live-edit). Window 1 never reloads: it is the only writer, "+
			"so a reload can only introduce a second truth.", got)
	}
}

// TestMembershipRefreshWritesNothing is the decoy discipline applied to the new
// path itself: a READ path that writes is exactly what the non-contact suite
// exists to catch, so it is asserted here too, at the unit level, where the
// failure names the cause directly.
func TestMembershipRefreshWritesNothing(t *testing.T) {
	root := t.TempDir()
	seedFleetMembership(t, root)
	path := filepath.Join(root, ".initech", "layout.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}

	viewer := viewerTUI(t, root, "eng1", "eng2")
	viewer.refreshMembershipIfFollower()
	viewer.refreshMembershipIfFollower() // idempotent, and still writes nothing

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("the inbound membership path WROTE the store:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(root, ".initech", "assignments.yaml")); err == nil {
		t.Error("the inbound membership path created the assignment store")
	}
}
