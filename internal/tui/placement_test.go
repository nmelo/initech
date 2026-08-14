package tui

// The pane-placement assertions for ini-xq4r -- the story AC in executable
// form, and the assertion six beads of multi-window fixes shipped without:
// 'each agent renders in exactly one window; nothing is duplicated'. The
// composed rig asserted survival + modal parity + propagation, so a correct
// modal over an unmoved fleet passed every gate.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

// placementTUIs builds window 1 (local panes) and window 2 (remote panes) over
// one assignment store, the way the live pair is shaped.
func placementTUIs(t *testing.T, storeYAML string) (*TUI, *TUI, *WindowAssignment) {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	os.WriteFile(filepath.Join(root, ".initech", "assignments.yaml"), []byte(storeYAML), 0o644)
	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"super", "pmm", "shipper", "growth", "pm", "qa1", "eng1", "eng2"}

	w1 := newTestTUI()
	w1.projectRoot, w1.windowID, w1.assignment = root, WindowOne, a
	for _, n := range names {
		w1.panes = append(w1.panes, testPane(n))
	}
	w1.ensureGroups(false)

	w2 := newTestTUI()
	w2.projectRoot, w2.windowID, w2.assignment = root, WindowPeerName(2), a
	for _, n := range names {
		w2.panes = append(w2.panes, &RemotePane{name: n, host: WindowOnePeerName, alive: true})
	}
	w2.ensureGroups(false)
	// SERVED OWNERSHIP (ini-x5ob): window 1 computes, window 2 is served. A
	// viewer no longer derives ownership from its own assignment copy, so the
	// fixture models the handshake. The assertions in this file are unchanged.
	w2.applyServedPaneOwnership(
		computePaneOwnership(w2.panes, a, w2.layoutState.GroupOf, map[string]bool{WindowPeerName(2): true}))
	return w1, w2, a
}

func planNames(t *testing.T, tui *TUI, connected map[string]bool) []string {
	t.Helper()
	var out []string
	for _, p := range tui.panes {
		if ownerOfAgent(agentKey(p), tui.assignment, tui.layoutState.GroupOf, connected) == tui.windowID {
			out = append(out, p.Name())
		}
	}
	return out
}

// TestPlacement_EachAgentRendersInExactlyOneWindow is the story AC itself:
// with eng assigned to window 2 and window 2 CONNECTED, the two windows'
// rendered sets partition the fleet -- no duplicates, no orphans. This is
// the operator's exact live arrangement, with the assignment written in the
// form the MODAL'S OWN GENERATOR produces, because the previous rig seeded
// the store by hand with a form the modal never wrote and thereby hid the
// generator drift ("window2" vs "window-2") that made both windows wrong.
func TestPlacement_EachAgentRendersInExactlyOneWindow(t *testing.T) {
	// THE DRIFT PIN, asserted on the generator DIRECTLY -- not through a store
	// round-trip, because LoadAssignment's legacy migration HEALS the drifted
	// form on load and thereby masks a generator regression (measured: the
	// 'window'+n mutant passed this test's first draft). Both layers are
	// load-bearing; each needs its own assertion.
	if got := agentsNextWindowID([]string{WindowOne}); got != WindowPeerName(2) {
		t.Fatalf("the modal generates %q for the next window; a viewer launched with --window 2 "+
			"presents %q. Two generators for one identity family is ini-xq4r's root -- the "+
			"assignment names a window that can never attach", got, WindowPeerName(2))
	}
	w1, w2, a := placementTUIs(t, "group_window:\n    eng: "+agentsNextWindowID([]string{WindowOne})+"\n")
	if got := a.WindowOfGroup("eng"); got != WindowPeerName(2) {
		t.Fatalf("store round-trip lost the canonical identity: %q", got)
	}
	connected := map[string]bool{WindowPeerName(2): true}

	got1 := planNames(t, w1, connected)
	got2 := planNames(t, w2, map[string]bool{WindowPeerName(2): true})

	want2 := map[string]bool{"eng1": true, "eng2": true}
	if len(got2) != 2 || !want2[got2[0]] || !want2[got2[1]] {
		t.Errorf("window 2 plans %v, want exactly its assigned group [eng1 eng2] -- the live "+
			"failure was ZERO panes (assignment named 'window2', a window that cannot exist)", got2)
	}
	for _, n := range got1 {
		if want2[n] {
			t.Errorf("window 1 still renders %s while window 2 is CONNECTED and owns it -- "+
				"the agent renders in two windows at once", n)
		}
	}
	if len(got1) != 6 {
		t.Errorf("window 1 plans %d panes %v, want the 6 non-eng agents", len(got1), got1)
	}
}

