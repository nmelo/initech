// layout.go contains the rendering architecture: LayoutState captures layout
// intent, computeLayout produces a RenderPlan, and the render loop consumes
// the plan without making layout decisions.
//
// It also contains layout persistence: SaveLayout/LoadLayout/DeleteLayout
// serialize the persistent subset of LayoutState to .initech/layout.yaml.

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LayoutState captures the complete layout intent. It is the single authority
// on what the screen should look like. Trivially serializable to YAML for
// persistent layout.
type LayoutState struct {
	Mode     LayoutMode      `yaml:"mode"`
	GridCols int             `yaml:"grid_cols"`
	GridRows int             `yaml:"grid_rows"`
	Zoomed   bool            `yaml:"zoomed,omitempty"`
	Focused  string          `yaml:"focused"`          // Pane key, not index.
	Hidden   map[string]bool `yaml:"hidden,omitempty"` // Pane keys that are hidden.
	Pinned   map[string]bool `yaml:"pinned,omitempty"` // Pane keys protected from auto-suspend.
	Order    []string        `yaml:"order,omitempty"`  // Pane keys in display order (from show command).
	Overlay  bool            `yaml:"overlay"`

	// Per-column and per-row proportional sizing (future).
	// nil means uniform. Values are relative weights (e.g., [60, 40] = 60%/40%).
	ColWeights []int `yaml:"col_weights,omitempty"`
	RowWeights []int `yaml:"row_weights,omitempty"`

	// Live mode: dynamic pane rotation by conviction score.
	LivePinned map[string]int `yaml:"live_pinned,omitempty"` // Agent name -> slot index.
	LiveSlots  []string       `yaml:"live_slots,omitempty"`  // Current agent name per slot (updated by live engine).
}

// RenderPlan is the complete set of instructions for one frame.
// Produced by computeLayout, consumed by the render loop.
type RenderPlan struct {
	Panes    []PaneRender // One entry per pane to draw (ordered).
	Dividers []Divider    // Vertical lines between pane columns.
	ScreenW  int
	ScreenH  int

	// ValidatedFocus is the name of the pane that actually receives input.
	// May differ from LayoutState.Focused if that pane is hidden.
	ValidatedFocus string
}

// PaneRender describes how to render a single pane.
type PaneRender struct {
	Pane    PaneView
	Region  Region
	Index   int  // 1-based pane number (position in full pane list).
	Focused bool // Receives keyboard input.
	Dimmed  bool // Render with reduced contrast.
}

// Divider describes a vertical or horizontal line between panes.
type Divider struct {
	X, Y     int
	Len      int
	Vertical bool
}

// computeLayout takes layout intent + pane list + screen dimensions and
// produces the complete render plan. ALL visibility, sizing, positioning,
// focus validation, and divider calculation lives here.
func computeLayout(state LayoutState, panes []PaneView, screenW, screenH int) RenderPlan {
	plan := RenderPlan{ScreenW: screenW, ScreenH: screenH}
	if len(panes) == 0 || screenW < 1 || screenH < 1 {
		return plan
	}

	// Build pane index map (1-based, from position in full pane list).
	// Uses paneKey for uniqueness (host:name for remote, name for local).
	paneIndex := make(map[string]int, len(panes))
	for i, p := range panes {
		paneIndex[paneKey(p)] = i + 1
	}

	// 1. Filter visible panes (preserve order).
	visible := make([]PaneView, 0, len(panes))
	for _, p := range panes {
		if !state.Hidden[paneKey(p)] {
			visible = append(visible, p)
		}
	}
	if len(visible) == 0 {
		return plan
	}

	// 2. Validate focus. If the focused pane is hidden or unknown, snap to
	// the first visible pane.
	focus := state.Focused
	focusValid := false
	for _, p := range visible {
		if paneKey(p) == focus {
			focusValid = true
			break
		}
	}
	if !focusValid {
		focus = paneKey(visible[0])
	}
	plan.ValidatedFocus = focus

	// 3. Compute regions based on layout mode.
	var regions []Region
	n := len(visible)

	if state.Zoomed || state.Mode == LayoutFocus {
		// Single pane: find the focused one, give it the full screen.
		regions = []Region{{X: 0, Y: 0, W: screenW, H: screenH}}
		for _, p := range visible {
			if paneKey(p) == focus {
				plan.Panes = append(plan.Panes, PaneRender{
					Pane:    p,
					Region:  regions[0],
					Index:   paneIndex[paneKey(p)],
					Focused: true,
					Dimmed:  false,
				})
				break
			}
		}
		return plan
	}

	switch state.Mode {
	case LayoutLive:
		// Live mode: reorder visible panes by slot assignment.
		// If LiveSlots is pre-computed (persistent engine on TUI), use it directly.
		// Otherwise fall back to stateless liveTickSlots (tests, first frame).
		slotNames := state.LiveSlots
		if len(slotNames) == 0 {
			slotNames = liveTickSlots(visible, state.LivePinned, state.GridCols*state.GridRows)
		}
		reordered := make([]PaneView, 0, len(slotNames))
		paneByKey := make(map[string]PaneView, len(visible))
		for _, p := range visible {
			paneByKey[paneKey(p)] = p
		}
		for _, name := range slotNames {
			if p, ok := paneByKey[name]; ok {
				reordered = append(reordered, p)
			}
		}
		visible = reordered
		n = len(visible)
		regions = gridRegions(state.GridCols, state.GridRows, n, screenW, screenH,
			state.ColWeights, state.RowWeights)
	case LayoutGrid:
		regions = gridRegions(state.GridCols, state.GridRows, n, screenW, screenH,
			state.ColWeights, state.RowWeights)
	case Layout2Col:
		regions = calcMainVertical(n, screenW, screenH)
	default:
		regions = gridRegions(state.GridCols, state.GridRows, n, screenW, screenH,
			state.ColWeights, state.RowWeights)
	}

	// 4. Assign regions to panes. Set focus and dimmed flags.
	for i, p := range visible {
		if i >= len(regions) {
			break
		}
		pk := paneKey(p)
		plan.Panes = append(plan.Panes, PaneRender{
			Pane:    p,
			Region:  regions[i],
			Index:   paneIndex[pk],
			Focused: pk == focus,
			Dimmed:  pk != focus,
		})
	}

	// 5. Compute dividers from region boundaries (per row).
	plan.Dividers = computeDividers(plan.Panes)

	return plan
}

