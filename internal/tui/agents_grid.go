// agents_grid.go implements the grouped 2-D grid agents modal (ini-2rc,
// transposed by ini-w771), replacing the flat Alt+a list. A GROUP IS A COLUMN:
// its agents stack top-to-bottom, one per row, under a "─ name ───" header
// drawn at column width. A MONITOR IS A BAND: "══ monitor N ══" headers stack
// top-to-bottom, each holding its groups side by side as columns; a band is as
// tall as its tallest group. When a band has more than gridMaxPerRow groups the
// rest start a second column-row inside the band, one blank line between.
// Remote machines render as their own bands, one column each.
//
// Design is spec-pinned (pm/specs/agents-grid-modal.md, section "Orientation
// reversal", workspace commit 7d7cb8b9 -- operator decision 2026-09-12 from an
// approved ASCII mockup; the superseded horizontal layout is kept on record
// there). Cell format, styles, search, keys and monitors-as-bands are the
// shipped ones; only the orientation and the arrow semantics turned.
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

// gridCellW is " NN [x] name*•  " -- uniform per frame so columns align across
// bands, but SIZED to the longest display name each frame instead of a fixed
// 17: host-qualified names (support:super) were silently clipped at the cell
// boundary (operator-observed 2026-08-15), and a name the operator cannot read
// is a disclosure the modal is not making. Recomputed at the top of
// agentsFrameGeometry -- the one walk -- so render, hit-testing and search all
// agree on the same cell width.
var gridCellW = 17

const gridCellWMin = 17

// agentsGridCellWFor sizes the cell (and therefore the column) to the longest
// display name AND the longest group label: a column header "─ name ─" wider
// than its column would bleed into the neighbour column's header, so a long
// group name widens every column the same way a long agent name does.
func agentsGridCellWFor(panes []PaneView, groups []string) int {
	w := gridCellWMin
	for _, p := range panes {
		// "%3d " (4) + "[x] " (4) + name + pin/protect markers (2) — which
		// reproduces the historical 17 exactly for a 7-char name (shipper),
		// so short fleets render byte-identically to the golden.
		if need := 8 + len(paneDisplayName(p)) + 2; need > w {
			w = need
		}
	}
	for _, g := range groups {
		// "─ " (2) + label + " " (1) + one column gutter (1).
		if need := len([]rune(g)) + 4; need > w {
			w = need
		}
	}
	return w
}

// Grid layout constants (spec section "Layout rules", operator-tuned).
const (
	gridMaxPerRow = 6 // Columns (groups) per column-row; more wrap into a second column-row inside the band.
	gridBandLead  = 1 // Blank line before each column-row's header row.
	gridLabelGap  = 1 // Blank line between the header row and its cell rows.
	gridTierLead  = 1 // Blank line before each monitor-tier header (ini-9ka.5).
)

