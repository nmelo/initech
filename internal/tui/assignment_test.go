package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// Tests for ini-9ka.4: group-to-window assignment. Assignment is WHICH groups
// a window shows; layout (ini-9ka.3) is HOW that window arranges what it
// shows. The two must round-trip independently through a full session
// restart, and fold-back must be able to read a GONE window's assignment.

// Fixture identities. Two DISTINCT non-default windows on purpose: the
// default is "everything on window 1", so a fixture using only one
// non-default window would let a bug that collapses everything to the default
// look identical to a correct round-trip for the window-1 groups. With two,
// a collapse and a swap are both visible.
const (
	winTwo   = "two"
	winThree = "three"
)

// assignFixture builds the canonical non-default arrangement used across
// these tests: core stays on window 1, eng and qa go to two DIFFERENT other
// windows. No default or auto path can produce this.
func assignFixture(t *testing.T, root string) *WindowAssignment {
	t.Helper()
	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatalf("LoadAssignment: %v", err)
	}
	if err := mustAssignWriter(t, a).MoveGroup("eng", winTwo); err != nil {
		t.Fatalf("MoveGroup eng: %v", err)
	}
	if err := mustAssignWriter(t, a).MoveGroup("qa", winThree); err != nil {
		t.Fatalf("MoveGroup qa: %v", err)
	}
	return a
}

// TestAssignment_FreshProjectDefaultsAllGroupsToWindowOne covers the
// fresh-project default AC: with no store on disk, every group resolves to
// window 1 and nothing is unassigned.
func TestAssignment_FreshProjectDefaultsAllGroupsToWindowOne(t *testing.T) {
	root := t.TempDir()
	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatalf("LoadAssignment on a fresh project: %v", err)
	}
	for _, g := range []string{"core", "eng", "qa", "never-seen-before"} {
		if got := a.WindowOfGroup(g); got != WindowOne {
			t.Errorf("WindowOfGroup(%q) = %q, want window 1 (%q)", g, got, WindowOne)
		}
	}
	all := []string{"core", "eng", "qa"}
	got := a.GroupsForWindow(WindowOne, all)
	if !reflect.DeepEqual(got, all) {
		t.Errorf("GroupsForWindow(window 1) = %v, want all groups %v", got, all)
	}
	if other := a.GroupsForWindow(winTwo, all); len(other) != 0 {
		t.Errorf("GroupsForWindow(%q) = %v, want empty on a fresh project", winTwo, other)
	}
}

// TestAssignment_RoundTripsAcrossRestart covers the persistence AC: the
// assignment survives a full session restart, i.e. re-reading from disk with
// no in-memory state carried over.
func TestAssignment_RoundTripsAcrossRestart(t *testing.T) {
	root := t.TempDir()
	assignFixture(t, root)

	// Restart: discard everything in memory, read from disk.
	reloaded, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatalf("LoadAssignment after restart: %v", err)
	}
	if got := reloaded.WindowOfGroup("eng"); got != winTwo {
		t.Errorf("after restart WindowOfGroup(eng) = %q, want %q", got, winTwo)
	}
	if got := reloaded.WindowOfGroup("qa"); got != winThree {
		t.Errorf("after restart WindowOfGroup(qa) = %q, want %q", got, winThree)
	}
	if got := reloaded.WindowOfGroup("core"); got != WindowOne {
		t.Errorf("after restart WindowOfGroup(core) = %q, want window 1", got)
	}
}

// TestAssignment_MoveGroupPersistsImmediately covers the AC that both
// assignment-editing grains persist immediately: the change must be on disk
// after MoveGroup returns, without a separate save call the modal could
// forget. Asserted by re-reading from disk, not by inspecting memory.
func TestAssignment_MoveGroupPersistsImmediately(t *testing.T) {
	root := t.TempDir()
	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	if err := mustAssignWriter(t, a).MoveGroup("eng", winTwo); err != nil {
		t.Fatalf("MoveGroup: %v", err)
	}

	onDisk, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.WindowOfGroup("eng"); got != winTwo {
		t.Errorf("on-disk WindowOfGroup(eng) = %q, want %q immediately after MoveGroup", got, winTwo)
	}
}

