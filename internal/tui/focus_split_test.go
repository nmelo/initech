package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestHandleKeyAltF_TogglesFocusSplit(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.Focused = "eng1"

	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt))
	if tui.layoutState.Mode != Layout2Col {
		t.Fatalf("Alt+f should enter the split: Mode = %v, want Layout2Col", tui.layoutState.Mode)
	}

	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt))
	if tui.layoutState.Mode != LayoutGrid {
		t.Errorf("Alt+f again should exit the split: Mode = %v, want restored LayoutGrid", tui.layoutState.Mode)
	}
}

func TestToggleFocusSplit_SinglePaneIsNoOp(t *testing.T) {
	tui := newTestTUI(testPane("a"))
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.Focused = "a"

	tui.toggleFocusSplit()

	if tui.layoutState.Mode != LayoutGrid {
		t.Errorf("single pane: Mode = %v, want unchanged LayoutGrid", tui.layoutState.Mode)
	}
	if tui.focusSplitPrev != nil {
		t.Error("single pane: focusSplitPrev should stay nil, no split entered")
	}
}

func TestToggleFocusSplit_EntersLayout2Col(t *testing.T) {
	tui := newTestTUI(testPane("a"), testPane("b"), testPane("c"))
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols = 3
	tui.layoutState.GridRows = 1
	tui.layoutState.GridExplicit = true
	tui.layoutState.Zoomed = true
	tui.layoutState.Focused = "b"

	tui.toggleFocusSplit()

	if tui.layoutState.Mode != Layout2Col {
		t.Errorf("Mode = %v, want Layout2Col", tui.layoutState.Mode)
	}
	if tui.layoutState.GridExplicit {
		t.Error("GridExplicit should be cleared entering the split")
	}
	if tui.layoutState.Zoomed {
		t.Error("Zoomed should be cleared entering the split")
	}
	if tui.layoutState.Focused != "b" {
		t.Errorf("Focused = %q, want unchanged %q (enters using the currently focused pane)", tui.layoutState.Focused, "b")
	}
	if tui.focusSplitPrev == nil {
		t.Fatal("focusSplitPrev should be set after entering the split")
	}
	if tui.focusSplitPrev.mode != LayoutGrid || tui.focusSplitPrev.gridCols != 3 ||
		tui.focusSplitPrev.gridRows != 1 || !tui.focusSplitPrev.gridExplicit ||
		!tui.focusSplitPrev.zoomed || tui.focusSplitPrev.focused != "b" {
		t.Errorf("focusSplitPrev = %+v, did not capture prior state correctly", tui.focusSplitPrev)
	}
}

