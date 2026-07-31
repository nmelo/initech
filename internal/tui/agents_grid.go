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
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/nmelo/initech/internal/roles"
)

// Grid layout constants (spec section "Layout rules", operator-tuned).
const (
	gridCellW     = 17 // " NN [x] name*•  " -- fixed so columns align across bands.
	gridMaxPerRow = 6  // Groups larger than this wrap into a multi-line band.
	gridBandLead  = 1  // Blank line before each group label.
	gridLabelGap  = 1  // Blank line between the label and its cell row(s).
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

// agentsGridLayoutCells computes cell positions for the current grid
// geometry, mirroring the PoC's layoutCells exactly. innerX/firstY are the
// modal's interior origin; perRow is from agentsGridPerRow. Groups iterates
// t.layoutState.Groups (not the members map, whose Go map order is
// unspecified) so band order is deterministic.
func agentsGridLayoutCells(members map[string][]int, groups []string, innerX, firstY, perRow int) []gridCell {
	var cells []gridCell
	y := firstY + 1
	line := 0
	for _, label := range groups {
		y += gridBandLead
		agentIdxs := members[label]
		for ai, paneIdx := range agentIdxs {
			col := ai % perRow
			row := ai / perRow
			cells = append(cells, gridCell{
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
		y += 1 + gridLabelGap + rows
		line += rows
	}
	return cells
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
	return strings.Contains(strings.ToLower(p.Name()), q) ||
		strings.HasPrefix(strconv.Itoa(paneIdx+1), q)
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
	groups := make([]string, 0, len(t.layoutState.Groups)+1)
	groups = append(groups, t.layoutState.Groups[:afterIdx+1]...)
	groups = append(groups, name)
	groups = append(groups, t.layoutState.Groups[afterIdx+1:]...)
	t.layoutState.Groups = groups
	t.saveLayoutIfConfigured()
}

// agentsPruneEmptyGroups removes bands with zero members (spec: "a group
// empty when the modal closes is removed"). Applied on modal close AND on
// LoadLayout (see layout.go) -- the same invariant enforced at both points
// a band could end up empty, not just the close-time case.
func (t *TUI) agentsPruneEmptyGroups() {
	if len(t.layoutState.Groups) == 0 {
		return
	}
	members := t.agentsGroupMembers()
	var kept []string
	changed := false
	for _, g := range t.layoutState.Groups {
		if len(members[g]) > 0 {
			kept = append(kept, g)
		} else {
			changed = true
		}
	}
	if changed {
		t.layoutState.Groups = kept
		t.saveLayoutIfConfigured()
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
func agentsGridBoxDims(members map[string][]int, groups []string, sw, sh int, searching bool) agentsGridBox {
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

	contentLines := 0
	for _, g := range groups {
		n := len(members[g])
		rows := (n + perRow - 1) / perRow
		if n == 0 {
			rows = 1
		}
		contentLines += gridBandLead + 1 + gridLabelGap + rows
	}
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
	members := t.agentsGroupMembers()
	box := agentsGridBoxDims(members, t.layoutState.Groups, sw, sh, t.agents.searching || t.agents.creatingGroup)
	perRow, boxW, boxH, startX, startY, searchRows := box.perRow, box.boxW, box.boxH, box.startX, box.startY, box.searchRows

	bgStyle := tcell.StyleDefault.Background(tcell.NewRGBColor(20, 20, 20)).Foreground(tcell.ColorSilver)
	borderStyle := bgStyle.Foreground(tcell.ColorGray)
	titleStyle := bgStyle.Foreground(tcell.ColorDodgerBlue).Bold(true)
	labelStyle := bgStyle.Foreground(tcell.ColorGray)
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

	innerX := startX + 2
	// Layout cells fresh every render call -- this frame's members/perRow,
	// never a value cached from the previous frame. The no-matches verdict
	// below reads THIS cells slice, computed just above it in this same
	// call, which is the fix for the exact bug the spec names (ini-2rc).
	cells := agentsGridLayoutCells(members, t.layoutState.Groups, innerX, startY+searchRows, perRow)
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

	y := startY + 1 + searchRows
	for _, g := range t.layoutState.Groups {
		y += gridBandLead
		lab := fmt.Sprintf("─ %s ", g)
		x := innerX
		for _, ch := range lab {
			if x < startX+boxW-1 {
				s.SetContent(x, y, ch, nil, labelStyle)
			}
			x++
		}
		for ; x < startX+boxW-2; x++ {
			s.SetContent(x, y, '─', nil, labelStyle)
		}
		n := len(members[g])
		rows := (n + perRow - 1) / perRow
		if n == 0 {
			rows = 1
		}
		y += 1 + gridLabelGap + rows
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
		put(fmt.Sprintf("%3d ", c.paneIdx+1), numStyle)
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
