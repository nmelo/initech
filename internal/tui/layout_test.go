package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"
)

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
	if pr.Pane.Name() != "super" {
		t.Errorf("pane name = %q, want super", pr.Pane.Name())
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
		if pr.Pane.Name() == "a" && !pr.Focused {
			t.Error("pane a should be focused")
		}
		if pr.Pane.Name() != "a" && pr.Focused {
			t.Errorf("pane %s should not be focused", pr.Pane.Name())
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
		if pr.Pane.Name() == "b" {
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
		if pr.Focused && pr.Pane.Name() != plan.ValidatedFocus {
			t.Errorf("plan says %q is focused but ValidatedFocus is %q", pr.Pane.Name(), plan.ValidatedFocus)
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
	if plan.Panes[0].Pane.Name() != "b" {
		t.Errorf("focus mode: got pane %q, want b", plan.Panes[0].Pane.Name())
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
	if plan.Panes[0].Pane.Name() != "c" {
		t.Errorf("zoomed: got pane %q, want c", plan.Panes[0].Pane.Name())
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
	// Focused pane (a) gets the 40% left slot (ini-vtki: was 60%), minus one
	// reserved column for the divider between it and the right grid (ini-czi).
	if plan.Panes[0].Region.W != 79 {
		t.Errorf("2col main pane width = %d, want 79 (80 - 1 reserved divider column)", plan.Panes[0].Region.W)
	}
}

// TestComputeLayout2Col_FocusedPaneGetsLeftSlot is the ini-vtki regression
// test for a real gap found while building the focus-split feature: before
// this bead, Layout2Col zipped visible[i] with regions[i] positionally, so
// whichever pane was first in the input list got the big region regardless
// of which pane was actually focused. Promotion (Option+F's whole point)
// depends on the focused pane always landing in the big slot.
func TestComputeLayout2Col_FocusedPaneGetsLeftSlot(t *testing.T) {
	panes := testPanes("a", "b", "c")
	state := LayoutState{
		Mode:    Layout2Col,
		Focused: "b", // NOT first in the input list.
		Hidden:  map[string]bool{},
	}
	plan := computeLayout(state, panes, 200, 60)

	if len(plan.Panes) != 3 {
		t.Fatalf("2col: got %d plan entries, want 3", len(plan.Panes))
	}

	var leftPane string
	for _, pr := range plan.Panes {
		if pr.Region.X == 0 { // the big/left slot always starts at X=0.
			leftPane = pr.Pane.Name()
			if !pr.Focused {
				t.Errorf("pane %q occupies the left slot but Focused=false", leftPane)
			}
		}
	}
	if leftPane != "b" {
		t.Errorf("left slot occupied by %q, want the focused pane %q", leftPane, "b")
	}

	// The other two panes (a, c) must be in the right grid: neither at the
	// left slot's position, and neither marked Focused.
	for _, pr := range plan.Panes {
		if pr.Pane.Name() == "b" {
			continue
		}
		if pr.Region.X == 0 {
			t.Errorf("non-focused pane %q occupies the left slot", pr.Pane.Name())
		}
		if pr.Focused {
			t.Errorf("non-focused pane %q has Focused=true", pr.Pane.Name())
		}
	}
}

// TestComputeLayout2Col_RemovedFocusedPanePromotesFirstRemaining covers the
// bead's edge case: if the focused pane is removed while in the split,
// promote the first remaining pane rather than leaving the left half
// empty. This is the existing generic focus-validation-snap (step 2 of
// computeLayout) composing with ini-vtki's reorder — no new code needed
// beyond the reorder itself, verified end to end here.
func TestComputeLayout2Col_RemovedFocusedPanePromotesFirstRemaining(t *testing.T) {
	panes := testPanes("a", "c") // "b", the focused pane, is gone.
	state := LayoutState{
		Mode:    Layout2Col,
		Focused: "b",
		Hidden:  map[string]bool{},
	}
	plan := computeLayout(state, panes, 200, 60)

	if len(plan.Panes) != 2 {
		t.Fatalf("2col: got %d plan entries, want 2", len(plan.Panes))
	}
	for _, pr := range plan.Panes {
		if pr.Region.W == 80 { // the left slot
			if pr.Pane.Name() != "a" {
				t.Errorf("left slot occupied by %q, want promoted pane %q", pr.Pane.Name(), "a")
			}
			if !pr.Focused {
				t.Error("promoted pane in the left slot should be Focused=true")
			}
		}
	}
	if plan.ValidatedFocus != "a" {
		t.Errorf("ValidatedFocus = %q, want %q", plan.ValidatedFocus, "a")
	}
}

// TestComputeLayout2Col_AddingPaneReflowsRightGrid covers the bead's edge
// case: a pane added while in the split reflows the right grid for the new
// n-1, and the focused pane stays in the left slot throughout.
func TestComputeLayout2Col_AddingPaneReflowsRightGrid(t *testing.T) {
	state := LayoutState{Mode: Layout2Col, Focused: "a", Hidden: map[string]bool{}}

	before := computeLayout(state, testPanes("a", "b", "c"), 200, 100)
	if len(before.Panes) != 3 {
		t.Fatalf("before: got %d plan entries, want 3", len(before.Panes))
	}

	after := computeLayout(state, testPanes("a", "b", "c", "d"), 200, 100)
	if len(after.Panes) != 4 {
		t.Fatalf("after: got %d plan entries, want 4", len(after.Panes))
	}

	for _, plan := range []RenderPlan{before, after} {
		for _, pr := range plan.Panes {
			if pr.Pane.Name() == "a" {
				if pr.Region.W != 79 {
					t.Errorf("focused pane a width = %d, want 79 (80 left slot - 1 reserved divider column)", pr.Region.W)
				}
				if !pr.Focused {
					t.Error("focused pane a should have Focused=true")
				}
			}
		}
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
		if pr.Pane.Name() == "b" {
			if pr.Dimmed {
				t.Error("focused pane b should not be dimmed")
			}
		} else {
			if !pr.Dimmed {
				t.Errorf("unfocused pane %s should be dimmed", pr.Pane.Name())
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
	// Unweighted split would be [120, 80]; the non-last column (index 0)
	// gives up one column as the divider gutter to its right (ini-czi), so
	// it owns 119. The last column in the row is never shrunk.
	if regions[0].W != 119 || regions[1].W != 80 {
		t.Errorf("widths = [%d, %d], want [119, 80]", regions[0].W, regions[1].W)
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
	// Uniform: each ~100x30, but the first column of each row (indices 0, 2)
	// gives up one column as the divider gutter to its right (ini-czi); the
	// last column in each row (indices 1, 3) is never shrunk.
	wantW := []int{99, 100, 99, 100}
	for i, r := range regions {
		if r.W != wantW[i] {
			t.Errorf("region %d width = %d, want %d", i, r.W, wantW[i])
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

// --- dimStyle / dimColor tests ---

func TestDimColorDefault(t *testing.T) {
	c := dimColor(tcell.ColorDefault)
	if c == tcell.ColorDefault {
		t.Error("dimColor should not return default for default input")
	}
}

func TestDimColorReducesBrightness(t *testing.T) {
	bright := tcell.NewRGBColor(255, 255, 255)
	dim := dimColor(bright)
	r, g, b := dim.RGB()
	// 255 * 7/10 = 178
	if r != 178 || g != 178 || b != 178 {
		t.Errorf("dimColor(white) = (%d,%d,%d), want (178,178,178)", r, g, b)
	}
}

func TestDimStylePreservesBackground(t *testing.T) {
	s := tcell.StyleDefault.
		Foreground(tcell.NewRGBColor(200, 200, 200)).
		Background(tcell.NewRGBColor(50, 50, 50)).
		Bold(true)
	d := dimStyle(s)
	_, bg, attrs := d.Decompose()
	// Background should be unchanged.
	bgr, bgg, bgb := bg.RGB()
	if bgr != 50 || bgg != 50 || bgb != 50 {
		t.Errorf("dimStyle changed bg: (%d,%d,%d)", bgr, bgg, bgb)
	}
	// Bold should be preserved.
	if attrs&tcell.AttrBold == 0 {
		t.Error("dimStyle should preserve Bold attribute")
	}
}

// ── Layout Persistence Tests ────────────────────────────────────────

func TestSaveLoadLayout(t *testing.T) {
	root := t.TempDir()
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 3,
		GridRows: 2,
		Hidden:   map[string]bool{"arch": true, "sec": true},
		Focused:  "super",
		Overlay:  true,
	}

	if err := SaveLayout(root, state); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	got, ok := LoadLayout(root, []string{"super", "eng1", "arch", "sec"})
	if !ok {
		t.Fatal("LoadLayout returned false")
	}
	if got.GridCols != 3 || got.GridRows != 2 {
		t.Errorf("grid = %dx%d, want 3x2", got.GridCols, got.GridRows)
	}
	if got.Mode != LayoutGrid {
		t.Errorf("mode = %d, want LayoutGrid", got.Mode)
	}
	// Hidden is NOT part of a layout file any more (ini-9ka.10): it is a
	// session-global fact in fleet-state.yaml. Asserting its absence here is
	// the point -- this is the format change, not an omission.
	if len(got.Hidden) != 0 {
		t.Errorf("hidden = %v, want empty -- layout files carry arrangement only", got.Hidden)
	}
	// Focused pane is NOT persisted; should default to first pane name.
	if got.Focused != "super" {
		t.Errorf("focused = %q, want super (first pane)", got.Focused)
	}
}

func TestSaveLayoutCreatesDir(t *testing.T) {
	root := t.TempDir()
	state := LayoutState{Mode: LayoutGrid, GridCols: 2, GridRows: 1}

	if err := SaveLayout(root, state); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	info, err := os.Stat(filepath.Join(root, ".initech"))
	if err != nil {
		t.Fatalf(".initech dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error(".initech is not a directory")
	}
}

func TestSaveLayoutAtomicNoTempFile(t *testing.T) {
	root := t.TempDir()
	state := LayoutState{Mode: LayoutGrid, GridCols: 2, GridRows: 2}

	if err := SaveLayout(root, state); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	tmp := layoutPath(root) + ".tmp"
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("temp file should not exist after save")
	}
}

func TestLoadLayoutNoFile(t *testing.T) {
	root := t.TempDir()
	_, ok := LoadLayout(root, []string{"super"})
	if ok {
		t.Error("LoadLayout should return false when file doesn't exist")
	}
}

func TestLoadLayoutEmptyFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".initech")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "layout.yaml"), []byte(""), 0644)

	_, ok := LoadLayout(root, []string{"super"})
	if ok {
		t.Error("LoadLayout should return false for empty file")
	}
}

func TestLoadLayoutInvalidYAML(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".initech")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "layout.yaml"), []byte("grid: [unterminated"), 0644)

	_, ok := LoadLayout(root, []string{"super"})
	if ok {
		t.Error("LoadLayout should return false for invalid YAML")
	}
}

func TestLoadLayoutWhitespaceOnly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".initech")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "layout.yaml"), []byte("   \n\n  "), 0644)

	_, ok := LoadLayout(root, []string{"super"})
	if ok {
		t.Error("LoadLayout should return false for whitespace-only file")
	}
}

