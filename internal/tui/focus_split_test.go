package tui

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
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

// regionsOverlap reports whether two regions share any screen cell.
func regionsOverlap(a, b Region) bool {
	if a.X+a.W <= b.X || b.X+b.W <= a.X {
		return false
	}
	if a.Y+a.H <= b.Y || b.Y+b.H <= a.Y {
		return false
	}
	return true
}

// findRegionOverlapsAndWidthMismatches is the T-free core of
// assertNoOverlapAndEmulatorMatchesRegion: for every currently-visible pane,
// its emulator's column width must match its assigned region's terminal
// width (a mismatch is the "wrapped continuation bleeds into the neighbor"
// signature reported), and no two visible panes' regions may overlap.
// Compares regions directly against each other — independent of what either
// pane actually draws — so, unlike the content-based contamination checks,
// it is not blind to a corrupted/overlapping self-region (see
// TestFocusSplit_ContaminationDetectorFiresOnRealOverlap).
func findRegionOverlapsAndWidthMismatches(tui *TUI) []string {
	type visible struct {
		name   string
		region Region
	}
	var vs []visible
	var problems []string
	for _, pv := range tui.panes {
		if tui.layoutState.Hidden[paneKey(pv)] {
			continue
		}
		r := pv.GetRegion()
		wantCols, _ := r.TerminalSize()
		gotCols := pv.Emulator().Width()
		if gotCols != wantCols {
			problems = append(problems, fmt.Sprintf("pane %q: emulator width = %d, region wants %d (region=%+v) — emulator wider/narrower than its own region", pv.Name(), gotCols, wantCols, r))
		}
		vs = append(vs, visible{name: pv.Name(), region: r})
	}
	for i := 0; i < len(vs); i++ {
		for j := i + 1; j < len(vs); j++ {
			if regionsOverlap(vs[i].region, vs[j].region) {
				problems = append(problems, fmt.Sprintf("regions overlap: %q=%+v and %q=%+v", vs[i].name, vs[i].region, vs[j].name, vs[j].region))
			}
		}
	}
	return problems
}

// assertNoOverlapAndEmulatorMatchesRegion is the ini-mdj5 diagnostic used by
// the metadata-level hide tests: fails the test with one Errorf per problem
// findRegionOverlapsAndWidthMismatches finds.
func assertNoOverlapAndEmulatorMatchesRegion(t *testing.T, tui *TUI) {
	t.Helper()
	for _, problem := range findRegionOverlapsAndWidthMismatches(tui) {
		t.Error(problem)
	}
}

// TestFocusSplit_HideNonFocusedPane is the ini-mdj5 regression test: hiding
// a pane while in the split must leave every remaining visible pane's
// emulator matching its own region, with no overlap.
func TestFocusSplit_HideNonFocusedPane(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d", "e")
	tui.layoutState.Focused = "a"
	tui.toggleFocusSplit() // enter: a left, b/c/d/e in a 2x2 grid right
	assertNoOverlapAndEmulatorMatchesRegion(t, tui)

	tui.toggleHidden("c") // hide a non-focused right-grid pane

	assertNoOverlapAndEmulatorMatchesRegion(t, tui)
}

// TestFocusSplit_HideFocusedPane is the ini-mdj5 regression test for the
// specific claim the bead calls out as untested-and-unverified: a comment in
// layout.go asserts that hiding the FOCUSED pane is "handled for free"
// because focus was already snapped to visible[0]. This is exactly the case
// where the big-slot occupant's identity changes, so it is tested directly
// rather than trusted.
func TestFocusSplit_HideFocusedPane(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d", "e")
	tui.layoutState.Focused = "a"
	tui.toggleFocusSplit() // enter: a left, b/c/d/e in a 2x2 grid right
	assertNoOverlapAndEmulatorMatchesRegion(t, tui)

	tui.toggleHidden("a") // hide the FOCUSED (left-slot) pane

	if tui.layoutState.Focused == "a" {
		t.Fatal("focus should have moved off the now-hidden pane a")
	}
	assertNoOverlapAndEmulatorMatchesRegion(t, tui)
}

// TestFocusSplit_HideThenReshow is the ini-mdj5 regression test for stale
// geometry: hidden panes are skipped by applyLayout's resize loop, so a
// pane's region/emulator could retain whatever it had before hiding once it
// reappears.
func TestFocusSplit_HideThenReshow(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d", "e")
	tui.layoutState.Focused = "a"
	tui.toggleFocusSplit()
	assertNoOverlapAndEmulatorMatchesRegion(t, tui)

	tui.toggleHidden("c") // hide
	assertNoOverlapAndEmulatorMatchesRegion(t, tui)

	tui.toggleHidden("c") // reshow
	assertNoOverlapAndEmulatorMatchesRegion(t, tui)
}

// TestFocusSplit_HideAcrossShapeChanges sweeps pane counts and hide
// sequences chosen to force autoGrid's COLUMN COUNT to change (3 cols -> 2
// cols), not just a partial last row within the same shape — a broader
// search than the single 5-pane case, in case the mismatch only appears at
// a shape boundary.
func TestFocusSplit_HideAcrossShapeChanges(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f", "g"}
	tui, _ := newTestTUIWithScreen(names...)
	tui.layoutState.Focused = "a"
	tui.toggleFocusSplit() // 7 panes: a left, b..g (rightCount=6, autoGrid=3x2) right
	assertNoOverlapAndEmulatorMatchesRegion(t, tui)

	// Hide one at a time down to 2 panes total, checking after every step.
	toHide := []string{"g", "f", "e", "d", "c"}
	for _, name := range toHide {
		tui.toggleHidden(name)
		assertNoOverlapAndEmulatorMatchesRegion(t, tui)
	}

	// Reshow one at a time back up, checking after every step.
	for i := len(toHide) - 1; i >= 0; i-- {
		tui.toggleHidden(toHide[i])
		assertNoOverlapAndEmulatorMatchesRegion(t, tui)
	}
}