// TestAssignment_MoveGroupBackToWindowOne confirms returning a group to the
// default is a real state change that persists, not an unrepresentable one.
func TestAssignment_MoveGroupBackToWindowOne(t *testing.T) {
	root := t.TempDir()
	a := assignFixture(t, root)

	if err := mustAssignWriter(t, a).MoveGroup("eng", WindowOne); err != nil {
		t.Fatalf("MoveGroup back to window 1: %v", err)
	}
	onDisk, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.WindowOfGroup("eng"); got != WindowOne {
		t.Errorf("WindowOfGroup(eng) = %q, want window 1 after moving back", got)
	}
	// qa must be untouched by eng's move.
	if got := onDisk.WindowOfGroup("qa"); got != winThree {
		t.Errorf("WindowOfGroup(qa) = %q, want %q -- moving eng must not disturb qa", got, winThree)
	}
}

// TestAssignment_EveryAgentResolvesToExactlyOneWindow covers the "no agent is
// ever assigned to zero or more than one window" invariant, across all three
// shapes an agent can have: in an explicitly-assigned group, in a defaulted
// group, and with no group entry at all.
func TestAssignment_EveryAgentResolvesToExactlyOneWindow(t *testing.T) {
	root := t.TempDir()
	a := assignFixture(t, root)

	groupOf := map[string]string{
		"super": "core", // defaulted group -> window 1
		"eng1":  "eng",  // explicitly assigned -> two
		"qa1":   "qa",   // explicitly assigned -> three
		// "stray" deliberately absent: an agent with no group entry.
	}
	want := map[string]string{
		"super": WindowOne,
		"eng1":  winTwo,
		"qa1":   winThree,
		"stray": WindowOne,
	}
	for agent, wantWin := range want {
		got := a.WindowOfAgent(agent, groupOf)
		if got != wantWin {
			t.Errorf("WindowOfAgent(%q) = %q, want %q", agent, got, wantWin)
		}
	}

	// No agent may appear under two windows simultaneously. Partition the
	// agent set by window and assert the parts are disjoint and total.
	windows := []string{WindowOne, winTwo, winThree}
	seen := map[string]int{}
	for _, w := range windows {
		for agent := range want {
			if a.WindowOfAgent(agent, groupOf) == w {
				seen[agent]++
			}
		}
	}
	for agent := range want {
		if seen[agent] != 1 {
			t.Errorf("agent %q resolved to %d windows, want exactly 1", agent, seen[agent])
		}
	}
}

// TestAssignment_AnswersForDisconnectedWindow is the fold-back invariant:
// window N dying must not change what is assigned to window N, and the store
// must answer for a window that is GONE. Note this tests the MODEL-level
// invariant -- that nothing in this store can mutate assignment on
// disconnect, and that reads take no liveness context -- rather than
// exercising ini-9ka.7's fold-back mechanism, which does not exist yet.
func TestAssignment_AnswersForDisconnectedWindow(t *testing.T) {
	root := t.TempDir()
	assignFixture(t, root)
	all := []string{"core", "eng", "qa"}

	before, err := os.ReadFile(assignmentPath(root))
	if err != nil {
		t.Fatalf("read store: %v", err)
	}

	// Window "two" is now GONE. Fold-back's read: which groups did it own?
	// There is deliberately no liveness argument to pass -- the store cannot
	// know or care that the window disconnected.
	reloaded, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.GroupsForWindow(winTwo, all)
	if !reflect.DeepEqual(got, []string{"eng"}) {
		t.Errorf("GroupsForWindow(%q) = %v, want [eng] for a disconnected window", winTwo, got)
	}
	if w := reloaded.WindowOfGroup("eng"); w != winTwo {
		t.Errorf("WindowOfGroup(eng) = %q, want %q -- a dead window keeps its assignment", w, winTwo)
	}

	after, err := os.ReadFile(assignmentPath(root))
	if err != nil {
		t.Fatalf("read store after: %v", err)
	}
	if string(before) != string(after) {
		t.Error("reading a disconnected window's assignment mutated the persisted store")
	}
}

