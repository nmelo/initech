package tui

// authority_extension_test.go verifies the ini-la97 authority extension BOTH
// DIRECTIONS on both stores, the way fleet-state was verified: the authority
// is allowed, the secondary is refused, and the refusal is not an accident of
// some unrelated precondition.
//
// The law implemented (workspace repo, docs/spec.md "Multi-window sessions —
// normative invariants"): the primary window is the sole writer of
// fleet-global state; a viewer requests mutations through window 1 and never
// writes project-root state directly; a viewer that cannot reach window 1
// DECLINES the mutation and never improvises.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

func authorityTUI(t *testing.T, root string, agents ...string) *TUI {
	t.Helper()
	var panes []PaneView
	for _, name := range agents {
		panes = append(panes, &mockPaneView{name: name, alive: true})
	}
	state, _ := LoadLayout(root, agents)
	return &TUI{
		projectRoot: root,
		layoutState: state,
		panes:       panes,
		windowID:    WindowOne,
		project:     &config.Project{Name: "rig", Root: root, WindowListen: ":7500"},
	}
}

// TestAssignmentWriter_OnlyTheAuthorityObtainsOne is the capability half of
// the structural requirement: a viewer has no value on which the mutation can
// be expressed, so the dangerous call is unsayable rather than merely refused.
func TestAssignmentWriter_OnlyTheAuthorityObtainsOne(t *testing.T) {
	root := seedProjectRoot(t)

	authority, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatalf("LoadAssignment(window 1): %v", err)
	}
	if _, ok := authority.Writer(); !ok {
		t.Error("window 1 could not obtain an assignment writer; the authority cannot write its own store")
	}

	secondary, err := LoadAssignment(root, "window-2")
	if err != nil {
		t.Fatalf("LoadAssignment(window-2): %v", err)
	}
	if _, ok := secondary.Writer(); ok {
		t.Error("a secondary window obtained an assignment writer; the capability is not " +
			"actually restricted to the authority")
	}
}

// TestAssignmentPrimitive_RefusesANonAuthorityWrite is the guard-in-the-
// primitive half: even holding a store, a non-authority process cannot reach
// the file. This is the layer that covers paths the type system cannot,
// which is why it is tested separately from the capability above.
func TestAssignmentPrimitive_RefusesANonAuthorityWrite(t *testing.T) {
	root := seedProjectRoot(t)
	path := filepath.Join(root, ".initech", "assignments.yaml")

	secondary, err := LoadAssignment(root, "window-2")
	if err != nil {
		t.Fatalf("LoadAssignment: %v", err)
	}
	// Reach past the capability deliberately, as a future call site might.
	if err := secondary.moveGroup("eng", "window-2"); !errors.Is(err, ErrAssignmentNotAuthority) {
		t.Errorf("moveGroup on a secondary = %v, want ErrAssignmentNotAuthority", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a secondary's refused write still created the assignment store")
	}
}

// TestMoveGroupSeam_AuthorityWritesDirectly is the positive direction: the
// authority's move lands in the store with no routing involved.
func TestMoveGroupSeam_AuthorityWritesDirectly(t *testing.T) {
	root := seedProjectRoot(t)
	tui := authorityTUI(t, root, "super", "eng1")

	if err := tui.moveGroupToWindow("eng", "window-2"); err != nil {
		t.Fatalf("authority move: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".initech", "assignments.yaml"))
	if err != nil {
		t.Fatalf("the authority's move did not reach the store: %v", err)
	}
	if got := string(data); !strings.Contains(got, "eng") || !strings.Contains(got, "window-2") {
		t.Errorf("store does not record the move:\n%s", got)
	}
}

// TestMoveGroupSeam_UnreachableAuthorityDeclines pins the invariant's own
// words: a viewer that cannot reach window 1 declines the mutation and never
// improvises. The failure mode this forbids is applying locally instead --
// which is exactly the two-truths state ini-i7fr traced.
func TestMoveGroupSeam_UnreachableAuthorityDeclines(t *testing.T) {
	root := seedProjectRoot(t)
	viewer := viewerTUI(t, root, "super", "eng1")

	err := viewer.moveGroupToWindow("eng", "window-2")
	if err == nil {
		t.Fatal("a viewer with no route to window 1 reported success; it must decline")
	}
	if _, statErr := os.Stat(filepath.Join(root, ".initech", "assignments.yaml")); statErr == nil {
		t.Error("a viewer that could not reach window 1 improvised: it wrote the store itself")
	}
}
