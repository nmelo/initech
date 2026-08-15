package tui

// The pruner asks a FLEET question, not a window question (ini-l5sy).
//
// agentsPruneEmptyGroups removes a band with no members and clears its window
// assignment, which is correct when "no members" means "no agent anywhere
// carries this label" (ini-9ka.5's rule: a group that no longer exists must
// not keep a group->window row that would resurface if the label were
// recreated).
//
// Scoping changed what "members" means underneath it. agentsGroupMembers now
// filters by the window's display scope, so on window 1 a group that has been
// MOVED TO WINDOW 2 has zero members -- and the pruner reads that as "the
// group is gone" and erases the very assignment that moved it. The operator's
// monitor arrangement is destroyed by the act of closing the modal, silently,
// and the next load shows every agent back on window 1.
//
// Two consumers, one question, opposite needs: the modal wants the SCOPED
// members (it renders them), the pruner wants the FLEET members (it decides a
// label is extinct). They must not share a predicate.

import (
	"os"
	"path/filepath"
	"testing"
)

// prunerTUI builds window 1 with the whole fleet present, the eng band moved
// to window 2, and window 1 scoped accordingly -- the state one modal move
// leaves behind.
func prunerTUI(t *testing.T) (*TUI, *WindowAssignment, string) {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	os.WriteFile(filepath.Join(root, ".initech", "assignments.yaml"),
		[]byte("group_window:\n    eng: window-2\n"), 0o644)

	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatalf("LoadAssignment: %v", err)
	}

	tui := newTestTUI()
	tui.projectRoot = root
	tui.windowID = WindowOne
	tui.assignment = a
	for _, n := range []string{"super", "qa1", "eng1", "eng2"} {
		tui.panes = append(tui.panes, testPane(n))
	}
	tui.ensureGroups(false)
	// Window 1 displays only what it owns: window 2 holds the eng band.
	tui.paneOwnership = computePaneOwnership(tui.panes, a, tui.layoutState.GroupOf,
		map[string]bool{"window-2": true})
	return tui, a, root
}

// TestPruneEmptyGroups_DoesNotEraseAGroupLivingOnAnotherWindow is the bug: it
// fails on the scoped predicate and passes on the fleet one.
func TestPruneEmptyGroups_DoesNotEraseAGroupLivingOnAnotherWindow(t *testing.T) {
	tui, a, root := prunerTUI(t)

	// Precondition: window 1 genuinely does not display the eng agents, which
	// is what makes the scoped member count zero. Without this the test would
	// pass on the broken code for lack of a scope.
	if got := len(tui.visiblePanesForWindow()); got != 2 {
		t.Fatalf("window 1 displays %d panes, want 2 (super, qa1) -- the eng band must be "+
			"out of scope or this cell cannot reproduce the erasure", got)
	}
	if a.WindowOfGroup("eng") != "window-2" {
		t.Fatalf("precondition: eng is on %q, want window-2", a.WindowOfGroup("eng"))
	}

	tui.agentsPruneEmptyGroups()

	if got := a.WindowOfGroup("eng"); got != "window-2" {
		t.Errorf("closing the modal on window 1 moved eng to %q.\n\nThe operator put that "+
			"band on monitor 2 and window 1 erased the assignment because the band has no "+
			"members IN WINDOW 1'S SCOPE. The arrangement is destroyed by looking at the "+
			"modal, and nothing tells the operator.", got)
	}
	if b, err := os.ReadFile(filepath.Join(root, ".initech", "assignments.yaml")); err == nil {
		if string(b) == "{}\n" || string(b) == "{}" {
			t.Errorf("the persisted store was emptied to %q -- the erasure reached disk, so "+
				"it survives a restart", string(b))
		}
	}

	var kept bool
	for _, g := range tui.layoutState.Groups {
		if g == "eng" {
			kept = true
		}
	}
	if !kept {
		t.Error("the eng band was dropped from Groups on window 1; the band still has " +
			"members, they are simply rendering on another monitor -- and a band missing " +
			"from Groups makes its members unreachable in the modal")
	}
}

// TestPruneEmptyGroups_StillPrunesAGenuinelyExtinctBand is the other half, and
// the one that keeps the fix honest: ini-9ka.5's rule must survive.
//
// Without this cell, "never prune" would pass the test above -- and a
// group->window row for a label no agent carries would resurface the next time
// that label were created, putting a band on a monitor the operator never
// chose.
func TestPruneEmptyGroups_StillPrunesAGenuinelyExtinctBand(t *testing.T) {
	tui, a, _ := prunerTUI(t)

	// A band nobody is in, on another window: extinct, not merely elsewhere.
	tui.layoutState.Groups = append(tui.layoutState.Groups, "ghost")
	w, ok := a.Writer()
	if !ok {
		t.Fatal("window 1 has no assignment writer")
	}
	if err := w.MoveGroup("ghost", "window-2"); err != nil {
		t.Fatalf("seed ghost: %v", err)
	}

	tui.agentsPruneEmptyGroups()

	for _, g := range tui.layoutState.Groups {
		if g == "ghost" {
			t.Fatal("a band with no members anywhere in the fleet survived the prune; the " +
				"empty-on-close rule (ini-9ka.5) is what keeps dead labels from " +
				"accumulating")
		}
	}
	if got := a.WindowOfGroup("ghost"); got != WindowOne {
		t.Errorf("the extinct band kept its window assignment (%q); recreating that label "+
			"later would silently place it on a monitor the operator never chose", got)
	}
}