func TestLoadLayoutStaleReference(t *testing.T) {
	root := t.TempDir()
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 2,
		GridRows: 1,
		Hidden:   map[string]bool{"arch": true, "sec": true},
	}
	SaveLayout(root, state)

	// Load with only "super" and "eng1" -- arch and sec don't exist.
	got, ok := LoadLayout(root, []string{"super", "eng1"})
	if !ok {
		t.Fatal("LoadLayout should succeed with stale references")
	}
	if len(got.Hidden) != 0 {
		t.Errorf("stale refs should be dropped, got hidden = %v", got.Hidden)
	}
}

func TestLoadLayoutPreservesUnknownRemotePaneKeys(t *testing.T) {
	root := t.TempDir()
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 2,
		GridRows: 1,
		Order:    []string{"workbench:intern", "super"},
	}
	if err := SaveLayout(root, state); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	got, ok := LoadLayout(root, []string{"super", "eng1"})
	if !ok {
		t.Fatal("LoadLayout should preserve offline remote pane keys")
	}
	// Hidden/Protected for an offline remote are preserved by the GLOBAL
	// store now (ini-9ka.10), which keys on the same host:name pane-key space
	// and applies no known-pane filter at all -- so an offline peer's state
	// survives more robustly than it did here. Order remains a layout concern
	// and is still what this test guards.
	if len(got.Order) < 3 {
		t.Fatalf("order = %v, want preserved remote placeholder plus current panes", got.Order)
	}
	if got.Order[0] != "workbench:intern" || got.Order[1] != "super" || got.Order[2] != "eng1" {
		t.Fatalf("order = %v, want [workbench:intern super eng1]", got.Order)
	}
}