// TestPlacement_DisconnectFoldsBackReattachRemoves is the cycle half: the
// orphan rule returns a dead window's agents to window 1, and a reattach
// takes them away again.
func TestPlacement_DisconnectFoldsBackReattachRemoves(t *testing.T) {
	w1, _, _ := placementTUIs(t, "group_window:\n    eng: window-2\n")

	folded := planNames(t, w1, map[string]bool{}) // window 2 gone
	if len(folded) != 8 {
		t.Errorf("with window 2 disconnected, window 1 plans %d panes, want all 8 folded back", len(folded))
	}
	back := planNames(t, w1, map[string]bool{"window-2": true}) // reattached
	if len(back) != 6 {
		t.Errorf("after reattach, window 1 plans %d panes %v, want the eng group gone again", len(back), back)
	}
}

// TestPlacement_LegacyStoreFormHealsOnLoad pins the migration: a store written
// by the pre-fix generator ("window2") names a window that can never attach,
// so LoadAssignment normalizes it -- the operator's existing store heals on
// next launch with zero action.
func TestPlacement_LegacyStoreFormHealsOnLoad(t *testing.T) {
	_, w2, a := placementTUIs(t, "group_window:\n    eng: window2\n")
	if got := a.WindowOfGroup("eng"); got != "window-2" {
		t.Fatalf("legacy identity %q not normalized on load; the operator's store stays "+
			"pointed at a window that can never attach", got)
	}
	got2 := planNames(t, w2, map[string]bool{"window-2": true})
	if len(got2) != 2 {
		t.Errorf("window 2 plans %v from a healed legacy store, want [eng1 eng2]", got2)
	}
}

// TestPlacement_HiddenWinsBothOrders pins grooming AC 2: visibility is global
// and wins; assignment moves an agent between windows' plans but never
// un-hides it. Both orders, because "hide then move" and "move then hide"
// exercise different write sequences over the same two stores.
func TestPlacement_HiddenWinsBothOrders(t *testing.T) {
	for _, order := range []string{"hide-then-move", "move-then-hide"} {
		t.Run(order, func(t *testing.T) {
			w1, w2, a := placementTUIs(t, "group_window: {}\n")
			hide := func() {
				w2.layoutState.Hidden = map[string]bool{
					"eng1": true, WindowOnePeerName + ":eng1": true,
				}
			}
			move := func() {
				if err := mustAssignWriter(t, a).MoveGroup("eng", "window-2"); err != nil {
					t.Fatal(err)
				}
			}
			if order == "hide-then-move" {
				hide()
				move()
			} else {
				move()
				hide()
			}
			// eng1 is assigned to window 2 AND hidden: it must render NOWHERE.
			conn := map[string]bool{"window-2": true}
			for _, n := range planNames(t, w1, conn) {
				if n == "eng1" {
					t.Error("hidden eng1 renders in window 1")
				}
			}
			visible2 := 0
			for _, p := range w2.panes {
				if (ownerOfAgent(agentKey(p), a, w2.layoutState.GroupOf, conn) == w2.windowID) &&
					!w2.layoutState.Hidden[agentKey(p)] {
					visible2++
				}
			}
			if visible2 != 1 {
				t.Errorf("window 2 shows %d visible panes, want 1 (eng2 only; hidden eng1 must "+
					"not be un-hidden by the move -- %s)", visible2, order)
			}
		})
	}
}

