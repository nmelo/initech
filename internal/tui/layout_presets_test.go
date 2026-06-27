package tui

import "testing"

func TestParseLayoutPreset_ValidGrid(t *testing.T) {
	p, ok := ParseLayoutPreset("4x1")
	if !ok {
		t.Fatalf("ParseLayoutPreset(%q) ok=false, want true", "4x1")
	}
	if p.Kind != presetGrid || p.Cols != 4 || p.Rows != 1 {
		t.Errorf("ParseLayoutPreset(%q) = %+v, want grid 4x1", "4x1", p)
	}
}

func TestParseLayoutPreset_Keywords(t *testing.T) {
	cases := map[string]presetKind{
		"focus": presetFocus,
		"live":  presetLive,
		"main":  presetMain,
	}
	for spec, want := range cases {
		p, ok := ParseLayoutPreset(spec)
		if !ok || p.Kind != want {
			t.Errorf("ParseLayoutPreset(%q) = %+v ok=%v, want kind %v", spec, p, ok, want)
		}
	}
}

// TestParseLayoutPreset_Grid1x1DistinctFromFocus pins the spec rule that
// "1x1" is a one-cell grid, NOT focus mode (which dims peers).
func TestParseLayoutPreset_Grid1x1DistinctFromFocus(t *testing.T) {
	p, ok := ParseLayoutPreset("1x1")
	if !ok || p.Kind != presetGrid || p.Cols != 1 || p.Rows != 1 {
		t.Errorf("ParseLayoutPreset(%q) = %+v ok=%v, want 1x1 grid (not focus)", "1x1", p, ok)
	}
}

// TestParseLayoutPreset_UppercaseSeparator accepts "3X2" (lenient, matching
// the existing :grid parser) since lowercasing can only widen, never break,
// the spec's lowercase grammar.
func TestParseLayoutPreset_UppercaseSeparator(t *testing.T) {
	p, ok := ParseLayoutPreset("3X2")
	if !ok || p.Kind != presetGrid || p.Cols != 3 || p.Rows != 2 {
		t.Errorf("ParseLayoutPreset(%q) = %+v ok=%v, want 3x2 grid", "3X2", p, ok)
	}
}

func TestParseLayoutPreset_Invalid(t *testing.T) {
	// "3" (bare column) is valid for :grid but INVALID for a preset: the spec
	// requires explicit CxR. 9x1 / 1x9 exceed the [1,8] cap.
	for _, spec := range []string{
		"0x0", "99x99", "3x", "x3", "banana", "", "3",
		"9x1", "1x9", "0x1", "1x0", "-1x1", "3x2x1", "2 x 1",
	} {
		if p, ok := ParseLayoutPreset(spec); ok {
			t.Errorf("ParseLayoutPreset(%q) = %+v ok=true, want invalid", spec, p)
		}
	}
}

func TestResolvePresets_NilUsesDefaults(t *testing.T) {
	presets, warnings := ResolvePresets(nil)
	if len(warnings) != 0 {
		t.Errorf("ResolvePresets(nil) warnings = %v, want none", warnings)
	}
	if presets != defaultLayoutPresets() {
		t.Errorf("ResolvePresets(nil) = %+v, want defaults %+v", presets, defaultLayoutPresets())
	}
}

// TestResolvePresets_Defaults pins the panel-count-consistent shipped defaults
// (ini-dxjk): focus / 2x1 / 3x1 / 4x1 / 3x2 / 4x2 / live.
func TestResolvePresets_Defaults(t *testing.T) {
	d := defaultLayoutPresets()
	want := [presetSlots]LayoutPreset{
		{Kind: presetFocus, Spec: "focus"},
		{Kind: presetGrid, Cols: 2, Rows: 1, Spec: "2x1"},
		{Kind: presetGrid, Cols: 3, Rows: 1, Spec: "3x1"},
		{Kind: presetGrid, Cols: 4, Rows: 1, Spec: "4x1"},
		{Kind: presetGrid, Cols: 3, Rows: 2, Spec: "3x2"},
		{Kind: presetGrid, Cols: 4, Rows: 2, Spec: "4x2"},
		{Kind: presetLive, Spec: "live"},
	}
	if d != want {
		t.Errorf("defaultLayoutPresets() = %+v, want %+v", d, want)
	}
}

