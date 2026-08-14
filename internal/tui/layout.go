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
	"regexp"
	"sort"
	"strings"

	"github.com/nmelo/initech/internal/config"
	"gopkg.in/yaml.v3"
)

// LayoutState captures the complete layout intent. It is the single authority
// on what the screen should look like. Trivially serializable to YAML for
// persistent layout.
type LayoutState struct {
	Mode     LayoutMode `yaml:"mode"`
	GridCols int        `yaml:"grid_cols"`
	GridRows int        `yaml:"grid_rows"`
	Zoomed   bool       `yaml:"zoomed,omitempty"`
	Focused  string     `yaml:"focused"` // Pane key, not index.
	// Hidden and Protected are a DERIVED PROJECTION of FleetState
	// (ini-9ka.10), not owned state. They are session-GLOBAL facts kept in
	// .initech/fleet-state.yaml; this struct is PER-WINDOW, so a copy stored
	// here is a read cache only. Refreshed store->memory and never the
	// reverse; never persisted (PersistentLayout carries neither); never
	// written directly -- route changes through TUI.setHidden/setProtected,
	// which go through FleetState so every window agrees. A direct write here
	// will not survive a reload, and a guardrail test asserts exactly that.
	Hidden    map[string]bool `yaml:"-"`
	Protected map[string]bool `yaml:"-"`
	Order     []string        `yaml:"order,omitempty"` // Pane keys in display order (from show command).
	Overlay   bool            `yaml:"overlay"`

	// GridExplicit is true when the user chose grid dimensions via :grid CxR
	// or Alt+2/Alt+3. When set, recalcGrid skips auto-recalculation so peer
	// updates and hot-adds don't overwrite the user's choice.
	GridExplicit bool `yaml:"grid_explicit,omitempty"`

	// Per-column and per-row proportional sizing (future).
	// nil means uniform. Values are relative weights (e.g., [60, 40] = 60%/40%).
	ColWeights []int `yaml:"col_weights,omitempty"`
	RowWeights []int `yaml:"row_weights,omitempty"`

	// Live mode: dynamic pane rotation by conviction score.
	// LivePinned is the same derived projection of FleetState as Hidden and
	// Protected above -- global, stored in fleet-state.yaml, cached here,
	// never persisted from this struct. Route changes through
	// TUI.setLiveSlot.
	LivePinned map[string]int `yaml:"-"`
	LiveSlots  []string       `yaml:"live_slots,omitempty"` // Current agent name per slot (updated by live engine).
	LiveAuto   bool           `yaml:"live_auto,omitempty"`  // True = auto-size grid from active agent count.

	// Agents-grid modal (ini-2rc): band membership. Groups is the ordered
	// list of band labels (top-to-bottom); GroupOf maps a pane key to its
	// band label. Deliberately NOT a per-band member list: within a band, a
	// pane's relative position is just its position in Order/t.panes, so a
	// same-band swap is an ordinary Order swap and a cross-band grab is
	// GroupOf reassignment plus an Order splice -- one flat source of truth
	// for ordering, Groups/GroupOf is a pure additive layer on top of it.
	Groups  []string          `yaml:"groups,omitempty"`
	GroupOf map[string]string `yaml:"group_of,omitempty"`
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
		// Re-validate focus against the live-visible set. An evicted agent
		// passes the earlier focus validation (not hidden) but is not in the
		// render plan. Snap focus to the first visible pane if the current
		// focus is not in the live-visible set.
		focusInLive := false
		for _, p := range visible {
			if paneKey(p) == focus {
				focusInLive = true
				break
			}
		}
		if !focusInLive && len(visible) > 0 {
			focus = paneKey(visible[0])
		}
		liveCols, liveRows := state.GridCols, state.GridRows
		if state.LiveAuto {
			liveCols, liveRows = autoGrid(n)
		}
		regions = gridRegions(liveCols, liveRows, n, screenW, screenH,
			state.ColWeights, state.RowWeights)
	case LayoutGrid:
		regions = gridRegions(state.GridCols, state.GridRows, n, screenW, screenH,
			state.ColWeights, state.RowWeights)
	case Layout2Col:
		// Reorder so the focused pane is first: calcMainVertical is purely
		// positional (region 0 is the big slot), and the focused pane must
		// always land there for Option+F's promotion to work (ini-vtki).
		// Mirrors the LayoutLive reorder above; uses the already-validated
		// `focus`, so a focused pane that was hidden/removed is handled for
		// free (it was already snapped to visible[0] in step 2).
		reordered := make([]PaneView, 0, len(visible))
		var focusedPane PaneView
		for _, p := range visible {
			if paneKey(p) == focus {
				focusedPane = p
			} else {
				reordered = append(reordered, p)
			}
		}
		if focusedPane != nil {
			visible = append([]PaneView{focusedPane}, reordered...)
		}
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
			ownedW := w
			if c < colsThisRow-1 {
				// Reserve this column's LAST cell as the gutter for the
				// divider computeDividers draws to this pane's right (ini-czi:
				// contiguous tiling left no gap, so the divider landed on and
				// erased this pane's real last column). x still advances by
				// the FULL w so the next column's start is unchanged and the
				// reserved cell -- computeDividers' existing X = nextX-1 --
				// falls on a column this pane no longer claims.
				ownedW = w - 1
				if ownedW < 1 {
					ownedW = 1
				}
			}
			regions = append(regions, Region{X: x, Y: y, W: ownedW, H: h})
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

	// Group panes by row (same Y AND H -- not Y alone). In Layout2Col the
	// full-height left pane shares Y=0 with the top grid row but has a
	// different H (screenH vs. the row's own height); keying on Y alone let
	// whichever pane arrived first set rowInfo.h for the whole group, so the
	// left pane (always first, as regions[0]) poisoned every top-row
	// divider's length to screenH, running dividers through the row below
	// (ini-mdj5). The left pane's own X=0 already excludes it from
	// contributing an xs entry (guard below), so keying on (Y,H) separates
	// it from the top row's group with no other change needed.
	type rowKey struct{ y, h int }
	type rowInfo struct {
		y, h int
		xs   []int
	}
	rowMap := make(map[rowKey]*rowInfo)
	for _, pr := range panes {
		r := pr.Region
		k := rowKey{r.Y, r.H}
		ri, ok := rowMap[k]
		if !ok {
			ri = &rowInfo{y: r.Y, h: r.H}
			rowMap[k] = ri
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
	// Super is protected by default (coordination hub, never auto-suspended).
	protected := make(map[string]bool)
	for _, name := range paneNames {
		if name == "super" {
			protected[name] = true
		}
	}
	return LayoutState{
		Mode:      LayoutGrid,
		GridCols:  cols,
		GridRows:  rows,
		Focused:   focused,
		Hidden:    make(map[string]bool),
		Protected: protected,
		Overlay:   true,
	}
}

// ── Layout Persistence ──────────────────────────────────────────────

// PersistentLayout is the subset of LayoutState that survives sessions.
// Focused pane is deliberately excluded (momentary choice, not a preference).
// Overlay and weights are excluded (not layout-changing from the user's perspective).
type PersistentLayout struct {
	Grid         string `yaml:"grid"`                    // e.g. "3x2"
	GridExplicit bool   `yaml:"grid_explicit,omitempty"` // True = user chose CxR explicitly; don't auto-resize.
	// Hidden/Protected/DepPinned/LivePinned are LEGACY (pre-ini-9ka.10).
	// These global facts now live in .initech/fleet-state.yaml. They remain
	// declared so the loader can still PARSE an old file -- import-once reads
	// them exactly once, when fleet-state.yaml is absent -- but SaveLayout no
	// longer populates them, so they are never written again. Old files keep
	// their copies as dead data forever: the file is never rewritten to strip
	// them (adoption-in-place, ini-9ka.3), because a rewrite opens a window in
	// which the operator's state can be lost and stripping buys nothing.
	Hidden     []string          `yaml:"hidden,omitempty"`
	Protected  []string          `yaml:"protected,omitempty"`
	DepPinned  []string          `yaml:"pinned,omitempty"`
	Order      []string          `yaml:"order,omitempty"`       // Pane keys in display order (from show command).
	Mode       string            `yaml:"mode"`                  // "grid", "focus", "main", "live"
	LivePinned map[string]int    `yaml:"live_pinned,omitempty"` // Legacy; see above.
	LiveAuto   bool              `yaml:"live_auto,omitempty"`   // True = auto-size grid from active agent count.
	Groups     []string          `yaml:"groups,omitempty"`      // Agents-grid band labels, in order.
	GroupOf    map[string]string `yaml:"group_of,omitempty"`    // Pane key -> band label.
}

// layoutDir returns the .initech directory path under projectRoot.
func layoutDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".initech")
}

