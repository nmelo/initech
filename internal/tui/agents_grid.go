// agents_grid.go implements the grouped 2-D grid agents modal (ini-2rc),
// replacing the flat Alt+a list. Groups are function bands (core/eng/qa/...)
// laid out top-to-bottom; within a band, agents lay out left-to-right,
// wrapping at gridMaxPerRow. Design is spec-pinned (pm/specs/agents-grid-modal.md
// at df005fe) and operator-approved verbatim against a running PoC
// (~/Desktop/agent-grid-poc) -- layout constants, styling, and behavior below
// port that PoC's algorithms, not a reinterpretation of them.
package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nmelo/initech/internal/roles"
)

// Grid layout constants (spec section "Layout rules", operator-tuned).
const (
	gridCellW     = 17 // " NN [x] name*•  " -- fixed so columns align across bands.
	gridMaxPerRow = 6  // Groups larger than this wrap into a multi-line band.
	gridBandLead  = 1  // Blank line before each group label.
	gridLabelGap  = 1  // Blank line between the label and its cell row(s).
	gridTierLead  = 1  // Blank line before each monitor-tier header (ini-9ka.5).
)

// agentsHelpText is the default (non-searching, non-group-creating) footer.
// The longest of the modal's footer variants -- used both to render the
// footer and as boxW's minimum width floor, so a narrow fleet never
// truncates its own keybinding help.
const agentsHelpText = " Arrows move  Space hide  Enter grab  p pin  P protect  / search  g group  A all  R reset  Esc close"

// groupFor computes the seed band for a pane name with no GroupOf entry yet,
// reusing roles.RoleFamilyOf's eng*/qa* prefix classification (already the
// project's one definition of "the eng family" / "the qa family", used by
// initech deliver's template selection) rather than re-deriving a parallel
// digit-suffix rule. FamilyOther and FamilyUnknown both fall to "core",
// matching the spec's "all remaining roles form core" exactly for the real
// role catalog (super, pm, shipper, pmm, growth, intern all lack a
// classified prefix and land in core, same as the digit-suffix reading
// would produce for today's fleet).
func groupFor(name string) string {
	switch roles.RoleFamilyOf(name) {
	case roles.FamilyEng:
		return "eng"
	case roles.FamilyQA:
		return "qa"
	default:
		return "core"
	}
}

// ensureGroups seeds GroupOf for any pane not yet assigned a band, and
// appends newly-introduced band labels to Groups in first-seen order (over
// t.panes, so a fresh seed's band order is deterministic run to run for the
// same fleet). Idempotent: a pane already in GroupOf is left untouched, so
// this never overwrites a manual grab. Call before any grid layout or
// navigation -- computing group membership from a stale/partial GroupOf is
// exactly the kind of previous-frame bug ini-2rc's spec calls out.
func (t *TUI) ensureGroups(persist bool) {
	if t.layoutState.GroupOf == nil {
		t.layoutState.GroupOf = make(map[string]string)
	}
	seen := make(map[string]bool, len(t.layoutState.Groups))
	for _, g := range t.layoutState.Groups {
		seen[g] = true
	}
	changed := false
	for _, p := range t.panes {
		key := paneKey(p)
		if _, ok := t.layoutState.GroupOf[key]; ok {
			continue
		}
		label := groupFor(p.Name())
		t.layoutState.GroupOf[key] = label
		changed = true
		if !seen[label] {
			t.layoutState.Groups = append(t.layoutState.Groups, label)
			seen[label] = true
			i7frLog("ensureGroups.appended", "label", label, "paneKey", key)
		}
	}
	if changed && persist {
		t.saveLayoutIfConfigured()
	}
}

// agentsGroupMembers returns, for each band label, the ordered list of
// indices into t.panes belonging to that band -- ordered index preserves
// t.panes' own relative order, which IS the within-band reading order (see
// LayoutState.Groups' doc comment: no separate per-band ordering exists).
// Panes with no GroupOf entry are defensively bucketed into "core" rather
// than dropped from the grid; ensureGroups should make this unreachable in
// normal operation, but a render must never silently lose an agent.
func (t *TUI) agentsGroupMembers() map[string][]int {
	members := make(map[string][]int)
	for i, p := range t.panes {
		label, ok := t.layoutState.GroupOf[paneKey(p)]
		if !ok {
			label = "core"
		}
		members[label] = append(members[label], i)
	}
	return members
}

// agentsGridPerRow computes cells-per-row: the widest band's member count,
// capped at gridMaxPerRow, further shrunk if the terminal itself is too
// narrow to fit that many 17-column cells (spec: "content-sized modal,
// never terminal-proportional" -- the cap comes from content first, the
// terminal-width shrink is a fallback for genuinely small terminals only).
func agentsGridPerRow(members map[string][]int, groups []string, screenW int) int {
	widest := 1
	for _, label := range groups {
		if n := len(members[label]); n > widest {
			widest = n
		}
	}
	perRow := widest
	if perRow > gridMaxPerRow {
		perRow = gridMaxPerRow
	}
	if fit := (screenW - 8 - 4) / gridCellW; fit < perRow && fit >= 1 {
		perRow = fit
	}
	return perRow
}

// gridCell is one occupied cell in the computed grid: which pane it shows
// and where it sits, both physically (x, y) and logically (line -- the
// global cell-row index used by vertical navigation, label/blank rows
// excluded so ↑↓ only ever lands on a cell row).
type gridCell struct {
	paneIdx int // index into t.panes
	group   string
	x, y    int
	line    int
}