// TestFocusSplit_HideMultipleAtOnce covers the operator's exact repro
// wording ("hide one or more panes") — several hides in a row without
// re-entering the split between them.
func TestFocusSplit_HideMultipleAtOnce(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d", "e", "f")
	tui.layoutState.Focused = "a"
	tui.toggleFocusSplit()
	assertNoOverlapAndEmulatorMatchesRegion(t, tui)

	tui.toggleHidden("c")
	tui.toggleHidden("e")
	assertNoOverlapAndEmulatorMatchesRegion(t, tui)
}

// TestFocusSplit_HideRenderedScreenNoCrossContamination is the most direct
// possible check for ini-mdj5's reported symptom: it renders to a real
// tcell.SimulationScreen with DISTINCTIVE per-pane content (each pane fills
// its emulator with its own repeated letter), triggers a hide inside the
// split, renders again for the very next frame (the frame where a freshly
// resized pane's resize-settle window is active), and inspects the actual
// screen cells for any OTHER pane's letter appearing where it should not —
// the literal "wrapped continuation of a neighbor's text" signature from the
// bug report, checked directly rather than inferred from region/emulator
// metadata.
func TestFocusSplit_HideRenderedScreenNoCrossContamination(t *testing.T) {
	tui, screen := newTestTUIWithScreen("a", "b", "c", "d", "e")
	tui.layoutState.Overlay = false // isolate pane content from the status overlay panel's own UI text

	// Fill each pane's emulator with enough of its own distinctive letter to
	// cover any plausible region width.
	letters := map[string]byte{"a": 'A', "b": 'B', "c": 'C', "d": 'D', "e": 'E'}
	for _, pv := range tui.panes {
		lp := pv.(*Pane)
		line := make([]byte, 200)
		for i := range line {
			line[i] = letters[lp.name]
		}
		lp.emu.Write(line)
	}

	tui.layoutState.Focused = "a"
	tui.toggleFocusSplit() // a left, b/c/d/e in a 2x2 grid right
	tui.render()           // baseline frame, before any hide

	tui.toggleHidden("c") // triggers a reflow: rightCount 4 -> 3, shape changes
	tui.render()          // the critical frame: freshly-resized panes are mid-settle

	w, h := screen.Size()
	// For every screen cell, if it shows a letter belonging to a pane whose
	// CURRENT region does not include that cell, that is cross-contamination:
	// either a geometry/emulator mismatch (as hypothesized) or stale content
	// left over from the pre-hide layout (the resize-settle interaction).
	regionOf := make(map[byte]Region)
	for _, pv := range tui.panes {
		if tui.layoutState.Hidden[pv.Name()] {
			continue
		}
		lp := pv.(*Pane)
		regionOf[letters[lp.name]] = lp.GetRegion()
	}

	contamination := 0
	for y := 0; y < h-2; y++ { // exclude the reserved status/tip bar rows (paneH = h-2), not pane content
		for x := 0; x < w; x++ {
			ch, _, _, _ := screen.GetContent(x, y)
			letter := byte(ch)
			r, known := regionOf[letter]
			if !known {
				continue // not one of our distinctive letters (ribbon/border/etc.)
			}
			inOwnRegion := x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
			if !inOwnRegion {
				contamination++
				if contamination <= 10 {
					t.Errorf("cell (%d,%d) shows %q, but that pane's current region is %+v — content outside its own region", x, y, string(ch), r)
				}
			}
		}
	}
	if contamination > 10 {
		t.Errorf("... and %d more contaminated cells", contamination-10)
	}
}

// TestFocusSplit_HideSteadyStateAfterSettling checks the STABLE state well
// after the resize-settle window (3 frames / 150ms) has fully expired, since
// the operator described a persistent break ("unreadable until the layout
// is changed"), not a one-frame flicker. Renders repeatedly past the settle
// window before inspecting the screen.
func TestFocusSplit_HideSteadyStateAfterSettling(t *testing.T) {
	tui, screen := newTestTUIWithScreen("a", "b", "c", "d", "e")
	tui.layoutState.Overlay = false

	letters := map[string]byte{"a": 'A', "b": 'B', "c": 'C', "d": 'D', "e": 'E'}
	for _, pv := range tui.panes {
		lp := pv.(*Pane)
		line := make([]byte, 200)
		for i := range line {
			line[i] = letters[lp.name]
		}
		lp.emu.Write(line)
	}

	tui.layoutState.Focused = "a"
	tui.toggleFocusSplit()
	for i := 0; i < 10; i++ {
		tui.render()
	}

	tui.toggleHidden("c")

	// Clear the settle deadline directly rather than sleeping in a test:
	// resizeSettleFrames still gates the first few renders (correct — those
	// are the transient frames), but force the wall-clock deadline into the
	// past so only the frame counter, not real time, determines when this
	// loop reaches steady state.
	for _, pv := range tui.panes {
		if lp, ok := pv.(*Pane); ok {
			lp.resizeSettleDeadline = time.Now().Add(-time.Second)
		}
	}
	for i := 0; i < 10; i++ {
		tui.render()
	}

	w, h := screen.Size()
	regionOf := make(map[byte]Region)
	for _, pv := range tui.panes {
		if tui.layoutState.Hidden[pv.Name()] {
			continue
		}
		lp := pv.(*Pane)
		regionOf[letters[lp.name]] = lp.GetRegion()
	}

	contamination := 0
	for y := 0; y < h-2; y++ {
		for x := 0; x < w; x++ {
			ch, _, _, _ := screen.GetContent(x, y)
			letter := byte(ch)
			r, known := regionOf[letter]
			if !known {
				continue
			}
			inOwnRegion := x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
			if !inOwnRegion {
				contamination++
				if contamination <= 10 {
					t.Errorf("steady state: cell (%d,%d) shows %q, but that pane's region is %+v", x, y, string(ch), r)
				}
			}
		}
	}
	if contamination > 10 {
		t.Errorf("... and %d more contaminated cells", contamination-10)
	}
}