// TestAssignment_IndependentOfLayoutState is the independence AC, and unlike
// ini-9ka.3's mirror of it this one is NOT vacuous: the layout store exists
// on disk today, so it is tested against real files in three states. Case (3)
// is the one that matters most -- a valid-but-conflicting layout would be
// silently consumed by an implementation that reads layout "just for the
// group list", which cases (1) and (2) alone would not catch (such an
// implementation could fall back on read error and still pass them).
func TestAssignment_IndependentOfLayoutState(t *testing.T) {
	all := []string{"core", "eng", "qa"}

	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{"layout absent", func(t *testing.T, root string) {}},
		{"layout corrupt", func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, ".initech"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(layoutPath(root), []byte("{{{ not: [valid yaml"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"layout valid but conflicting groups", func(t *testing.T, root string) {
			// A perfectly loadable layout whose group set disagrees with the
			// assignment fixture. If assignment consulted layout for its
			// group universe, these labels would leak into the answers.
			st := LayoutState{
				Mode: LayoutGrid, GridCols: 3, GridRows: 2,
				Groups:  []string{"decoy-a", "decoy-b"},
				GroupOf: map[string]string{"super": "decoy-a", "eng1": "decoy-b"},
			}
			if err := SaveLayout(root, st); err != nil {
				t.Fatal(err)
			}
		}},
	}

	var results []map[string]string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.setup(t, root)
			assignFixture(t, root)

			reloaded, err := LoadAssignment(root, WindowOne)
			if err != nil {
				t.Fatalf("LoadAssignment with %s: %v", tc.name, err)
			}
			got := map[string]string{}
			for _, g := range all {
				got[g] = reloaded.WindowOfGroup(g)
			}
			if w := reloaded.GroupsForWindow(winTwo, all); !reflect.DeepEqual(w, []string{"eng"}) {
				t.Errorf("GroupsForWindow(%q) = %v with %s, want [eng]", winTwo, w, tc.name)
			}
			// No decoy label may appear in any answer.
			for _, g := range all {
				if got[g] == "decoy-a" || got[g] == "decoy-b" {
					t.Errorf("group %q resolved to a layout decoy window %q -- assignment read layout state", g, got[g])
				}
			}
			results = append(results, got)
		})
	}

	for i := 1; i < len(results); i++ {
		if !reflect.DeepEqual(results[0], results[i]) {
			t.Errorf("assignment differed by layout state: %v (%s) vs %v (%s)",
				results[0], cases[0].name, results[i], cases[i].name)
		}
	}
}

// TestAssignment_DoesNotTouchLayoutFiles is the structural half of
// independence: a full assignment round-trip must write exactly its own file
// and leave any layout file byte-identical.
func TestAssignment_DoesNotTouchLayoutFiles(t *testing.T) {
	root := t.TempDir()
	if err := SaveLayout(root, LayoutState{Mode: LayoutGrid, GridCols: 3, GridRows: 2}); err != nil {
		t.Fatal(err)
	}
	layoutBefore, err := os.ReadFile(layoutPath(root))
	if err != nil {
		t.Fatal(err)
	}

	assignFixture(t, root)
	if _, err := LoadAssignment(root, WindowOne); err != nil {
		t.Fatal(err)
	}

	layoutAfter, err := os.ReadFile(layoutPath(root))
	if err != nil {
		t.Fatalf("layout file missing after an assignment round-trip: %v", err)
	}
	if string(layoutBefore) != string(layoutAfter) {
		t.Error("assignment round-trip modified the layout file")
	}

	entries, err := os.ReadDir(filepath.Join(root, ".initech"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, de := range entries {
		names = append(names, de.Name())
	}
	sort.Strings(names)
	want := []string{"assignments.yaml", "layout.yaml"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf(".initech contains %v, want exactly %v", names, want)
	}
}

// TestAssignment_RejectsUnsafeWindowIdentity covers identity discipline: the
// window id is validated with the same canonical rule as everywhere else, so
// a traversal attempt is refused rather than sanitized.
func TestAssignment_RejectsUnsafeWindowIdentity(t *testing.T) {
	root := t.TempDir()
	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../escape", "a/b", "a:b", "..", ".", "with space", "dot.name"} {
		if err := mustAssignWriter(t, a).MoveGroup("eng", bad); err == nil {
			t.Errorf("MoveGroup to window %q should be rejected", bad)
		}
		if got := a.WindowOfGroup("eng"); got != WindowOne {
			t.Errorf("a rejected move changed the assignment to %q", got)
		}
	}
}

// TestAssignment_EmptyGroupRejected guards the other identifier: a group label
// must be non-empty, or an unlabeled agent could silently claim a window.
func TestAssignment_EmptyGroupRejected(t *testing.T) {
	root := t.TempDir()
	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	if err := mustAssignWriter(t, a).MoveGroup("", winTwo); err == nil {
		t.Error("MoveGroup with an empty group label should be rejected")
	}
}

// TestLoadAssignment_CorruptStoreIsAnError makes the failure mode explicit:
// a corrupt assignment file must not be silently treated as "fresh project,
// everything on window 1", which would look like a successful reset and lose
// the operator's real arrangement.
func TestLoadAssignment_CorruptStoreIsAnError(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assignmentPath(root), []byte("{{{ not: [valid yaml"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAssignment(root, WindowOne); err == nil {
		t.Error("LoadAssignment on a corrupt store should error, not silently default to window 1")
	}
}
