package tui

import (
	"testing"
)

// ini-2p91: adding or removing an agent never changes the layout mode. Before
// the fix, addPane and removePane called recalcGrid(true), whose force flag
// set Mode = LayoutGrid unless the mode was live: focus, zoom, main and the
// live focus split were all thrown into a grid on every hot-add and remove.

// keptFleet builds a four-pane fleet a..d with b focused.
func keptFleet() *TUI {
	tui := newTestTUI(testPane("a"), testPane("b"), testPane("c"), testPane("d"))
	tui.layoutState.Focused = "b"
	return tui
}

func planPaneNames(tui *TUI) []string {
	var out []string
	for _, pr := range tui.plan.Panes {
		out = append(out, pr.Pane.Name())
	}
	return out
}

func inPlan(tui *TUI, name string) bool {
	for _, n := range planPaneNames(tui) {
		if n == name {
			return true
		}
	}
	return false
}

// addThenRemove hot-adds "e", runs check, removes "e", runs check again.
func addThenRemove(t *testing.T, tui *TUI, afterAdd, afterRemove func(t *testing.T)) {
	t.Helper()
	tui.insertPane(testPane("e"))
	afterAdd(t)
	if err := tui.removePane("e"); err != nil {
		t.Fatalf("removePane: %v", err)
	}
	afterRemove(t)
}

func wantModeFocus(t *testing.T, tui *TUI, when string, mode LayoutMode, focused string) {
	t.Helper()
	if tui.layoutState.Mode != mode {
		t.Fatalf("%s: mode = %v, want %v (a fleet change must never pick the layout)", when, tui.layoutState.Mode, mode)
	}
	if tui.layoutState.Focused != focused {
		t.Fatalf("%s: focused = %q, want %q", when, tui.layoutState.Focused, focused)
	}
}

func TestAddRemovePane_FocusModeStaysFocusedOnTheSamePane(t *testing.T) {
	tui := keptFleet()
	tui.layoutState.Mode = LayoutFocus
	check := func(when string) func(*testing.T) {
		return func(t *testing.T) {
			wantModeFocus(t, tui, when, LayoutFocus, "b")
			if got := planPaneNames(tui); len(got) != 1 || got[0] != "b" {
				t.Fatalf("%s: plan = %v, want only b drawn", when, got)
			}
		}
	}
	addThenRemove(t, tui, check("after add"), check("after remove"))
	// The new agent is listed like any unfocused pane.
	tui.insertPane(testPane("e"))
	if tui.findPaneByName("e") == nil {
		t.Fatal("added agent missing from the fleet")
	}
}

func TestAddRemovePane_ZoomStaysZoomedOnTheSamePane(t *testing.T) {
	tui := keptFleet()
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 2
	tui.layoutState.Zoomed = true
	check := func(when string) func(*testing.T) {
		return func(t *testing.T) {
			wantModeFocus(t, tui, when, LayoutGrid, "b")
			if !tui.layoutState.Zoomed {
				t.Fatalf("%s: zoom dropped", when)
			}
			if got := planPaneNames(tui); len(got) != 1 || got[0] != "b" {
				t.Fatalf("%s: plan = %v, want only b drawn", when, got)
			}
		}
	}
	addThenRemove(t, tui, check("after add"), check("after remove"))
}

func TestAddRemovePane_MainModeStaysMainAndTheNewAgentJoinsTheStack(t *testing.T) {
	tui := keptFleet()
	tui.layoutState.Mode = Layout2Col
	addThenRemove(t, tui,
		func(t *testing.T) {
			wantModeFocus(t, tui, "after add", Layout2Col, "b")
			got := planPaneNames(tui)
			if len(got) != 5 || got[0] != "b" || got[len(got)-1] != "e" {
				t.Fatalf("after add: plan = %v, want b on the left and e last in the stack", got)
			}
		},
		func(t *testing.T) {
			wantModeFocus(t, tui, "after remove", Layout2Col, "b")
			if inPlan(tui, "e") {
				t.Fatal("after remove: e still drawn")
			}
		})
}

func TestAddRemovePane_AutoGridGrowsAndShrinksWithTheFleet(t *testing.T) {
	tui := keptFleet()
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = autoGrid(4)
	c5, r5 := autoGrid(5)
	c4, r4 := autoGrid(4)
	addThenRemove(t, tui,
		func(t *testing.T) {
			wantModeFocus(t, tui, "after add", LayoutGrid, "b")
			if tui.layoutState.GridCols != c5 || tui.layoutState.GridRows != r5 {
				t.Fatalf("after add: grid %dx%d, want %dx%d", tui.layoutState.GridCols, tui.layoutState.GridRows, c5, r5)
			}
			if !inPlan(tui, "e") {
				t.Fatal("after add: e not drawn in the auto grid")
			}
		},
		func(t *testing.T) {
			if tui.layoutState.GridCols != c4 || tui.layoutState.GridRows != r4 {
				t.Fatalf("after remove: grid %dx%d, want %dx%d", tui.layoutState.GridCols, tui.layoutState.GridRows, c4, r4)
			}
		})
}