// checkScreenContamination renders and inspects the full screen for any
// pane's distinctive letter appearing outside that pane's own CURRENT
// region. Excludes the status overlay (disabled by the caller) and the
// reserved bottom status/tip rows.
func checkScreenContamination(t *testing.T, tui *TUI, screen tcell.SimulationScreen, letters map[string]byte) int {
	t.Helper()
	w, h := screen.Size()
	regionOf := make(map[byte]Region)
	for _, pv := range tui.panes {
		if tui.layoutState.Hidden[pv.Name()] {
			continue
		}
		lp := pv.(*Pane)
		regionOf[letters[lp.name]] = lp.GetRegion()
	}
	contamination := 0
	for y := 0; y < h-2; y++ {
		for x := 0; x < w; x++ {
			ch, _, _, _ := screen.GetContent(x, y)
			letter := byte(ch)
			r, known := regionOf[letter]
			if !known {
				continue
			}
			if !(x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H) {
				contamination++
				if contamination <= 5 {
					t.Errorf("cell (%d,%d) shows %q, owner's region is %+v", x, y, string(ch), r)
				}
			}
		}
	}
	return contamination
}

// TestFocusSplit_OddVsEvenRightCount directly tests the operator's own
// diagnosis: an odd number of panes on the RIGHT side breaks rendering,
// while an even number renders fine. Sweeps rightCount = 1..7 (the focused
// pane always occupies the left slot, so total panes = rightCount+1), each
// as its own starting scenario (not a hide transition), with full
// screen-content inspection.
func TestFocusSplit_OddVsEvenRightCount(t *testing.T) {
	allNames := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	allLetters := map[string]byte{"a": 'A', "b": 'B', "c": 'C', "d": 'D', "e": 'E', "f": 'F', "g": 'G', "h": 'H'}

	for rightCount := 1; rightCount <= 7; rightCount++ {
		rightCount := rightCount
		t.Run(fmt.Sprintf("rightCount=%d", rightCount), func(t *testing.T) {
			names := allNames[:rightCount+1] // "a" (focus) + rightCount others
			tui, screen := newTestTUIWithScreen(names...)
			tui.layoutState.Overlay = false

			for _, pv := range tui.panes {
				lp := pv.(*Pane)
				line := make([]byte, 400)
				for i := range line {
					line[i] = allLetters[lp.name]
				}
				lp.emu.Write(line)
			}

			tui.layoutState.Focused = "a"
			tui.toggleFocusSplit()
			for i := 0; i < 8; i++ { // past the resize-settle window
				tui.render()
			}

			n := checkScreenContamination(t, tui, screen, allLetters)
			t.Logf("rightCount=%d: contamination=%d", rightCount, n)
		})
	}
}

// countScreenContamination is the non-failing twin of checkScreenContamination:
// it counts cells showing a pane's letter outside that pane's current region
// without calling t.Errorf, so a polling loop can sample many frames and only
// report the worst one found.
func countScreenContamination(tui *TUI, screen tcell.SimulationScreen, letters map[string]byte) (int, string) {
	w, h := screen.Size()
	regionOf := make(map[byte]Region)
	for _, pv := range tui.panes {
		if tui.layoutState.Hidden[pv.Name()] {
			continue
		}
		lp := pv.(*Pane)
		regionOf[letters[lp.name]] = lp.GetRegion()
	}
	contamination := 0
	sample := ""
	for y := 0; y < h-2; y++ {
		for x := 0; x < w; x++ {
			ch, _, _, _ := screen.GetContent(x, y)
			letter := byte(ch)
			r, known := regionOf[letter]
			if !known {
				continue
			}
			if !(x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H) {
				contamination++
				if sample == "" {
					sample = fmt.Sprintf("cell (%d,%d) shows %q, owner's current region is %+v", x, y, string(ch), r)
				}
			}
		}
	}
	return contamination, sample
}