// layoutPath returns the full path to .initech/layout.yaml.
func layoutPath(projectRoot string) string {
	return filepath.Join(layoutDir(projectRoot), "layout.yaml")
}

// layoutPathFor returns the layout file path for a given window identity.
//
// The empty identity is window 1 and resolves to the original
// .initech/layout.yaml, unchanged in both path and format. That is what makes
// upgrades safe without a migration step: an existing project's layout.yaml
// from any prior release is simply adopted in place as window 1's file, so
// there is no rename, copy, or rewrite that could lose it partway (ini-9ka.3).
//
// Other windows get .initech/layout-<windowID>.yaml. windowID must already
// have passed validWindowID; callers reject invalid identities before reaching
// here rather than sanitizing, so a traversal attempt is a refusal, not a
// silently rewritten path.
func layoutPathFor(projectRoot, windowID string) string {
	if windowID == "" {
		return layoutPath(projectRoot)
	}
	return filepath.Join(layoutDir(projectRoot), "layout-"+windowID+".yaml")
}

// validWindowID reports whether windowID is safe to embed in a layout
// filename. The empty identity (window 1) is always valid. Everything else
// must satisfy the canonical peer-name rule (config.ValidPeerName), which
// admits only letters, digits, and hyphens — so separators, dots, and ".."
// are rejected and the resulting path cannot escape .initech. Deliberately
// reuses the existing validator rather than defining a second regex here,
// since two independently-maintained rules for the same identity is how they
// drift apart.
func validWindowID(windowID string) bool {
	return windowID == "" || config.ValidPeerName(windowID)
}