// bandLabel is a rendered group rule: its label and the row it occupies.
type bandLabel struct {
	label string
	y     int
}

// tierLabel is a rendered monitor-tier header: the window it names and the row
// it occupies. Empty unless tiers are active (more than one window configured).
type tierLabel struct {
	windowID string
	index    int // 1-based monitor number as displayed.
	y        int
}

// lineInfo describes one navigable cell-line: which band owns it and whether
// that band is empty. Indexed by the global line number vertical navigation
// uses, so a line lookup is an index rather than a second walk.
type lineInfo struct {
	label   string
	isEmpty bool
}

// agentsGridGeometry is the SINGLE source of truth for modal geometry: every
// cell position, every band and tier label position, the per-line band map,
// and the total content height all come from one walk (ini-9ka.5).
//
// Before this, four sites independently re-derived the same per-band row
// accounting (rows = ceil(n/perRow), min 1): the cell layout, the line->band
// lookup used by vertical navigation, the box-height computation, and the
// render loop that drew band labels. Four places had to agree, and the
// reference PoC hit that exact multi-site-accounting bug twice before
// consolidating. Adding the tier level would have made it four sites across
// three hierarchy levels. Now there is nowhere else a row count or a y is
// computed, so a divergence between what is drawn and what is computed is not
// merely absent -- it cannot be written.
type agentsGridGeometry struct {
	cells        []gridCell
	bands        []bandLabel
	tiers        []tierLabel
	lines        []lineInfo
	contentLines int
}

// tierGroup pairs a window identity with the groups displayed under it, in
// render order. Built by agentsTierGroups; a single entry with an empty
// windowID and tiersActive=false is the single-window shape.
type tierGroup struct {
	windowID string
	groups   []string
}

// agentsGridWalk computes all modal geometry in one pass. innerX/firstY are
// the modal's interior origin and perRow is from agentsGridPerRow; callers
// that only need the height (box sizing) pass a zero origin and read
// contentLines, so height and positions can never come from different
// accounting.
//
// tiers carries the window grouping. When tiersActive is false it holds
// exactly one entry whose groups are rendered with no tier header, which is
// today's single-window layout byte-for-byte: no tier lead, no header row,
// identical band rhythm.
func agentsGridWalk(members map[string][]int, tiers []tierGroup, tiersActive bool, innerX, firstY, perRow int) agentsGridGeometry {
	var g agentsGridGeometry
	y := firstY + 1
	line := 0
	startY := y

	for ti, tg := range tiers {
		if tiersActive {
			y += gridTierLead
			g.tiers = append(g.tiers, tierLabel{windowID: tg.windowID, index: ti + 1, y: y})
			y++
		}
		for _, label := range tg.groups {
			y += gridBandLead
			g.bands = append(g.bands, bandLabel{label: label, y: y})

			agentIdxs := members[label]
			for ai, paneIdx := range agentIdxs {
				col := ai % perRow
				row := ai / perRow
				g.cells = append(g.cells, gridCell{
					paneIdx: paneIdx,
					group:   label,
					x:       innerX + col*gridCellW,
					y:       y + 1 + gridLabelGap + row,
					line:    line + row,
				})
			}

			rows := (len(agentIdxs) + perRow - 1) / perRow
			if rows < 1 {
				rows = 1
			}
			for r := 0; r < rows; r++ {
				g.lines = append(g.lines, lineInfo{label: label, isEmpty: len(agentIdxs) == 0})
			}
			y += 1 + gridLabelGap + rows
			line += rows
		}
	}

	g.contentLines = y - startY
	return g
}

// untieredTiers is the single-window tier shape: every group under one
// implicit window, no tier header. Used by the box-height path and by callers
// that predate tiers.
func untieredTiers(groups []string) []tierGroup {
	return []tierGroup{{windowID: WindowOne, groups: groups}}
}

// agentsGridLayoutCells returns just the cell positions for an untiered
// layout. A thin DELEGATE to agentsGridWalk, not a second accounting site --
// it exists so callers that only want cells need not destructure the full
// geometry.
func agentsGridLayoutCells(members map[string][]int, groups []string, innerX, firstY, perRow int) []gridCell {
	return agentsGridWalk(members, untieredTiers(groups), false, innerX, firstY, perRow).cells
}

// agentsTiersActive reports whether monitor tiers should render: only when
// more than one window is CONFIGURED (project.WindowListen non-empty, the
// ini-9ka.2 gate).
//
// Deliberately configuration, not live attach count. Gating on attachment
// would make tiers appear and disappear as windows connect, and during
// fold-back -- when window N is gone and its groups are temporarily rendered
// in window 1 -- the tier showing where those groups actually belong would
// vanish at exactly the moment the operator needs to see it. Configuration is
// also what makes the single-window zero-change guarantee structural: an
// empty WindowListen is the only state a single-window fleet is ever in, so
// this returns false and the walk emits today's layout unchanged.
func (t *TUI) agentsTiersActive() bool {
	active := t.project != nil && participatesInMultiWindow(t.project)
	i7frLog("tiers.gate", "projectNil", t.project == nil, "active", active)
	return active
}

// agentsWindowOrder returns the window identities to render as tiers, in a
// stable order: window 1 first, then any other window that owns at least one
// group, sorted. Sorted rather than first-seen so the tier order cannot shift
// between frames as group membership changes.
func agentsWindowOrder(assign *WindowAssignment, groups []string) []string {
	order := []string{WindowOne}
	seen := map[string]bool{WindowOne: true}
	var others []string
	for _, g := range groups {
		w := assign.WindowOfGroup(g)
		if !seen[w] {
			seen[w] = true
			others = append(others, w)
		}
	}
	sort.Strings(others)
	return append(order, others...)
}

