package tui

import (
	"testing"

	"github.com/charmbracelet/x/vt"
)

// testPane creates a minimal Pane for layout testing (no PTY or process).
func testPane(name string) *Pane {
	return &Pane{
		name:    name,
		emu:     vt.NewSafeEmulator(10, 5),
		alive:   true,
		visible: true,
	}
}

func testPanes(names ...string) []*Pane {
	panes := make([]*Pane, len(names))
	for i, n := range names {
		panes[i] = testPane(n)
	}
	return panes
}

// --- computeLayout tests ---

func TestComputeLayoutEmptyPanes(t *testing.T) {
	state := DefaultLayoutState(nil)
	plan := computeLayout(state, nil, 200, 60)
	if len(plan.Panes) != 0 {
		t.Errorf("empty panes: got %d plan entries, want 0", len(plan.Panes))
	}
}

func TestComputeLayoutSinglePane(t *testing.T) {
	panes := testPanes("super")
	state := DefaultLayoutState([]string{"super"})
	plan := computeLayout(state, panes, 200, 60)

	if len(plan.Panes) != 1 {
		t.Fatalf("single pane: got %d plan entries, want 1", len(plan.Panes))
	}
	pr := plan.Panes[0]
	if pr.Pane.name != "super" {
		t.Errorf("pane name = %q, want super", pr.Pane.name)
	}
	if !pr.Focused {
		t.Error("single pane should be focused")
	}
	if pr.Region.W != 200 || pr.Region.H != 60 {
		t.Errorf("region = %dx%d, want 200x60", pr.Region.W, pr.Region.H)
	}
}

func TestComputeLayoutGridMode(t *testing.T) {
	panes := testPanes("a", "b", "c", "d")
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 2,
		GridRows: 2,
		Focused:  "a",
		Hidden:   map[string]bool{},
		Overlay:  true,
	}
	plan := computeLayout(state, panes, 200, 60)

	if len(plan.Panes) != 4 {
		t.Fatalf("4 panes in 2x2: got %d plan entries, want 4", len(plan.Panes))
	}
	// Each pane should be ~100x30.
	for i, pr := range plan.Panes {
		if pr.Region.W < 99 || pr.Region.W > 101 {
			t.Errorf("pane %d width = %d, want ~100", i, pr.Region.W)
		}
		if pr.Region.H < 29 || pr.Region.H > 31 {
			t.Errorf("pane %d height = %d, want ~30", i, pr.Region.H)
		}
	}
	// Only "a" should be focused.
	for _, pr := range plan.Panes {
		if pr.Pane.name == "a" && !pr.Focused {
			t.Error("pane a should be focused")
		}
		if pr.Pane.name != "a" && pr.Focused {
			t.Errorf("pane %s should not be focused", pr.Pane.name)
		}
	}
}

func TestComputeLayoutHiddenPanesExcluded(t *testing.T) {
	panes := testPanes("a", "b", "c")
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 2,
		GridRows: 2,
		Focused:  "a",
		Hidden:   map[string]bool{"b": true},
		Overlay:  true,
	}
	plan := computeLayout(state, panes, 200, 60)

	if len(plan.Panes) != 2 {
		t.Fatalf("2 visible panes: got %d plan entries, want 2", len(plan.Panes))
	}
	for _, pr := range plan.Panes {
		if pr.Pane.name == "b" {
			t.Error("hidden pane b should not be in the plan")
		}
	}
}

func TestComputeLayoutFocusOnHiddenSnaps(t *testing.T) {
	panes := testPanes("a", "b", "c")
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 2,
		GridRows: 2,
		Focused:  "b", // b is hidden
		Hidden:   map[string]bool{"b": true},
		Overlay:  true,
	}
	plan := computeLayout(state, panes, 200, 60)

	if plan.ValidatedFocus == "b" {
		t.Error("focus should snap away from hidden pane b")
	}
	if plan.ValidatedFocus != "a" {
		t.Errorf("focus should snap to first visible pane a, got %q", plan.ValidatedFocus)
	}
	// The focused pane in the plan should match ValidatedFocus.
	for _, pr := range plan.Panes {
		if pr.Focused && pr.Pane.name != plan.ValidatedFocus {
			t.Errorf("plan says %q is focused but ValidatedFocus is %q", pr.Pane.name, plan.ValidatedFocus)
		}
	}
}