// TestLoadLayoutAllHiddenFallback guarded against a layout file that hid every
// pane. Since ini-9ka.10 a layout file cannot express that at all -- Hidden is
// session-global and lives in fleet-state.yaml -- so the guard has no input
// here and the nonsensical state is unrepresentable in this file rather than
// merely rejected. The legacy-tolerance half is what still matters and is what
// this now asserts: an OLD file carrying hidden: keys must still load (its
// hidden data is imported once, then ignored), not be rejected.
func TestLoadLayoutAllHiddenFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0700); err != nil {
		t.Fatal(err)
	}
	legacy := "grid: 2x1\nmode: grid\nhidden:\n    - super\n    - eng1\n"
	if err := os.WriteFile(layoutPath(root), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}

	got, ok := LoadLayout(root, []string{"super", "eng1"})
	if !ok {
		t.Fatal("a legacy layout file carrying hidden: keys must still load; its hidden data is dead but the arrangement is not")
	}
	if len(got.Hidden) != 0 {
		t.Errorf("hidden = %v, want empty -- legacy hidden keys are dead data after import", got.Hidden)
	}
	if got.GridCols != 2 || got.GridRows != 1 {
		t.Errorf("grid = %dx%d, want the legacy arrangement 2x1 to survive", got.GridCols, got.GridRows)
	}
}

