package tui

// i7fr_cache_test.go is the DISCRIMINATING EXPERIMENT for ini-i7fr's one open
// leg: does window 1's modal reflect a group move the VIEWER made, or does it
// keep the assignment it cached at startup?
//
// Branch investigate/i7fr only. No fixes, no live stores.
//
// WHAT THIS RIG IS, stated so nobody reads more into it than it earns: two TUI
// values in ONE process, sharing one project root on disk, one configured as
// window 1 (local panes, WindowListen set) and one as a viewer (remote panes
// keyed window1:*, secondary peer identity). It replays a SEQUENCE -- window 1
// derives its modal first, THEN the viewer performs the real `m` move, THEN
// window 1 re-derives WITHOUT restarting -- which is the property the frozen
// snapshot could not carry and the qkwc seeded-store rig lacked.
//
// WHAT IT IS NOT: two OS processes over a socket. It shares no in-memory state
// between the two TUIs (each has its own assignment pointer and layoutState),
// so the store on disk is the only channel between them, which is the channel
// under investigation. But a real second process could differ in startup
// ORDER, and that limit is named rather than papered over.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

// i7frWindowOneTUI builds a window-1 TUI over root: local panes, bare keys,
// WindowListen set so participatesInMultiWindow (and therefore tiers) is true.
func i7frWindowOneTUI(t *testing.T, root string) *TUI {
	t.Helper()
	panes := i7frPanes("")
	keys := make([]string, len(panes))
	for i, p := range panes {
		keys[i] = paneKey(p)
	}
	state, ok := LoadLayout(root, keys)
	if !ok {
		t.Fatalf("window 1 LoadLayout returned !ok; the rig would be measuring defaults")
	}
	tui := &TUI{
		projectRoot: root,
		layoutState: state,
		panes:       panes,
		windowID:    WindowOne,
		project:     &config.Project{Name: "rig", Root: root, WindowListen: ":7500"},
	}
	tui.ensureGroups(false)
	return tui
}

// i7frViewerTUI builds a viewer TUI over the SAME root: remote panes keyed
// window1:*, secondary peer identity, WindowListen cleared (a viewer serves
// nothing) -- exactly what viewerProject produces.
func i7frViewerTUI(t *testing.T, root string) *TUI {
	t.Helper()
	panes := i7frPanes("window1")
	// A viewer has Roles=nil, so cfg.Agents is empty and LoadLayout is called
	// with no known keys. Reproduced faithfully: nil, not the pane keys.
	state, _ := LoadLayout(root, nil)
	tui := &TUI{
		projectRoot: root,
		layoutState: state,
		panes:       panes,
		windowID:    "window-2",
		project:     &config.Project{Name: "rig", Root: root, PeerName: "window-2"},
	}
	tui.ensureGroups(false)
	return tui
}

// i7frTierSummary renders a TUI's modal tier derivation as a comparable string.
func i7frTierSummary(t *testing.T, tui *TUI) string {
	t.Helper()
	assign := tui.agentsAssignment()
	tiers := tui.agentsTierGroups(assign, tui.agentsTiersActive())
	out := ""
	for _, tg := range tiers {
		w := tg.windowID
		if w == "" {
			w = "window-1"
		}
		out += w + ":" + i7frKeys(tg.groups) + "  "
	}
	return out
}

// TestI7FR_Window1ModalAfterViewerMove is the experiment named in the model:
// window 1 derives, the viewer moves a group with the real `m` handler, and
// window 1 re-derives WITHOUT restarting.
func TestI7FR_Window1ModalAfterViewerMove(t *testing.T) {
	t.Setenv("INITECH_I7FR_WHO", "cache-experiment")
	root := i7frSeedProject(t)

	// Start from the state the experiment specifies: no core entry, i.e. the
	// store as it would have read BEFORE the operator's move. Writing the
	// SEED is legal (it is the rig's own temp dir); the frozen evidence and
	// live stores are untouched.
	assignPath := filepath.Join(root, ".initech", "assignments.yaml")
	if err := os.WriteFile(assignPath, []byte("group_window: {}\n"), 0o600); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	w1 := i7frWindowOneTUI(t, root)
	viewer := i7frViewerTUI(t, root)

	before := i7frTierSummary(t, w1)
	t.Logf("W1 MODAL BEFORE viewer move: %s", before)
	t.Logf("VIEWER MODAL BEFORE move:    %s", i7frTierSummary(t, viewer))

	// The viewer performs the REAL `m` action on an agent in the core band.
	sel := -1
	for i, p := range viewer.panes {
		if viewer.layoutState.GroupOf[paneKey(p)] == "core" {
			sel = i
			break
		}
	}
	if sel < 0 {
		t.Fatal("no core-band agent in the viewer's own group map; rig assumption broken")
	}
	viewer.agents.selected = sel
	viewer.agentsMoveGroupToNextWindow()

	onDisk, err := os.ReadFile(assignPath)
	if err != nil {
		t.Fatalf("read assignment: %v", err)
	}
	t.Logf("ASSIGNMENTS ON DISK AFTER VIEWER MOVE:\n%s", string(onDisk))

	// Window 1's per-frame refresh hook, called WITHOUT restarting it.
	w1.reloadAssignmentIfFollower()
	after := i7frTierSummary(t, w1)
	t.Logf("W1 MODAL AFTER viewer move:  %s", after)
	t.Logf("VIEWER MODAL AFTER move:     %s", i7frTierSummary(t, viewer))

	fresh := i7frWindowOneTUI(t, root)
	t.Logf("W1 MODAL IF RESTARTED:       %s", i7frTierSummary(t, fresh))

	if before == after {
		t.Logf("RESULT: CACHE CONFIRMED — window 1's modal did NOT change after the viewer's " +
			"move, while the store on disk did. Window 1 shows a stale arrangement until restart.")
	} else {
		t.Logf("RESULT: CACHE REFUTED — window 1's modal tracked the viewer's write without a " +
			"restart, so the cache is not the window-1 mechanism and the open leg needs another cause.")
	}
}