// TestFocusSplit_HideWithRealPTYNoCrossContamination drives REAL PTY-backed
// panes through a hide transition inside focus-split, instead of a single
// static emu.Write() call. Every other regression test added for ini-mdj5
// writes synthetic content once into an otherwise-idle emulator; none of them
// reproduced the reported bug. This test exists to answer a specific
// hypothesis: that the harness itself is blind to the bug because it never
// exercises the VT emulator's real reflow-on-resize path (existing multi-row
// WRAPPED content being reflowed when Resize(cols, rows) changes the width)
// or a live readLoop goroutine actively writing while a hide-triggered resize
// happens. Each pane here runs a real shell loop continuously printing a long
// line (forcing wraps across several rows) so there is genuine wrapped
// scrollback content, written by a live process, at the moment of the hide.
func TestFocusSplit_HideWithRealPTYNoCrossContamination(t *testing.T) {
	skipInCI(t)

	names := []string{"a", "b", "c", "d", "e"}
	letters := map[string]byte{"a": 'A', "b": 'B', "c": 'C', "d": 'D', "e": 'E'}

	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)

	panes := make([]*Pane, len(names))
	views := make([]PaneView, len(names))
	for i, n := range names {
		letter := letters[n]
		// A long line (well past any plausible pane width) forces real
		// multi-row wrapping; the short sleep keeps the readLoop goroutine
		// actively writing throughout the test instead of finishing instantly.
		script := fmt.Sprintf(`while true; do printf '%s\n'; sleep 0.005; done`, strings.Repeat(string(letter), 300))
		cfg := PaneConfig{Name: n, Command: []string{"/bin/sh", "-c", script}}
		p, err := NewPane(cfg, 10, 40)
		if err != nil {
			t.Fatalf("NewPane(%s): %v", n, err)
		}
		p.Start()
		t.Cleanup(func() { p.Close() })
		panes[i] = p
		views[i] = p
	}

	ls := DefaultLayoutState(names)
	ls.Overlay = false
	tui := &TUI{screen: s, panes: views, layoutState: ls, lastW: 120, lastH: 40}
	tui.plan = computeLayout(ls, views, 120, 40)

	tui.layoutState.Focused = "a"
	tui.toggleFocusSplit() // a left, b/c/d/e in a grid right

	// Let real, actively-written output accumulate and wrap for a while
	// before the hide, so each emulator holds genuine multi-row wrapped
	// content (and scrollback) rather than one static line written once.
	warmup := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(warmup) {
		tui.render()
		time.Sleep(10 * time.Millisecond)
	}

	tui.toggleHidden("c") // triggers a reflow: rightCount 4 -> 3, shape changes

	// Keep rendering WHILE the readLoop goroutines are still actively
	// writing into their emulators — the concurrent-write-during-and-after
	// resize case a single static emu.Write() test cannot exercise — and
	// sample contamination across many frames, including the settle window
	// and steady state past it.
	maxContamination := 0
	var worstSample string
	var worstRegionProblems []string
	settleDeadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(settleDeadline) {
		tui.render()
		if n, sample := countScreenContamination(tui, s, letters); n > maxContamination {
			maxContamination = n
			worstSample = sample
		}
		// Region-vs-region check too: the content check alone is blind to a
		// corrupted/overlapping self-region (see
		// TestFocusSplit_ContaminationDetectorFiresOnRealOverlap) — this
		// covers what that one structurally cannot.
		if problems := findRegionOverlapsAndWidthMismatches(tui); len(problems) > 0 {
			worstRegionProblems = problems
		}
		time.Sleep(10 * time.Millisecond)
	}

	if maxContamination > 0 {
		t.Errorf("real-PTY hide transition produced cross-region contamination (worst frame: %d cells) — reproduces with real reflow/live-write dynamics where synthetic emu.Write() content did not. Worst sample: %s", maxContamination, worstSample)
	} else {
		t.Logf("real-PTY hide transition: zero contamination across the sampled window (this does NOT rule out the emulator-reflow hypothesis, only refutes it for this specific script/timing/pane-count combination)")
	}
	for _, p := range worstRegionProblems {
		t.Errorf("region-vs-region check: %s", p)
	}
}

// countScreenContaminationGeneric is the PaneView-generic twin of
// countScreenContamination: it works across mixed local/*Pane and *RemotePane
// panes via the shared interface (Name/Emulator/GetRegion), instead of
// asserting pv.(*Pane). Keys on the full rune, not a byte-truncated cast: a
// byte truncation aliases distinct Unicode box-drawing/ribbon glyphs (used by
// dividers and pane chrome) onto the same low byte as an ASCII letter (e.g.
// U+2552 truncates to the same byte as 'R'), producing false-positive
// "contamination" that is really just a divider character.
func countScreenContaminationGeneric(tui *TUI, screen tcell.SimulationScreen, letters map[string]byte) (int, string) {
	w, h := screen.Size()
	regionOf := make(map[rune]Region)
	for _, pv := range tui.panes {
		if tui.layoutState.Hidden[paneKey(pv)] {
			continue
		}
		regionOf[rune(letters[pv.Name()])] = pv.GetRegion()
	}
	contamination := 0
	sample := ""
	for y := 0; y < h-2; y++ {
		for x := 0; x < w; x++ {
			ch, _, _, _ := screen.GetContent(x, y)
			r, known := regionOf[ch]
			if !known {
				continue
			}
			if !(x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H) {
				contamination++
				if sample == "" {
					sample = fmt.Sprintf("cell (%d,%d) shows %q, owner's current region is %+v", x, y, string(ch), r)
				}
			}
		}
	}
	return contamination, sample
}

// countScreenContaminationRune is countScreenContaminationGeneric's twin for
// markers that don't fit in a byte — double-width CJK/emoji runes, whose
// code points exceed 255, cannot be represented as map[string]byte.
func countScreenContaminationRune(tui *TUI, screen tcell.SimulationScreen, markers map[string]rune) (int, string) {
	w, h := screen.Size()
	regionOf := make(map[rune]Region)
	for _, pv := range tui.panes {
		if tui.layoutState.Hidden[paneKey(pv)] {
			continue
		}
		regionOf[markers[pv.Name()]] = pv.GetRegion()
	}
	contamination := 0
	sample := ""
	for y := 0; y < h-2; y++ {
		for x := 0; x < w; x++ {
			ch, _, _, _ := screen.GetContent(x, y)
			r, known := regionOf[ch]
			if !known {
				continue
			}
			if !(x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H) {
				contamination++
				if sample == "" {
					sample = fmt.Sprintf("cell (%d,%d) shows %q, owner's current region is %+v", x, y, string(ch), r)
				}
			}
		}
	}
	return contamination, sample
}