// agentsTierGroups partitions groups by window for rendering. When tiers are
// inactive it returns a single untiered entry holding every group in its
// existing order -- the shape that reproduces today's single-window layout
// exactly, so the untiered path is the same code path rather than a parallel
// one that has to be kept in sync.
func (t *TUI) agentsTierGroups(assign *WindowAssignment, tiersActive bool) []tierGroup {
	groups := t.layoutState.Groups
	if !tiersActive || assign == nil {
		i7frLog("tiers.derive", "tiersActive", tiersActive, "assignNil", assign == nil,
			"universe", i7frKeys(groups), "result", "single-untiered-windowOne")
		return []tierGroup{{windowID: WindowOne, groups: groups}}
	}
	var out []tierGroup
	for _, w := range agentsWindowOrder(assign, groups) {
		g := assign.GroupsForWindow(w, groups)
		i7frLog("tiers.derive.window", "window", w, "groups", i7frKeys(g))
		out = append(out, tierGroup{windowID: w, groups: g})
	}
	i7frLog("tiers.derive", "tiersActive", true, "universe", i7frKeys(groups), "tierCount", len(out))
	return out
}

// agentsAssignment returns the group-to-window assignment store (ini-9ka.4),
// loading it once per session and caching it. A project with no root (tests,
// ad-hoc TUIs) or an unreadable store yields an empty assignment, in which
// every group is on window 1 -- so the modal degrades to the single-window
// arrangement rather than failing to render.
func (t *TUI) agentsAssignment() *WindowAssignment {
	if t.assignment != nil {
		return t.assignment
	}
	a, err := LoadAssignment(t.projectRoot)
	if err != nil {
		// READ-ONLY fallback (ini-9ka.9). The store is unreadable, not
		// absent -- the operator's real arrangement is still in that file,
		// merely unparseable. A writable fallback would replace it with a
		// near-empty one on the next move, turning a recoverable parse error
		// into silent erasure. Reads still work and report everything on
		// window 1, which is the correct degraded view.
		LogWarn("agents", "assignment store unreadable, treating all groups as window 1 and refusing writes", "err", err)
		a = newFallbackAssignment(t.projectRoot)
	}
	t.assignment = a
	return t.assignment
}

// agentsFrameGeometry computes the box and the one-walk geometry for the
// current frame. Every consumer -- render, navigation, cell lookup -- goes
// through here, so they cannot disagree about where anything is (ini-9ka.5).
func (t *TUI) agentsFrameGeometry(sw, sh int, searching bool) (agentsGridBox, agentsGridGeometry) {
	members := t.agentsGroupMembers()
	tiersActive := t.agentsTiersActive()
	tiers := t.agentsTierGroups(t.agentsAssignment(), tiersActive)
	box := agentsGridBoxDims(members, t.layoutState.Groups, tiers, tiersActive, sw, sh, searching)
	geo := agentsGridWalk(members, tiers, tiersActive, box.innerX, box.startY+box.searchRows, box.perRow)
	return box, geo
}

// agentsCellForPane returns the cell for the given t.panes index, or nil if
// not present (shouldn't happen for a valid index, since every pane gets a
// cell -- defensive, matching the PoC's cellAt).
func agentsCellForPane(cells []gridCell, paneIdx int) *gridCell {
	for i := range cells {
		if cells[i].paneIdx == paneIdx {
			return &cells[i]
		}
	}
	return nil
}

// agentsLineBand walks the same per-band line/row accounting
// agentsGridLayoutCells uses (each band reserves ceil(n/perRow) lines,
// minimum 1 even when n==0) and reports which band owns the given line and
// whether that band is empty. A band with zero members still reserves
// exactly one line -- visible in the grid as a label with a blank row
// beneath it -- but agentsGridLayoutCells emits no gridCell for it, so
// agentsMoveV's normal cell scan can never find a landing point there on
// its own. This is the lookup that lets it recognize "this line is real,
// it's just an empty band" instead of treating the line as unreachable.
// It is now a pure INDEX into the walk's per-line output rather than a second
// walk of its own -- the accounting lives in agentsGridWalk only (ini-9ka.5).
func agentsLineBand(geo agentsGridGeometry, targetLine int) (label string, isEmpty bool, ok bool) {
	if targetLine < 0 || targetLine >= len(geo.lines) {
		return "", false, false
	}
	li := geo.lines[targetLine]
	return li.label, li.isEmpty, true
}

// agentsFlatInsertionForEmptyBand returns the t.panes index at which a sole
// new member of an empty band should be inserted, so that filtering the
// resulting flat order by band (agentsGroupMembers) places it correctly:
// immediately after the last member of the nearest earlier band in Groups
// order that actually has one, or at position 0 if no earlier band does.
func (t *TUI) agentsFlatInsertionForEmptyBand(members map[string][]int, targetLabel string) int {
	insertAt := 0
	for _, g := range t.layoutState.Groups {
		if g == targetLabel {
			break
		}
		if idxs := members[g]; len(idxs) > 0 {
			insertAt = idxs[len(idxs)-1] + 1
		}
	}
	return insertAt
}

// ---------- search ----------