// TestToggleFocusSplit_RoundTripRestoresExactly covers the bead's explicit
// requirement: toggling off restores the exact previous layout, including
// which pane was focused (not whatever was promoted during the split).
func TestToggleFocusSplit_RoundTripRestoresExactly(t *testing.T) {
	tui := newTestTUI(testPane("a"), testPane("b"), testPane("c"))
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols = 2
	tui.layoutState.GridRows = 2
	tui.layoutState.GridExplicit = true
	tui.layoutState.Focused = "a"

	tui.toggleFocusSplit() // enter, focused on "a"

	// Promote a different pane while inside the split.
	tui.layoutState.Focused = "c"
	tui.applyLayout()

	tui.toggleFocusSplit() // exit

	if tui.layoutState.Mode != LayoutGrid {
		t.Errorf("Mode = %v, want restored LayoutGrid", tui.layoutState.Mode)
	}
	if tui.layoutState.GridCols != 2 || tui.layoutState.GridRows != 2 {
		t.Errorf("grid dims = %dx%d, want restored 2x2", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}
	if !tui.layoutState.GridExplicit {
		t.Error("GridExplicit should be restored to true")
	}
	if tui.layoutState.Focused != "a" {
		t.Errorf("Focused = %q, want restored %q (pre-split focus, not the in-split promotion)", tui.layoutState.Focused, "a")
	}
	if tui.focusSplitPrev != nil {
		t.Error("focusSplitPrev should be cleared after exiting")
	}
}

// TestToggleFocusSplit_TogglingOffWithoutOptionFSession covers entering
// Layout2Col some other way (:main, a preset) and then pressing Option+F:
// there is no Option+F session to restore, so it falls back to LayoutGrid,
// mirroring presetLive's own toggle-off-with-nothing-to-restore behavior.
func TestToggleFocusSplit_TogglingOffWithoutOptionFSession(t *testing.T) {
	tui := newTestTUI(testPane("a"), testPane("b"))
	tui.layoutState.Mode = Layout2Col // e.g. entered via :main
	tui.layoutState.Focused = "a"

	tui.toggleFocusSplit()

	if tui.layoutState.Mode != LayoutGrid {
		t.Errorf("Mode = %v, want fallback LayoutGrid", tui.layoutState.Mode)
	}
	if tui.focusSplitPrev != nil {
		t.Error("focusSplitPrev should stay nil on the no-session fallback path")
	}
}

// TestToggleFocusSplit_LiveModeExitsAndResumes covers the deliberate
// live-mode decision: entering the split from LayoutLive exits live mode
// without touching liveEngine (it stays dormant), and toggling off restores
// LayoutLive with the same engine intact so rotation resumes correctly.
func TestToggleFocusSplit_LiveModeExitsAndResumes(t *testing.T) {
	tui := newTestTUI(testPane("a"), testPane("b"), testPane("c"))
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.LiveAuto = true
	tui.layoutState.GridCols = 2
	tui.layoutState.GridRows = 2
	tui.layoutState.Focused = "a"
	tui.liveEngine = NewLiveEngine(4, nil, nil)
	engineBefore := tui.liveEngine

	tui.toggleFocusSplit() // enter: should exit live mode

	if tui.layoutState.Mode != Layout2Col {
		t.Errorf("Mode = %v, want Layout2Col", tui.layoutState.Mode)
	}
	if tui.liveEngine != engineBefore {
		t.Error("liveEngine should be untouched (same pointer) while the split is active")
	}

	tui.toggleFocusSplit() // exit: should restore live mode

	if tui.layoutState.Mode != LayoutLive {
		t.Errorf("Mode = %v, want restored LayoutLive", tui.layoutState.Mode)
	}
	if !tui.layoutState.LiveAuto {
		t.Error("LiveAuto should be restored to true")
	}
	if tui.liveEngine != engineBefore {
		t.Error("liveEngine should still be the same engine after restoring live mode (no re-init needed)")
	}
}

// TestFocusSplit_ClickPromotesRightPane closes the loop on the AC's
// promotion requirement end to end: enter the split, click a pane in the
// right grid, and confirm it becomes the new left/focused pane while the
// previously-left pane rejoins the grid. Real mouse hit-testing against
// live-resized regions (newTestTUIWithScreen), not just state assertions.
func TestFocusSplit_ClickPromotesRightPane(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c")
	tui.layoutState.Focused = "a"
	tui.toggleFocusSplit() // enter: a in the left slot
	if tui.layoutState.Mode != Layout2Col {
		t.Fatalf("Mode = %v, want Layout2Col", tui.layoutState.Mode)
	}
	if tui.layoutState.Focused != "a" {
		t.Fatalf("Focused = %q, want a", tui.layoutState.Focused)
	}

	// Find pane "b"'s current on-screen region (it should be in the right
	// grid, since "a" occupies the left slot) and click inside it.
	var bRegion Region
	found := false
	for _, p := range tui.panes {
		if p.Name() == "b" {
			bRegion = p.GetRegion()
			found = true
		}
	}
	if !found {
		t.Fatal("pane b not found")
	}

	ev := tcell.NewEventMouse(bRegion.X+1, bRegion.Y+1, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	if tui.layoutState.Focused != "b" {
		t.Errorf("Focused = %q after clicking b, want b", tui.layoutState.Focused)
	}

	// Re-render and confirm b is now in the left slot, and a rejoined the grid.
	tui.applyLayout()
	for _, p := range tui.panes {
		r := p.GetRegion()
		switch p.Name() {
		case "b":
			if r.X != 0 {
				t.Errorf("promoted pane b.X = %d, want 0 (left slot)", r.X)
			}
		case "a":
			if r.X == 0 {
				t.Error("displaced pane a should have rejoined the right grid (X != 0), still at X=0")
			}
		}
	}
}

// TestFocusSplit_AltArrowsPromotesNextPane covers the other promotion path
// the AC calls out (click and Alt+arrows): cycling focus while in the split
// must move the newly-focused pane into the left slot.
func TestFocusSplit_AltArrowsPromotesNextPane(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c")
	tui.layoutState.Focused = "a"
	tui.toggleFocusSplit()

	tui.handleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt))

	if tui.layoutState.Focused == "a" {
		t.Fatal("Alt+Right should have moved focus off pane a")
	}
	promoted := tui.layoutState.Focused

	for _, p := range tui.panes {
		r := p.GetRegion()
		if p.Name() == promoted && r.X != 0 {
			t.Errorf("promoted pane %q.X = %d, want 0 (left slot)", promoted, r.X)
		}
		if p.Name() == "a" && r.X == 0 {
			t.Error("pane a should have rejoined the right grid, still at X=0")
		}
	}
}

// TestFocusSplit_ZoomComposes verifies the bead's zoom edge case end to end
// rather than just by reading computeLayout's precedence (Zoomed is checked
// before Mode). Alt+z while in the split should full-screen the focused
// pane, and un-zooming should return to the split unchanged.
func TestFocusSplit_ZoomComposes(t *testing.T) {
	tui, screen := newTestTUIWithScreen("a", "b", "c")
	tui.layoutState.Focused = "a"
	tui.toggleFocusSplit()

	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModAlt))
	if !tui.layoutState.Zoomed {
		t.Fatal("Alt+z should set Zoomed")
	}
	if len(tui.plan.Panes) != 1 || tui.plan.Panes[0].Pane.Name() != "a" {
		t.Fatalf("zoomed plan should show only the focused pane a, got %d panes", len(tui.plan.Panes))
	}
	w, h := screen.Size()
	if tui.plan.Panes[0].Region.W != w || tui.plan.Panes[0].Region.H != h-2 {
		t.Errorf("zoomed region = %+v, want full pane area", tui.plan.Panes[0].Region)
	}

	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModAlt))
	if tui.layoutState.Zoomed {
		t.Fatal("Alt+z again should clear Zoomed")
	}
	if tui.layoutState.Mode != Layout2Col {
		t.Errorf("Mode = %v after un-zoom, want the split unchanged (Layout2Col)", tui.layoutState.Mode)
	}
	if len(tui.plan.Panes) != 3 {
		t.Errorf("got %d plan entries after un-zoom, want 3 (split restored)", len(tui.plan.Panes))
	}
}