// windowAliasKeyRe matches group_of keys written under the WINDOW-ALIAS
// family -- xq4r's exact irregular trio (window1:, window-N:, windowN:) as a
// key prefix. Only viewer panes ever carry these hosts, and a viewer pane is
// an alias of a window-1 agent, never a distinct one. Cross-machine hosts
// (workbench:) are distinct agents and MUST NOT match.
var windowAliasKeyRe = regexp.MustCompile(`^window(?:1|-?[0-9]+):(.+)$`)

// normalizeGroupOfKeys collapses window-alias-prefixed group_of keys onto the
// canonical bare agent name. Bare wins a conflict (logged once per agent at
// WARN, naming both values); an agent present ONLY under a prefixed key keeps
// its group under the bare name rather than being dropped.
func normalizeGroupOfKeys(in map[string]string) (map[string]string, bool) {
	if len(in) == 0 {
		return in, false
	}
	out := make(map[string]string, len(in))
	healed := false
	// Bare keys first, so the conflict rule is order-independent.
	for k, v := range in {
		if !windowAliasKeyRe.MatchString(k) {
			out[k] = v
		}
	}
	for k, v := range in {
		m := windowAliasKeyRe.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		healed = true
		bare := m[1]
		if existing, ok := out[bare]; ok {
			if existing != v {
				LogWarn("layout", "group_of identity-family conflict; bare key wins",
					"agent", bare, "bare_group", existing, "aliased_group", v, "aliased_key", k)
			}
			continue
		}
		out[bare] = v
	}
	return out, healed
}

