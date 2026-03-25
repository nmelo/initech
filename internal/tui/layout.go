// layout.go contains the rendering architecture: LayoutState captures layout
// intent, computeLayout produces a RenderPlan, and the render loop consumes
// the plan without making layout decisions.

package tui

// LayoutState captures the complete layout intent. It is the single authority
// on what the screen should look like. Trivially serializable to YAML for
// persistent layout.
type LayoutState struct {
	Mode     LayoutMode      `yaml:"mode"`
	GridCols int             `yaml:"grid_cols"`
	GridRows int             `yaml:"grid_rows"`
	Zoomed   bool            `yaml:"zoomed,omitempty"`
	Focused  string          `yaml:"focused"`            // Pane name, not index.
	Hidden   map[string]bool `yaml:"hidden,omitempty"`   // Pane names that are hidden.
	Overlay  bool            `yaml:"overlay"`

	// Per-column and per-row proportional sizing (future).
	// nil means uniform. Values are relative weights (e.g., [60, 40] = 60%/40%).
	ColWeights []int `yaml:"col_weights,omitempty"`
	RowWeights []int `yaml:"row_weights,omitempty"`
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
	Pane    *Pane
	Region  Region
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
func computeLayout(state LayoutState, panes []*Pane, screenW, screenH int) RenderPlan {
	plan := RenderPlan{ScreenW: screenW, ScreenH: screenH}
	if len(panes) == 0 || screenW < 1 || screenH < 1 {
		return plan
	}

	// 1. Filter visible panes (preserve order).
	visible := make([]*Pane, 0, len(panes))
	for _, p := range panes {
		if !state.Hidden[p.name] {
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
		if p.name == focus {
			focusValid = true
			break
		}
	}
	if !focusValid {
		focus = visible[0].name
	}
	plan.ValidatedFocus = focus

	// 3. Compute regions based on layout mode.
	var regions []Region
	n := len(visible)

	if state.Zoomed || state.Mode == LayoutFocus {
		// Single pane: find the focused one, give it the full screen.
		regions = []Region{{X: 0, Y: 0, W: screenW, H: screenH}}
		for _, p := range visible {
			if p.name == focus {
				plan.Panes = append(plan.Panes, PaneRender{
					Pane:    p,
					Region:  regions[0],
					Focused: true,
					Dimmed:  false,
				})
				break
			}
		}
		return plan
	}

	switch state.Mode {
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
		plan.Panes = append(plan.Panes, PaneRender{
			Pane:    p,
			Region:  regions[i],
			Focused: p.name == focus,
			Dimmed:  p.name != focus,
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
	if numPanes <= 0 {
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
	return LayoutState{
		Mode:     LayoutGrid,
		GridCols: cols,
		GridRows: rows,
		Focused:  focused,
		Hidden:   make(map[string]bool),
		Overlay:  true,
	}
}