// TestI7FR_Window1UniverseWithAnAgentOutsideGroupOf closes the last leg of the
// window-1 explanation. The frozen group_of names 8 agents, but the live fleet
// has more (eng3..eng6 workspaces exist). An agent absent from group_of is the
// ONE case where ensureGroups' append branch is reachable in window 1 -- and
// groupFor("eng3") is "eng", a label group_of already uses but groups: lacks.
func TestI7FR_Window1UniverseWithAnAgentOutsideGroupOf(t *testing.T) {
	t.Setenv("INITECH_I7FR_WHO", "w1-extra-agent")
	root := i7frSeedProject(t)
	if err := os.WriteFile(filepath.Join(root, ".initech", "assignments.yaml"),
		[]byte("group_window: {}\n"), 0o600); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	// Window 1's fleet: the 8 persisted agents PLUS one the store never saw.
	panes := i7frPanes("")
	panes = append(panes, &mockPaneView{name: "eng3", alive: true})
	keys := make([]string, len(panes))
	for i, p := range panes {
		keys[i] = paneKey(p)
	}
	state, ok := LoadLayout(root, keys)
	if !ok {
		t.Fatal("LoadLayout !ok")
	}
	t.Logf("GROUPS AFTER LOAD:        %v", state.Groups)

	tui := &TUI{
		projectRoot: root, layoutState: state, panes: panes, windowID: WindowOne,
		project: &config.Project{Name: "rig", Root: root, WindowListen: ":7500"},
	}
	tui.ensureGroups(false)
	t.Logf("GROUPS AFTER ensureGroups: %v", tui.layoutState.Groups)
	t.Logf("W1 MODAL (empty assignment, i.e. pre-viewer-move): %s", i7frTierSummary(t, tui))
}

// TestI7FR_FleetStateRefusesAViewerWrite is the census's most-suspicious field
// resolved, and it is the CONTRAST case: fleet-state.yaml has exactly the
// enforcement layout.yaml and assignments.yaml lack. A viewer's write is
// refused at the mutation point, so the file is only ever written by window 1
// in window 1's (bare) key space -- which is why live_pinned keys are bare in
// the same fleet, at the same instant, that layout order is window1:-prefixed.
func TestI7FR_FleetStateRefusesAViewerWrite(t *testing.T) {
	t.Setenv("INITECH_I7FR_WHO", "fleet-authority")
	root := i7frSeedProject(t)

	viewer := &TUI{projectRoot: root, windowID: "window-2",
		project: &config.Project{Name: "rig", Root: root, PeerName: "window-2"}}
	err := viewer.mutateFleet("i7fr probe", func(fs *FleetState) error { return nil })
	t.Logf("VIEWER mutateFleet -> %v", err)
	if err == nil {
		t.Error("a viewer WROTE fleet state; the authority rule that distinguishes this store " +
			"from layout/assignments does not actually hold")
	}

	w1 := &TUI{projectRoot: root, windowID: WindowOne,
		project: &config.Project{Name: "rig", Root: root, WindowListen: ":7500"}}
	if err := w1.mutateFleet("i7fr probe", func(fs *FleetState) error { return nil }); err != nil {
		t.Errorf("window 1 was refused its own store: %v", err)
	} else {
		t.Log("WINDOW 1 mutateFleet -> allowed (authority holds in both directions)")
	}
}