// stampFleetThenApplyOrder stamps each local pane's fleet-canonical number
// from its CREATION position, then applies the window's saved arrangement.
//
// The sequence is the contract (ini-6m4): panes arrive here in creation
// (config) order, which is also what the window server snapshots and serves to
// viewers as the ordered hello_ok list -- so every window's modal numbers from
// the same sequence. Stamping AFTER the reorder would bake this window's saved
// arrangement into the "canonical" numbers, quietly recreating the two-windows-
// two-numberings bug whenever a layout file exists. One function rather than
// two calls in Run so the order of the two steps is a testable property
// instead of a line-adjacency hope.
func stampFleetThenApplyOrder(panes []PaneView, order []string) {
	for i, p := range panes {
		if lp, ok := p.(*Pane); ok {
			lp.SetFleetIdx(i)
		}
	}
	if len(order) > 0 {
		reorderPanes(panes, order)
	}
}

// SaveLayout writes the layout state to .initech/layout.yaml using atomic write
// (temp file + rename) to prevent corruption. Creates .initech/ if it doesn't exist.
//
// Equivalent to SaveLayoutForWindow with the window-1 identity; kept as the
// project-scoped entry point every existing caller already uses.
func SaveLayout(projectRoot string, state LayoutState) error {
	return SaveLayoutForWindow(projectRoot, "", state)
}

// SaveLayoutForWindow writes the layout state for one window identity, so N
// windows over the same project each persist independently instead of
// clobbering a single shared file on every layout change (ini-9ka.3). The
// empty windowID is window 1 and writes the original .initech/layout.yaml.
// Returns an error for an unsafe windowID rather than sanitizing it.
func SaveLayoutForWindow(projectRoot, windowID string, state LayoutState) error {
	if !validWindowID(windowID) {
		return fmt.Errorf("invalid window id %q: must contain only letters, digits, or hyphens", windowID)
	}
	// WRITE GUARD (ini-qkwc AC4): no window-alias key ever persists again,
	// regardless of which future caller built the map. Read-side healing
	// without this is a leak with a mop -- a viewer whose paneKeys carry the
	// window1: host would re-contaminate its file on every save.
	normalizedGroupOf, _ := normalizeGroupOfKeys(state.GroupOf)
	pl := PersistentLayout{
		Grid:         fmt.Sprintf("%dx%d", state.GridCols, state.GridRows),
		GridExplicit: state.GridExplicit,
		Mode:         layoutModeToString(state.Mode),
		Order:        state.Order,
		LiveAuto:     state.LiveAuto,
		Groups:       state.Groups,
		GroupOf:      normalizedGroupOf,
	}
	// Hidden/Protected/LivePinned are deliberately NOT written: they are
	// session-global and live in fleet-state.yaml (ini-9ka.10). A layout file
	// carries arrangement only.

	dir := layoutDir(projectRoot)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create .initech/: %w", err)
	}

	data, err := yaml.Marshal(&pl)
	if err != nil {
		return fmt.Errorf("marshal layout: %w", err)
	}

	// Atomic write: write to temp file, then rename. The staging path is
	// derived from the PER-WINDOW path, not the shared project path: two
	// windows saving concurrently would otherwise stage through the same
	// .tmp and rename over each other, cross-contaminating final files that
	// look independent by name (ini-9ka.3).
	final := layoutPathFor(projectRoot, windowID)
	if err := writeFileAtomic(final, data, 0600); err != nil {
		return fmt.Errorf("write layout: %w", err)
	}
	return nil
}