func TestComputeLayoutFocusMode(t *testing.T) {
	panes := testPanes("a", "b", "c")
	state := LayoutState{
		Mode:    LayoutFocus,
		Focused: "b",
		Hidden:  map[string]bool{},
	}
	plan := computeLayout(state, panes, 200, 60)

	if len(plan.Panes) != 1 {
		t.Fatalf("focus mode: got %d plan entries, want 1", len(plan.Panes))
	}
	if plan.Panes[0].Pane.name != "b" {
		t.Errorf("focus mode: got pane %q, want b", plan.Panes[0].Pane.name)
	}
	if plan.Panes[0].Region.W != 200 || plan.Panes[0].Region.H != 60 {
		t.Errorf("focus mode: region = %dx%d, want 200x60",
			plan.Panes[0].Region.W, plan.Panes[0].Region.H)
	}
}

func TestComputeLayoutZoomOverridesGrid(t *testing.T) {
	panes := testPanes("a", "b", "c", "d")
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 2,
		GridRows: 2,
		Zoomed:   true,
		Focused:  "c",
		Hidden:   map[string]bool{},
	}
	plan := computeLayout(state, panes, 200, 60)

	if len(plan.Panes) != 1 {
		t.Fatalf("zoomed: got %d plan entries, want 1", len(plan.Panes))
	}
	if plan.Panes[0].Pane.name != "c" {
		t.Errorf("zoomed: got pane %q, want c", plan.Panes[0].Pane.name)
	}
}

func TestComputeLayout2Col(t *testing.T) {
	panes := testPanes("a", "b", "c")
	state := LayoutState{
		Mode:    Layout2Col,
		Focused: "a",
		Hidden:  map[string]bool{},
	}
	plan := computeLayout(state, panes, 200, 60)

	if len(plan.Panes) != 3 {
		t.Fatalf("2col: got %d plan entries, want 3", len(plan.Panes))
	}
	// First pane (a) gets 60% width.
	if plan.Panes[0].Region.W != 120 {
		t.Errorf("2col main pane width = %d, want 120", plan.Panes[0].Region.W)
	}
}

func TestComputeLayoutLastRowExpands(t *testing.T) {
	panes := testPanes("a", "b", "c", "d", "e", "f", "g")
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 4,
		GridRows: 2,
		Focused:  "a",
		Hidden:   map[string]bool{},
	}
	plan := computeLayout(state, panes, 200, 60)

	if len(plan.Panes) != 7 {
		t.Fatalf("7 panes in 4x2: got %d plan entries, want 7", len(plan.Panes))
	}
	// First row: 4 panes at ~50 wide. Last row: 3 panes at ~66 wide.
	row1Width := plan.Panes[0].Region.W
	row2Width := plan.Panes[4].Region.W
	if row2Width <= row1Width {
		t.Errorf("last row panes should be wider: row1=%d, row2=%d", row1Width, row2Width)
	}
}

func TestComputeLayoutDividers(t *testing.T) {
	panes := testPanes("a", "b", "c", "d")
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 2,
		GridRows: 2,
		Focused:  "a",
		Hidden:   map[string]bool{},
	}
	plan := computeLayout(state, panes, 200, 60)

	if len(plan.Dividers) == 0 {
		t.Fatal("2x2 grid should have dividers")
	}
	// Should have vertical dividers between columns.
	for _, d := range plan.Dividers {
		if !d.Vertical {
			t.Error("expected only vertical dividers")
		}
	}
}

func TestComputeLayoutAllHidden(t *testing.T) {
	panes := testPanes("a", "b")
	state := LayoutState{
		Mode:    LayoutGrid,
		Focused: "a",
		Hidden:  map[string]bool{"a": true, "b": true},
	}
	plan := computeLayout(state, panes, 200, 60)

	if len(plan.Panes) != 0 {
		t.Errorf("all hidden: got %d plan entries, want 0", len(plan.Panes))
	}
}