// agentsHelpText is the default (non-searching, non-group-creating) footer.
// The longest of the modal's footer variants -- used both to render the
// footer and as boxW's minimum width floor, so a narrow fleet never
// truncates its own keybinding help.
// Width budget: boxW floors at len(agentsHelpText)+4 and caps at the screen,
// so this line must stay ≤ ~114 chars or "Esc close" clips on a 120-col
// terminal. "s/S suspend" = s parks/wakes the agent, S its whole band.
const agentsHelpText = " Arrows move  Space hide  Enter grab  p pin  P protect  s/S suspend  / search  g group  A all  R reset  Esc close"

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
		key := agentKey(p)
		if paneIsRemoteMachine(p) {
			// READ half of "machine sections are derived, never persisted":
			// a group entry for a remote pane — hand-placed via grab-move or
			// stale from before the write half — routes it to a window that
			// has no stream for it, and it renders NOWHERE (support:pm,
			// 2026-08-15). Purge on sight, not just skip.
			if _, ok := t.layoutState.GroupOf[key]; ok {
				delete(t.layoutState.GroupOf, key)
				changed = true
			}
			continue
		}
		if existing, ok := t.layoutState.GroupOf[key]; ok {
			// DEAD-GROUP RULE, runtime half (ini-qkwc AC3): an entry pointing
			// at a group missing from Groups must surface the group, not
			// swallow the agent -- the modal renders bands from Groups, so a
			// referenced-but-unlisted label made its members vanish from the
			// viewer while window 1 showed them.
			if !seen[existing] {
				t.layoutState.Groups = append(t.layoutState.Groups, existing)
				seen[existing] = true
				changed = true
			}
			continue
		}
		label := groupFor(p.Name())
		t.layoutState.GroupOf[key] = label
		changed = true
		if !seen[label] {
			t.layoutState.Groups = append(t.layoutState.Groups, label)
			seen[label] = true
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
//
// Members are SCOPED to this window unless the modal is expanded (ini-9isx
// AC5). The scope is decided by visiblePanesForWindow -- the same primitive the
// layout and the overlay use -- so all three surfaces answer "whose agent is
// this" identically rather than each deriving it.
//
// Indices remain indices into t.panes, deliberately: selection, moves, and
// fleet numbering all address panes by that index, and re-basing them onto a
// filtered slice would make every one of those an off-by-scope bug waiting for
// the first group to move.
func (t *TUI) agentsGroupMembers() map[string][]int {
	return t.groupMembersIn(t.agentsScopeSet())
}

// agentsFleetGroupMembers is the same membership question asked of the WHOLE
// FLEET, ignoring this window's display scope (ini-l5sy).
//
// TWO CONSUMERS, OPPOSITE NEEDS, SO TWO NAMES. The modal renders what this
// window shows, so it must ask the scoped question. The pruner decides whether
// a band still EXISTS, which is a fact about the fleet and not about one
// monitor -- and it acts on that answer by deleting the band's window
// assignment.
//
// SAME DOCTRINE AS zjhg's paneHasModal / paneShowsModalOnScreen split, and
// worth reading together: there the two consumers had opposite RISK
// asymmetries (a stale attention row is a visible nuisance; a stale "no
// dialog" is a forged answer), so the eager screen predicate and the
// conservative latch stayed separate. Here they have opposite SCOPES. Same
// rule either way -- when two callers want different answers in the edge
// case, they get two names, never a union.
//
// Sharing one predicate cost a release: after a band moved to window 2, window
// 1 saw zero members for it, concluded it was extinct, and erased the very
// assignment that had moved it -- silently, to disk, surviving restart. The
// operator's monitor arrangement was destroyed by closing the modal. The
// pruner was correct when it was written (ini-9ka.5); scoping changed the
// meaning of "empty" underneath it.
func (t *TUI) agentsFleetGroupMembers() map[string][]int {
	return t.groupMembersIn(nil)
}

// agentsBandLabelOf is the ONE answer to "which band is this pane in".
// The renderer and the arrow-key navigation both ask it — tonight's arrow
// bug (ini-ap3i: left/right dead inside the machine section) was the two of
// them deriving the answer separately: render used the machine label, nav
// looked up GroupOf where remote panes deliberately have no entry, found an
// empty band, and returned. Two derivations of one fact, the month's most
// expensive class; this function is the fix shaped as the doctrine demands.
func (t *TUI) agentsBandLabelOf(p PaneView) string {
	if paneIsRemoteMachine(p) {
		return machineTierPrefix + p.Host()
	}
	label, ok := t.layoutState.GroupOf[agentKey(p)]
	if !ok {
		return "core"
	}
	return label
}

// groupMembersIn is the one walk both questions use. inScope nil means the
// whole fleet.
func (t *TUI) groupMembersIn(inScope map[string]bool) map[string][]int {
	members := make(map[string][]int)
	for i, p := range t.panes {
		if inScope != nil && !inScope[agentKey(p)] {
			continue
		}
		members[t.agentsBandLabelOf(p)] = append(members[t.agentsBandLabelOf(p)], i)
	}
	return members
}

// agentsScopeSet returns the agent keys this modal may show, or nil for "no
// scope" — which is both the expanded view and every single-window session.
func (t *TUI) agentsScopeSet() map[string]bool {
	// Operator decision, 2026-08-15 (revises the ini-9isx gate choice): the
	// MODAL always shows the whole fleet — it is the management surface, and
	// the operator never wants to press a key to see what he can grab. The
	// hover OVERLAY remains scoped per window; only the modal's default
	// changed. The expanded flag and 'a' toggle are retired with it.
	return nil
}

// agentsGridColumnsPerRow computes columns-per-row: the widest band's GROUP
// count, capped at gridMaxPerRow, further shrunk if the terminal itself is
// too narrow to fit that many cells (spec: "content-sized modal, never
// terminal-proportional" -- the cap comes from content first, the
// terminal-width shrink is a fallback for genuinely small terminals only).
func agentsGridColumnsPerRow(tiers []tierGroup, screenW int) int {
	widest := 1
	for _, tg := range tiers {
		if n := len(tg.groups); n > widest {
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
// and where it sits, both physically (x, y) and logically. line is the global
// cell-row index used by vertical navigation (header/blank rows excluded, so
// ↑↓ only ever land on a cell row); col is the column within its column-row;
// colRow is the global column-row index, so colRow±1 is the adjacent
// column-row -- inside the same band when it wrapped, otherwise the next band.
type gridCell struct {
	paneIdx int // index into t.panes
	group   string
	x, y    int
	line    int
	col     int
	colRow  int
}

// colHeader is a rendered group column header: its label and where it sits.
type colHeader struct {
	label string
	x, y  int
}

// tierLabel is a rendered monitor-tier header: the window it names and the row
// it occupies. Empty unless tiers are active (more than one window configured).
type tierLabel struct {
	windowID string
	index    int      // 1-based monitor number as displayed.
	groups   []string // the tier's group keys, for member lookup at draw time (ini-68qv)
	y        int
}

// slotInfo is one column position on a cell line: which group owns it,
// whether that group is empty (a header over a blank row, no gridCell), and
// its x. Empty slots are how a grabbed agent lands in a freshly created group.
type slotInfo struct {
	label   string
	isEmpty bool
	x       int
}

// lineInfo describes one navigable cell line: the column-row it belongs to
// and the slots (columns) that column-row has. Indexed by the global line
// number, so a slot lookup is an index rather than a second walk.
type lineInfo struct {
	colRow int
	slots  []slotInfo
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
	headers      []colHeader
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
// the modal's interior origin and perRow is from agentsGridColumnsPerRow;
// callers that only need the height (box sizing) pass a zero origin and read
// contentLines, so height and positions can never come from different
// accounting.
//
// Per tier (monitor band): optional tier header, then the tier's groups in
// column-rows of at most perRow. Each column-row is: blank lead, one header
// row holding every column's "─ name ─" rule, blank gap, then as many cell
// rows as its tallest group (minimum one, so an empty group still reserves a
// landing slot). Cells are emitted in reading order -- column-row, column,
// row -- which is the order search steps through matches.
//
// tiers carries the window grouping. When tiersActive is false it holds
// exactly one entry whose groups are rendered with no tier header.
func agentsGridWalk(members map[string][]int, tiers []tierGroup, tiersActive bool, innerX, firstY, perRow int) agentsGridGeometry {
	var g agentsGridGeometry
	if perRow < 1 {
		perRow = 1
	}
	y := firstY + 1
	line := 0
	colRow := 0
	startY := y

	for ti, tg := range tiers {
		if tiersActive {
			y += gridTierLead
			g.tiers = append(g.tiers, tierLabel{windowID: tg.windowID, index: ti + 1, groups: tg.groups, y: y})
			y++
		}
		for start := 0; start < len(tg.groups); start += perRow {
			end := start + perRow
			if end > len(tg.groups) {
				end = len(tg.groups)
			}
			chunk := tg.groups[start:end]

			y += gridBandLead
			headerY := y
			cellY := headerY + 1 + gridLabelGap
			rows := 1
			slots := make([]slotInfo, len(chunk))
			for ci, label := range chunk {
				x := innerX + ci*gridCellW
				bandName := label
				if h, ok := strings.CutPrefix(label, machineTierPrefix); ok {
					bandName = h
				}
				g.headers = append(g.headers, colHeader{label: bandName, x: x, y: headerY})

				agentIdxs := members[label]
				slots[ci] = slotInfo{label: label, isEmpty: len(agentIdxs) == 0, x: x}
				for ai, paneIdx := range agentIdxs {
					g.cells = append(g.cells, gridCell{
						paneIdx: paneIdx,
						group:   label,
						x:       x,
						y:       cellY + ai,
						line:    line + ai,
						col:     ci,
						colRow:  colRow,
					})
				}
				if len(agentIdxs) > rows {
					rows = len(agentIdxs)
				}
			}
			for r := 0; r < rows; r++ {
				g.lines = append(g.lines, lineInfo{colRow: colRow, slots: slots})
			}
			y += 1 + gridLabelGap + rows
			line += rows
			colRow++
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
	return t.project != nil && participatesInMultiWindow(t.project)
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
		// Machine sections render in the untiered (single-window) modal too --
		// a remote agent invisible in a single-window session would be the
		// same collapse one door over.
		return append([]tierGroup{{windowID: WindowOne, groups: groups}}, t.agentsMachineTiers()...)
	}
	var out []tierGroup
	for _, w := range agentsWindowOrder(assign, groups) {
		out = append(out, tierGroup{windowID: w, groups: assign.GroupsForWindow(w, groups)})
	}
	return append(out, t.agentsMachineTiers()...)
}

// tierAllHidden reports how many panes a monitor tier's groups hold and
// whether every one of them is hidden. An empty tier is never "all hidden":
// there is nothing to explain.
func (t *TUI) tierAllHidden(members map[string][]int, groups []string) (int, bool) {
	n := 0
	for _, g := range groups {
		for _, i := range members[g] {
			n++
			if !t.layoutState.Hidden[agentKey(t.panes[i])] {
				return n, false
			}
		}
	}
	return n, n > 0
}

// machineTierPrefix marks a band/tier as a remote MACHINE section rather than
// a monitor: derived from pane hosts each frame, never stored, never movable
// with m (a machine is not a monitor you can send agents to -- yet).
const machineTierPrefix = "machine:"

// agentsMachineTiers returns one tier per distinct remote machine, in first-
// seen pane order, each holding its single host band.
func (t *TUI) agentsMachineTiers() []tierGroup {
	seen := map[string]bool{}
	var machines []string
	for _, p := range t.panes {
		if paneIsRemoteMachine(p) && !seen[p.Host()] {
			seen[p.Host()] = true
			machines = append(machines, p.Host())
		}
	}
	// Alphabetical, matching the overlay's machine order (one ordering rule
	// for both surfaces — see orderPanesForDisplay).
	sort.Strings(machines)
	var out []tierGroup
	for _, m := range machines {
		label := machineTierPrefix + m
		out = append(out, tierGroup{windowID: label, groups: []string{label}})
	}
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
	a, err := LoadAssignment(t.projectRoot, t.windowID)
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
	gridCellW = agentsGridCellWFor(t.panes, t.layoutState.Groups)
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

// agentsSlotAt reports the column slot at (line, col): which group owns that
// column position and whether it is empty. An empty group reserves exactly
// one cell row under its header -- visible as a header with a blank row
// beneath -- but agentsGridWalk emits no gridCell for it, so the normal cell
// scans can never find a landing point there on their own. This is the
// lookup that lets a grab recognise "this column is real, it is just empty".
// A pure INDEX into the walk's per-line output, never a second walk.
func agentsSlotAt(geo agentsGridGeometry, line, col int) (slotInfo, bool) {
	if line < 0 || line >= len(geo.lines) {
		return slotInfo{}, false
	}
	slots := geo.lines[line].slots
	if col < 0 || col >= len(slots) {
		return slotInfo{}, false
	}
	return slots[col], true
}

// agentsCellAt returns the cell at a logical position, or nil.
func agentsCellAt(cells []gridCell, colRow, col, line int) *gridCell {
	for i := range cells {
		c := &cells[i]
		if c.colRow == colRow && c.col == col && c.line == line {
			return c
		}
	}
	return nil
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
// agentsGridNumber returns the number the grid shows (and search accepts) for
// t.panes[idx], zero-based. Stamped fleet numbers pass through untouched so
// every window's modal numbers local agents from the same served sequence
// (ini-6m4). Panes with NO stamp — cross-machine peers, which attach after
// stampFleetThenApplyOrder and are not *Pane — get deterministic numbers
// AFTER the stamped range instead of falling back to their local slice index,
// which collided with a stamped agent's number (ini-ap3i: support:super and
// local super both showed "1"; a shared number with a different agent breaks
// number-addressed search and grab). One function serves both the number cell
// and agentsMatched because search and display disagreeing on what "3" means
// would be worse than either bug — same sentence as the search site's comment,
// now enforced by a shared code path instead of a shared intention.
func (t *TUI) agentsGridNumber(idx int) int {
	p := t.panes[idx]
	if fn, ok := p.(fleetNumbered); ok && !paneIsRemoteMachine(p) {
		// A remote MACHINE's pane arrives stamped in ITS fleet's number space
		// (it is #1 over there); rendering that number here collided with a
		// local agent's. Foreign stamps are ignored and remote panes number
		// after the local fleet like any unstamped pane.
		if i := fn.FleetIdx(); i >= 0 {
			return i
		}
	}
	// Unstamped: number past the highest stamped index, in pane order among
	// the unstamped, so the result is stable within a session and never
	// collides with a stamped number.
	maxStamped := -1
	for _, q := range t.panes {
		if fn, ok := q.(fleetNumbered); ok && !paneIsRemoteMachine(q) {
			if i := fn.FleetIdx(); i > maxStamped {
				maxStamped = i
			}
		}
	}
	rank := 0
	for j := 0; j < idx; j++ {
		q := t.panes[j]
		fn, ok := q.(fleetNumbered)
		if !ok || fn.FleetIdx() < 0 || paneIsRemoteMachine(q) {
			rank++
		}
	}
	return maxStamped + 1 + rank
}

func (t *TUI) agentsMatched(paneIdx int) bool {
	if !t.agents.searching || len(t.agents.searchBuf) == 0 {
		return true
	}
	q := strings.ToLower(string(t.agents.searchBuf))
	p := t.panes[paneIdx]
	// Same number the cell displays (the FLEET number, ini-6m4) -- search and
	// display disagreeing on what "3" means would be worse than either bug.
	return strings.Contains(strings.ToLower(paneDisplayName(p)), q) ||
		strings.HasPrefix(strconv.Itoa(t.agentsGridNumber(paneIdx)+1), q)
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

// agentsMoveH moves the selection (or, while grabbed, the agent itself) to
// the adjacent group COLUMN in the same column-row, landing on the nearest
// row: the same row when the target column has it, otherwise that column's
// last row. Stops at the column-row's ends. Grabbed, this is how membership
// is edited: the agent is spliced into t.panes at the destination cell's own
// position (taking over its slot, pushing it and everything after it in that
// group one row down) and its GroupOf reassigned -- the shipped cross-band
// grab mechanics, rotated a quarter turn. An empty column (a group fresh
// from g) is a valid grabbed destination and a no-op for plain navigation.
func (t *TUI) agentsMoveH(delta int) {
	t.ensureGroups(true)
	sel := t.agents.selected
	if sel < 0 || sel >= len(t.panes) || t.screen == nil {
		return
	}
	sw, sh := t.screen.Size()
	_, geo := t.agentsFrameGeometry(sw, sh, t.agents.searching || t.agents.creatingGroup)
	cells := geo.cells
	cur := agentsCellForPane(cells, sel)
	if cur == nil {
		return
	}
	targetCol := cur.col + delta
	slot, ok := agentsSlotAt(geo, cur.line, targetCol)
	if !ok {
		return // Column-row end: ←→ never wrap into the next column-row (↑↓ own that crossing).
	}

	// Nearest row: same line if the column has it, else its last row. Every
	// column in a column-row starts on the same first line, so a non-empty
	// column always has a cell at or above cur.line.
	var best *gridCell
	for i := range cells {
		c := &cells[i]
		if c.colRow != cur.colRow || c.col != targetCol {
			continue
		}
		if c.line == cur.line {
			best = c
			break
		}
		if c.line < cur.line && (best == nil || c.line > best.line) {
			best = c
		}
	}
	if best == nil {
		if !slot.isEmpty || !t.agents.moving {
			return
		}
		t.agentsGrabIntoEmptyColumn(sel, slot.label)
		return
	}
	if !t.agents.moving {
		t.agents.selected = best.paneIdx
		return
	}
	t.agentsGrabSplice(sel, best.paneIdx, best.group, false)
}

// agentsMoveV moves within the selected agent's COLUMN: ↑↓ step to the
// adjacent agent of the same group and stop at the column's ends within the
// band -- a short column never hops sideways into a neighbour column's cells.
// At a column's end, ↑↓ continue into the adjacent column-row (the band's own
// wrapped row, or the next/previous monitor band) and land on the nearest
// column by x: its top agent when descending, its bottom agent when ascending.
//
// Grabbed, ↑↓ swap the agent with its neighbour in the same group (order edit,
// persisted through agentsPersistOrder). At the group's end the grabbed agent
// is carried into the adjacent band's nearest column (membership edit) --
// operator-accepted interpretation I3 on ini-w771: without it a single agent
// could not reach a group on another monitor at all, since ←→ only see the
// columns of one band and m moves whole groups.
func (t *TUI) agentsMoveV(cells []gridCell, delta int) {
	sel := t.agents.selected
	if sel < 0 || sel >= len(t.panes) {
		return
	}
	cur := agentsCellForPane(cells, sel)
	if cur == nil {
		return
	}
	if next := agentsCellAt(cells, cur.colRow, cur.col, cur.line+delta); next != nil {
		if t.agents.moving {
			// Within-group order IS t.panes' relative order (no separate
			// per-group list), so swapping the two panes' positions is
			// exactly a same-column cell swap.
			t.panes[sel], t.panes[next.paneIdx] = t.panes[next.paneIdx], t.panes[sel]
			t.agentsPersistOrder()
		}
		t.agents.selected = next.paneIdx
		return
	}
	t.agentsContinueToColumnRow(cells, cur, delta)
}

// agentsContinueToColumnRow handles ↑↓ leaving a column's end: select (or,
// grabbed, carry into) the nearest column of the adjacent column-row.
func (t *TUI) agentsContinueToColumnRow(cells []gridCell, cur *gridCell, delta int) {
	sel := t.agents.selected
	targetColRow := cur.colRow + delta
	var best *gridCell
	bestDist := 1 << 30
	for i := range cells {
		c := &cells[i]
		if c.colRow != targetColRow {
			continue
		}
		d := c.x - cur.x
		if d < 0 {
			d = -d
		}
		// Nearest column; within it the edge row we enter from -- top when
		// descending, bottom when ascending.
		closer := d < bestDist
		sameColBetterRow := d == bestDist && best != nil &&
			((delta > 0 && c.line < best.line) || (delta < 0 && c.line > best.line))
		if closer || sameColBetterRow {
			bestDist = d
			best = c
		}
	}
	if best == nil {
		// No cell in that column-row: it does not exist (band edge, no-op),
		// or every column there is empty. Grabbed, an empty column is the
		// only way to populate a group created on another monitor.
		if !t.agents.moving || t.screen == nil {
			return
		}
		sw, sh := t.screen.Size()
		_, geo := t.agentsFrameGeometry(sw, sh, t.agents.searching || t.agents.creatingGroup)
		var slot *slotInfo
		for li := range geo.lines {
			if geo.lines[li].colRow != targetColRow {
				continue
			}
			for si := range geo.lines[li].slots {
				sl := &geo.lines[li].slots[si]
				d := sl.x - cur.x
				if d < 0 {
					d = -d
				}
				if sl.isEmpty && d < bestDist {
					bestDist = d
					slot = sl
				}
			}
			break
		}
		if slot == nil {
			return
		}
		t.agentsGrabIntoEmptyColumn(sel, slot.label)
		return
	}
	if !t.agents.moving {
		t.agents.selected = best.paneIdx
		return
	}
	t.agentsGrabSplice(sel, best.paneIdx, best.group, delta < 0)
}

// agentsGrabSplice carries the grabbed pane at sel into the group of the cell
// showing destPaneIdx: spliced out of t.panes and back in at the
// destination's own (post-removal) position -- BEFORE it (taking over its
// slot) or, when after is set, immediately AFTER it (entering a column from
// below lands at its bottom). Then reassigns the group. Refused for remote
// machine panes and for machine bands: a machine section is derived from
// pane hosts each frame and is never a group an agent can be filed under
// (setPaneGroup would refuse the write, leaving a reorder that renders
// nowhere different -- better to not move at all).
func (t *TUI) agentsGrabSplice(sel, destPaneIdx int, group string, after bool) {
	ag := t.panes[sel]
	if paneIsRemoteMachine(ag) || strings.HasPrefix(group, machineTierPrefix) {
		return
	}
	insertAt := destPaneIdx
	if after {
		insertAt++
	}
	t.panes = append(t.panes[:sel], t.panes[sel+1:]...)
	if insertAt > sel {
		insertAt--
	}
	if insertAt > len(t.panes) {
		insertAt = len(t.panes)
	}
	t.panes = append(t.panes[:insertAt], append([]PaneView{ag}, t.panes[insertAt:]...)...)
	t.agents.selected = insertAt
	t.setPaneGroup(ag, group)
}

// agentsGrabIntoEmptyColumn files the grabbed pane at sel as the sole member
// of the empty group label, inserting it into t.panes where a band-filtered
// read of the flat order will place it (agentsFlatInsertionForEmptyBand).
// Grabbed, this is the only way to populate a freshly-created group at all --
// without it, g can create a column the shipped UI can never put an agent
// into (the ini-2rc qa1 regression, rotated).
func (t *TUI) agentsGrabIntoEmptyColumn(sel int, label string) {
	ag := t.panes[sel]
	if paneIsRemoteMachine(ag) || strings.HasPrefix(label, machineTierPrefix) {
		return
	}
	members := t.agentsGroupMembers()
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
	t.setPaneGroup(ag, label)
}

// setPaneGroup records a pane's band assignment and persists it. Remote-
// machine panes never get one — their band is the machine section, derived
// per frame, and a persisted group would route them to a window that has no
// stream for them (support:pm rendered nowhere, 2026-08-15). The grab still
// reorders them within their machine band; only the group write is refused.
func (t *TUI) setPaneGroup(ag PaneView, label string) {
	if paneIsRemoteMachine(ag) {
		return
	}
	t.layoutState.GroupOf[agentKey(ag)] = label
	t.agentsPersistGrouping(ag.Name(), label)
}

// ---------- group lifecycle ----------

// groupNameExists reports whether name (expected already trimmed) exactly
// matches an existing group label.
//
// CASE-SENSITIVE, NO WHITESPACE FOLDING, ON PURPOSE (ini-9y3s spec): "Eng"
// and "eng" are different bands to the operator, not the same one typed
// twice, so this never lowercases or collapses internal whitespace before
// comparing. The single call site that must still trim leading/trailing
// whitespace (so "  eng  " and "eng" collide, which is not folding a
// variant -- it is the same blank-name-shaped edge the empty-name check
// already handles) does so before calling this, not inside it.
//
// The ONE check every place that can introduce a new label calls. It had two
// consumers when ini-9y3s added it -- the create-group prompt and
// applyGroupOfCmd -- and ini-fn77 deleted the second along with the rest of
// the follower group-command path. The rule it encodes is unchanged and the
// next label-introducing path belongs here rather than growing its own copy:
// two independent duplicate checks is how that bug shipped a route covering
// only one of them.
func groupNameExists(groups []string, name string) bool {
	for _, g := range groups {
		if g == name {
			return true
		}
	}
	return false
}

// agentsCreateGroup inserts a new, empty band labeled name immediately
// after the band containing the current selection (spec: "the new (empty)
// band appears after the current one"). A blank/whitespace-only name is
// rejected by the caller before this is invoked.
func (t *TUI) agentsCreateGroup(name string) {
	t.ensureGroups(false)
	afterIdx := len(t.layoutState.Groups) - 1
	if sel := t.agents.selected; sel >= 0 && sel < len(t.panes) {
		curLabel := t.layoutState.GroupOf[agentKey(t.panes[sel])]
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
		if err := t.moveGroupToWindow(name, targetWindow); err != nil {
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
	return t.agentsAssignment().WindowOfAgent(agentKey(t.panes[sel]), t.layoutState.GroupOf)
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
	group, ok := t.layoutState.GroupOf[agentKey(t.panes[sel])]
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
	if err := t.moveGroupToWindow(group, next); err != nil {
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
	// THE FLEET question, never the scoped one: a band rendering on another
	// monitor is elsewhere, not extinct (ini-l5sy).
	members := t.agentsFleetGroupMembers()
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
		if err := t.moveGroupToWindow(g, WindowOne); err != nil {
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
	perRow := agentsGridColumnsPerRow(tiers, sw)
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

	// Scope disclosure into the BOTTOM border, mirroring the title's use of
	// the top one (ini-9isx AC6). Placed on the border rather than in a
	// content row because it needs no vertical budget and therefore cannot be
	// squeezed out by a tall fleet -- a disclosure that disappears under load
	// is the one that matters least when it is there and most when it is not.
	if note := t.agentsScopeNote(); note != "" {
		nx := startX + 2
		// Rune-indexed for the same reason as the overlay's line: the note
		// carries "·", and a byte index smears it across the border.
		for i, ch := range []rune(note) {
			if nx+i >= startX+1 && nx+i < startX+boxW-1 {
				s.SetContent(nx+i, startY+boxH-1, ch, nil, labelStyle)
			}
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
	// A monitor whose EVERY agent is hidden says so on its own header row
	// (ini-68qv). That state produced a blank monitor on hover: the panel
	// listed the whole eng group under monitor 2, monitor 2 rendered nothing,
	// and nothing on the panel said why. Drawn on the row the walk already
	// reserved for the header, so geometry is untouched (ini-9ka.5). Remote
	// machines are left alone: the bead scopes remote-machine rows unchanged.
	members := t.agentsGroupMembers()
	for _, tl := range geo.tiers {
		lab := fmt.Sprintf("══ monitor %d ", tl.index)
		if n, allHidden := t.tierAllHidden(members, tl.groups); allHidden {
			noun := "agents"
			if n == 1 {
				noun = "agent"
			}
			lab = fmt.Sprintf("══ monitor %d (%d %s, all hidden) ", tl.index, n, noun)
		}
		if h, ok := strings.CutPrefix(tl.windowID, machineTierPrefix); ok {
			lab = fmt.Sprintf("══ %s (remote machine) ", h)
		}
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
	// Column headers: each "─ name ───" rule is drawn at ITS column's x and
	// at column width (cell width minus a one-column gutter -- the same
	// width as the selection bar), so a header never runs into its
	// neighbour's.
	for _, h := range geo.headers {
		lab := fmt.Sprintf("─ %s ", h.label)
		x := h.x
		end := h.x + gridCellW - 1
		if end > startX+boxW-2 {
			end = startX + boxW - 2
		}
		for _, ch := range lab {
			if x < end {
				s.SetContent(x, h.y, ch, nil, labelStyle)
			}
			x++
		}
		for ; x < end; x++ {
			s.SetContent(x, h.y, '─', nil, labelStyle)
		}
	}

	for _, c := range cells {
		p := t.panes[c.paneIdx]
		pk := agentKey(p)
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
		case StateSuspended:
			// Parked: distinct from idle silver, dead gray, and hidden's
			// italic — the modal's s/S keys toggle this state, so it must
			// read back from the same screen.
			nameStyle = bgStyle.Foreground(tcell.NewRGBColor(100, 140, 190))
		}
		numStyle := bgStyle.Foreground(tcell.ColorSilver)
		boxStyle := bgStyle.Foreground(tcell.ColorSilver)
		pStyle := pinStyle
		prStyle := protStyle
		if hidden {
			// Hidden is italic (and [h] in the box). It also grays the name,
			// per the grid spec -- EXCEPT for a suspended agent (ini-68qv):
			// suspended is the name's colour and nothing else, the modal's
			// own s/S keys toggle it, and graying it here made a hidden
			// parked agent read as merely hidden. Both states show now.
			nameStyle = nameStyle.Italic(true)
			if p.Activity() != StateSuspended {
				nameStyle = nameStyle.Foreground(tcell.ColorGray)
			}
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
			// The selection bar must not erase the suspended signal — the
			// operator reads state exactly where they toggle it, and the
			// selected agent is the one they are about to toggle (the very
			// first live use of s produced "is eng2 really suspended?").
			if p.Activity() == StateSuspended {
				nameStyle = base.Italic(true)
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

		// Hidden is [h], the overlay's glyph (render.go ' [h]'), in the slot
		// the operator already reads as the visibility axis (ini-68qv). The
		// operator's confusion on hover was exactly this: an agent listed
		// under monitor 2 with an empty box read as "on monitor 2", and the
		// monitor rendered nothing. Same 3-character box, so the cell budget
		// and the golden are untouched; the name keeps its gray italic, so a
		// hidden AND suspended agent shows both -- suspended is the name's
		// colour, hidden is the glyph. The grid spec's line "hidden = italic
		// + gray + [ ]" chose the box over per-cell words for density; [h]
		// keeps the density and changes only that line's letter.
		vis := "[x]"
		if hidden {
			vis = "[h]"
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
		put(fmt.Sprintf("%3d ", t.agentsGridNumber(c.paneIdx)+1), numStyle)
		put(vis+" ", boxStyle)
		put(paneDisplayName(p), nameStyle)
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