// TestLiveTickInputs_ReleasesSlotsOfDepartedGroup pins grooming AC 3: when a
// group leaves a window that is in live mode, any slots its agents occupied
// release IN THAT WINDOW -- no dangling reservation rotating a departed
// agent -- while the pin itself, being global, survives to re-apply wherever
// the agent now renders. The engine's pin set is re-derived per tick as
// global-state ∩ this-window's-panes, so there is nothing to mutate and
// nothing to forget to mutate.
func TestLiveTickInputs_ReleasesSlotsOfDepartedGroup(t *testing.T) {
	w1, _, a := placementTUIs(t, "group_window: {}\n")
	w1.layoutState.LivePinned = map[string]int{"eng1": 0, "super": 1}

	_, pinnedBefore := w1.liveTickInputs()
	if _, ok := pinnedBefore["eng1"]; !ok {
		t.Fatal("setup: eng1's pin absent before the move")
	}

	if err := mustAssignWriter(t, a).MoveGroup("eng", "window-2"); err != nil {
		t.Fatal(err)
	}
	// Window 2 connects; eng leaves window 1's plan. Simulate the connected
	// registry the way the placement tests do -- through the same predicate
	// applyLayout consults. liveTickInputs goes through visiblePanesForWindow,
	// which asks connectedWindowSet; on a test TUI with no windowSrv that
	// reports nothing connected, so eng would FOLD BACK. Pin the pane set
	// directly instead: drop eng from w1's panes as a connected move does.
	kept := w1.panes[:0]
	for _, p := range w1.panes {
		if p.Name() != "eng1" && p.Name() != "eng2" {
			kept = append(kept, p)
		}
	}
	w1.panes = kept

	live, pinnedAfter := w1.liveTickInputs()
	if _, ok := pinnedAfter["eng1"]; ok {
		t.Error("eng1's slot still reserved in window 1 after its group moved away -- a dangling " +
			"pin rotates a departed agent's empty slot forever")
	}
	if _, ok := pinnedAfter["super"]; !ok {
		t.Error("super's pin was released though super never moved; the intersection is over-broad")
	}
	if w1.layoutState.LivePinned["eng1"] != 0 {
		t.Error("the GLOBAL pin was mutated; pins survive a move and re-apply where the agent renders")
	}
	for _, p := range live {
		if p.Name() == "eng1" || p.Name() == "eng2" {
			t.Errorf("departed %s still in window 1's live rotation universe", p.Name())
		}
	}
}

// TestMoveGroup_EmitsExactlyOneNotice pins grooming AC 4: a group moving off
// the window the operator is watching is explained in the moment -- exactly
// one session notice per move, naming group and destination. Zero notices is
// the silent vanish; two would train the operator to skim them.
func TestMoveGroup_EmitsExactlyOneNotice(t *testing.T) {
	w1, _, _ := placementTUIs(t, "group_window: {}\n")
	w1.project = &config.Project{WindowListen: ":0"} // tiers active on window 1
	w1.agents.selected = 6                           // eng1 in placementTUIs' pane order
	w1.agentEvents = make(chan AgentEvent, 8)

	w1.agentsMoveGroupToNextWindow()

	var moves []AgentEvent
drain:
	for {
		select {
		case ev := <-w1.agentEvents:
			if ev.Type == EventGroupMoved {
				moves = append(moves, ev)
			}
		default:
			break drain
		}
	}
	if len(moves) != 1 {
		t.Fatalf("one assignment move produced %d group-moved notices, want exactly 1", len(moves))
	}
	if moves[0].Detail != "eng → monitor 2" {
		t.Errorf("notice detail = %q, want %q -- it must name the group and where it went, "+
			"because the operator watching this window just saw two panes vanish", moves[0].Detail, "eng → monitor 2")
	}
	if moves[0].Pane != "" {
		t.Errorf("group-moved notice carries pane %q; session-level notices attach no pane "+
			"(they describe the session's shape, and the [] bracket bug was ini-1ch)", moves[0].Pane)
	}
}