func TestResolvePresets_PartialMap(t *testing.T) {
	presets, warnings := ResolvePresets(map[string]string{"1": "focus", "3": "2x2"})
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if presets[0].Kind != presetFocus {
		t.Errorf("slot 1 = %+v, want focus", presets[0])
	}
	if presets[2].Kind != presetGrid || presets[2].Cols != 2 || presets[2].Rows != 2 {
		t.Errorf("slot 3 = %+v, want grid 2x2", presets[2])
	}
	// Unspecified slots fall back to defaults.
	d := defaultLayoutPresets()
	if presets[1] != d[1] || presets[3] != d[3] || presets[4] != d[4] {
		t.Errorf("unspecified slots changed: got [2]=%+v [4]=%+v [5]=%+v", presets[1], presets[3], presets[4])
	}
}

func TestResolvePresets_InvalidValueFallsBackWithWarning(t *testing.T) {
	presets, warnings := ResolvePresets(map[string]string{"3": "banana"})
	d := defaultLayoutPresets()
	if presets[2] != d[2] {
		t.Errorf("invalid slot 3 = %+v, want default %+v", presets[2], d[2])
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want exactly 1", warnings)
	}
}

func TestResolvePresets_InvalidKeyIgnoredWithWarning(t *testing.T) {
	// "8" is out of range (valid slots are "1".."7"); "abc" is non-numeric.
	presets, warnings := ResolvePresets(map[string]string{"8": "2x2", "abc": "focus"})
	if presets != defaultLayoutPresets() {
		t.Errorf("invalid keys changed presets: %+v", presets)
	}
	if len(warnings) != 2 {
		t.Errorf("warnings = %v, want exactly 2", warnings)
	}
}

func TestApplyLayoutPreset_Grid(t *testing.T) {
	tui := testTUIWithPanes("a", "b", "c")
	tui.layoutPresets = defaultLayoutPresets() // slot index 2 = 3x1 (Option+3)
	tui.applyLayoutPreset(2)
	ls := tui.layoutState
	if ls.Mode != LayoutGrid || ls.GridCols != 3 || ls.GridRows != 1 {
		t.Errorf("applyLayoutPreset(2) => mode=%v cols=%d rows=%d, want grid 3x1", ls.Mode, ls.GridCols, ls.GridRows)
	}
	if !ls.GridExplicit {
		t.Error("applyLayoutPreset(grid) must set GridExplicit=true (so recalcGrid won't override)")
	}
	if ls.Zoomed {
		t.Error("applyLayoutPreset(grid) must clear Zoomed")
	}
}

func TestApplyLayoutPreset_Focus(t *testing.T) {
	tui := testTUIWithPanes("a", "b")
	tui.layoutPresets = [presetSlots]LayoutPreset{0: {Kind: presetFocus, Spec: "focus"}}
	tui.applyLayoutPreset(0)
	ls := tui.layoutState
	if ls.Mode != LayoutFocus || ls.GridExplicit {
		t.Errorf("applyLayoutPreset(focus) => mode=%v explicit=%v, want focus / not-explicit", ls.Mode, ls.GridExplicit)
	}
}

func TestApplyLayoutPreset_Main(t *testing.T) {
	tui := testTUIWithPanes("a", "b", "c")
	tui.layoutPresets = [presetSlots]LayoutPreset{0: {Kind: presetMain, Spec: "main"}}
	tui.applyLayoutPreset(0)
	if tui.layoutState.Mode != Layout2Col {
		t.Errorf("applyLayoutPreset(main) => mode=%v, want Layout2Col", tui.layoutState.Mode)
	}
}