func TestLoadLayoutGridTooSmall(t *testing.T) {
	root := t.TempDir()
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 1,
		GridRows: 1,
		Hidden:   map[string]bool{},
	}
	SaveLayout(root, state)

	got, ok := LoadLayout(root, []string{"a", "b", "c", "d"})
	if !ok {
		t.Fatal("LoadLayout should succeed with undersized grid")
	}
	// Grid should auto-recalculate since 1x1 < 4 panes.
	if got.GridCols*got.GridRows < 4 {
		t.Errorf("grid %dx%d cannot hold 4 panes", got.GridCols, got.GridRows)
	}
}

func TestDeleteLayout(t *testing.T) {
	root := t.TempDir()
	SaveLayout(root, LayoutState{Mode: LayoutGrid, GridCols: 2, GridRows: 2})

	if err := DeleteLayout(root); err != nil {
		t.Fatalf("DeleteLayout: %v", err)
	}
	_, ok := LoadLayout(root, []string{"super"})
	if ok {
		t.Error("layout should be gone after delete")
	}
}

func TestDeleteLayoutIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := DeleteLayout(root); err != nil {
		t.Fatalf("DeleteLayout on nonexistent should not error: %v", err)
	}
}

// ── Mode conversion tests ───────────────────────────────────────────