// TestFleetSurfaces_ScopedByDefaultWholeFleetOnExpand replaces the former
// TestFleetSurfaces_StayWholeFleet, and the replacement is a DECISION CHANGE,
// not a fix to a broken test.
//
// The old test pinned "the agents modal shows the whole fleet from every
// window" as a decided behavior, and it was one -- until ini-9isx, where the
// operator decided the opposite for a 39-agent fleet on two monitors and pm
// amended docs/spec.md's parity invariant to permit deliberate, DISCLOSED
// display scoping. A test that pins a superseded decision is not a guard; it is
// a claim about what the product does that is no longer true.
//
// What the old test was PROTECTING survives here and is the second half of this
// one: the whole fleet must still be reachable from a secondary window, or
// cross-monitor moves become impossible. It moved from "always shown" to "one
// key away, and the key is disclosed on screen".
func TestFleetSurfaces_ScopedByDefaultWholeFleetOnExpand(t *testing.T) {
	_, w2, _ := placementTUIs(t, "group_window:\n    eng: window-2\n")

	count := func() int {
		total := 0
		for _, idxs := range w2.agentsGroupMembers() {
			total += len(idxs)
		}
		return total
	}

	// Default: window 2 owns the eng group only.
	if got := count(); got != 2 {
		t.Fatalf("window 2's modal shows %d agents, want only its own 2 (eng1, eng2): an unscoped "+
			"default is the clutter ini-9isx exists to remove", got)
	}

	// Expanded: the whole fleet, so a cross-monitor move has something to grab.
	w2.agents.expanded = true
	if got := count(); got != 8 {
		t.Fatalf("the EXPANDED modal shows %d agents, want the whole fleet (8): without this the "+
			"operator cannot move a group to another monitor from a secondary window", got)
	}

	// And back, because a mode you cannot leave is a trap.
	w2.agents.expanded = false
	if got := count(); got != 2 {
		t.Fatalf("collapsing did not restore the window scope; modal shows %d, want 2", got)
	}
}

// ── ini-9fn: the empty-viewer hint ──────────────────────────────────
//
// One render state, two entrances (ini-xq4r's cross-reference): a first attach
// before anything is assigned, and the last group being moved away. The
// operator decided the shape out loud: ONE dim hint line, never bare-empty
// (post-crash-loop, a black window reads as a broken one), never auto-assign
// (initech does not decide his monitor layout).

// TestViewerOwnsNoGroups_BothEntrancesOneState pins the condition across both
// ways of arriving at emptiness, and the vanish the moment a group arrives.
func TestViewerOwnsNoGroups_BothEntrancesOneState(t *testing.T) {
	// Entrance 1: first attach, nothing ever assigned.
	_, w2, a := placementTUIs(t, "group_window: {}\n")
	if !w2.viewerOwnsNoGroups() {
		t.Fatal("first attach with nothing assigned: the hint condition is false, so the " +
			"operator gets the bare black window he explicitly rejected")
	}

	// A group arrives: the hint vanishes with no state to clear.
	if err := mustAssignWriter(t, a).MoveGroup("eng", "window-2"); err != nil {
		t.Fatal(err)
	}
	if w2.viewerOwnsNoGroups() {
		t.Fatal("a group is assigned and the hint condition is still true; the hint would " +
			"cover live panes")
	}

	// Entrance 2: the last group moves away again.
	if err := mustAssignWriter(t, a).MoveGroup("eng", WindowOne); err != nil {
		t.Fatal(err)
	}
	if !w2.viewerOwnsNoGroups() {
		t.Fatal("last group moved away: same state as first attach, and the hint condition " +
			"must be true by derivation, not by someone remembering to set it")
	}
}