// gridRegions computes regions for a grid layout with optional weighted sizing.
// If colWeights or rowWeights are nil, sizing is uniform.
func gridRegions(cols, rows, numPanes, screenW, screenH int,
	colWeights, rowWeights []int) []Region {
	if numPanes <= 0 || cols <= 0 || rows <= 0 {
		return nil
	}

	// Number of rows actually needed.
	actualRows := (numPanes + cols - 1) / cols
	if actualRows > rows {
		actualRows = rows
	}

	// Row heights.
	rowHeights := distributeWeighted(screenH, actualRows, rowWeights)

	regions := make([]Region, 0, numPanes)
	y := 0
	placed := 0
	for r := 0; r < actualRows && placed < numPanes; r++ {
		h := rowHeights[r]

		// How many panes in this row?
		colsThisRow := cols
		remaining := numPanes - placed
		if remaining < cols {
			colsThisRow = remaining
		}

		// Column widths for this row.
		// For the last (partial) row, recalculate weights for fewer columns.
		var weights []int
		if colWeights != nil && colsThisRow == cols {
			weights = colWeights
		}
		colWidths := distributeWeighted(screenW, colsThisRow, weights)

		x := 0
		for c := 0; c < colsThisRow; c++ {
			w := colWidths[c]
			regions = append(regions, Region{X: x, Y: y, W: w, H: h})
			x += w
			placed++
		}
		y += h
	}
	return regions
}

// distributeWeighted distributes total across n items, using weights if
// provided. If weights is nil or wrong length, distributes uniformly.
func distributeWeighted(total, n int, weights []int) []int {
	if n <= 0 {
		return nil
	}
	sizes := make([]int, n)

	if len(weights) == n {
		// Proportional distribution.
		sum := 0
		for _, w := range weights {
			sum += w
		}
		if sum <= 0 {
			sum = n // fallback to uniform
			weights = nil
		}
		if weights != nil {
			remaining := total
			for i, w := range weights {
				if i == n-1 {
					sizes[i] = remaining
				} else {
					sizes[i] = total * w / sum
					remaining -= sizes[i]
				}
			}
			return sizes
		}
	}

	// Uniform distribution.
	base := total / n
	extra := total - base*n
	for i := 0; i < n; i++ {
		sizes[i] = base
		if i < extra {
			sizes[i]++
		}
	}
	return sizes
}

// computeDividers generates vertical divider lines between pane columns.
// Each row may have different column boundaries (last row can be wider).
func computeDividers(panes []PaneRender) []Divider {
	if len(panes) < 2 {
		return nil
	}

	// Group panes by row (same Y value).
	type rowInfo struct {
		y, h int
		xs   []int
	}
	rowMap := make(map[int]*rowInfo)
	for _, pr := range panes {
		r := pr.Region
		ri, ok := rowMap[r.Y]
		if !ok {
			ri = &rowInfo{y: r.Y, h: r.H}
			rowMap[r.Y] = ri
		}
		if r.X > 0 {
			ri.xs = append(ri.xs, r.X)
		}
	}

	var dividers []Divider
	for _, ri := range rowMap {
		for _, x := range ri.xs {
			dividers = append(dividers, Divider{
				X:        x - 1,
				Y:        ri.y,
				Len:      ri.h,
				Vertical: true,
			})
		}
	}
	return dividers
}