// agentsMatched reports whether the pane at paneIdx matches the current
// search buffer. Empty buffer (or not searching) matches everything. Names
// match by case-insensitive substring; pane numbers by prefix, so typing
// "1" reaches pane 1 and 10-19 before a second digit disambiguates.
func (t *TUI) agentsMatched(paneIdx int) bool {
	if !t.agents.searching || len(t.agents.searchBuf) == 0 {
		return true
	}
	q := strings.ToLower(string(t.agents.searchBuf))
	p := t.panes[paneIdx]
	// Same number the cell displays (the FLEET number, ini-6m4) -- search and
	// display disagreeing on what "3" means would be worse than either bug.
	return strings.Contains(strings.ToLower(p.Name()), q) ||
		strings.HasPrefix(strconv.Itoa(fleetIdxOf(p, paneIdx)+1), q)
}

// agentsMatchCells returns indices into cells (grid order) whose pane
// matches the current search. Computed from the cells slice passed in, never
// cached -- see the package doc: a stale cell list is exactly the bug this
// spec calls out by name.
func (t *TUI) agentsMatchCells(cells []gridCell) []int {
	var out []int
	for i, c := range cells {
		if t.agentsMatched(c.paneIdx) {
			out = append(out, i)
		}
	}
	return out
}

// agentsEnsureMatchSelected snaps the selection to the first match if the
// current selection no longer matches (buffer just changed).
func (t *TUI) agentsEnsureMatchSelected(cells []gridCell) {
	if t.agents.selected >= 0 && t.agents.selected < len(t.panes) && t.agentsMatched(t.agents.selected) {
		return
	}
	if mc := t.agentsMatchCells(cells); len(mc) > 0 {
		t.agents.selected = cells[mc[0]].paneIdx
	}
}

// agentsMatchNav steps the selection through matches in grid order.
func (t *TUI) agentsMatchNav(cells []gridCell, delta int) {
	mc := t.agentsMatchCells(cells)
	if len(mc) == 0 {
		return
	}
	cur := -1
	for pos, ci := range mc {
		if cells[ci].paneIdx == t.agents.selected {
			cur = pos
			break
		}
	}
	next := cur + delta
	if cur == -1 {
		next = 0
	}
	if next < 0 {
		next = 0
	}
	if next > len(mc)-1 {
		next = len(mc) - 1
	}
	t.agents.selected = cells[mc[next]].paneIdx
}

// ---------- navigation + grab ----------

// agentsMoveH moves the selection (or, while grabbed, the agent itself)
// left/right within its band. Grabbing swaps the two panes' positions in
// t.panes directly: since within-band order IS t.panes' relative order (no
// separate per-band list), swapping adjacent same-band panes in t.panes is
// exactly a same-band cell swap.
func (t *TUI) agentsMoveH(delta int) {
	t.ensureGroups(true)
	members := t.agentsGroupMembers()
	sel := t.agents.selected
	if sel < 0 || sel >= len(t.panes) {
		return
	}
	label := t.layoutState.GroupOf[paneKey(t.panes[sel])]
	band := members[label]
	pos := -1
	for i, pi := range band {
		if pi == sel {
			pos = i
			break
		}
	}
	if pos < 0 {
		return
	}
	npos := pos + delta
	if npos < 0 || npos >= len(band) {
		return
	}
	other := band[npos]
	if t.agents.moving {
		t.panes[sel], t.panes[other] = t.panes[other], t.panes[sel]
		t.agentsPersistOrder()
	}
	t.agents.selected = other
}