// TestApplyLayoutPreset_LiveToggles preserves the existing Alt+5 contract:
// pressing the live slot enters live mode, pressing it again toggles back to grid.
func TestApplyLayoutPreset_LiveToggles(t *testing.T) {
	tui := testTUIWithPanes("a", "b", "c")
	tui.layoutPresets = [presetSlots]LayoutPreset{0: {Kind: presetLive, Spec: "live"}}

	tui.applyLayoutPreset(0)
	if tui.layoutState.Mode != LayoutLive {
		t.Fatalf("first press => mode=%v, want LayoutLive", tui.layoutState.Mode)
	}
	tui.applyLayoutPreset(0)
	if tui.layoutState.Mode != LayoutGrid {
		t.Errorf("second press => mode=%v, want toggle back to LayoutGrid", tui.layoutState.Mode)
	}
	if tui.liveEngine != nil {
		t.Error("toggling live off must release liveEngine")
	}
}

func TestApplyLayoutPreset_OutOfRangeNoChange(t *testing.T) {
	tui := testTUIWithPanes("a")
	tui.layoutPresets = defaultLayoutPresets()
	before := tui.layoutState
	tui.applyLayoutPreset(-1)
	tui.applyLayoutPreset(7) // slot 7 is out of range (valid slots are 0..6)
	if tui.layoutState.Mode != before.Mode || tui.layoutState.GridCols != before.GridCols {
		t.Errorf("out-of-range slot mutated layout: %+v", tui.layoutState)
	}
}

// TestApplyLayoutPreset_OverflowDoesNotHide is the load-bearing regression lock
// on the operator's exact-shape/hide-extras design: applying a preset whose grid
// is smaller than the visible agent count changes the GRID, never membership.
// Overflow agents MUST stay out of layoutState.Hidden and MUST remain listed in
// AllPanes (the source for the agents overlay and `initech status`).
func TestApplyLayoutPreset_OverflowDoesNotHide(t *testing.T) {
	tui := testTUIWithPanes("a", "b", "c", "d", "e", "f") // 6 visible
	tui.layoutPresets = [presetSlots]LayoutPreset{0: {Kind: presetGrid, Cols: 4, Rows: 1, Spec: "4x1"}}

	if len(tui.layoutState.Hidden) != 0 {
		t.Fatalf("precondition: Hidden = %v, want empty", tui.layoutState.Hidden)
	}

	tui.applyLayoutPreset(0) // 4x1 grid = 4 cells, 6 agents -> 2 overflow

	// Hidden must be byte-for-byte unchanged (still empty): no overflow agent
	// was hidden as a side effect of shrinking the grid.
	if len(tui.layoutState.Hidden) != 0 {
		t.Errorf("Hidden = %v after applying small grid, want empty (overflow != hidden)", tui.layoutState.Hidden)
	}

	// Only 4 panes get grid regions; the other 2 are off-grid.
	if len(tui.plan.Panes) != 4 {
		t.Errorf("plan has %d panes on grid, want 4 (4x1)", len(tui.plan.Panes))
	}

	// All 6 agents remain enumerable and Visible for overlay / status.
	panes, ok := tui.AllPanes()
	if !ok {
		t.Fatal("AllPanes() ok=false")
	}
	if len(panes) != 6 {
		t.Fatalf("AllPanes() returned %d panes, want all 6", len(panes))
	}
	for _, p := range panes {
		if !p.Visible {
			t.Errorf("agent %q reported Visible=false; overflow agents must stay visible in overlay/status", p.Name)
		}
	}
}

// TestApplyLayoutPreset_PreservesManualHide proves a preset switch leaves a
// user's deliberate hide intact — the apply path must not rebuild the Hidden set.
func TestApplyLayoutPreset_PreservesManualHide(t *testing.T) {
	tui := testTUIWithPanes("a", "b", "c")
	tui.layoutState.Hidden["b"] = true // user hid b
	tui.layoutPresets = [presetSlots]LayoutPreset{0: {Kind: presetGrid, Cols: 2, Rows: 2, Spec: "2x2"}}

	tui.applyLayoutPreset(0)

	if !tui.layoutState.Hidden["b"] {
		t.Error("manual hide of b was dropped by preset apply")
	}
	if len(tui.layoutState.Hidden) != 1 {
		t.Errorf("Hidden = %v, want exactly {b}", tui.layoutState.Hidden)
	}
}