// LoadLayout reads .initech/layout.yaml and merges it into a LayoutState.
// The caller passes the currently known pane keys. Unknown remote pane keys
// (host:name) are preserved so delayed peer reconnects can reuse saved hidden
// and order preferences, while stale local-only names are filtered out.
// Returns false if the file doesn't exist, is empty, contains invalid YAML,
// or would result in all currently known panes hidden.
// Equivalent to LoadLayoutForWindow with the window-1 identity; kept as the
// project-scoped entry point every existing caller already uses.
func LoadLayout(projectRoot string, paneKeys []string) (LayoutState, bool) {
	return LoadLayoutForWindow(projectRoot, "", paneKeys)
}

// LoadLayoutForWindow reads one window identity's layout file and merges it
// into a LayoutState, with the same filtering and migration behavior as
// LoadLayout (ini-9ka.3). The empty windowID is window 1 and reads the
// original .initech/layout.yaml, so a file written by any prior release loads
// unchanged after upgrade. Returns false for an unsafe windowID, and for the
// same reasons LoadLayout does: missing, empty, invalid, or all-hidden.
//
// Reads exactly one file and consults no other state — in particular it does
// not read group-to-window assignment (ini-9ka.4), so the two persistence
// concerns round-trip independently and neither can corrupt the other.
func LoadLayoutForWindow(projectRoot, windowID string, paneKeys []string) (LayoutState, bool) {
	if !validWindowID(windowID) {
		return LayoutState{}, false
	}
	data, err := os.ReadFile(layoutPathFor(projectRoot, windowID))
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

	// pl.Hidden / pl.Protected / pl.DepPinned / pl.LivePinned are LEGACY dead
	// data (ini-9ka.10). They are still PARSED so importLegacyFleetState can
	// read them exactly once, but they are deliberately NOT consumed here:
	// these are session-global facts owned by fleet-state.yaml, and the TUI
	// projects them onto LayoutState after this returns. Consuming them would
	// be actively wrong twice over -- an old file's stale hidden set would
	// briefly overwrite the global truth, and the all-hidden guard below would
	// reject a perfectly good arrangement on the strength of data that no
	// longer governs anything.
	hidden := make(map[string]bool)
	protected := make(map[string]bool)
	currentHiddenCount := 0

	// Edge case: all currently known panes hidden -> nonsensical, fall back to defaults.
	if len(paneKeys) > 0 && currentHiddenCount >= len(paneKeys) {
		return LayoutState{}, false
	}

	// Determine visible count for grid auto-recalc.
	visCount := len(paneKeys) - currentHiddenCount
	mode := stringToLayoutMode(pl.Mode)

	// If grid can't hold visible panes, auto-recalculate.
	// Skip for live mode: the grid is a viewport, intentionally smaller
	// than total agents — conviction scoring rotates the rest.
	if mode != LayoutLive && cols*rows < visCount {
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

	// GroupOf: same staleness filter as Hidden/Protected/Order. Groups is
	// then pruned to labels that still have at least one surviving member --
	// the same "no empty bands surface" invariant the agents-grid modal
	// enforces on close, applied here too since a stale-key filter is
	// exactly the other way a band can end up empty (ini-2rc).
	// NORMALIZE THE IDENTITY FAMILY FIRST (ini-qkwc, the xq4r class one store
	// over): group_of may hold window-alias-prefixed keys ("window1:eng1")
	// alongside bare ones, written by a viewer whose paneKeys carry the host
	// alias. Those are the SAME agents, not distinct ones -- and resolving
	// them per-window split the fleet: window 1 read the bare family, window
	// 2 read the prefixed family, and agents whose prefixed keys pointed at a
	// group absent from the groups list silently vanished from the viewer's
	// modal. Bare wins a conflict (it is what window 1 writes today), logged
	// once at WARN. Cross-machine host prefixes (workbench:eng1) are REAL
	// distinct agents and pass through untouched.
	normalized, healed := normalizeGroupOfKeys(pl.GroupOf)

	var groupOf map[string]string
	memberCount := make(map[string]int)
	for name, label := range normalized {
		if shouldKeepPersistedPaneKey(name, known) {
			if groupOf == nil {
				groupOf = make(map[string]string)
			}
			groupOf[name] = label
			memberCount[label]++
		}
	}
	var groups []string
	inGroups := make(map[string]bool)
	for _, label := range pl.Groups {
		if memberCount[label] > 0 {
			groups = append(groups, label)
			inGroups[label] = true
		}
	}
	// DEAD-GROUP RULE (ini-qkwc AC3, surfacing chosen over swallowing): a
	// group referenced by a surviving group_of entry but absent from the
	// groups list is APPENDED, sorted for stability -- the operator grouped
	// those agents deliberately, and the alternative was agents silently
	// missing from the modal.
	var resurfaced []string
	for label, n := range memberCount {
		if n > 0 && !inGroups[label] {
			resurfaced = append(resurfaced, label)
		}
	}
	sort.Strings(resurfaced)
	groups = append(groups, resurfaced...)

	// PERSIST THE HEAL ONCE (ini-m495's rule applied to this store): the
	// loading window owns its layout file, so writing the normalized form
	// back is per-window property, not shared session state -- civ satisfied
	// by ownership. Without this the heal re-runs and re-logs on every load
	// forever, and the disk form never converges.
	if healed {
		pl.GroupOf = normalized
		if data, err := yaml.Marshal(&pl); err == nil {
			if err := writeFileAtomic(layoutPathFor(projectRoot, windowID), data, 0600); err != nil {
				LogWarn("layout", "could not persist group_of heal; will re-heal next load", "err", err)
			}
		}
	}

	return LayoutState{
		Mode:         mode,
		GridCols:     cols,
		GridRows:     rows,
		GridExplicit: pl.GridExplicit,
		Focused:      focused,
		Hidden:       hidden,
		Protected:    protected,
		Order:        order,
		Overlay:      true, // Always start with overlay visible.
		LiveAuto:     pl.LiveAuto,
		Groups:       groups,
		GroupOf:      groupOf,
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
//
// Equivalent to DeleteLayoutForWindow with the window-1 identity; kept as the
// project-scoped entry point every existing caller already uses.
func DeleteLayout(projectRoot string) error {
	return DeleteLayoutForWindow(projectRoot, "")
}

// DeleteLayoutForWindow removes one window identity's layout file, leaving
// other windows' layouts intact (ini-9ka.3). Idempotent: returns nil if the
// file does not exist. Returns an error for an unsafe windowID rather than
// deleting a path derived from it.
func DeleteLayoutForWindow(projectRoot, windowID string) error {
	if !validWindowID(windowID) {
		return fmt.Errorf("invalid window id %q: must contain only letters, digits, or hyphens", windowID)
	}
	err := os.Remove(layoutPathFor(projectRoot, windowID))
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

// LoadFleetScopedLayout reads ONLY the fleet-scoped fields of the layout store:
// the band universe and the agent -> band map, exactly as persisted, under
// their canonical bare-name keys.
//
// It deliberately does NOT go through LoadLayoutForWindow. That path filters
// persisted keys against the caller's own known pane keys, which is correct for
// adopting an ARRANGEMENT (a stale local name should not linger) and actively
// wrong for reading FLEET state: a viewer's pane keys are alias-prefixed
// presentation identities, so the filter drops every canonically-keyed
// membership entry and the viewer sees no membership at all (ini-la97 round 2,
// found by qa1). Fleet-scoped state is read as written; the caller maps it onto
// its own panes by canonical name.
//
// Read-only by construction: it opens the file and returns values. ok is false
// for a missing, empty, or unparseable store, so a caller can keep what it has
// rather than blanking on a transient failure.
func LoadFleetScopedLayout(projectRoot string) (groups []string, groupOf map[string]string, ok bool) {
	data, err := os.ReadFile(layoutPath(projectRoot))
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil, false
	}
	var pl PersistentLayout
	if err := yaml.Unmarshal(data, &pl); err != nil {
		return nil, nil, false
	}
	return pl.Groups, pl.GroupOf, true
}