// agentsMoveV moves to the nearest cell (by x-distance) on the adjacent
// visual line, across band boundaries. While grabbed, crossing into a
// different band's line reassigns the agent's group -- this is the spec's
// "grab across bands edits group membership" mechanism: moveV both splices
// t.panes (so the new band's relative order includes the agent at the right
// spot) and reassigns GroupOf.
func (t *TUI) agentsMoveV(cells []gridCell, delta int) {
	sel := t.agents.selected
	if sel < 0 || sel >= len(t.panes) {
		return
	}
	cur := agentsCellForPane(cells, sel)
	if cur == nil {
		return
	}
	targetLine := cur.line + delta
	var best *gridCell
	bestDist := 1 << 30
	for i := range cells {
		c := &cells[i]
		if c.line != targetLine {
			continue
		}
		d := c.x - cur.x
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	if best == nil {
		// The normal scan finds nothing when targetLine belongs to an empty
		// band: agentsGridLayoutCells reserves the line (so it renders, with
		// a label and a blank row) but emits no gridCell for it, since there
		// are no members to place. Plain navigation has nothing to select
		// there, so it stays put. Grabbed, this is the only way to populate
		// a freshly-created group at all -- without it, 'g' can create a
		// band the shipped UI can never put an agent into.
		if !t.agents.moving || t.screen == nil {
			return
		}
		// Recompute perRow via the same agentsGridBoxDims every other caller
		// uses (render, agentsCurrentCells) -- not a second formula, the
		// same one, so this can't drift from what built `cells` in the
		// first place.
		members := t.agentsGroupMembers()
		sw, sh := t.screen.Size()
		_, geo := t.agentsFrameGeometry(sw, sh, t.agents.searching || t.agents.creatingGroup)
		label, isEmpty, ok := agentsLineBand(geo, targetLine)
		if !ok || !isEmpty {
			return
		}
		ag := t.panes[sel]
		insertAt := t.agentsFlatInsertionForEmptyBand(members, label)
		t.panes = append(t.panes[:sel], t.panes[sel+1:]...)
		if insertAt > sel {
			insertAt--
		}
		if insertAt > len(t.panes) {
			insertAt = len(t.panes)
		}
		t.panes = append(t.panes[:insertAt], append([]PaneView{ag}, t.panes[insertAt:]...)...)
		t.agents.selected = insertAt
		t.layoutState.GroupOf[paneKey(ag)] = label
		t.agentsPersistOrder()
		return
	}
	if !t.agents.moving {
		t.agents.selected = best.paneIdx
		return
	}

	// Grabbed: splice sel out of t.panes and back in at the destination
	// cell's own (post-removal) position, matching the PoC's moveV exactly
	// -- the moved agent takes over the destination's old slot, pushing the
	// destination and everything after it in that band one position later.
	ag := t.panes[sel]
	destPaneIdx := best.paneIdx
	t.panes = append(t.panes[:sel], t.panes[sel+1:]...)
	insertAt := destPaneIdx
	if destPaneIdx > sel {
		insertAt--
	}
	if insertAt > len(t.panes) {
		insertAt = len(t.panes)
	}
	t.panes = append(t.panes[:insertAt], append([]PaneView{ag}, t.panes[insertAt:]...)...)
	t.agents.selected = insertAt
	t.layoutState.GroupOf[paneKey(ag)] = best.group
	t.agentsPersistOrder()
}

// ---------- group lifecycle ----------

// agentsCreateGroup inserts a new, empty band labeled name immediately
// after the band containing the current selection (spec: "the new (empty)
// band appears after the current one"). A blank/whitespace-only name is
// rejected by the caller before this is invoked.
func (t *TUI) agentsCreateGroup(name string) {
	t.ensureGroups(false)
	afterIdx := len(t.layoutState.Groups) - 1
	if sel := t.agents.selected; sel >= 0 && sel < len(t.panes) {
		curLabel := t.layoutState.GroupOf[paneKey(t.panes[sel])]
		for i, g := range t.layoutState.Groups {
			if g == curLabel {
				afterIdx = i
				break
			}
		}
	}
	// Capture the selection's window BEFORE mutating Groups: the new band
	// lands on the window the selection was in at creation time (ini-9ka.5's
	// grooming decision -- "you create where you are"). Read first because
	// the lookup goes through GroupOf, which the splice below does not touch
	// but which a future edit here easily could.
	targetWindow := t.agentsSelectedWindow()

	groups := make([]string, 0, len(t.layoutState.Groups)+1)
	groups = append(groups, t.layoutState.Groups[:afterIdx+1]...)
	groups = append(groups, name)
	groups = append(groups, t.layoutState.Groups[afterIdx+1:]...)
	t.layoutState.Groups = groups
	t.saveLayoutIfConfigured()

	// Only non-default windows need a stored row; window 1 is absence
	// (ini-9ka.4), so creating on window 1 correctly writes nothing.
	if targetWindow != WindowOne {
		if err := t.agentsAssignment().MoveGroup(name, targetWindow); err != nil {
			t.noticeAssignmentWriteFailed("assign new group "+name, err)
			LogWarn("agents", "assigning new group to the selection's window failed",
				"group", name, "window", targetWindow, "err", err)
		}
	}
}

// agentsSelectedWindow returns the window the current selection's group is
// assigned to, or window 1 when there is no valid selection.
func (t *TUI) agentsSelectedWindow() string {
	sel := t.agents.selected
	if sel < 0 || sel >= len(t.panes) {
		return WindowOne
	}
	return t.agentsAssignment().WindowOfAgent(paneKey(t.panes[sel]), t.layoutState.GroupOf)
}

// agentsMoveGroupToNextWindow implements `m`: move the selected agent's WHOLE
// group to the next window, cycling through the configured windows when there
// are more than two (spec: "cycles through windows if N>2").
//
// The window list is the tier order plus one slot past the last, so a group
// can always be pushed onto a window that has no groups yet -- otherwise the
// very first move would have nowhere to go and `m` would be inert on a fresh
// two-window fleet. Persists immediately via MoveGroup (ini-9ka.4).
func (t *TUI) agentsMoveGroupToNextWindow() {
	if !t.agentsTiersActive() {
		return // Single window: nothing to move between.
	}
	sel := t.agents.selected
	if sel < 0 || sel >= len(t.panes) {
		return
	}
	group, ok := t.layoutState.GroupOf[paneKey(t.panes[sel])]
	if !ok || group == "" {
		return
	}

	assign := t.agentsAssignment()
	windows := agentsWindowOrder(assign, t.layoutState.Groups)

	// A brand-new window is offered ONLY when the group is currently on
	// window 1. Offering one from every window would make the cycle
	// unbounded and, worse, non-cycling: moving the last group off a window
	// makes that window disappear from the order, so a fresh slot would be
	// re-offered under a recycled name each press and the group would
	// ping-pong between two new windows without ever returning to window 1.
	// Push-out-from-window-1, then cycle through the windows that exist and
	// back to 1, is bounded and predictable.
	cur := assign.WindowOfGroup(group)
	if cur == WindowOne {
		windows = append(windows, agentsNextWindowID(windows))
	}

	idx := 0
	for i, w := range windows {
		if w == cur {
			idx = i
			break
		}
	}
	next := windows[(idx+1)%len(windows)]
	if err := assign.MoveGroup(group, next); err != nil {
		t.noticeAssignmentWriteFailed("move group "+group, err)
		LogWarn("agents", "move group to next window failed", "group", group, "window", next, "err", err)
		return
	}
	// Monitor number for the notice: position in the tier order, recomputed
	// AFTER the move so a brand-new window (appended past the end) gets the
	// number the tiers will actually display for it.
	t.noticeGroupMoved(group, next, agentsWindowOrder(assign, t.layoutState.Groups))
	// RE-LAY OUT NOW (ini-xq4r): the move changed which panes this window
	// renders, and nothing else triggers a layout in grid mode -- without this
	// the store, the modal and the notice all update while the PANES stay
	// where they were until some unrelated event re-plans. This is the half
	// of the live bug the unit tests could not see: they called the predicate
	// directly, so the missing trigger between "store changed" and "predicate
	// consulted" was invisible. Same-frame with the notice, which is what
	// AC 4's mid-glance rule promises.
	t.recalcGrid(false)
	t.applyLayout()
}

// noticeGroupMoved raises the exactly-one session notice per assignment move
// (ini-xq4r AC 4): a group leaving the window the operator is watching must be
// explained in the moment, or panes silently vanish mid-glance. Session-level
// (no pane attached) and fanned out to every attached window, the same shape
// as the fold-back notices -- the notice renders where the panes disappeared
// FROM, not only where the move was made.
func (t *TUI) noticeGroupMoved(group, dest string, order []string) {
	monitor := 0
	for i, w := range order {
		if w == dest {
			monitor = i + 1
			break
		}
	}
	detail := fmt.Sprintf("%s → monitor %d", group, monitor)
	EmitEvent(t.agentEvents, AgentEvent{Type: EventGroupMoved, Detail: detail, Time: time.Now()})
	t.windowSrv.broadcastSessionNotice(detail)
}

// agentsNextWindowID returns an identity for a window that does not exist yet,
// so `m` can create the second (or Nth) window's assignment. Numeric-suffixed
// and validated by the same canonical rule every other window identity uses.
func agentsNextWindowID(existing []string) string {
	for n := 2; ; n++ {
		// WindowPeerName, NEVER a local "window"+n: the synthesized identity
		// must be the one a viewer launched with --window N actually presents,
		// or the assignment names a window that can never attach (ini-xq4r).
		candidate := WindowPeerName(n)
		taken := false
		for _, w := range existing {
			if w == candidate {
				taken = true
				break
			}
		}
		if !taken {
			return candidate
		}
	}
}

// agentsPruneEmptyGroups removes bands with zero members (spec: "a group
// empty when the modal closes is removed"). Applied on modal close AND on
// LoadLayout (see layout.go) -- the same invariant enforced at both points
// a band could end up empty, not just the close-time case.
//
// Also clears the pruned band's window assignment (ini-9ka.5). Dropping the
// label from Groups without clearing its assignment would leave a
// group->window row for a group that no longer exists, which would silently
// resurface if that label were ever recreated -- a group reappearing on a
// window the operator never put it on. This is the empty-on-close half of the
// g-create rule, and it applies on every window, not just window 1.
func (t *TUI) agentsPruneEmptyGroups() {
	if len(t.layoutState.Groups) == 0 {
		return
	}
	members := t.agentsGroupMembers()
	var kept []string
	var pruned []string
	for _, g := range t.layoutState.Groups {
		if len(members[g]) > 0 {
			kept = append(kept, g)
		} else {
			pruned = append(pruned, g)
		}
	}
	if len(pruned) == 0 {
		return
	}
	t.layoutState.Groups = kept
	t.saveLayoutIfConfigured()

	assign := t.agentsAssignment()
	for _, g := range pruned {
		if assign.WindowOfGroup(g) == WindowOne {
			continue // Nothing stored for a window-1 group.
		}
		if err := assign.MoveGroup(g, WindowOne); err != nil {
			LogWarn("agents", "clearing assignment for pruned group failed", "group", g, "err", err)
		}
	}
}

// ---------- rendering ----------

// agentsGridBox is the computed geometry of the modal's floating box --
// the one source of truth for box position/size, shared by renderAgentsGrid
// and agentsCurrentCells (and tests) so they can never compute two different
// answers for "where is this cell on screen" the way a duplicated formula
// silently drifted during this bead's own development.
type agentsGridBox struct {
	perRow     int
	innerX     int
	boxW       int
	boxH       int
	startX     int
	startY     int
	searchRows int
}

// agentsGridBoxDims computes the modal's box geometry for the given members/
// groups/screen size/search-or-create-active state. searching covers both
// the / search bar and the g group-creation prompt -- both take the same
// one-row slot under the top border.
func agentsGridBoxDims(members map[string][]int, groups []string, tiers []tierGroup, tiersActive bool, sw, sh int, searching bool) agentsGridBox {
	perRow := agentsGridPerRow(members, groups, sw)
	if perRow < 1 {
		perRow = 1
	}
	innerW := perRow * gridCellW
	boxW := innerW + 4
	// Floor boxW at the footer text's own width plus margin: a fleet small
	// enough to need only 1-2 cells per row (e.g. a 1-2 agent dev project)
	// would otherwise produce a box too narrow to show its own keybinding
	// help -- content-sized means "no bigger than needed", not "so small the
	// footer clips its own Esc-close hint". The PoC's fixed 23-agent, 6-wide
	// test fleet never exercises this; not a load-bearing spec value.
	if footerFloor := len(agentsHelpText) + 4; footerFloor > boxW {
		boxW = footerFloor
	}
	if boxW > sw {
		boxW = sw
	}

	// Height comes from the SAME walk that produces positions, at a zero
	// origin since only the total matters here. Previously this re-derived
	// the per-band row accounting independently, which is the divergence
	// ini-9ka.5's one-walk AC exists to make unrepresentable.
	contentLines := agentsGridWalk(members, tiers, tiersActive, 0, 0, perRow).contentLines
	searchRows := 0
	if searching {
		searchRows = 1
	}
	contentLines += searchRows
	boxH := contentLines + 4 // top border, pad-to-content, footer, bottom border
	if boxH > sh-2 {
		boxH = sh - 2
	}
	if boxH < 6 {
		boxH = 6
	}

	startX := (sw - boxW) / 2
	startY := (sh - boxH) / 2
	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}

	return agentsGridBox{
		perRow:     perRow,
		innerX:     startX + 2,
		boxW:       boxW,
		boxH:       boxH,
		startX:     startX,
		startY:     startY,
		searchRows: searchRows,
	}
}