// TestFocusSplit_HideRemotePaneNoCrossContamination drives a MIX of real
// PTY-backed local panes and a real *RemotePane through a hide transition in
// focus-split. This is directly motivated by this fleet's own live
// .initech/layout.yaml, which shows mode: main (Layout2Col) active with
// "workbench-intern:intern" (a host:name remote pane) in the hidden list —
// i.e. a remote-pane hide-while-in-focus-split has actually happened in the
// exact session that surfaced ini-mdj5. Every other regression test in this
// file exercises *Pane exclusively; RemotePane is a structurally separate
// PaneView implementation (its own Resize, its own Render, no resize-settle
// window at all — see remote_pane.go) that has never been included in a
// contamination check. NewRemotePane's stream/mux are unused by Resize/
// GetRegion/Render for a pane that never has Start() called (no readLoop),
// so a nil stream and nil mux are safe here; content is injected directly via
// emu.Write, same as the local half of the mix.
func TestFocusSplit_HideRemotePaneNoCrossContamination(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)

	letters := map[string]byte{"a": 'A', "b": 'B', "c": 'C', "remote": 'R'}

	localNames := []string{"a", "b", "c"}
	views := make([]PaneView, 0, 4)
	for _, n := range localNames {
		lp := &Pane{
			name:    n,
			emu:     vt.NewSafeEmulator(40, 10),
			alive:   true,
			visible: true,
		}
		line := make([]byte, 200)
		for i := range line {
			line[i] = letters[n]
		}
		lp.emu.Write(line)
		views = append(views, lp)
	}

	rp := NewRemotePane("intern", "workbench", nil, nil, 40, 10)
	rline := make([]byte, 200)
	for i := range rline {
		rline[i] = letters["remote"]
	}
	rp.emu.Write(rline)
	views = append(views, rp)

	names := []string{"a", "b", "c", "workbench:intern"}
	ls := DefaultLayoutState(names)
	tui := &TUI{screen: s, panes: views, layoutState: ls, lastW: 120, lastH: 40}
	tui.plan = computeLayout(ls, views, 120, 40)
	tui.layoutState.Overlay = false

	tui.layoutState.Focused = "a"
	tui.toggleFocusSplit() // a left, b/c/remote in a grid right
	for i := 0; i < 8; i++ {
		tui.render()
	}

	if n, sample := countScreenContaminationGeneric(tui, s, letters); n > 0 {
		t.Errorf("baseline (no hide yet) already contaminated: %d cells; %s", n, sample)
	}
	for _, p := range findRegionOverlapsAndWidthMismatches(tui) {
		t.Errorf("baseline region-vs-region problem: %s", p)
	}

	tui.toggleHidden("workbench:intern") // hide the REMOTE pane specifically
	for i := 0; i < 8; i++ {
		tui.render()
	}
	if n, sample := countScreenContaminationGeneric(tui, s, letters); n > 0 {
		t.Errorf("hiding the remote pane produced cross-region contamination: %d cells; %s", n, sample)
	}
	for _, p := range findRegionOverlapsAndWidthMismatches(tui) {
		t.Errorf("hiding the remote pane: region-vs-region problem: %s", p)
	}

	tui.toggleHidden("workbench:intern") // re-show it
	tui.toggleHidden("b")                // now hide a LOCAL pane while remote is visible
	for i := 0; i < 8; i++ {
		tui.render()
	}
	if n, sample := countScreenContaminationGeneric(tui, s, letters); n > 0 {
		t.Errorf("hiding a local pane while a remote pane is visible produced cross-region contamination: %d cells; %s", n, sample)
	}
	for _, p := range findRegionOverlapsAndWidthMismatches(tui) {
		t.Errorf("hiding a local pane while remote visible: region-vs-region problem: %s", p)
	}
}

// TestFocusSplit_HideMatchesLiveFleetShape reproduces this project's own
// ACTUAL live .initech/layout.yaml shape as closely as possible: 23 total
// panes (super, pmm, shipper, growth, pm, eng1-6, qa1-12), mode: main
// (Layout2Col) already active, with growth/pmm/qa1-12/shipper already hidden
// -- exactly the config found sitting in this fleet's own layout.yaml while
// investigating ini-mdj5. Only 8 panes are visible (super, pm, eng1-6), a
// materially different shape from every other test in this file, which use
// 3-8 panes with NONE hidden at construction time. Hides one more of the
// visible panes (matching the operator's literal next action from that
// state) and checks for cross-region contamination.
func TestFocusSplit_HideMatchesLiveFleetShape(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)

	allNames := []string{
		"super", "pmm", "shipper", "growth", "pm",
		"eng1", "eng2", "eng3", "eng4", "eng5", "eng6",
		"qa1", "qa2", "qa3", "qa4", "qa5", "qa6", "qa7", "qa8", "qa9", "qa10", "qa11", "qa12",
	}
	alreadyHidden := map[string]bool{
		"growth": true, "pmm": true, "shipper": true,
		"qa1": true, "qa2": true, "qa3": true, "qa4": true, "qa5": true, "qa6": true,
		"qa7": true, "qa8": true, "qa9": true, "qa10": true, "qa11": true, "qa12": true,
	}
	// Deliberately NOT digits and NOT letters that appear in any pane's own
	// name: the ribbon title format is " {1-based index in the FULL,
	// including-hidden, pane list} {name} " (pane_render.go), so a digit
	// letter here can alias with another pane's own index number in its
	// ribbon (e.g. "pm" is the 5th pane overall, so its ribbon literally
	// contains the digit '5' — colliding with an eng5 marker of '5' and
	// producing a false-positive "eng5 leaked into pm's region" that is
	// really just pm's own chrome).
	visibleLetters := map[string]byte{
		"super": 'U', "pm": 'M', "eng1": 'A', "eng2": 'B', "eng3": 'C',
		"eng4": 'D', "eng5": 'E', "eng6": 'F',
	}

	views := make([]PaneView, len(allNames))
	for i, n := range allNames {
		lp := &Pane{name: n, emu: vt.NewSafeEmulator(40, 10), alive: true, visible: !alreadyHidden[n]}
		if letter, ok := visibleLetters[n]; ok {
			line := make([]byte, 200)
			for j := range line {
				line[j] = letter
			}
			lp.emu.Write(line)
		}
		views[i] = lp
	}

	ls := DefaultLayoutState(allNames)
	ls.Hidden = make(map[string]bool, len(alreadyHidden))
	for n := range alreadyHidden {
		ls.Hidden[n] = true
	}
	ls.Mode = Layout2Col
	ls.Focused = "super"
	ls.Overlay = false

	tui := &TUI{screen: s, panes: views, layoutState: ls, lastW: 120, lastH: 40}
	tui.applyLayout() // syncs each pane's own .region/emulator size to the plan, not just tui.plan itself
	for i := 0; i < 8; i++ {
		tui.render()
	}

	runeLetters := make(map[string]byte, len(visibleLetters))
	for k, v := range visibleLetters {
		runeLetters[k] = v
	}
	if n, sample := countScreenContaminationGeneric(tui, s, runeLetters); n > 0 {
		t.Errorf("baseline (matching live layout.yaml, before any new hide) already contaminated: %d cells; %s", n, sample)
	}
	for _, p := range findRegionOverlapsAndWidthMismatches(tui) {
		t.Errorf("baseline region-vs-region problem: %s", p)
	}

	tui.toggleHidden("eng3") // hide one more visible pane, as the operator's likely next action
	for i := 0; i < 8; i++ {
		tui.render()
	}
	if n, sample := countScreenContaminationGeneric(tui, s, runeLetters); n > 0 {
		t.Errorf("hiding eng3 from the live-fleet-shaped layout produced cross-region contamination: %d cells; %s", n, sample)
	}
	for _, p := range findRegionOverlapsAndWidthMismatches(tui) {
		t.Errorf("hiding eng3: region-vs-region problem: %s", p)
	}

	// And hiding the FOCUSED pane ("super") from this exact shape, since that
	// is the one case the bead explicitly says to test rather than trust.
	tui.toggleHidden("eng3") // re-show
	tui.toggleHidden("super")
	for i := 0; i < 8; i++ {
		tui.render()
	}
	if n, sample := countScreenContaminationGeneric(tui, s, runeLetters); n > 0 {
		t.Errorf("hiding the focused pane (super) from the live-fleet-shaped layout produced cross-region contamination: %d cells; %s", n, sample)
	}
	for _, p := range findRegionOverlapsAndWidthMismatches(tui) {
		t.Errorf("hiding focused pane super: region-vs-region problem: %s", p)
	}
}

