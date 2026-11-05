// agents_form.go is the Agents overlay's second form (ini-5d3): pinned, a
// section across the top of the page with the pane grid below it, beside the
// floating box over the panes. One content, two forms: both draw what
// overlayContent and boardRows give them. Shift+Option+s and the command
// "agents pin|float" flip the form; layout.yaml remembers it as agents_form.
package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// Pinned section geometry (ini-5d3). The section is a fixed height, not one
// sized to its content, so the panes below do not resize each time an agent
// starts or stops working.
const (
	// agentsPinnedRows is the rows under the header on a terminal of
	// agentsPinnedTallFrom rows or more; each column scrolls within them.
	agentsPinnedRows = 8
	// agentsPinnedTallFrom is the terminal height from which the columns are
	// drawn in full; below it each column is one summary line.
	agentsPinnedTallFrom = 30
)

// agentsPinState is the pinned section's scroll offsets and the column
// bounds of its last draw, which the mouse wheel reads.
type agentsPinState struct {
	scrollL, scrollR int
	// splitX is the divider's column in the last draw; 0 before one.
	splitX int
}

// agentsPinned reports whether the overlay is in its pinned form.
func (t *TUI) agentsPinned() bool {
	return t.layoutState.AgentsForm == agentsFormPinned
}

// agentsPinnedTop is how many rows the pinned section takes above the pane
// grid on a screen h rows tall: none unless the overlay is shown and pinned;
// the header and agentsPinnedRows rows on a tall screen; on a short one the
// header, the summary line and, once window 1 has them, the rig numbers line.
// A screen too short to keep a pane row below it gets no section.
func (t *TUI) agentsPinnedTop(h int) int {
	if !t.layoutState.Overlay || !t.agentsPinned() {
		return 0
	}
	top := 1 + agentsPinnedRows
	if h < agentsPinnedTallFrom {
		top = 2
		if t.RigNumbersSnapshot() != nil {
			top++
		}
	}
	if h-2-top < 1 {
		return 0
	}
	return top
}

// shiftPlan moves every region of a plan computed for the rows below the
// pinned section down by top rows.
func shiftPlan(plan RenderPlan, top int) RenderPlan {
	if top == 0 {
		return plan
	}
	plan.ScreenH += top
	for i := range plan.Panes {
		plan.Panes[i].Region.Y += top
	}
	for i := range plan.Dividers {
		plan.Dividers[i].Y += top
	}
	for i := range plan.Waiting {
		plan.Waiting[i].Y += top
	}
	return plan
}

// setAgentsForm puts the overlay in form, showing it if Option+s had hidden
// it, and saves the layout once. Choosing the form already shown changes
// nothing and writes nothing.
func (t *TUI) setAgentsForm(form string) {
	if t.layoutState.AgentsForm == form && t.layoutState.Overlay {
		return
	}
	t.layoutState.AgentsForm = form
	t.layoutState.Overlay = true
	t.agentsPin.scrollL, t.agentsPin.scrollR = 0, 0
	t.applyLayout()
	t.saveLayoutIfConfigured()
}

// toggleAgentsForm flips the overlay between its two forms (Shift+Option+s).
func (t *TUI) toggleAgentsForm() {
	if t.agentsPinned() {
		t.setAgentsForm(agentsFormFloating)
		return
	}
	t.setAgentsForm(agentsFormPinned)
}

// adoptLoadedAgentsForm keeps the form layout.yaml left in window 1. A
// viewer starts floating, as it starts with the overlay shown: its
// arrangement is session-scoped and it never writes the file (ini-qodm).
func (t *TUI) adoptLoadedAgentsForm(viewer bool) {
	if viewer || t.layoutState.AgentsForm == "" {
		t.layoutState.AgentsForm = agentsFormFloating
	}
}

// cmdAgentsForm runs "agents pin" and "agents float".
func (t *TUI) cmdAgentsForm(arg string) bool {
	switch arg {
	case "pin":
		t.setAgentsForm(agentsFormPinned)
	case "float":
		t.setAgentsForm(agentsFormFloating)
	default:
		t.cmd.error = "usage: agents [pin|float]"
	}
	return false
}

// effortTitles maps each polled bead's id to its bd title.
func effortTitles(beads []bdBead) map[string]string {
	out := make(map[string]string, len(beads))
	for _, b := range beads {
		if b.Title != "" {
			out[b.ID] = b.Title
		}
	}
	return out
}

