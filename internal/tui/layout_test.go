package tui

import (
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
	if !got.Hidden["arch"] || !got.Hidden["sec"] {
		t.Errorf("hidden = %v, want arch+sec", got.Hidden)
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
		Hidden:   map[string]bool{"workbench:intern": true},
		Protected: map[string]bool{"workbench:intern": true},
		Order:    []string{"workbench:intern", "super"},
	}
	if err := SaveLayout(root, state); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	got, ok := LoadLayout(root, []string{"super", "eng1"})
	if !ok {
		t.Fatal("LoadLayout should preserve offline remote pane keys")
	}
	if !got.Hidden["workbench:intern"] {
		t.Fatalf("offline remote hidden key lost: %v", got.Hidden)
	}
	if !got.Protected["workbench:intern"] {
		t.Fatalf("offline remote protected key lost: %v", got.Protected)
	}
	if len(got.Order) < 3 {
		t.Fatalf("order = %v, want preserved remote placeholder plus current panes", got.Order)
	}
	if got.Order[0] != "workbench:intern" || got.Order[1] != "super" || got.Order[2] != "eng1" {
		t.Fatalf("order = %v, want [workbench:intern super eng1]", got.Order)
	}
}

func TestLoadLayoutAllHiddenFallback(t *testing.T) {
	root := t.TempDir()
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 2,
		GridRows: 1,
		Hidden:   map[string]bool{"super": true, "eng1": true},
	}
	SaveLayout(root, state)

	_, ok := LoadLayout(root, []string{"super", "eng1"})
	if ok {
		t.Error("LoadLayout should return false when all panes would be hidden")
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
	if !got.Hidden["eng1"] {
		t.Errorf("eng1 should be hidden in saved layout")
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
	tui.handlePeerUpdate("workbench", []PaneView{rp})

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

	got, ok := LoadLayout(root, []string{"super", "eng1", "eng2"})
	if !ok {
		t.Fatal("layout should be saved after agents modal hide")
	}
	if !got.Hidden["eng2"] {
		t.Errorf("eng2 should be hidden in saved layout")
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