// TestFocusSplit_ContaminationDetectorFiresOnRealOverlap is a POSITIVE
// CONTROL, run before trusting any of the "zero contamination" results
// elsewhere in this file. Every one of those results is an absence claim,
// and an absence claim is only as good as the detector that produced it —
// this proves the detector actually fires when the exact defect described in
// ini-mdj5 is real, instead of only ever proving code paths that happen not
// to trigger it. It deliberately corrupts a real, fully-rendered Layout2Col
// screen by shifting one pane's OWN region (the same lp.region field
// applyLayout writes) to overlap its neighbor's -- "two regions were assigned
// overlapping horizontal space", the second mechanism the bug report names --
// through the REAL tui.render() / Pane.Render() / renderCells() pipeline, not
// a synthetic screen.SetContent poke. Checked against BOTH contamination
// helpers used elsewhere in this investigation: the original byte-truncating
// checkScreenContamination (used by HideRenderedScreen/HideSteadyState/
// OddVsEvenRightCount) and the rune-keyed countScreenContaminationGeneric
// (used by the remote-pane and live-fleet-shape tests) -- so a clean result
// here validates every prior negative in this file, not just the newest one.
func TestFocusSplit_ContaminationDetectorFiresOnRealOverlap(t *testing.T) {
	tui, screen := newTestTUIWithScreen("x", "y")
	tui.layoutState.Overlay = false

	letters := map[string]byte{"x": 'X', "y": 'Y'}
	for _, pv := range tui.panes {
		lp := pv.(*Pane)
		line := make([]byte, 200)
		for i := range line {
			line[i] = letters[lp.name]
		}
		lp.emu.Write(line)
	}

	tui.layoutState.Focused = "x"
	tui.toggleFocusSplit() // x left (40%), y right (60%, single cell)
	tui.render()

	var xp, yp *Pane
	for _, pv := range tui.panes {
		lp := pv.(*Pane)
		if lp.name == "x" {
			xp = lp
		} else {
			yp = lp
		}
	}
	if xp == nil || yp == nil {
		t.Fatal("expected both panes x and y in tui.panes")
	}
	xRegion := xp.GetRegion()
	yRegion := yp.GetRegion()
	if yRegion.X <= xRegion.X {
		t.Fatalf("test setup assumption violated: expected y's region (X=%d) to start right of x's (X=%d)", yRegion.X, xRegion.X)
	}

	// Corrupt y's OWN region field directly -- the same field applyLayout
	// writes via `lp.region = pr.Region` -- shifting it 10 columns left so it
	// overlaps x's region by 10 columns, without touching x at all. This is
	// exactly "two regions were assigned overlapping horizontal space" from
	// the bug report, constructed directly rather than hoped-for from some
	// input combination.
	overlapAmount := 10
	yp.region.X -= overlapAmount
	tui.render() // real Pane.Render() / renderCells() draws y's real content into the corrupted, overlapping region

	// All three checks below are T-free (return values, no Errorf side
	// effect), so this test's own pass/fail state reflects MY assertions
	// about what each detector should report — not whatever the detector
	// happens to do internally.
	byteCount, byteSample := countScreenContamination(tui, screen, letters)
	genericCount, genericSample := countScreenContaminationGeneric(tui, screen, letters)
	problems := findRegionOverlapsAndWidthMismatches(tui)

	t.Logf("byte-based content check: %d cells (sample=%q); rune-based content check: %d cells (sample=%q); region-vs-region check: %d problems",
		byteCount, byteSample, genericCount, genericSample, len(problems))

	// THE REGION-VS-REGION CHECK must fire: it compares every pane's assigned
	// region directly against every other's (regionsOverlap), independent of
	// what either pane actually draws. This is the check ini-mdj5's earlier
	// metadata-level tests (HideNonFocusedPane, HideFocusedPane,
	// HideThenReshow, HideAcrossShapeChanges, HideMultipleAtOnce) rely on.
	if len(problems) == 0 {
		t.Fatal("findRegionOverlapsAndWidthMismatches reported zero overlap on a deliberately corrupted, overlapping layout — the region-vs-region detector cannot see the defect this investigation is trying to rule out")
	}
	for _, p := range problems {
		t.Logf("region-vs-region detector correctly found: %s", p)
	}

	// THE CONTENT-BASED CHECKS are expected to report ZERO here — and that is
	// the finding, not a passing grade for those checks. A pane's content is
	// drawn through a screen clamped to that pane's OWN region (see
	// renderCells/clampedScreen), so "does X's content stay inside X's own
	// CURRENTLY-ASSIGNED region" is true BY CONSTRUCTION whenever that region
	// itself is the thing that's wrong. Corrupting a pane's own region to
	// overlap a neighbor — exactly what "two regions were assigned
	// overlapping horizontal space" describes — is invisible to a
	// self-referential content check no matter how thoroughly it is swept.
	// Every content-based "zero contamination" result elsewhere in this file
	// (HideRenderedScreen, HideSteadyState, OddVsEvenRightCount, real-PTY,
	// remote-pane-mixed, live-fleet-shape) is therefore NOT evidence against
	// this mechanism specifically — only the region-vs-region check
	// (confirmed firing above) is. If a content check somehow DID fire here,
	// that would mean this test's corruption produced some OTHER,
	// unanticipated visible effect worth investigating on its own.
	if byteCount != 0 || genericCount != 0 {
		t.Logf("content-based checks unexpectedly fired (byte=%d, generic=%d) on a self-region-overlap corruption — worth investigating what else changed, though this is not itself a failure of this test", byteCount, genericCount)
	}
}

