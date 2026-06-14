package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestApplyLayoutPresetLive_GridSlot: a grid preset (CxR) entered via the live
// path becomes LayoutLive at those fixed dims with LiveAuto=false, and pins
// GridExplicit so recalcGrid won't auto-resize the live viewport on hot-add.
func TestApplyLayoutPresetLive_GridSlot(t *testing.T) {
	tui := testTUIWithPanes("a", "b", "c")
	tui.layoutPresets = defaultLayoutPresets() // slot index 2 = 4x1
	tui.applyLayoutPresetLive(2)
	ls := tui.layoutState
	if ls.Mode != LayoutLive {
		t.Errorf("mode = %v, want LayoutLive", ls.Mode)
	}
	if ls.LiveAuto {
		t.Error("grid preset live: LiveAuto must be false (fixed dims)")
	}
	if ls.GridCols != 4 || ls.GridRows != 1 {
		t.Errorf("dims = %dx%d, want 4x1", ls.GridCols, ls.GridRows)
	}
	if !ls.GridExplicit {
		t.Error("grid preset live must set GridExplicit (preserve fixed dims across hot-adds)")
	}
	if ls.Zoomed {
		t.Error("live preset must clear Zoomed")
	}
	if tui.liveEngine == nil {
		t.Error("live preset must initialize the live engine")
	}
}

// TestApplyLayoutPresetLive_KeywordSlot: a keyword preset (focus/live/main)
// entered via the live path becomes LayoutLive with LiveAuto=true (autoGrid dims).
func TestApplyLayoutPresetLive_KeywordSlot(t *testing.T) {
	tui := testTUIWithPanes("a", "b", "c")
	tui.layoutPresets = defaultLayoutPresets() // slot index 4 = live keyword
	tui.applyLayoutPresetLive(4)
	if tui.layoutState.Mode != LayoutLive {
		t.Errorf("mode = %v, want LayoutLive", tui.layoutState.Mode)
	}
	if !tui.layoutState.LiveAuto {
		t.Error("keyword preset live: LiveAuto must be true (auto dims)")
	}
}

// TestApplyLayoutPresetLive_FocusKeywordIsAuto pins verify-path #4: remapping a
// slot to the `focus` keyword makes Shift+Opt land in live AUTO-grid (a keyword
// never produces fixed dims on the live path).
func TestApplyLayoutPresetLive_FocusKeywordIsAuto(t *testing.T) {
	tui := testTUIWithPanes("a", "b")
	tui.layoutPresets = [5]LayoutPreset{0: {Kind: presetFocus, Spec: "focus"}}
	tui.applyLayoutPresetLive(0)
	if tui.layoutState.Mode != LayoutLive || !tui.layoutState.LiveAuto {
		t.Errorf("focus keyword live => mode=%v auto=%v, want LayoutLive + auto", tui.layoutState.Mode, tui.layoutState.LiveAuto)
	}
}

func TestApplyLayoutPresetLive_OutOfRangeLeavesLayoutUnchanged(t *testing.T) {
	tui := testTUIWithPanes("a")
	tui.layoutPresets = defaultLayoutPresets()
	before := tui.layoutState
	tui.applyLayoutPresetLive(-1)
	tui.applyLayoutPresetLive(5)
	if tui.layoutState.Mode != before.Mode || tui.layoutState.GridCols != before.GridCols {
		t.Errorf("out-of-range slot mutated layout: %+v", tui.layoutState)
	}
}

// TestShiftAltDigit_GridPreset: Shift+Alt+3 routes through the live path and
// applies the default slot-3 grid (4x1) in live mode at fixed dims.
func TestShiftAltDigit_GridPreset(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d")
	tui.layoutPresets = defaultLayoutPresets()

	ev := tcell.NewEventKey(tcell.KeyRune, '3', tcell.ModShift|tcell.ModAlt)
	tui.handleKey(ev)

	ls := tui.layoutState
	if ls.Mode != LayoutLive || ls.LiveAuto || ls.GridCols != 4 || ls.GridRows != 1 {
		t.Errorf("Shift+Alt+3 => mode=%v auto=%v %dx%d, want LayoutLive fixed 4x1", ls.Mode, ls.LiveAuto, ls.GridCols, ls.GridRows)
	}
}

// TestShiftAltDigit_KeywordPreset: Shift+Alt+5 (default `live` keyword) routes
// to live auto-grid.
func TestShiftAltDigit_KeywordPreset(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c")
	tui.layoutPresets = defaultLayoutPresets()

	ev := tcell.NewEventKey(tcell.KeyRune, '5', tcell.ModShift|tcell.ModAlt)
	tui.handleKey(ev)

	if tui.layoutState.Mode != LayoutLive || !tui.layoutState.LiveAuto {
		t.Errorf("Shift+Alt+5 => mode=%v auto=%v, want LayoutLive + auto", tui.layoutState.Mode, tui.layoutState.LiveAuto)
	}
}

// TestShiftAltDigit_FailSafe: a Shift+Alt rune outside the 1-5 slot range must
// NOT fire any layout preset (it falls through; never misfires as a preset).
// '9' is an out-of-range digit with no other Alt handler, so it's a clean no-op
// — the fail-safe guarantee from AC #7 (never trigger a preset on an
// unrecognized shifted key).
func TestShiftAltDigit_FailSafe(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c")
	tui.layoutPresets = defaultLayoutPresets()
	before := tui.layoutState.Mode

	ev := tcell.NewEventKey(tcell.KeyRune, '9', tcell.ModShift|tcell.ModAlt)
	tui.handleKey(ev)

	if tui.layoutState.Mode == LayoutLive {
		t.Error("Shift+Alt+9 fired a live preset; out-of-range shifted key must be a no-op")
	}
	if tui.layoutState.Mode != before {
		t.Errorf("Shift+Alt+9 changed mode %v -> %v, want unchanged", before, tui.layoutState.Mode)
	}
}

// TestShiftAltDigit_RegressionStaticUnchanged is the ini-lkww regression guard:
// plain Alt+1 (no Shift) must still apply the STATIC preset (LayoutGrid 2x1),
// never the live variant. Proves the Shift interceptor didn't capture the
// unshifted path.
func TestShiftAltDigit_RegressionStaticUnchanged(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d")
	tui.layoutPresets = defaultLayoutPresets()

	ev := tcell.NewEventKey(tcell.KeyRune, '1', tcell.ModAlt) // no Shift
	tui.handleKey(ev)

	ls := tui.layoutState
	if ls.Mode != LayoutGrid {
		t.Errorf("Alt+1 (no shift) => mode=%v, want static LayoutGrid (not live)", ls.Mode)
	}
	if ls.GridCols != 2 || ls.GridRows != 1 {
		t.Errorf("Alt+1 (no shift) => %dx%d, want static 2x1", ls.GridCols, ls.GridRows)
	}
	if ls.LiveAuto {
		t.Error("Alt+1 (no shift) must not enable LiveAuto")
	}
}