// effortLabelEnd is the rune index just past an effort row's id, the token
// after its state icon if it has one; -1 for the rig numbers row or a row
// with no id.
func effortLabelEnd(line string) int {
	if strings.HasPrefix(line, rigRowPrefix) {
		return -1
	}
	r := []rune(line)
	i := 0
	if len(r) > 1 && r[1] == ' ' && r[0] > 0x7f {
		i = 2 // the state icon and its space (ini-z9t)
	}
	start := i
	for i < len(r) && r[i] != ' ' {
		i++
	}
	if i == start {
		return -1
	}
	return i
}

// titleSep goes between an effort row's id and its title.
const titleSep = " · "

// titledEffortRow is an effort row with " · <title>" after its id, the title
// cut with an ellipsis so the row fits width runes, and left out when not one
// of its runes fits. A row is never wrapped, and the title never widens it
// past width.
func titledEffortRow(line string, titles map[string]string, width int) string {
	end := effortLabelEnd(line)
	if end < 0 {
		return line
	}
	r := []rune(line)
	id := string(r[:end])
	if f := strings.Fields(id); len(f) > 0 {
		id = f[len(f)-1]
	}
	title := []rune(titles[id])
	if len(title) == 0 {
		return line
	}
	room := width - len(r) - len([]rune(titleSep))
	if room < 2 {
		return line
	}
	if len(title) > room {
		cut := []rune(strings.TrimRight(string(title[:room-1]), " "))
		title = append(cut, '…')
	}
	return string(r[:end]) + titleSep + string(title) + string(r[end:])
}

// titledBoardRows is boardRows' lines, each fit to width with its title.
func (t *TUI) titledBoardRows(rows []string, width int) []string {
	if len(t.paceTitles) == 0 {
		return rows
	}
	out := make([]string, len(rows))
	for i, line := range rows {
		out[i] = titledEffortRow(line, t.paceTitles, width)
	}
	return out
}

// pinnedLine is one line of the pinned agents column: an agent, a machine
// divider, or a plain line ("all idle").
type pinnedLine struct {
	agent *AgentInfo
	text  string
	rule  bool
}

// pinnedAgentLines flattens the overlay's sections into the agents column:
// per machine, its divider, then its working agents, then the rest.
func pinnedAgentLines(c overlayAgents) []pinnedLine {
	var out []pinnedLine
	if c.allIdle {
		out = append(out, pinnedLine{text: overlayAllIdle})
	}
	for _, sec := range c.sections {
		if sec.machine != "" {
			out = append(out, pinnedLine{text: sec.machine, rule: true})
		}
		for _, col := range [][]AgentInfo{sec.left, sec.right} {
			for i := range col {
				out = append(out, pinnedLine{agent: &col[i]})
			}
		}
	}
	return out
}

// pinnedSummary is a column folded to one line: the count, then the first
// entries that fit width, then +N for the entries left out.
func pinnedSummary(count int, noun string, entries []string, width int) string {
	line := fmt.Sprintf("%d %s", count, noun)
	if len(entries) == 0 {
		return line
	}
	line += ":"
	for i, e := range entries {
		more := ""
		if rest := len(entries) - i - 1; rest > 0 {
			more = fmt.Sprintf(" +%d", rest)
		}
		sep := " "
		if i > 0 {
			sep = ", "
		}
		if len([]rune(line+sep+e+more)) > width {
			return line + fmt.Sprintf(" +%d", len(entries)-i)
		}
		line += sep + e
	}
	return line
}

// clampScroll keeps a column's offset where it still shows a full window.
func clampScroll(off, n, rows int) int {
	if off > n-rows {
		off = n - rows
	}
	if off < 0 {
		off = 0
	}
	return off
}