// TestFocusSplit_HideAtFleetScale is super's follow-up hypothesis: Nelson's
// real fleet has 23 agents, and pm (5th of 23) is what produced the
// ribbon-index false positive in the live-fleet-shape test. Every prior hide
// test in this file uses rightCount 1-7 (small enough that autoGrid still
// picks cols<=4 with only 1-2 rows). Past rightCount=12, autoGrid's default
// branch pins cols=4 forever and grows ROWS instead — at rightCount 20-25
// that means 5-7 rows packed into the same screen height, so row height
// (and TerminalSize's H-2 subtraction) gets small enough that clamping
// (`if rows < 1 { rows = 1 }`) can plausibly kick in where it never does at
// small scale. gridRegions/distributeWeighted are proven exact for ANY
// input, so this cannot produce a bad accounting inside gridRegions itself
// -- but applyLayout's resize DECISION (comparing old vs new Region, then
// resizing the emulator to TerminalSize()) is a separate piece of code the
// math proof does not cover, and is exactly where a pane's ASSIGNED region
// could drift from its RESIZED emulator size. This uses the NOW-VALIDATED
// region-vs-region check, not the content-based one already proven blind to
// this failure mode, and specifically drives a HIDE from each starting
// count (a fresh static layout at N-1 already passed in OddVsEvenRightCount;
// the operator's break is on the hide TRANSITION, not the static state).
func TestFocusSplit_HideAtFleetScale(t *testing.T) {
	for rightCount := 15; rightCount <= 25; rightCount++ {
		rightCount := rightCount
		t.Run(fmt.Sprintf("rightCount=%d", rightCount), func(t *testing.T) {
			names := make([]string, rightCount+1)
			for i := range names {
				names[i] = fmt.Sprintf("p%02d", i) // p00 = focus, p01..pN = right grid
			}

			s := tcell.NewSimulationScreen("")
			s.Init()
			s.SetSize(120, 40)

			views := make([]PaneView, len(names))
			for i, n := range names {
				views[i] = &Pane{name: n, emu: vt.NewSafeEmulator(40, 10), alive: true, visible: true}
			}

			ls := DefaultLayoutState(names)
			ls.Mode = Layout2Col
			ls.Focused = names[0]
			ls.Overlay = false
			tui := &TUI{screen: s, panes: views, layoutState: ls, lastW: 120, lastH: 40}
			tui.applyLayout()
			for i := 0; i < 8; i++ {
				tui.render()
			}

			if problems := findRegionOverlapsAndWidthMismatches(tui); len(problems) > 0 {
				for _, p := range problems {
					t.Errorf("baseline (fresh, %d panes, no hide yet): %s", rightCount+1, p)
				}
			}

			// Hide at three different positions in the visible list, each
			// from a fresh copy of this same starting layout: the FIRST
			// non-focused pane (names[1]), a MIDDLE one, and the LAST one --
			// a positional-index shift in the visible slice affects everyone
			// after the hidden pane's old position, so where in the list the
			// hide happens is itself a variable, not just how many remain.
			hidePositions := map[string]string{
				"first":  names[1],
				"middle": names[1+len(names)/2],
				"last":   names[len(names)-1],
			}
			for label, target := range hidePositions {
				tui.toggleHidden(target)
				for i := 0; i < 8; i++ {
					tui.render()
				}
				if problems := findRegionOverlapsAndWidthMismatches(tui); len(problems) > 0 {
					for _, p := range problems {
						t.Errorf("after hiding %s (%s) from %d panes: %s", label, target, rightCount+1, p)
					}
				}
				tui.toggleHidden(target) // re-show before testing the next position
				for i := 0; i < 8; i++ {
					tui.render()
				}
			}
		})
	}
}

