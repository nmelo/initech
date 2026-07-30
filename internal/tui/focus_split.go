// focus_split.go implements Option+F: split the screen with the focused pane
// in Layout2Col's left slot and every other pane reflowed as a grid on the
// right. Modifies the existing Layout2Col mode in place rather than adding a
// parallel mode (ini-vtki) — entering/exiting just flips layoutState.Mode,
// the same way applyLayoutPreset's presetMain case already does.
package tui

// focusSplitSnapshot holds the layout state to restore when Option+F is
// pressed again to exit the split. Deliberately NOT part of LayoutState
// (which persists to layout.yaml — this is session-only) and deliberately
// NOT a shallow copy of it: LayoutState carries maps/slices (Hidden,
// Protected, Order, ColWeights...) that are reference types, so a shallow
// copy would alias them and any mutation during the split (e.g. hiding a
// pane) would silently leak into the "saved" snapshot. This struct holds
// only value-typed fields.
type focusSplitSnapshot struct {
	mode         LayoutMode
	gridCols     int
	gridRows     int
	gridExplicit bool
	zoomed       bool
	liveAuto     bool
	focused      string
}

// toggleFocusSplit implements Option+F.
//
// Single pane: a no-op. A blank 60% region with no explanation is worse than
// nothing happening (pm/super's explicit steer).
//
// Otherwise toggles Layout2Col on/off. Entering remembers the previous
// layout state, including which pane was focused, so a second press
// restores it exactly — even if a different pane was promoted during the
// split. Exiting when there is no remembered state (Layout2Col was entered
// some other way, e.g. :main or a preset) falls back to LayoutGrid,
// mirroring applyLayoutPreset's presetLive toggle-off-with-nothing-to-
// restore behavior.
//
// Entering from LayoutLive exits live mode without touching t.liveEngine —
// it stays dormant (the only tick/update logic gates on
// Mode == LayoutLive, in Run's render loop) and resumes correctly if the
// split is later toggled off back into live mode, with no re-init needed.
func (t *TUI) toggleFocusSplit() {
	if t.visibleCountFromState() <= 1 {
		return
	}

	if t.layoutState.Mode == Layout2Col {
		if t.focusSplitPrev == nil {
			t.layoutState.Mode = LayoutGrid
		} else {
			prev := t.focusSplitPrev
			t.layoutState.Mode = prev.mode
			t.layoutState.GridCols = prev.gridCols
			t.layoutState.GridRows = prev.gridRows
			t.layoutState.GridExplicit = prev.gridExplicit
			t.layoutState.Zoomed = prev.zoomed
			t.layoutState.LiveAuto = prev.liveAuto
			t.layoutState.Focused = prev.focused
			t.focusSplitPrev = nil
		}
	} else {
		t.focusSplitPrev = &focusSplitSnapshot{
			mode:         t.layoutState.Mode,
			gridCols:     t.layoutState.GridCols,
			gridRows:     t.layoutState.GridRows,
			gridExplicit: t.layoutState.GridExplicit,
			zoomed:       t.layoutState.Zoomed,
			liveAuto:     t.layoutState.LiveAuto,
			focused:      t.layoutState.Focused,
		}
		t.layoutState.Mode = Layout2Col
		t.layoutState.GridExplicit = false
		t.layoutState.Zoomed = false
	}
	t.applyLayout()
	t.saveLayoutIfConfigured()
}
