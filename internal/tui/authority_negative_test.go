package tui

// authority_negative_test.go holds the NEGATIVE CONTROLS for ini-la97: the two
// unguarded viewer writes that i7fr proved are reachable today. Both must be
// RED against the code as it stands before the authority extension exists, or
// the extension would be shipping a guard against nothing.
//
// They are written as assertions about the CONTRACT (a viewer must not reach
// project-root state), so they stay meaningful after the fix rather than
// becoming tautologies: post-fix they pass because the write is impossible,
// not because the test was rewritten.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

// viewerTUI builds a secondary-window TUI over root, with a viewer's real
// shape: remote panes keyed window1:<name>, secondary peer identity, and no
// WindowListen (viewerProject clears it -- a viewer serves nothing).
func viewerTUI(t *testing.T, root string, agents ...string) *TUI {
	t.Helper()
	var panes []PaneView
	for _, name := range agents {
		panes = append(panes, &mockPaneView{name: name, host: "window1", alive: true})
	}
	state, _ := LoadLayout(root, nil) // a viewer has Roles=nil, so no known keys
	return &TUI{
		projectRoot: root,
		layoutState: state,
		panes:       panes,
		windowID:    "window-2",
		project:     &config.Project{Name: "rig", Root: root, PeerName: "window-2"},
	}
}

// seedProjectRoot creates a project root with a layout store already present,
// so a viewer write is detectable as a CHANGE rather than a creation.
func seedProjectRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	layout := "grid: 4x2\nmode: grid\norder:\n    - super\n    - eng1\ngroups:\n    - core\n    - eng\n" +
		"group_of:\n    super: core\n    eng1: eng\n"
	if err := os.WriteFile(filepath.Join(root, ".initech", "layout.yaml"), []byte(layout), 0o600); err != nil {
		t.Fatalf("seed layout: %v", err)
	}
	return root
}

// TestViewerMustNotWriteLayoutStore is negative control 1. i7fr measured a
// viewer rewriting window 1's layout.yaml through saveLayoutIfConfigured,
// which gates only on projectRoot -- there is no authority notion on that
// path. The canonized rule (ini-civ) is that a viewer never writes
// project-root state.
func TestViewerMustNotWriteLayoutStore(t *testing.T) {
	root := seedProjectRoot(t)
	path := filepath.Join(root, ".initech", "layout.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}

	viewer := viewerTUI(t, root, "super", "eng1")
	viewer.ensureGroups(false)
	viewer.saveLayoutIfConfigured()

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf(`A VIEWER REWROTE WINDOW 1'S LAYOUT STORE.

A secondary window must not write project-root state (ini-civ). saveLayoutIfConfigured
gates only on projectRoot, so a viewer's arrangement lands in the shared file --
and in the viewer's OWN key space, which is how ini-i7fr's cascade started.

before:
%s
after:
%s`, string(before), string(after))
	}
}

// TestViewerMustNotWriteAssignmentStoreDirectly is negative control 2. A
// viewer's group move must reach the store through window 1, not by writing
// the file itself: MoveGroup is reachable from any window today, and window 1
// never re-reads (by design), so a direct viewer write makes window 1's modal
// permanently stale -- the divergence the operator reported.
func TestViewerMustNotWriteAssignmentStoreDirectly(t *testing.T) {
	root := seedProjectRoot(t)
	path := filepath.Join(root, ".initech", "assignments.yaml")

	viewer := viewerTUI(t, root, "super", "eng1")
	viewer.ensureGroups(false)

	// Drive the REAL keybinding path, not the store API underneath it: this is
	// what the operator's `m` press runs, and it is the seam that must route.
	// Written against the keybinding so this control survives the refactor
	// that moves the store mutator behind a capability type.
	sel := -1
	for i, p := range viewer.panes {
		if viewer.layoutState.GroupOf[paneKey(p)] != "" {
			sel = i
			break
		}
	}
	if sel < 0 {
		t.Fatal("no grouped agent in the viewer's map; the rig cannot exercise `m`")
	}
	viewer.agents.selected = sel

	// No mux is wired here, so a correctly-routing viewer cannot reach window 1
	// and must leave the store alone. A viewer that writes the file itself
	// needs no mux at all -- which is exactly the defect.
	viewer.agentsMoveGroupToNextWindow()

	if _, statErr := os.Stat(path); statErr == nil {
		data, _ := os.ReadFile(path)
		t.Errorf(`A VIEWER WROTE THE ASSIGNMENT STORE DIRECTLY (%s).

Fleet-global mutations must route through window 1, the only writer (the
fleet-state precedent). A direct viewer write is invisible to window 1,
because window 1 never re-reads the store -- by design and correctly, GIVEN
that it is the only writer. This write falsifies that premise.

store contents:
%s`, path, string(data))
	}
}