func TestLayoutModeToString(t *testing.T) {
	tests := []struct {
		mode LayoutMode
		want string
	}{
		{LayoutFocus, "focus"},
		{LayoutGrid, "grid"},
		{Layout2Col, "main"},
		{LayoutLive, "live"},
		{LayoutMode(99), "grid"},
	}
	for _, tt := range tests {
		if got := layoutModeToString(tt.mode); got != tt.want {
			t.Errorf("layoutModeToString(%d) = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestStringToLayoutMode(t *testing.T) {
	tests := []struct {
		s    string
		want LayoutMode
	}{
		{"focus", LayoutFocus},
		{"grid", LayoutGrid},
		{"main", Layout2Col},
		{"live", LayoutLive},
		{"unknown", LayoutGrid},
		{"", LayoutGrid},
	}
	for _, tt := range tests {
		if got := stringToLayoutMode(tt.s); got != tt.want {
			t.Errorf("stringToLayoutMode(%q) = %d, want %d", tt.s, got, tt.want)
		}
	}
}

// ── saveLayoutIfConfigured ──────────────────────────────────────────

func TestSaveLayoutIfConfiguredNoRoot(t *testing.T) {
	tui := newTestTUI(testPane("super"))
	tui.projectRoot = ""
	// Should be a no-op, not panic.
	tui.saveLayoutIfConfigured()
}

func TestSaveLayoutIfConfiguredWritesFile(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI(
		testPane("super"),
		hiddenTestPane("eng1"),
	)
	tui.projectRoot = root

	tui.saveLayoutIfConfigured()

	got, ok := LoadLayout(root, []string{"super", "eng1"})
	if !ok {
		t.Fatal("expected layout file to exist after save")
	}
	// The file exists and carries arrangement; hidden state is no longer part
	// of it (ini-9ka.10) and is asserted in the fleet-state tests instead.
	if got.Mode != LayoutGrid {
		t.Errorf("mode = %v, want the saved arrangement to round-trip", got.Mode)
	}
}

func TestHandlePeerUpdateRespectsSavedRemoteKeys(t *testing.T) {
	tui := newTestTUI(testPane("super"))
	tui.layoutState = LayoutState{
		Mode:     LayoutGrid,
		GridCols: 1,
		GridRows: 1,
		Focused:  "super",
		Hidden:   map[string]bool{"workbench:intern": true},
		Order:    []string{"workbench:intern", "super"},
		Protected: map[string]bool{},
	}

	rp := newFakeRemotePaneView("intern", "workbench")
	tui.handlePeerUpdate("workbench", []PaneView{rp}, true)

	if len(tui.panes) != 2 {
		t.Fatalf("panes = %d, want 2", len(tui.panes))
	}
	if paneKey(tui.panes[0]) != "workbench:intern" {
		t.Fatalf("remote pane should be reordered into saved position, got first=%q", paneKey(tui.panes[0]))
	}
	if !tui.layoutState.Hidden["workbench:intern"] {
		t.Fatalf("saved hidden remote key lost after peer update: %v", tui.layoutState.Hidden)
	}
	if rp.Visible() {
		t.Fatalf("remote pane should be marked hidden on reconnect")
	}
	if tui.visibleCountFromState() != 1 {
		t.Fatalf("visibleCountFromState = %d, want 1 with remote pane still hidden", tui.visibleCountFromState())
	}
	if len(tui.plan.Panes) != 1 || paneKey(tui.plan.Panes[0].Pane) != "super" {
		t.Fatalf("visible panes after peer update = %v, want only super visible", len(tui.plan.Panes))
	}
}

// ── Layout reset command ────────────────────────────────────────────

func TestLayoutResetCommand(t *testing.T) {
	root := t.TempDir()
	SaveLayout(root, LayoutState{
		Mode:     LayoutGrid,
		GridCols: 3,
		GridRows: 2,
		Hidden:   map[string]bool{"eng1": true},
	})

	tui := newTestTUI(
		testPane("super"),
		hiddenTestPane("eng1"),
		testPane("eng2"),
		testPane("qa1"),
	)
	tui.projectRoot = root

	tui.execCmd("layout reset")

	// File should be deleted.
	_, ok := LoadLayout(root, []string{"super", "eng1", "eng2", "qa1"})
	if ok {
		t.Error("layout.yaml should be deleted after layout reset")
	}

	// All panes should be visible (no hidden entries).
	if len(tui.layoutState.Hidden) != 0 {
		t.Errorf("hidden = %v, want empty after reset", tui.layoutState.Hidden)
	}

	// Grid should be auto-recalculated for 4 panes.
	expectedCols, expectedRows := autoGrid(4)
	if tui.layoutState.GridCols != expectedCols || tui.layoutState.GridRows != expectedRows {
		t.Errorf("grid = %dx%d, want %dx%d",
			tui.layoutState.GridCols, tui.layoutState.GridRows, expectedCols, expectedRows)
	}
}

func TestLayoutResetNoFile(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI(
		testPane("super"),
		testPane("eng1"),
	)
	tui.projectRoot = root

	tui.execCmd("layout reset")
	if tui.cmd.error != "" {
		t.Errorf("unexpected error: %q", tui.cmd.error)
	}
}

func TestLayoutUnknownSubcommand(t *testing.T) {
	tui := newTestTUI(testPane("super"))
	tui.execCmd("layout foo")
	if tui.cmd.error == "" {
		t.Error("expected error for unknown layout subcommand")
	}
}

func TestLayoutNoSubcommand(t *testing.T) {
	tui := newTestTUI(testPane("super"))
	tui.execCmd("layout")
	if tui.cmd.error == "" {
		t.Error("expected error for layout with no subcommand")
	}
}

// ── Integration: commands trigger save ──────────────────────────────

func TestGridCommandSavesLayout(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI(
		testPane("super"),
		testPane("eng1"),
		testPane("eng2"),
		testPane("qa1"),
	)
	tui.projectRoot = root

	tui.execCmd("grid 3x2")

	got, ok := LoadLayout(root, []string{"super", "eng1", "eng2", "qa1"})
	if !ok {
		t.Fatal("layout should be saved after grid command")
	}
	if got.GridCols != 3 || got.GridRows != 2 {
		t.Errorf("saved grid = %dx%d, want 3x2", got.GridCols, got.GridRows)
	}
}

func TestAgentsModalHideSavesLayout(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI(
		testPane("super"),
		testPane("eng1"),
		testPane("eng2"),
	)
	tui.projectRoot = root

	// Hide eng2 via agents modal (select row 2, toggle visibility).
	tui.openAgentsModal()
	tui.agents.selected = 2 // eng2
	tui.agentsToggleVisibility()

	// Hide is GLOBAL now (ini-9ka.10): it persists to fleet-state.yaml, not
	// to this window's layout file.
	fs, err := LoadFleetState(root)
	if err != nil {
		t.Fatalf("LoadFleetState: %v", err)
	}
	if !fs.IsHidden("eng2") {
		t.Error("eng2 should be hidden in the persisted fleet state after a modal hide")
	}

	// The layout file is still written (arrangement), and must NOT carry the
	// hidden flag any more -- that is the format change.
	got, ok := LoadLayout(root, []string{"super", "eng1", "eng2"})
	if !ok {
		t.Fatal("layout should be saved after agents modal hide")
	}
	if len(got.Hidden) != 0 {
		t.Errorf("layout file carried hidden = %v; hidden belongs to fleet state now", got.Hidden)
	}
}

func TestAgentsModalUnhideSavesLayout(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI(
		testPane("super"),
		hiddenTestPane("eng1"),
	)
	tui.projectRoot = root

	// Unhide eng1 via agents modal.
	tui.openAgentsModal()
	tui.agents.selected = 1 // eng1
	tui.agentsToggleVisibility()

	got, ok := LoadLayout(root, []string{"super", "eng1"})
	if !ok {
		t.Fatal("layout should be saved after agents modal unhide")
	}
	if len(got.Hidden) != 0 {
		t.Errorf("saved hidden = %v, want empty", got.Hidden)
	}
}

func TestFocusCommandSavesLayout(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI(
		testPane("super"),
		testPane("eng1"),
	)
	tui.projectRoot = root

	tui.execCmd("focus")

	got, ok := LoadLayout(root, []string{"super", "eng1"})
	if !ok {
		t.Fatal("layout should be saved after focus command")
	}
	if got.Mode != LayoutFocus {
		t.Errorf("saved mode = %d, want LayoutFocus", got.Mode)
	}
}

func TestMainCommandSavesLayout(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI(
		testPane("super"),
		testPane("eng1"),
	)
	tui.projectRoot = root

	tui.execCmd("main")

	got, ok := LoadLayout(root, []string{"super", "eng1"})
	if !ok {
		t.Fatal("layout should be saved after main command")
	}
	if got.Mode != Layout2Col {
		t.Errorf("saved mode = %d, want Layout2Col", got.Mode)
	}
}

func TestAgentsModalRevealAllSavesLayout(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI(
		testPane("super"),
		hiddenTestPane("eng1"),
		hiddenTestPane("eng2"),
	)
	tui.projectRoot = root

	// Reveal all via agents modal.
	tui.openAgentsModal()
	tui.agentsRevealAll()

	got, ok := LoadLayout(root, []string{"super", "eng1", "eng2"})
	if !ok {
		t.Fatal("layout should be saved after agents modal reveal all")
	}
	if len(got.Hidden) != 0 {
		t.Errorf("saved hidden = %v, want empty", got.Hidden)
	}
}

func TestZoomCommandSavesLayout(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI(
		testPane("super"),
		testPane("eng1"),
	)
	tui.projectRoot = root

	tui.execCmd("zoom")

	_, ok := LoadLayout(root, []string{"super", "eng1"})
	if !ok {
		t.Fatal("layout should be saved after zoom command")
	}
}

// ── LoadLayout mode persistence ─────────────────────────────────────

func TestLoadLayoutFocusMode(t *testing.T) {
	root := t.TempDir()
	SaveLayout(root, LayoutState{
		Mode:     LayoutFocus,
		GridCols: 2,
		GridRows: 1,
	})

	got, ok := LoadLayout(root, []string{"super", "eng1"})
	if !ok {
		t.Fatal("LoadLayout returned false")
	}
	if got.Mode != LayoutFocus {
		t.Errorf("mode = %d, want LayoutFocus", got.Mode)
	}
}

func TestLoadLayoutMainMode(t *testing.T) {
	root := t.TempDir()
	SaveLayout(root, LayoutState{
		Mode:     Layout2Col,
		GridCols: 2,
		GridRows: 1,
	})

	got, ok := LoadLayout(root, []string{"super", "eng1"})
	if !ok {
		t.Fatal("LoadLayout returned false")
	}
	if got.Mode != Layout2Col {
		t.Errorf("mode = %d, want Layout2Col", got.Mode)
	}
}

func TestLoadLayout_LiveModePreservesSmallGrid(t *testing.T) {
	root := t.TempDir()
	state := LayoutState{
		Mode:         LayoutLive,
		GridCols:     2,
		GridRows:     1,
		GridExplicit: true,
		Hidden:       make(map[string]bool),
	}
	if err := SaveLayout(root, state); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	agents := []string{"super", "eng1", "eng2", "eng3", "qa1", "pm", "shipper"}
	got, ok := LoadLayout(root, agents)
	if !ok {
		t.Fatal("LoadLayout returned false")
	}
	if got.GridCols != 2 || got.GridRows != 1 {
		t.Errorf("grid = %dx%d, want 2x1 (live mode should not auto-expand)", got.GridCols, got.GridRows)
	}
	if got.Mode != LayoutLive {
		t.Errorf("mode = %d, want LayoutLive", got.Mode)
	}
}

func TestLoadLayout_GridModeStillAutoExpands(t *testing.T) {
	root := t.TempDir()
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 2,
		GridRows: 1,
		Hidden:   make(map[string]bool),
	}
	if err := SaveLayout(root, state); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	agents := []string{"super", "eng1", "eng2", "eng3", "qa1"}
	got, ok := LoadLayout(root, agents)
	if !ok {
		t.Fatal("LoadLayout returned false")
	}
	if got.GridCols*got.GridRows < len(agents) {
		t.Errorf("grid = %dx%d (%d slots), want >= %d for grid mode", got.GridCols, got.GridRows, got.GridCols*got.GridRows, len(agents))
	}
}

// ── ini-czi: dividers must not steal a pane's column (C1, C3) ─────────
//
// gridRegions/calcMainVertical previously tiled regions CONTIGUOUSLY, with
// no column reserved for a divider. computeDividers computes divider X as
// (next region's X) - 1, which is only a real gutter if the pane to the
// left does not already claim that column -- with contiguous tiling it
// always did, silently erasing the final character of every line in every
// non-rightmost pane (the operator's "Either" -> "Eithe" report).

// assertNoDividerOverlapsAnyRegion is the C1 assertion: a divider occupies
// a column no pane owns.
func assertNoDividerOverlapsAnyRegion(t *testing.T, plan RenderPlan) {
	t.Helper()
	for _, d := range plan.Dividers {
		for _, pr := range plan.Panes {
			r := pr.Region
			// A partial last row legitimately has different column
			// boundaries than the row above it (TestComputeLayoutLastRowExpands,
			// out of scope for this bead per the spec: "the row shapes are
			// correct; only the divider geometry derived from them is
			// wrong"). Grid columns align vertically, so without a Y-overlap
			// check, a divider's X can coincidentally fall inside an
			// unrelated, differently-shaped row's pane -- a false positive,
			// since the divider never actually draws into that pane's rows.
			overlapsY := d.Y < r.Y+r.H && r.Y < d.Y+d.Len
			if overlapsY && d.X >= r.X && d.X < r.X+r.W {
				t.Errorf("divider at X=%d, Y=%d overlaps pane %q's region %+v -- C1 violated (divider drawn on a column the pane owns)", d.X, d.Y, pr.Pane.Name(), r)
			}
		}
	}
}

// assertDividersRespectRowWidth is the C3 assertion: for each row, the sum
// of pane content widths plus the divider columns between them equals the
// width allotted to that row -- nothing lost, nothing overlapping.
func assertDividersRespectRowWidth(t *testing.T, plan RenderPlan) {
	t.Helper()
	type rowKey struct{ y, h int }
	rows := make(map[rowKey][]PaneRender)
	for _, pr := range plan.Panes {
		k := rowKey{pr.Region.Y, pr.Region.H}
		rows[k] = append(rows[k], pr)
	}
	for k, prs := range rows {
		if len(prs) < 2 {
			continue // no internal dividers possible with a single pane in the row
		}
		minX, maxX := prs[0].Region.X, prs[0].Region.X+prs[0].Region.W
		sumW := 0
		for _, pr := range prs {
			sumW += pr.Region.W
			if pr.Region.X < minX {
				minX = pr.Region.X
			}
			if pr.Region.X+pr.Region.W > maxX {
				maxX = pr.Region.X + pr.Region.W
			}
		}
		rowWidth := maxX - minX
		// Matches on Y and X range only, deliberately NOT on Len: this test
		// targets C3 (content-width accounting) in isolation, and must not
		// be confused by ini-mdj5's separate C2 (divider length) defect,
		// which can make a top-row divider's Y match but Len disagree with
		// this row's H. Safe from double-counting a divider across two
		// row-groups that share a Y (Layout2Col's left pane vs. the top
		// grid row): the left pane is always alone in its own (Y,H) group
		// and is skipped above by the len(prs) < 2 guard.
		internalDividers := 0
		for _, d := range plan.Dividers {
			if d.Y == k.y && d.X > minX && d.X < maxX {
				internalDividers++
			}
		}
		if sumW+internalDividers != rowWidth {
			t.Errorf("row y=%d h=%d: content width %d + %d divider column(s) = %d, want %d (allotted row width) -- C3 violated", k.y, k.h, sumW, internalDividers, sumW+internalDividers, rowWidth)
		}
	}
}

func namesN(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("p%d", i)
	}
	return names
}

func TestComputeDividers_NoDividerOverlapsAnyRegion(t *testing.T) {
	cases := []struct {
		name  string
		state func(names []string) LayoutState
	}{
		{"grid", func(names []string) LayoutState {
			cols, rows := autoGrid(len(names))
			return LayoutState{Mode: LayoutGrid, GridCols: cols, GridRows: rows, Focused: names[0], Hidden: map[string]bool{}}
		}},
		{"main", func(names []string) LayoutState {
			return LayoutState{Mode: Layout2Col, Focused: names[0], Hidden: map[string]bool{}}
		}},
		{"live", func(names []string) LayoutState {
			return LayoutState{Mode: LayoutLive, LiveAuto: true, Focused: names[0], Hidden: map[string]bool{}}
		}},
	}
	for _, tc := range cases {
		for _, n := range []int{2, 3, 4, 5, 7, 8, 9, 12} {
			n := n
			names := namesN(n)
			t.Run(fmt.Sprintf("%s/n=%d", tc.name, n), func(t *testing.T) {
				plan := computeLayout(tc.state(names), testPanes(names...), 250, 60)
				assertNoDividerOverlapsAnyRegion(t, plan)
			})
		}
	}
}

func TestComputeDividers_ContentPlusDividersEqualsRowWidth(t *testing.T) {
	cases := []struct {
		name  string
		state func(names []string) LayoutState
	}{
		{"grid", func(names []string) LayoutState {
			cols, rows := autoGrid(len(names))
			return LayoutState{Mode: LayoutGrid, GridCols: cols, GridRows: rows, Focused: names[0], Hidden: map[string]bool{}}
		}},
		{"main", func(names []string) LayoutState {
			return LayoutState{Mode: Layout2Col, Focused: names[0], Hidden: map[string]bool{}}
		}},
	}
	for _, tc := range cases {
		for _, n := range []int{2, 3, 4, 5, 7, 8, 9, 12} {
			n := n
			names := namesN(n)
			t.Run(fmt.Sprintf("%s/n=%d", tc.name, n), func(t *testing.T) {
				plan := computeLayout(tc.state(names), testPanes(names...), 250, 60)
				assertDividersRespectRowWidth(t, plan)
			})
		}
	}
}

// ── ini-mdj5: a divider must not cross a row boundary (C2) ────────────
//
// computeDividers grouped panes by Region.Y alone and took rowInfo.h from
// whichever pane it met first for that Y, never revisited. Layout2Col's
// full-height left pane (Y=0, H=screenH, regions[0], always inserted
// first) poisoned the Y=0 group's h for every OTHER pane sharing that Y --
// the top grid row's internal dividers inherited screenH and ran straight
// through the bottom row. The defect fires at every pane count but is only
// VISIBLE when the last grid row is partial (its column boundaries differ
// from the top row's, so an overlong divider lands mid-pane instead of on
// a real boundary) -- both a partial and a full last row must be tested,
// since the full case is exactly where a future regression would hide.

// assertNoDividerCrossesRowBoundary is the C2 assertion: for every
// divider, find a pane whose region it borders (the divider sits exactly
// at that region's right edge, per the ini-czi column-reservation fix) and
// assert the divider's [Y, Y+Len) exactly matches that pane's own row
// span [Region.Y, Region.Y+Region.H) -- not just "within", since a
// divider separating one row must span exactly that row, no more.
func assertNoDividerCrossesRowBoundary(t *testing.T, plan RenderPlan) {
	t.Helper()
	for _, d := range plan.Dividers {
		if !d.Vertical {
			continue
		}
		// Grid columns are vertically aligned, so a divider's X can border
		// several panes on ONE side stacked across different rows (e.g. the
		// Layout2Col left-pane boundary borders every grid row's first
		// column at the same X). The correct C2 check is therefore not
		// "equals this one neighbor's row exactly" -- it is "is a SUBSET of
		// every neighboring pane's own row span": a single tall neighbor
		// (like the left pane) legitimately contains a shorter divider
		// without that divider needing to span the tall neighbor's full
		// height. A divider extending past ANY neighbor's own bounds is the
		// one thing that means it has crossed into a row it doesn't belong
		// to.
		found := false
		for _, pr := range plan.Panes {
			r := pr.Region
			adjacentX := r.X+r.W == d.X || r.X == d.X+1
			if !adjacentX {
				continue
			}
			// Grid columns align vertically, so a pane from a COMPLETELY
			// different, non-overlapping row can share this X (e.g. a
			// bottom-row pane sharing a top-row divider's column) without
			// being a real neighbor of THIS divider at all -- exclude those
			// before applying the subset check, or an unrelated row's pane
			// would wrongly fail it.
			overlapsY := d.Y < r.Y+r.H && r.Y < d.Y+d.Len
			if !overlapsY {
				continue
			}
			found = true
			if d.Y < r.Y || d.Y+d.Len > r.Y+r.H {
				t.Errorf("divider at X=%d [Y=%d, Y+Len=%d) extends beyond neighboring pane %q's own row span [Y=%d, Y+H=%d) -- C2 violated (divider crosses a row boundary)",
					d.X, d.Y, d.Y+d.Len, pr.Pane.Name(), r.Y, r.Y+r.H)
			}
		}
		if !found {
			t.Errorf("divider at X=%d, Y=%d, Len=%d has no neighboring pane region on either side", d.X, d.Y, d.Len)
		}
	}
}

func TestComputeDividers_NoDividerCrossesRowBoundary(t *testing.T) {
	// Matches the spec's worked example (pm/specs/divider-geometry.md) at
	// a 250x60 screen: rightCount=7 (8 visible) gives a PARTIAL last grid
	// row (4+3, where the bug is currently visible); rightCount=8 (9
	// visible) gives a FULL last row (4+4, where it currently hides).
	for _, rightCount := range []int{7, 8} {
		rightCount := rightCount
		t.Run(fmt.Sprintf("rightCount=%d", rightCount), func(t *testing.T) {
			names := namesN(rightCount + 1) // p0 = focus (left pane), rest = right grid
			state := LayoutState{Mode: Layout2Col, Focused: names[0], Hidden: map[string]bool{}}
			plan := computeLayout(state, testPanes(names...), 250, 60)
			assertNoDividerCrossesRowBoundary(t, plan)
		})
	}
}