func TestAddRemovePane_ExplicitGridPresetKeepsItsSizeAndListsTheSurplus(t *testing.T) {
	tui := keptFleet()
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 2
	tui.layoutState.GridExplicit = true
	check := func(when string) func(*testing.T) {
		return func(t *testing.T) {
			wantModeFocus(t, tui, when, LayoutGrid, "b")
			if tui.layoutState.GridCols != 2 || tui.layoutState.GridRows != 2 || !tui.layoutState.GridExplicit {
				t.Fatalf("%s: grid %dx%d explicit=%v, want the operator's 2x2", when,
					tui.layoutState.GridCols, tui.layoutState.GridRows, tui.layoutState.GridExplicit)
			}
			if inPlan(tui, "e") {
				t.Fatalf("%s: e drawn in a full 2x2; the surplus is off-grid", when)
			}
		}
	}
	addThenRemove(t, tui, check("after add"), check("after remove"))
}

func TestAddRemovePane_LiveModeStaysLive(t *testing.T) {
	tui := keptFleet()
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.LiveAuto = true
	tui.initLiveEngine(0)
	check := func(when string) func(*testing.T) {
		return func(t *testing.T) {
			if tui.layoutState.Mode != LayoutLive {
				t.Fatalf("%s: mode = %v, want live", when, tui.layoutState.Mode)
			}
		}
	}
	addThenRemove(t, tui, check("after add"), check("after remove"))
}

func TestAddRemovePane_LiveFocusSplitKeepsTheLeftPaneAndAdmitsOnlyWorking(t *testing.T) {
	tui := liveFocusTUI(t) // a held left; a, b, c working, d idle
	altShiftF(tui)
	if tui.liveFocus == nil {
		t.Fatal("setup: split not entered")
	}
	idle := testPane("f")
	idle.activity = StateIdle
	tui.insertPane(idle)
	addThenRemove(t, tui,
		func(t *testing.T) {
			wantModeFocus(t, tui, "after add", Layout2Col, "a")
			if tui.liveFocus == nil {
				t.Fatal("after add: the live focus split was dropped")
			}
			if !inPlan(tui, "e") {
				t.Fatalf("after add: working e not on the right, plan = %v", planPaneNames(tui))
			}
			if inPlan(tui, "f") {
				t.Fatal("after add: idle f forced onto the right")
			}
		},
		func(t *testing.T) {
			wantModeFocus(t, tui, "after remove", Layout2Col, "a")
			if tui.liveFocus == nil {
				t.Fatal("after remove: the live focus split was dropped")
			}
		})
}

// AC 3: removing the focused pane moves focus to the NEXT pane in display
// order and keeps the mode. Removing b must land on c; the old "" reset
// snapped to the first pane, a.
func TestRemovePane_FocusedPaneInFocusModeMovesFocusToTheNextPane(t *testing.T) {
	tui := keptFleet()
	tui.layoutState.Mode = LayoutFocus
	tui.applyLayout()
	if err := tui.removePane("b"); err != nil {
		t.Fatal(err)
	}
	wantModeFocus(t, tui, "after removing the focused pane", LayoutFocus, "c")
	if got := planPaneNames(tui); len(got) != 1 || got[0] != "c" {
		t.Fatalf("plan = %v, want c alone", got)
	}
}

func TestRemovePane_FocusedLastPaneWrapsToTheFirst(t *testing.T) {
	tui := keptFleet()
	tui.layoutState.Mode = LayoutFocus
	tui.layoutState.Focused = "d"
	if err := tui.removePane("d"); err != nil {
		t.Fatal(err)
	}
	wantModeFocus(t, tui, "after removing the last pane in order", LayoutFocus, "a")
}

func TestRemovePane_NextFocusSkipsHiddenPanes(t *testing.T) {
	tui := keptFleet()
	tui.layoutState.Mode = LayoutFocus
	tui.layoutState.Hidden = map[string]bool{"c": true}
	if err := tui.removePane("b"); err != nil {
		t.Fatal(err)
	}
	wantModeFocus(t, tui, "after removing b with c hidden", LayoutFocus, "d")
}

func TestRemovePane_LeftPaneOfTheLiveFocusSplitKeepsTheSplit(t *testing.T) {
	tui := liveFocusTUI(t)
	altShiftF(tui)
	if err := tui.removePane("a"); err != nil {
		t.Fatal(err)
	}
	wantModeFocus(t, tui, "after removing the left pane", Layout2Col, "b")
	if tui.liveFocus == nil {
		t.Fatal("removing the left pane dropped the live focus split")
	}
	if got := planPaneNames(tui); len(got) == 0 || got[0] != "b" {
		t.Fatalf("plan = %v, want b held on the left", got)
	}
}

// AC 4: the saved layout after an add records the operator's mode, not grid.
func TestAddPane_SavedLayoutKeepsTheOperatorsMode(t *testing.T) {
	tui := keptFleet()
	tui.projectRoot = t.TempDir()
	tui.layoutState.Mode = LayoutFocus
	tui.insertPane(testPane("e"))
	got, ok := LoadLayout(tui.projectRoot, []string{"a", "b", "c", "d", "e"})
	if !ok {
		t.Fatal("no layout saved after the add")
	}
	// The focused pane is not persisted by design (layout.go: a momentary
	// choice, not a preference); the mode is.
	if got.Mode != LayoutFocus {
		t.Fatalf("loaded mode=%v, want focus", got.Mode)
	}
}
