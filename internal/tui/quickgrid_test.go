// quickgrid_test.go tests the Option+G / Option+L quick grid/live dimension
// popup (ini-dvy5). Digits are columns-then-rows, matching :grid/:live's own
// CxR convention exactly (Nelson reversed his original rows-first decision
// mid-build, so there is only one convention in the product now -- see the
// PLAN comment on ini-dvy5 for the full history).
package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func altKey(r rune) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModAlt)
}

func plainKey(r rune) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)
}

func TestQuickGrid_AltGOpensGridMode(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.handleKey(altKey('g'))
	if !tui.quickGrid.active {
		t.Fatal("Alt-g should open the quick grid popup")
	}
	if tui.quickGrid.live {
		t.Error("Alt-g should open in grid mode (live=false)")
	}
	if tui.quickGrid.firstDigit != 0 {
		t.Errorf("firstDigit = %d, want 0 on open", tui.quickGrid.firstDigit)
	}
}

func TestQuickGrid_AltLOpensLiveMode(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.handleKey(altKey('l'))
	if !tui.quickGrid.active {
		t.Fatal("Alt-l should open the quick grid popup")
	}
	if !tui.quickGrid.live {
		t.Error("Alt-l should open in live mode (live=true)")
	}
}

// TestQuickGrid_AsymmetricDigitsApplyCorrectOrder is the sharpest test in
// this file, required by both pm and super: a symmetric case like 2,2 would
// pass under either a straight-through or a swapped mapping and hide a bug.
// 3 then 2 must produce 3 COLUMNS and 2 ROWS -- straight passthrough into
// parseGrid's own CxR order, matching :grid 3x2 exactly. No swap anywhere.
func TestQuickGrid_AsymmetricDigitsApplyCorrectOrder(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d", "e", "f")
	tui.handleKey(altKey('g'))
	tui.handleKey(plainKey('3'))
	if tui.quickGrid.firstDigit != 3 {
		t.Fatalf("firstDigit after '3' = %d, want 3", tui.quickGrid.firstDigit)
	}
	if !tui.quickGrid.active {
		t.Fatal("popup should still be open after the first digit")
	}
	tui.handleKey(plainKey('2'))

	if tui.layoutState.GridCols != 3 || tui.layoutState.GridRows != 2 {
		t.Errorf("layout = %d cols x %d rows, want 3 cols x 2 rows (first digit typed must be COLUMNS)",
			tui.layoutState.GridCols, tui.layoutState.GridRows)
	}
}

func TestQuickGrid_SecondDigitAppliesImmediatelyAndCloses(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d")
	tui.handleKey(altKey('g'))
	tui.handleKey(plainKey('2'))
	tui.handleKey(plainKey('2'))

	if tui.quickGrid.active {
		t.Error("popup should close on the second digit, no Enter required")
	}
	if tui.quickGrid.firstDigit != 0 {
		t.Errorf("firstDigit = %d, want reset to 0 after submit", tui.quickGrid.firstDigit)
	}
	if tui.layoutState.Mode != LayoutGrid {
		t.Errorf("Mode = %v, want LayoutGrid", tui.layoutState.Mode)
	}
	if !tui.layoutState.GridExplicit {
		t.Error("GridExplicit should be true, matching :grid CxR's own behavior")
	}
}

func TestQuickGrid_LiveVariantAppliesViaCmdLive(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c")
	tui.handleKey(altKey('l'))
	tui.handleKey(plainKey('1'))
	tui.handleKey(plainKey('2'))

	if tui.layoutState.Mode != LayoutLive {
		t.Errorf("Mode = %v, want LayoutLive", tui.layoutState.Mode)
	}
	if tui.layoutState.LiveAuto {
		t.Error("LiveAuto should be false: explicit dimensions were given")
	}
	if tui.layoutState.GridCols != 1 || tui.layoutState.GridRows != 2 {
		t.Errorf("layout = %d cols x %d rows, want 1 col x 2 rows", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}
	if tui.quickGrid.active {
		t.Error("popup should close after the live variant submits")
	}
}

func TestQuickGrid_EscCancelsWithNoLayoutChange(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	beforeMode, beforeCols, beforeRows, beforeExplicit := tui.layoutState.Mode, tui.layoutState.GridCols, tui.layoutState.GridRows, tui.layoutState.GridExplicit

	tui.handleKey(altKey('g'))
	tui.handleKey(plainKey('4'))
	tui.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))

	if tui.quickGrid.active {
		t.Error("Esc should close the popup")
	}
	if tui.layoutState.Mode != beforeMode || tui.layoutState.GridCols != beforeCols ||
		tui.layoutState.GridRows != beforeRows || tui.layoutState.GridExplicit != beforeExplicit {
		t.Errorf("layout changed after Esc: Mode=%v Cols=%d Rows=%d Explicit=%v, want unchanged (Mode=%v Cols=%d Rows=%d Explicit=%v)",
			tui.layoutState.Mode, tui.layoutState.GridCols, tui.layoutState.GridRows, tui.layoutState.GridExplicit,
			beforeMode, beforeCols, beforeRows, beforeExplicit)
	}
}