// DefaultLayoutState creates a LayoutState with auto-calculated grid
// dimensions for the given pane names.
func DefaultLayoutState(paneNames []string) LayoutState {
	cols, rows := autoGrid(len(paneNames))
	focused := ""
	if len(paneNames) > 0 {
		focused = paneNames[0]
	}
	// Super is pinned by default (coordination hub, never auto-suspended).
	pinned := make(map[string]bool)
	for _, name := range paneNames {
		if name == "super" {
			pinned[name] = true
		}
	}
	return LayoutState{
		Mode:     LayoutGrid,
		GridCols: cols,
		GridRows: rows,
		Focused:  focused,
		Hidden:   make(map[string]bool),
		Pinned:   pinned,
		Overlay:  true,
	}
}

// ── Layout Persistence ──────────────────────────────────────────────

// PersistentLayout is the subset of LayoutState that survives sessions.
// Focused pane is deliberately excluded (momentary choice, not a preference).
// Overlay and weights are excluded (not layout-changing from the user's perspective).
type PersistentLayout struct {
	Grid       string         `yaml:"grid"`                  // e.g. "3x2"
	Hidden     []string       `yaml:"hidden,omitempty"`      // Pane keys: name for local, host:name for remote.
	Pinned     []string       `yaml:"pinned,omitempty"`      // Pane keys protected from auto-suspend.
	Order      []string       `yaml:"order,omitempty"`       // Pane keys in display order (from show command).
	Mode       string         `yaml:"mode"`                  // "grid", "focus", "main", "live"
	LivePinned map[string]int `yaml:"live_pinned,omitempty"` // Agent name -> fixed slot index for live mode.
}

// layoutDir returns the .initech directory path under projectRoot.
func layoutDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".initech")
}

// layoutPath returns the full path to .initech/layout.yaml.
func layoutPath(projectRoot string) string {
	return filepath.Join(layoutDir(projectRoot), "layout.yaml")
}

// SaveLayout writes the layout state to .initech/layout.yaml using atomic write
// (temp file + rename) to prevent corruption. Creates .initech/ if it doesn't exist.
func SaveLayout(projectRoot string, state LayoutState) error {
	pl := PersistentLayout{
		Grid:       fmt.Sprintf("%dx%d", state.GridCols, state.GridRows),
		Mode:       layoutModeToString(state.Mode),
		Order:      state.Order,
		LivePinned: state.LivePinned,
	}
	for name, hidden := range state.Hidden {
		if hidden {
			pl.Hidden = append(pl.Hidden, name)
		}
	}
	sort.Strings(pl.Hidden)
	for name, pinned := range state.Pinned {
		if pinned {
			pl.Pinned = append(pl.Pinned, name)
		}
	}
	sort.Strings(pl.Pinned)

	dir := layoutDir(projectRoot)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create .initech/: %w", err)
	}

	data, err := yaml.Marshal(&pl)
	if err != nil {
		return fmt.Errorf("marshal layout: %w", err)
	}

	// Atomic write: write to temp file, then rename.
	tmp := layoutPath(projectRoot) + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write temp layout: %w", err)
	}
	if err := os.Rename(tmp, layoutPath(projectRoot)); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename layout: %w", err)
	}
	return nil
}

