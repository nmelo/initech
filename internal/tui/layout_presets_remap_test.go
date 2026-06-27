package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestDefaultLayoutPresets_PanelCountConsistent pins the panel-count-consistent
// default mapping (ini-dxjk): Option+N draws N panels for N=1..4, denser grids on
// 5-6, and live on 7. This is the contract the off-by-one remap establishes.
func TestDefaultLayoutPresets_PanelCountConsistent(t *testing.T) {
	d := defaultLayoutPresets()

	type want struct {
		kind       presetKind
		cols, rows int // meaningful only for grid presets
		spec       string
		panels     int // expected cell count for grid presets
	}
	cases := []want{
		{presetFocus, 0, 0, "focus", 1}, // Option+1: single focused pane
		{presetGrid, 2, 1, "2x1", 2},    // Option+2
		{presetGrid, 3, 1, "3x1", 3},    // Option+3
		{presetGrid, 4, 1, "4x1", 4},    // Option+4
		{presetGrid, 3, 2, "3x2", 6},    // Option+5
		{presetGrid, 4, 2, "4x2", 8},    // Option+6
		{presetLive, 0, 0, "live", 0},   // Option+7: auto-grid
	}

	if len(d) != len(cases) {
		t.Fatalf("defaultLayoutPresets() has %d slots, want %d (presetSlots must be 7)", len(d), len(cases))
	}
	for i, w := range cases {
		p := d[i]
		if p.Kind != w.kind || p.Spec != w.spec {
			t.Errorf("slot %d (Option+%d) = {Kind:%v Spec:%q}, want {Kind:%v Spec:%q}", i, i+1, p.Kind, p.Spec, w.kind, w.spec)
			continue
		}
		if w.kind == presetGrid {
			if p.Cols != w.cols || p.Rows != w.rows {
				t.Errorf("slot %d (Option+%d) dims = %dx%d, want %dx%d", i, i+1, p.Cols, p.Rows, w.cols, w.rows)
			}
			if got := p.Cols * p.Rows; got != w.panels {
				t.Errorf("slot %d (Option+%d) panel count = %d, want %d", i, i+1, got, w.panels)
			}
		}
	}
}

// TestHandleKeyAltDigit_OutOfRangeNoOp verifies Alt+digits outside the 1-7 slot
// range (0, 8, 9) never fire a layout preset — the digit switch only matches
// '1'..'7', so an out-of-range digit leaves the layout untouched (AC verify #6).
func TestHandleKeyAltDigit_OutOfRangeNoOp(t *testing.T) {
	for _, r := range []rune{'0', '8', '9'} {
		tui, _ := newTestTUIWithScreen("a", "b", "c")
		tui.layoutPresets = defaultLayoutPresets()
		before := tui.layoutState

		tui.handleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModAlt))

		ls := tui.layoutState
		if ls.Mode != before.Mode || ls.GridCols != before.GridCols || ls.GridRows != before.GridRows {
			t.Errorf("Alt+%c mutated layout (mode=%v %dx%d), want no-op", r, ls.Mode, ls.GridCols, ls.GridRows)
		}
	}
}