func TestQuickGrid_ZeroIsIgnored(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.handleKey(altKey('g'))
	tui.handleKey(plainKey('0'))

	if !tui.quickGrid.active {
		t.Error("'0' should not close the popup")
	}
	if tui.quickGrid.firstDigit != 0 {
		t.Errorf("firstDigit = %d, want 0 ('0' is invalid and must be ignored, not accepted)", tui.quickGrid.firstDigit)
	}
}

func TestQuickGrid_NonDigitRuneIsIgnoredNotClosed(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.handleKey(altKey('g'))
	tui.handleKey(plainKey('x'))

	if !tui.quickGrid.active {
		t.Error("a non-digit keypress must be ignored, not close the popup (spec: 'Ignore, do not close, do not apply')")
	}
	if tui.quickGrid.firstDigit != 0 {
		t.Errorf("firstDigit = %d, want unchanged (0)", tui.quickGrid.firstDigit)
	}
}

func TestQuickGrid_BackspaceClearsFirstDigit(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.handleKey(altKey('g'))
	tui.handleKey(plainKey('7'))
	if tui.quickGrid.firstDigit != 7 {
		t.Fatalf("firstDigit = %d, want 7 before backspace", tui.quickGrid.firstDigit)
	}
	tui.handleKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))

	if !tui.quickGrid.active {
		t.Error("backspace should not close the popup")
	}
	if tui.quickGrid.firstDigit != 0 {
		t.Errorf("firstDigit = %d, want 0 after backspace", tui.quickGrid.firstDigit)
	}
}

func TestQuickGrid_AnotherModalChordClosesWithoutStacking(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.handleKey(altKey('g'))
	tui.handleKey(plainKey('2'))

	tui.handleKey(altKey('a')) // Alt-a normally opens the agents modal.

	if tui.quickGrid.active {
		t.Error("another modal chord should close the quick grid popup")
	}
	if tui.agents.active {
		t.Error("the same keystroke that closed the popup must not also open agents -- no modal stacking, matching every other modal's behavior in this codebase")
	}
}

// TestGridCommand_StillColumnsFirst pins that :grid's own CxR convention is
// completely unaffected by this feature -- the popup calls cmdGrid, it does
// not change it.
func TestGridCommand_StillColumnsFirst(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d", "e", "f")
	tui.cmdGrid([]string{"grid", "3x2"})
	if tui.layoutState.GridCols != 3 || tui.layoutState.GridRows != 2 {
		t.Errorf(":grid 3x2 = %d cols x %d rows, want 3 cols x 2 rows (unchanged by ini-dvy5)",
			tui.layoutState.GridCols, tui.layoutState.GridRows)
	}
}

// TestQuickGrid_RenderDoesNotPanic covers the AC "opening the popup while
// agents stream output does not disturb rendering" -- render() draws panes
// first, then the popup, every frame; this proves the popup's own draw path
// is safe to call inline with normal frame rendering, in both digit states.
func TestQuickGrid_RenderDoesNotPanic(t *testing.T) {
	tui, s := newTestTUIWithScreen("a", "b", "c")
	tui.handleKey(altKey('g'))

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("render panicked with no digits typed: %v", r)
			}
		}()
		tui.render()
	}()

	sw, sh := s.Size()
	found := false
	for y := 0; y < sh && !found; y++ {
		for x := 0; x < sw; x++ {
			ch, _, _ := s.Get(x, y)
			if ch == "G" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("expected the GRID title to appear somewhere on screen")
	}

	tui.handleKey(plainKey('4'))
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("render panicked with one digit typed: %v", r)
			}
		}()
		tui.render()
	}()
}

func TestQuickGrid_ExistingAltBindingsStillWork(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d")
	tui.layoutPresets = defaultLayoutPresets()

	tui.handleKey(altKey('z'))
	if !tui.layoutState.Zoomed {
		t.Error("Alt-z should still toggle zoom after adding g/l bindings")
	}
	tui.handleKey(altKey('z'))

	tui.handleKey(altKey('1'))
	if tui.layoutState.Mode != LayoutFocus {
		t.Error("Alt-1 should still apply the focus preset after adding g/l bindings")
	}
}