// TestFocusSplit_HideAcrossTerminalWidths is super's corrected version of the
// scale hypothesis: autoGrid caps columns at 4 regardless of pane count, so
// column width is driven by TERMINAL WIDTH, not pane count. The right region
// is 60% of the screen divided across up to 4 columns; on an 80-column
// terminal that is ~48 columns split 4 ways, roughly 12 characters per cell
// -- a real sliver, reachable with an ordinary pane count. Every other test
// in this file uses a 120-wide screen; this is the first to vary width.
// Sweeps 40 through 160 (well past "normal" in both directions) with 8
// visible panes (rightCount=7, the shape most of this file's small-scale
// tests already used at 120-wide), hides one pane at each width (a genuine
// transition, not just a fresh static layout), and checks with the
// validated region-vs-region detector.
func TestFocusSplit_HideAcrossTerminalWidths(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for _, width := range []int{40, 50, 60, 70, 80, 90, 100, 110, 120, 140, 160} {
		width := width
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			s := tcell.NewSimulationScreen("")
			s.Init()
			s.SetSize(width, 40)

			views := make([]PaneView, len(names))
			for i, n := range names {
				views[i] = &Pane{name: n, emu: vt.NewSafeEmulator(40, 10), alive: true, visible: true}
			}

			ls := DefaultLayoutState(names)
			ls.Mode = Layout2Col
			ls.Focused = "a"
			ls.Overlay = false
			tui := &TUI{screen: s, panes: views, layoutState: ls, lastW: width, lastH: 40}
			tui.applyLayout()
			for i := 0; i < 8; i++ {
				tui.render()
			}

			if problems := findRegionOverlapsAndWidthMismatches(tui); len(problems) > 0 {
				for _, p := range problems {
					t.Errorf("baseline (width=%d, fresh, no hide yet): %s", width, p)
				}
			}

			tui.toggleHidden("d") // a middle, non-focused pane
			for i := 0; i < 8; i++ {
				tui.render()
			}
			if problems := findRegionOverlapsAndWidthMismatches(tui); len(problems) > 0 {
				for _, p := range problems {
					t.Errorf("after hiding d (width=%d): %s", width, p)
				}
			}

			// And the focused pane, at this same width -- the "handled for
			// free" claim, tested at narrow width specifically.
			tui.toggleHidden("d") // re-show
			tui.toggleHidden("a")
			for i := 0; i < 8; i++ {
				tui.render()
			}
			if problems := findRegionOverlapsAndWidthMismatches(tui); len(problems) > 0 {
				for _, p := range problems {
					t.Errorf("after hiding focused pane a (width=%d): %s", width, p)
				}
			}
		})
	}
}

// TestFocusSplit_HideWithDoubleWidthRunesNoCrossContamination is super's
// wide-rune hypothesis: every other test's content is ASCII, one cell per
// column, written via a single emu.Write() or a shell loop printing plain
// letters. Real Claude Code output contains box-drawing, spinners, emoji,
// and CJK — double-width runes that occupy TWO terminal columns each. If any
// width calculation on the write/wrap path (in the vt emulator, not
// necessarily in initech's own code) counts RUNES where it should count
// DISPLAY COLUMNS, content sized for N runes could occupy up to 2N columns
// and visibly spill past its region — the exact "wrapped continuation in the
// sliver beside it" signature — and every ASCII-based test in this file
// would be structurally blind to it, since for ASCII a rune count and a
// column count are the same number.
//
// Uses a REAL PTY process (python3, falls back to skipping if unavailable)
// printing genuine CJK characters repeatedly, so the emulator's own
// write/wrap path handles real double-width runes over real wrapping — not
// a single static write of already-correct-width content. Checked with BOTH
// the content-based check (the relevant one here: this hypothesis is about
// content overflowing a correctly-SIZED region, which
// findRegionOverlapsAndWidthMismatches's region-vs-region comparison would
// not directly see) and the region-vs-region check, for completeness.
func TestFocusSplit_HideWithDoubleWidthRunesNoCrossContamination(t *testing.T) {
	skipInCI(t)
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available to generate real double-width PTY output")
	}

	names := []string{"a", "b", "c", "d", "e"}
	wideChars := map[string]string{"a": "あ", "b": "国", "c": "漢", "d": "語", "e": "字"}
	markers := make(map[string]rune, len(wideChars))
	for n, wc := range wideChars {
		markers[n] = []rune(wc)[0]
	}

	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)

	views := make([]PaneView, len(names))
	for i, n := range names {
		// Print the double-width character on a real PTY continuously: a
		// long unbroken run forces genuine multi-row wrapping of wide runes
		// as they are actively written, not a single pre-sized emu.Write().
		script := fmt.Sprintf(`python3 -c "
import sys, time
while True:
    sys.stdout.write('%s' * 300)
    sys.stdout.flush()
    time.sleep(0.005)
"`, wideChars[n])
		cfg := PaneConfig{Name: n, Command: []string{"/bin/sh", "-c", script}}
		p, err := NewPane(cfg, 10, 40)
		if err != nil {
			t.Fatalf("NewPane(%s): %v", n, err)
		}
		p.Start()
		t.Cleanup(func() { p.Close() })
		views[i] = p
	}

	ls := DefaultLayoutState(names)
	ls.Mode = Layout2Col
	ls.Focused = "a"
	ls.Overlay = false
	tui := &TUI{screen: s, panes: views, layoutState: ls, lastW: 120, lastH: 40}
	tui.applyLayout()

	warmup := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(warmup) {
		tui.render()
		time.Sleep(10 * time.Millisecond)
	}

	tui.toggleHidden("c") // shape-changing hide, same transition as the ASCII real-PTY test

	maxContamination := 0
	var worstSample string
	var worstRegionProblems []string
	settleDeadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(settleDeadline) {
		tui.render()
		if n, sample := countScreenContaminationRune(tui, s, markers); n > maxContamination {
			maxContamination = n
			worstSample = sample
		}
		if problems := findRegionOverlapsAndWidthMismatches(tui); len(problems) > 0 {
			worstRegionProblems = problems
		}
		time.Sleep(10 * time.Millisecond)
	}

	if maxContamination > 0 {
		t.Errorf("double-width-rune hide transition produced cross-region contamination (worst frame: %d cells): %s", maxContamination, worstSample)
	} else {
		t.Logf("double-width-rune hide transition: zero contamination across the sampled window")
	}
	for _, p := range worstRegionProblems {
		t.Errorf("region-vs-region check: %s", p)
	}
}
