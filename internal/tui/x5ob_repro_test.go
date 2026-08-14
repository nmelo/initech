package tui

// x5ob_repro_test.go reproduces ini-x5ob: a group moved from window 1 vanishes
// from window 1 and never appears in window 2 mid-session.
//
// MEMBERSHIP IS ASSERTED AS SETS, NEVER COUNTS. The bead names this trap up
// front: the pre-move and post-move memberships are both 4 panes, so a window
// rendering a stale membership is indistinguishable from a correct one by
// count. Every assertion here compares the SET of agent names.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

// paneNameSet renders a pane list as a sorted, comparable set of agent names.
func paneNameSet(panes []PaneView) string {
	names := make([]string, 0, len(panes))
	for _, p := range panes {
		names = append(names, p.Name())
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// x5obViewer builds a running viewer over root holding the whole fleet as
// remote panes, with its assignment already loaded -- i.e. a viewer that has
// been up for a while, which is the state the bug is reported in.
func x5obViewer(t *testing.T, root string, agents ...string) *TUI {
	t.Helper()
	var panes []PaneView
	for _, name := range agents {
		panes = append(panes, &mockPaneView{name: name, host: WindowOnePeerName, alive: true})
	}
	tui := &TUI{
		projectRoot: root,
		panes:       panes,
		windowID:    "window-2",
		project:     &config.Project{Name: "rig", Root: root, PeerName: "window-2"},
		agentEvents: make(chan AgentEvent, 32),
	}
	tui.refreshMembershipIfFollower()
	a, err := LoadAssignment(root, tui.windowID)
	if err != nil {
		t.Fatalf("LoadAssignment: %v", err)
	}
	tui.assignment = a
	return tui
}

// seedX5obStores writes the pre-move state: every group on window 1.
func seedX5obStores(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	layout := "grid: 4x2\nmode: grid\ngroups:\n    - core\n    - eng\n" +
		"group_of:\n    eng1: core\n    eng2: core\n    growth: core\n    qa1: core\n" +
		"    super: eng\n    pm: eng\n    pmm: eng\n    shipper: eng\n"
	if err := os.WriteFile(filepath.Join(root, ".initech", "layout.yaml"), []byte(layout), 0o600); err != nil {
		t.Fatalf("seed layout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".initech", "assignments.yaml"),
		[]byte("group_window: {}\n"), 0o600); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
}

// servedViewer builds a secondary window holding the fleet, with ownership
// SERVED by window 1 -- the only way a viewer learns what it renders.
func servedViewer(t *testing.T, root string, owner map[string]string, agents ...string) *TUI {
	t.Helper()
	var panes []PaneView
	for _, name := range agents {
		panes = append(panes, &mockPaneView{name: name, host: WindowOnePeerName, alive: true})
	}
	tui := &TUI{
		projectRoot: root,
		panes:       panes,
		windowID:    "window-2",
		project:     &config.Project{Name: "rig", Root: root, PeerName: "window-2"},
		agentEvents: make(chan AgentEvent, 32),
	}
	tui.applyServedPaneOwnership(owner)
	return tui
}

// TestX5ob_ThePartitionIsNotAPartition is pm's named REGRESSION ANCHOR.
//
// The name is kept deliberately: it names the defect this bead bought the fix
// for, so a future reader searching the symptom lands here. What it asserts
// has inverted. It used to demonstrate that the partition was NOT one -- that
// the same predicate, evaluated independently per process, produced a double
// in one input combination and a hole in another. It now asserts that both are
// IMPOSSIBLE, because there is one computer and the viewer renders what it is
// served.
//
// Both modes are driven from the inputs that used to produce them:
//   - MODE 1 (double, eng2's ini-9isx rig): window 1 believes the viewer is
//     gone while the viewer believes itself present.
//   - MODE 2 (hole, the operator's live incident): the viewer's own assignment
//     copy is stale.
func TestX5ob_ThePartitionIsNotAPartition(t *testing.T) {
	root := t.TempDir()
	seedX5obStores(t, root)
	fleet := []string{"eng1", "eng2", "growth", "qa1", "super", "pm", "pmm", "shipper"}

	moved, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatalf("LoadAssignment: %v", err)
	}
	w, ok := moved.Writer()
	if !ok {
		t.Fatal("no writer")
	}
	if err := w.MoveGroup("core", "window-2"); err != nil {
		t.Fatalf("MoveGroup: %v", err)
	}
	groupOf := map[string]string{
		"eng1": "core", "eng2": "core", "growth": "core", "qa1": "core",
		"super": "eng", "pm": "eng", "pmm": "eng", "shipper": "eng",
	}
	var panes []PaneView
	for _, n := range fleet {
		panes = append(panes, &mockPaneView{name: n, alive: true})
	}

	// MODE 1's input combination: window 1 believes window-2 is GONE.
	// The old predicate made window 1 cover for it AND the viewer render it.
	// Under one computer, window 1's answer is the only answer: it owns them.
	ownerW2Gone := computePaneOwnership(panes, moved, groupOf, map[string]bool{})
	viewer := servedViewer(t, root, ownerW2Gone, fleet...)
	w1Set := setOf(ownershipKeysFor(ownerW2Gone, WindowOne))
	w2Set := paneNameSet(viewer.visiblePanesForWindow())
	if w2Set != "" {
		t.Errorf("MODE 1 (DOUBLE) IS BACK: window 1 owns %q while the viewer also renders %q. "+
			"Under served ownership the viewer cannot render what window 1 kept.", w1Set, w2Set)
	}

	// MODE 2's input combination: the viewer's OWN assignment copy is stale.
	// It is not consulted any more, so it cannot produce a hole.
	ownerW2Present := computePaneOwnership(panes, moved, groupOf, map[string]bool{"window-2": true})
	viewer2 := servedViewer(t, root, ownerW2Present, fleet...)
	stale, err := LoadAssignment(t.TempDir(), "window-2") // pre-move: nothing assigned
	if err != nil {
		t.Fatalf("LoadAssignment(stale): %v", err)
	}
	viewer2.assignment = stale
	got := paneNameSet(viewer2.visiblePanesForWindow())
	if got != "eng1,eng2,growth,qa1" {
		t.Errorf("MODE 2 (HOLE) IS BACK: viewer renders %q with a stale assignment copy, "+
			"want eng1,eng2,growth,qa1. Ownership must not derive from that copy.", got)
	}
}

// TestX5ob_StalenessImmunity is AC 4 and the cell that distinguishes this fix
// from the convergence palliative that was declined.
//
// The viewer is handed an assignment copy that is not merely stale but WRONG
// in the opposite direction, and a connected set that disagrees with window
// 1's. If rendering still follows the served answer, ownership demonstrably
// does not derive from local copies -- which is the decision's whole claim. If
// this cell can be made to fail by staleness, the build is (a) with extra
// steps and must not ship as (b).
func TestX5ob_StalenessImmunity(t *testing.T) {
	root := t.TempDir()
	seedX5obStores(t, root)
	fleet := []string{"eng1", "eng2", "growth", "qa1", "super", "pm", "pmm", "shipper"}
	groupOf := map[string]string{
		"eng1": "core", "eng2": "core", "growth": "core", "qa1": "core",
		"super": "eng", "pm": "eng", "pmm": "eng", "shipper": "eng",
	}

	moved, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatalf("LoadAssignment: %v", err)
	}
	w, _ := moved.Writer()
	if err := w.MoveGroup("core", "window-2"); err != nil {
		t.Fatalf("MoveGroup: %v", err)
	}
	var panes []PaneView
	for _, n := range fleet {
		panes = append(panes, &mockPaneView{name: n, alive: true})
	}
	owner := computePaneOwnership(panes, moved, groupOf, map[string]bool{"window-2": true})

	viewer := servedViewer(t, root, owner, fleet...)
	// Deliberately hostile local state: an assignment that knows nothing of
	// the move, and an empty group map.
	blank, err := LoadAssignment(t.TempDir(), "window-2")
	if err != nil {
		t.Fatalf("LoadAssignment(blank): %v", err)
	}
	viewer.assignment = blank
	viewer.layoutState.GroupOf = map[string]string{}

	if got := paneNameSet(viewer.visiblePanesForWindow()); got != "eng1,eng2,growth,qa1" {
		t.Errorf(`STALENESS IMMUNITY FAILED: viewer renders %q with a deliberately wrong
assignment copy and an empty group map.

Ownership is served, so neither should matter. If they do, this is the
convergence palliative wearing the authority's clothes.`, got)
	}
}

// TestX5ob_UnionInvariant is AC 3: across both windows the rendered sets union
// to exactly the fleet, with no overlap. Sets, never counts -- this bug's own
// measurement trap.
func TestX5ob_UnionInvariant(t *testing.T) {
	root := t.TempDir()
	seedX5obStores(t, root)
	fleet := []string{"eng1", "eng2", "growth", "qa1", "super", "pm", "pmm", "shipper"}
	groupOf := map[string]string{
		"eng1": "core", "eng2": "core", "growth": "core", "qa1": "core",
		"super": "eng", "pm": "eng", "pmm": "eng", "shipper": "eng",
	}
	moved, _ := LoadAssignment(root, WindowOne)
	w, _ := moved.Writer()
	if err := w.MoveGroup("core", "window-2"); err != nil {
		t.Fatalf("MoveGroup: %v", err)
	}

	for _, connected := range []map[string]bool{
		{"window-2": true}, // viewer attached
		{},                 // viewer gone: everything folds back
	} {
		var panes []PaneView
		for _, n := range fleet {
			panes = append(panes, &mockPaneView{name: n, alive: true})
		}
		owner := computePaneOwnership(panes, moved, groupOf, connected)

		seen := map[string]int{}
		for _, windowID := range []string{WindowOne, "window-2"} {
			for _, k := range ownershipKeysFor(owner, windowID) {
				seen[k]++
			}
		}
		for _, agent := range fleet {
			switch seen[agent] {
			case 0:
				t.Errorf("connected=%v: %s renders in NO window (hole)", connected, agent)
			case 1: // exactly right
			default:
				t.Errorf("connected=%v: %s renders in %d windows (double)", connected, agent, seen[agent])
			}
		}
	}
}

// TestX5ob_UnservedViewerRendersNothingRatherThanGuessing pins the deliberate
// choice at the one moment a viewer has no served answer: it renders nothing.
// Guessing from local copies is precisely the behaviour that produced the
// double, so "briefly empty" is the designed failure mode.
func TestX5ob_UnservedViewerRendersNothingRatherThanGuessing(t *testing.T) {
	root := t.TempDir()
	seedX5obStores(t, root)

	viewer := &TUI{
		projectRoot: root,
		panes: []PaneView{
			&mockPaneView{name: "eng1", host: WindowOnePeerName, alive: true},
		},
		windowID:    "window-2",
		project:     &config.Project{Name: "rig", Root: root, PeerName: "window-2"},
		agentEvents: make(chan AgentEvent, 8),
	}
	// A full local assignment that WOULD hand it the agent, if it were consulted.
	a, _ := LoadAssignment(root, WindowOne)
	w, _ := a.Writer()
	if err := w.MoveGroup("core", "window-2"); err != nil {
		t.Fatalf("MoveGroup: %v", err)
	}
	viewer.assignment = a
	viewer.layoutState.GroupOf = map[string]string{"eng1": "core"}

	if got := paneNameSet(viewer.visiblePanesForWindow()); got != "" {
		t.Errorf("an unserved viewer rendered %q by deriving from its own copies; it must "+
			"render nothing until window 1 serves it", got)
	}
}

// setOf renders a key slice as a comparable set string.
func setOf(keys []string) string {
	sort.Strings(keys)
	return strings.Join(keys, ",")
}