// renderPinnedAgents draws the pinned section across the top planTop rows:
// a header with the counts, the agents column on the left and the effort
// rows on the right, the rig numbers as the section's last line.
func (t *TUI) renderPinnedAgents() {
	top := t.planTop
	if top == 0 {
		return
	}
	s := t.screen
	sw, _ := s.Size()
	t.overlayBounds.prev, t.overlayBounds.dots = t.overlayBounds.dots, nil
	t.overlayBounds.dots = map[[2]int]overlayDot{}
	// The floating box's click bounds do not apply to this form.
	t.overlayBounds.effortTop, t.overlayBounds.effortEnd = -1, -1
	c := t.overlayContent()

	bg := tcell.StyleDefault.Background(tcell.NewRGBColor(20, 25, 40))
	border := bg.Foreground(tcell.ColorWhite)
	dim := bg.Foreground(tcell.ColorGray)
	silver := bg.Foreground(tcell.ColorSilver)
	put := func(x, y int, line string, st tcell.Style, limit int) {
		for _, ch := range line {
			if x >= limit {
				return
			}
			s.SetContent(x, y, ch, nil, st)
			x++
		}
	}

	// Header: the title, then the visible/hidden counts and the scope line
	// the floating box carries under its columns.
	for x := 0; x < sw; x++ {
		s.SetContent(x, 0, '─', nil, border)
	}
	header := " Agents "
	if c.hiddenCount > 0 {
		header += fmt.Sprintf("─ %d visible, %d hidden ", c.agentRows-c.hiddenCount, c.hiddenCount)
	}
	if c.scopeHidden > 0 {
		header += fmt.Sprintf("─ +%d %s · alt+a shows all ", c.scopeHidden, c.scopeWhere)
	}
	put(max(1, t.projectBadgeEnd()+1), 0, header, border, sw)
	for y := 1; y < top; y++ {
		for x := 0; x < sw; x++ {
			s.SetContent(x, y, ' ', nil, bg)
		}
	}

	agents := pinnedAgentLines(c)
	statusW := 7
	for _, l := range agents {
		if l.agent != nil {
			statusW = max(statusW, len([]rune(l.agent.Status)))
		}
	}
	colW := 2 + c.maxNameLen + 1 + statusW
	short := top-1 < agentsPinnedRows
	splitX := min(sw/2, colW+3)
	if short {
		splitX = sw / 2 // a summary line needs the room, not the name column
	}
	t.agentsPin.splitX = splitX
	rightX, rightW := splitX+2, sw-splitX-3
	rows := t.boardRows()
	var rig string
	if n := len(rows); n > 0 && strings.HasPrefix(rows[n-1], rigRowPrefix) {
		rig, rows = rows[n-1], rows[:n-1]
	}
	rows = t.titledBoardRows(rows, rightW)
	for y := 1; y < top; y++ {
		s.SetContent(splitX, y, '│', nil, dim)
	}

	if short {
		// Short terminal: each column folded to one line.
		var names []string
		listed := 0
		for _, l := range agents {
			if l.agent != nil {
				names = append(names, l.agent.Name+" "+l.agent.Status)
				listed++
			}
		}
		put(1, 1, pinnedSummary(listed, "agents", names, splitX-2), silver, splitX)
		var ids []string
		for _, line := range rows {
			if end := effortLabelEnd(line); end > 0 {
				ids = append(ids, string([]rune(line)[:end]))
			}
		}
		put(rightX, 1, pinnedSummary(len(rows), "efforts", ids, rightW), silver, sw)
		if rig != "" && top > 2 {
			put(rightX, 2, rig, silver, sw)
		}
		return
	}

	// Agents column, scrolled.
	t.agentsPin.scrollL = clampScroll(t.agentsPin.scrollL, len(agents), agentsPinnedRows)
	for i := 0; i < agentsPinnedRows && t.agentsPin.scrollL+i < len(agents); i++ {
		l, y := agents[t.agentsPin.scrollL+i], 1+i
		switch {
		case l.agent != nil:
			t.drawOverlayRow(2, y, splitX-1, c.maxNameLen, *l.agent, bg)
		case l.rule:
			put(2, y, "─ "+l.text+" "+strings.Repeat("─", max(0, splitX-len(l.text)-5)), dim, splitX-1)
		default:
			put(2, y, l.text, silver, splitX-1)
		}
	}

	// Effort column, scrolled, the rig numbers fixed on the last line.
	room := agentsPinnedRows
	if rig != "" {
		room--
		put(rightX, top-1, rig, silver, sw)
	}
	t.agentsPin.scrollR = clampScroll(t.agentsPin.scrollR, len(rows), room)
	for i := 0; i < room && t.agentsPin.scrollR+i < len(rows); i++ {
		drawEffortLine(s, rightX, 1+i, sw, rows[t.agentsPin.scrollR+i], silver)
	}
}

// drawEffortLine draws one effort row from x up to limit, its over part
// (" · N over") red, as the floating box draws it.
func drawEffortLine(s tcell.Screen, x, y, limit int, line string, st tcell.Style) {
	overAt, overEnd := overSpan(line)
	for j, ch := range []rune(line) {
		if x >= limit {
			return
		}
		cs := st
		if j >= overAt && j < overEnd {
			cs = st.Foreground(tcell.ColorRed)
		}
		s.SetContent(x, y, ch, nil, cs)
		x++
	}
}

// scrollPinnedAgents scrolls the pinned column under x by delta rows; it
// reports whether (x, y) is inside the pinned section at all, so the caller
// sends a wheel there to no pane.
func (t *TUI) scrollPinnedAgents(x, y, delta int) bool {
	if !t.layoutState.Overlay || !t.agentsPinned() || y >= t.planTop {
		return false
	}
	if x < t.agentsPin.splitX {
		t.agentsPin.scrollL = max(0, t.agentsPin.scrollL+delta)
	} else {
		t.agentsPin.scrollR = max(0, t.agentsPin.scrollR+delta)
	}
	return true
}