// renderAgentsGrid draws the grouped 2-D grid modal. Geometry and styling
// port the approved PoC's draw() (~/Desktop/agent-grid-poc/main.go) exactly:
// content-sized box, gridMaxPerRow cap, the exact blank/label/blank/cells
// rhythm, and the same RGB(20,20,20) surface / DodgerBlue title / DarkBlue
// selection register the flat modal already used.
func (t *TUI) renderAgentsGrid() {
	s := t.screen
	sw, sh := s.Size()

	t.ensureGroups(true)
	box, geo := t.agentsFrameGeometry(sw, sh, t.agents.searching || t.agents.creatingGroup)
	boxW, boxH, startX, startY := box.boxW, box.boxH, box.startX, box.startY

	bgStyle := tcell.StyleDefault.Background(tcell.NewRGBColor(20, 20, 20)).Foreground(tcell.ColorSilver)
	borderStyle := bgStyle.Foreground(tcell.ColorGray)
	titleStyle := bgStyle.Foreground(tcell.ColorDodgerBlue).Bold(true)
	labelStyle := bgStyle.Foreground(tcell.ColorGray)
	// Tier headers take the title's DodgerBlue per the PoC/spec, so the
	// hierarchy reads monitor -> group -> agent by weight as well as by rule.
	tierStyle := bgStyle.Foreground(tcell.ColorDodgerBlue).Bold(true)
	selectedStyle := tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite)
	movingStyle := tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorWhite).Bold(true)
	helpStyle := bgStyle.Foreground(tcell.ColorGray)
	pinStyle := bgStyle.Foreground(tcell.ColorMediumPurple).Bold(true)
	protStyle := bgStyle.Foreground(tcell.ColorSilver)
	searchStyle := bgStyle.Foreground(tcell.ColorYellow)
	dimStyle := bgStyle.Foreground(tcell.NewRGBColor(70, 70, 70))

	for y := startY; y < startY+boxH && y < sh; y++ {
		for x := startX; x < startX+boxW && x < sw; x++ {
			s.SetContent(x, y, ' ', nil, bgStyle)
		}
	}
	s.SetContent(startX, startY, '┌', nil, borderStyle)
	s.SetContent(startX+boxW-1, startY, '┐', nil, borderStyle)
	s.SetContent(startX, startY+boxH-1, '└', nil, borderStyle)
	s.SetContent(startX+boxW-1, startY+boxH-1, '┘', nil, borderStyle)
	for x := startX + 1; x < startX+boxW-1 && x < sw; x++ {
		s.SetContent(x, startY, '─', nil, borderStyle)
		s.SetContent(x, startY+boxH-1, '─', nil, borderStyle)
	}
	for y := startY + 1; y < startY+boxH-1 && y < sh; y++ {
		s.SetContent(startX, y, '│', nil, borderStyle)
		s.SetContent(startX+boxW-1, y, '│', nil, borderStyle)
	}

	title := " initech agents "
	if t.agents.moving && t.agents.selected >= 0 && t.agents.selected < len(t.panes) {
		title = fmt.Sprintf(" moving %s ", t.panes[t.agents.selected].Name())
	}
	if t.agents.creatingGroup {
		title = " new group "
	}
	tx := startX + (boxW-len([]rune(title)))/2
	for i, ch := range title {
		if tx+i >= startX+1 && tx+i < startX+boxW-1 {
			s.SetContent(tx+i, startY, ch, nil, titleStyle)
		}
	}

	innerX := box.innerX
	// Geometry is computed fresh every render call by agentsFrameGeometry --
	// this frame's members/perRow, never a value cached from the previous
	// frame. The no-matches verdict below reads THIS cells slice, from the
	// same walk, which is the fix for the exact bug the spec names (ini-2rc).
	cells := geo.cells
	t.agentsEnsureMatchSelected(cells)

	if t.agents.creatingGroup {
		bar := fmt.Sprintf(" g %s_", string(t.agents.groupNameBuf))
		x := innerX - 1
		for _, ch := range bar {
			if x >= startX+1 && x < startX+boxW-1 {
				s.SetContent(x, startY+1, ch, nil, searchStyle)
			}
			x++
		}
	} else if t.agents.searching {
		bar := fmt.Sprintf(" / %s_", string(t.agents.searchBuf))
		x := innerX - 1
		for _, ch := range bar {
			if x >= startX+1 && x < startX+boxW-1 {
				s.SetContent(x, startY+1, ch, nil, searchStyle)
			}
			x++
		}
		if len(t.agents.searchBuf) > 0 && len(t.agentsMatchCells(cells)) == 0 {
			for _, ch := range "   no matches" {
				if x >= startX+1 && x < startX+boxW-1 {
					s.SetContent(x, startY+1, ch, nil, labelStyle)
				}
				x++
			}
		}
	}

	// Tier headers and band labels are drawn at the positions the ONE walk
	// computed. No y is advanced here: this loop reads geometry, it does not
	// re-derive it, which is what makes a drawn/computed divergence
	// unrepresentable rather than merely absent (ini-9ka.5).
	for _, tl := range geo.tiers {
		lab := fmt.Sprintf("══ monitor %d ", tl.index)
		x := innerX
		for _, ch := range lab {
			if x < startX+boxW-1 {
				s.SetContent(x, tl.y, ch, nil, tierStyle)
			}
			x++
		}
		for ; x < startX+boxW-2; x++ {
			s.SetContent(x, tl.y, '═', nil, tierStyle)
		}
	}
	for _, bl := range geo.bands {
		lab := fmt.Sprintf("─ %s ", bl.label)
		x := innerX
		for _, ch := range lab {
			if x < startX+boxW-1 {
				s.SetContent(x, bl.y, ch, nil, labelStyle)
			}
			x++
		}
		for ; x < startX+boxW-2; x++ {
			s.SetContent(x, bl.y, '─', nil, labelStyle)
		}
	}

	for _, c := range cells {
		p := t.panes[c.paneIdx]
		pk := paneKey(p)
		isSel := c.paneIdx == t.agents.selected
		dimmed := t.agents.searching && !t.agentsMatched(c.paneIdx)
		hidden := t.layoutState.Hidden[pk]
		protected := t.layoutState.Protected[pk]
		_, livePinned := t.layoutState.LivePinned[pk]
		liveDisplayed := false
		if !livePinned && t.layoutState.Mode == LayoutLive {
			for _, sn := range t.layoutState.LiveSlots {
				if sn == pk {
					liveDisplayed = true
					break
				}
			}
		}

		nameStyle := bgStyle.Foreground(tcell.ColorSilver) // idle
		switch p.Activity() {
		case StateRunning:
			nameStyle = bgStyle.Foreground(tcell.ColorGreen)
		case StateDead:
			nameStyle = bgStyle.Foreground(tcell.ColorGray)
		}
		numStyle := bgStyle.Foreground(tcell.ColorSilver)
		boxStyle := bgStyle.Foreground(tcell.ColorSilver)
		pStyle := pinStyle
		prStyle := protStyle
		if hidden {
			nameStyle = nameStyle.Italic(true).Foreground(tcell.ColorGray)
		}
		if dimmed {
			nameStyle, numStyle, boxStyle, pStyle, prStyle = dimStyle, dimStyle, dimStyle, dimStyle, dimStyle
			if hidden {
				nameStyle = dimStyle.Italic(true)
			}
		}
		if isSel {
			base := selectedStyle
			if t.agents.moving {
				base = movingStyle
			}
			nameStyle = base
			if p.Activity() == StateRunning {
				nameStyle = base.Bold(true)
			}
			if hidden {
				nameStyle = nameStyle.Italic(true)
			}
			numStyle, boxStyle = base, base
			pStyle = base.Foreground(tcell.ColorWhite).Bold(true)
			prStyle = base
			for i := 0; i < gridCellW-1; i++ {
				s.SetContent(c.x+i, c.y, ' ', nil, base)
			}
		}

		vis := "[x]"
		if hidden {
			vis = "[ ]"
		}
		x := c.x
		put := func(str string, st tcell.Style) {
			for _, ch := range str {
				if x < startX+boxW-1 {
					s.SetContent(x, c.y, ch, nil, st)
				}
				x++
			}
		}
		put(fmt.Sprintf("%3d ", fleetIdxOf(t.panes[c.paneIdx], c.paneIdx)+1), numStyle)
		put(vis+" ", boxStyle)
		put(p.Name(), nameStyle)
		// Pin/slot marker: '*' for an explicit live pin (matches the flat
		// modal's [P] semantics), '◦' for live-mode auto-displayed-but-
		// unpinned (today's D:N). Exact glyph is an implementation choice
		// within the spec's 17-column budget -- see agents_grid.go's doc and
		// the DONE comment on ini-2rc: a 2-digit slot number does not fit
		// alongside a long name, pin, and protect marker within 17 columns
		// without breaking column alignment across bands, so the specific
		// slot index is not shown here (the live grid itself shows it).
		if livePinned {
			put("*", pStyle)
		} else if liveDisplayed {
			put("◦", pStyle)
		}
		if protected {
			put("•", prStyle)
		}
	}

	errorStyle := bgStyle.Foreground(tcell.ColorRed)
	if t.agents.error != "" {
		errY := startY + boxH - 3
		ex := innerX - 1
		for _, ch := range " " + t.agents.error {
			if ex >= startX+1 && ex < startX+boxW-1 {
				s.SetContent(ex, errY, ch, nil, errorStyle)
			}
			ex++
		}
	}

	help := agentsHelpText
	if t.agents.searching {
		help = " type to filter  Arrows next/prev match  Space hide  Enter keep  Esc cancel"
	}
	if t.agents.creatingGroup {
		help = " type a name  Enter create  Esc cancel"
	}
	hy := startY + boxH - 2
	hx := innerX - 1
	for i, ch := range help {
		if hx+i >= startX+1 && hx+i < startX+boxW-1 {
			s.SetContent(hx+i, hy, ch, nil, helpStyle)
		}
	}
}