// LoadLayout reads .initech/layout.yaml and merges it into a LayoutState.
// The caller passes the currently known pane keys. Unknown remote pane keys
// (host:name) are preserved so delayed peer reconnects can reuse saved hidden
// and order preferences, while stale local-only names are filtered out.
// Returns false if the file doesn't exist, is empty, contains invalid YAML,
// or would result in all currently known panes hidden.
func LoadLayout(projectRoot string, paneKeys []string) (LayoutState, bool) {
	data, err := os.ReadFile(layoutPath(projectRoot))
	if err != nil {
		return LayoutState{}, false
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return LayoutState{}, false
	}

	var pl PersistentLayout
	if err := yaml.Unmarshal(data, &pl); err != nil {
		return LayoutState{}, false
	}

	// Parse grid dimensions.
	cols, rows, ok := parseGrid(pl.Grid, len(paneKeys))
	if !ok {
		return LayoutState{}, false
	}

	// Build known pane set for filtering stale references.
	known := make(map[string]bool, len(paneKeys))
	for _, name := range paneKeys {
		known[name] = true
	}

	// Preserve remote pane keys even when the peer is offline at startup.
	// A host:name key is not stale just because it is not in paneKeys yet.
	hidden := make(map[string]bool)
	currentHiddenCount := 0
	for _, name := range pl.Hidden {
		if shouldKeepPersistedPaneKey(name, known) {
			hidden[name] = true
			if known[name] {
				currentHiddenCount++
			}
		}
	}

	pinned := make(map[string]bool)
	for _, name := range pl.Pinned {
		if shouldKeepPersistedPaneKey(name, known) {
			pinned[name] = true
		}
	}

	// Edge case: all currently known panes hidden -> nonsensical, fall back to defaults.
	if len(paneKeys) > 0 && currentHiddenCount >= len(paneKeys) {
		return LayoutState{}, false
	}

	// Determine visible count for grid auto-recalc.
	visCount := len(paneKeys) - currentHiddenCount
	mode := stringToLayoutMode(pl.Mode)

	// If grid can't hold visible panes, auto-recalculate.
	if cols*rows < visCount {
		cols, rows = autoGrid(visCount)
	}

	focused := ""
	if len(paneKeys) > 0 {
		focused = paneKeys[0]
	}

	// Preserve remote pane-key placeholders in the saved order so later peer
	// reconnects can slot remote panes back into position. Stale local-only
	// names are dropped. Currently known panes not present in the saved order
	// are appended.
	var order []string
	if len(pl.Order) > 0 {
		orderSet := make(map[string]bool)
		for _, name := range pl.Order {
			if shouldKeepPersistedPaneKey(name, known) && !orderSet[name] {
				order = append(order, name)
				orderSet[name] = true
			}
		}
		for _, name := range paneKeys {
			if !orderSet[name] {
				order = append(order, name)
			}
		}
	}

	return LayoutState{
		Mode:       mode,
		GridCols:   cols,
		GridRows:   rows,
		Focused:    focused,
		Hidden:     hidden,
		Pinned:     pinned,
		Order:      order,
		Overlay:    true, // Always start with overlay visible.
		LivePinned: pl.LivePinned,
	}, true
}

// shouldKeepPersistedPaneKey reports whether a saved pane identifier should
// survive LoadLayout filtering. Current panes are always kept. Unknown remote
// pane keys are also kept so saved layout preferences survive delayed peer
// reconnects.
func shouldKeepPersistedPaneKey(name string, known map[string]bool) bool {
	if known[name] {
		return true
	}
	return strings.Contains(name, ":")
}

// DeleteLayout removes .initech/layout.yaml. Returns nil if the file
// doesn't exist (idempotent).
func DeleteLayout(projectRoot string) error {
	err := os.Remove(layoutPath(projectRoot))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// reorderPanes rearranges the panes slice to match the given order.
// Names in order that don't match a pane are skipped. Panes not in order
// are appended at the end in their current relative order.
func reorderPanes(panes []PaneView, order []string) {
	if len(order) == 0 {
		return
	}
	// Snapshot the original order before mutating. Without this, panes not
	// in the explicit order would be appended in random map iteration order,
	// causing non-deterministic positioning for hot-added panes or incomplete
	// order lists loaded from a prior session.
	orig := make([]PaneView, len(panes))
	copy(orig, panes)

	byKey := make(map[string]PaneView, len(panes))
	for _, p := range panes {
		byKey[paneKey(p)] = p
	}
	placed := make(map[string]bool, len(order))
	idx := 0
	for _, name := range order {
		if p, ok := byKey[name]; ok && !placed[name] {
			panes[idx] = p
			placed[name] = true
			idx++
		}
	}
	// Append unspecified panes in their original slice order.
	for _, p := range orig {
		pk := paneKey(p)
		if !placed[pk] {
			panes[idx] = p
			placed[pk] = true
			idx++
		}
	}
}

// layoutModeToString converts a LayoutMode to its YAML string.
func layoutModeToString(m LayoutMode) string {
	switch m {
	case LayoutFocus:
		return "focus"
	case LayoutGrid:
		return "grid"
	case Layout2Col:
		return "main"
	case LayoutLive:
		return "live"
	default:
		return "grid"
	}
}

// stringToLayoutMode converts a YAML string to a LayoutMode.
func stringToLayoutMode(s string) LayoutMode {
	switch s {
	case "focus":
		return LayoutFocus
	case "grid":
		return LayoutGrid
	case "main":
		return Layout2Col
	case "live":
		return LayoutLive
	default:
		return LayoutGrid
	}
}