func TestComputeLayoutDimmedFlag(t *testing.T) {
	panes := testPanes("a", "b", "c")
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 3,
		GridRows: 1,
		Focused:  "b",
		Hidden:   map[string]bool{},
	}
	plan := computeLayout(state, panes, 300, 60)

	for _, pr := range plan.Panes {
		if pr.Pane.name == "b" {
			if pr.Dimmed {
				t.Error("focused pane b should not be dimmed")
			}
		} else {
			if !pr.Dimmed {
				t.Errorf("unfocused pane %s should be dimmed", pr.Pane.name)
			}
		}
	}
}

// --- distributeWeighted tests ---

func TestDistributeWeightedUniform(t *testing.T) {
	sizes := distributeWeighted(200, 4, nil)
	if len(sizes) != 4 {
		t.Fatalf("got %d sizes, want 4", len(sizes))
	}
	total := 0
	for _, s := range sizes {
		total += s
	}
	if total != 200 {
		t.Errorf("total = %d, want 200", total)
	}
}

func TestDistributeWeightedProportional(t *testing.T) {
	sizes := distributeWeighted(200, 2, []int{60, 40})
	if len(sizes) != 2 {
		t.Fatalf("got %d sizes, want 2", len(sizes))
	}
	if sizes[0] != 120 || sizes[1] != 80 {
		t.Errorf("sizes = %v, want [120, 80]", sizes)
	}
}

func TestDistributeWeightedRemainder(t *testing.T) {
	// 201 / 3 = 67 each, but 67*3=201. With weights [1,1,1]:
	sizes := distributeWeighted(201, 3, []int{1, 1, 1})
	total := 0
	for _, s := range sizes {
		total += s
	}
	if total != 201 {
		t.Errorf("total = %d, want 201 (remainder must be distributed)", total)
	}
}

func TestDistributeWeightedWrongLength(t *testing.T) {
	// Weights length doesn't match n: falls back to uniform.
	sizes := distributeWeighted(200, 4, []int{1, 2})
	total := 0
	for _, s := range sizes {
		total += s
	}
	if total != 200 {
		t.Errorf("total = %d, want 200", total)
	}
}

// --- gridRegions tests ---

func TestGridRegionsWeightedColumns(t *testing.T) {
	regions := gridRegions(2, 1, 2, 200, 60, []int{60, 40}, nil)
	if len(regions) != 2 {
		t.Fatalf("got %d regions, want 2", len(regions))
	}
	if regions[0].W != 120 || regions[1].W != 80 {
		t.Errorf("widths = [%d, %d], want [120, 80]", regions[0].W, regions[1].W)
	}
}

func TestGridRegionsWeightedRows(t *testing.T) {
	regions := gridRegions(1, 2, 2, 200, 60, nil, []int{2, 1})
	if len(regions) != 2 {
		t.Fatalf("got %d regions, want 2", len(regions))
	}
	if regions[0].H != 40 || regions[1].H != 20 {
		t.Errorf("heights = [%d, %d], want [40, 20]", regions[0].H, regions[1].H)
	}
}

func TestGridRegionsNilWeights(t *testing.T) {
	regions := gridRegions(2, 2, 4, 200, 60, nil, nil)
	if len(regions) != 4 {
		t.Fatalf("got %d regions, want 4", len(regions))
	}
	// Uniform: each ~100x30.
	for i, r := range regions {
		if r.W != 100 {
			t.Errorf("region %d width = %d, want 100", i, r.W)
		}
		if r.H != 30 {
			t.Errorf("region %d height = %d, want 30", i, r.H)
		}
	}
}

// --- DefaultLayoutState tests ---

func TestDefaultLayoutState(t *testing.T) {
	state := DefaultLayoutState([]string{"super", "eng1", "eng2", "qa1"})
	if state.Mode != LayoutGrid {
		t.Errorf("mode = %d, want LayoutGrid", state.Mode)
	}
	if state.GridCols != 2 || state.GridRows != 2 {
		t.Errorf("grid = %dx%d, want 2x2", state.GridCols, state.GridRows)
	}
	if state.Focused != "super" {
		t.Errorf("focused = %q, want super", state.Focused)
	}
	if len(state.Hidden) != 0 {
		t.Errorf("hidden = %v, want empty", state.Hidden)
	}
}