// TestViewerOwnsNoGroups_NeverLies pins the states where the copy would be
// false and the hint must therefore not show: window 1 (it renders orphans and
// its own agents; 'no groups assigned' is not its vocabulary), a viewer before
// its panes arrive (the fleet is not known yet, and 'press Alt+a' over a
// connecting screen misdirects), and a viewer whose assigned panes are all
// hidden (groups ARE assigned; hidden is a different fact with different
// copy ownership).
func TestViewerOwnsNoGroups_NeverLies(t *testing.T) {
	w1, w2, _ := placementTUIs(t, "group_window:\n    eng: window-2\n")

	if w1.viewerOwnsNoGroups() {
		t.Error("window 1 claims to be an unassigned viewer")
	}
	// Window 1 with EVERY group assigned away is the discriminating case: it
	// owns nothing by the store's arithmetic, and the hint must still never
	// show there -- window 1 renders orphans and is the fallback surface, so
	// "no groups assigned" is not its vocabulary. (Mutation-found: dropping
	// the windowID gate passed the earlier w1 case because that w1 owned
	// groups; only this one forces the gate to exist.)
	w1bare, _, _ := placementTUIs(t, "group_window:\n    core: window-2\n    qa: window-2\n    eng: window-2\n")
	if w1bare.viewerOwnsNoGroups() {
		t.Error("window 1 with every group assigned away shows the viewer hint; window 1 is " +
			"the orphan/fallback surface and never an 'unassigned viewer'")
	}
	if w2.viewerOwnsNoGroups() {
		t.Error("a viewer WITH an assigned group shows the no-groups hint")
	}

	pre := newTestTUI() // viewer pre-connect: no panes yet
	pre.windowID = "window-2"
	pre.assignment = w2.assignment
	if pre.viewerOwnsNoGroups() {
		t.Error("a viewer with no panes (still connecting) shows the hint; the fleet is not " +
			"known yet and the copy would misdirect")
	}
}

// TestRenderEmptyViewerHint_DrawsTheDecidedCopyCentered renders through a real
// simulation screen and asserts the PM-owned string appears verbatim -- the
// one-copy rule: if the words drift from the constant, someone paraphrased.
func TestRenderEmptyViewerHint_DrawsTheDecidedCopyCentered(t *testing.T) {
	tui, screen := newTestTUIWithScreen()
	tui.windowID = "window-2"
	root := t.TempDir()
	a, _ := LoadAssignment(root, WindowOne)
	tui.assignment = a
	tui.panes = []PaneView{&RemotePane{name: "super", host: WindowOnePeerName, alive: true}}
	tui.ensureGroups(false)
	// The lone group is assigned to window 1 (absent = window 1), so this
	// viewer owns nothing and the hint must draw.
	w, h := screen.Size()
	tui.renderEmptyViewerHint(screen, w, h)
	screen.Show()

	row := make([]rune, 0, w)
	for x := 0; x < w; x++ {
		ch, _, _, _ := screen.GetContent(x, h/2)
		row = append(row, ch)
	}
	// The DECIDED LITERAL, not the constant: comparing the render to
	// emptyViewerHint would pass for any value of the constant, including a
	// paraphrase -- the constant-on-both-sides tautology, caught by mutation
	// here for the third time in this arc. The words below are Nelson's
	// decision via pm; if this test fails on a copy change, the change is a
	// re-decision and pm signs it here.
	const decidedCopy = "no groups assigned to this window — press Alt+a to assign"
	if got := strings.TrimSpace(string(row)); got != decidedCopy {
		t.Fatalf("hint row = %q, want the pm-decided copy %q rendered verbatim", got, decidedCopy)
	}
}
