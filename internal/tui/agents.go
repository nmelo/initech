// agents.go implements the agent management modal. It displays all agents in
// current display order with visibility, pin state, activity, and bead info.
// Rendered as a centered floating box over the live TUI.
// Opened via backtick+agents command or Alt+a shortcut.
//
// Actions apply immediately and persist through the layout save path.
// Keybindings: Space (toggle visibility), Enter (grab/drop for reorder),
// p (toggle pin), A (reveal all), R (reset to config order).
package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
)

// agentsBoxW and agentsBoxH are the target floating box dimensions.
const agentsBoxW = 78
const agentsBoxH = 18

// agentsChromeRows is the number of rows used by modal chrome (title, header,
// separator, error, help). Everything else is the scrollable viewport.
const agentsChromeRows = 6

// openAgentsModal initializes and opens the agent management modal.
func (t *TUI) openAgentsModal() {
	t.agents.active = true
	t.agents.selected = 0
	t.agents.scrollOffset = 0
	t.agents.moving = false
	t.agents.error = ""
}

// agentsViewportHeight returns the number of visible agent rows for the
// current screen size.
func (t *TUI) agentsViewportHeight() int {
	_, sh := t.screen.Size()
	boxH := agentsBoxH
	if sh-4 < boxH {
		boxH = sh - 4
	}
	if boxH < agentsChromeRows+1 {
		return 1
	}
	return boxH - agentsChromeRows
}

// agentsEnsureVisible adjusts scrollOffset so that t.agents.selected is
// within the visible viewport.
func (t *TUI) agentsEnsureVisible() {
	vp := t.agentsViewportHeight()
	if t.agents.selected < t.agents.scrollOffset {
		t.agents.scrollOffset = t.agents.selected
	}
	if t.agents.selected >= t.agents.scrollOffset+vp {
		t.agents.scrollOffset = t.agents.selected - vp + 1
	}
}

// handleAgentsKey processes key events while the agents modal is open.
func (t *TUI) handleAgentsKey(ev *tcell.EventKey) bool {
	// Alt+a toggles the modal closed.
	if ev.Modifiers()&tcell.ModAlt != 0 && ev.Key() == tcell.KeyRune && ev.Rune() == 'a' {
		t.agents.moving = false
		t.agents.active = false
		return false
	}

	n := len(t.panes)
	if n == 0 {
		t.agents.active = false
		return false
	}

	// Clear stale error on any keypress.
	t.agents.error = ""

	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.agents.moving = false
		t.agents.active = false
		return false

	case tcell.KeyEnter:
		t.agents.moving = !t.agents.moving
		return false

	case tcell.KeyUp:
		t.agentsMoveUp()
		return false

	case tcell.KeyDown:
		t.agentsMoveDown()
		return false

	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q', '`':
			t.agents.moving = false
			t.agents.active = false
			return false
		case 'j':
			t.agentsMoveDown()
			return false
		case 'k':
			t.agentsMoveUp()
			return false
		case ' ':
			t.agentsToggleVisibility()
			return false
		case 'p':
			t.agentsTogglePin()
			return false
		case 'A':
			t.agentsRevealAll()
			return false
		case 'R':
			t.agentsResetOrder()
			return false
		}
	}
	return false
}

// agentsMoveUp moves the cursor or the grabbed row up by one position.
func (t *TUI) agentsMoveUp() {
	if t.agents.moving {
		if t.agents.selected > 0 {
			i := t.agents.selected
			t.panes[i], t.panes[i-1] = t.panes[i-1], t.panes[i]
			t.agents.selected--
			t.agentsPersistOrder()
		}
	} else if t.agents.selected > 0 {
		t.agents.selected--
	}
	t.agentsEnsureVisible()
}

// agentsMoveDown moves the cursor or the grabbed row down by one position.
func (t *TUI) agentsMoveDown() {
	n := len(t.panes)
	if t.agents.moving {
		if t.agents.selected < n-1 {
			i := t.agents.selected
			t.panes[i], t.panes[i+1] = t.panes[i+1], t.panes[i]
			t.agents.selected++
			t.agentsPersistOrder()
		}
	} else if t.agents.selected < n-1 {
		t.agents.selected++
	}
	t.agentsEnsureVisible()
}

// agentsToggleVisibility toggles hidden state for the selected pane.
// Blocks hiding the last visible pane.
func (t *TUI) agentsToggleVisibility() {
	if t.agents.selected < 0 || t.agents.selected >= len(t.panes) {
		return
	}
	name := paneKey(t.panes[t.agents.selected])

	if t.layoutState.Hidden[name] {
		// Unhide.
		delete(t.layoutState.Hidden, name)
	} else {
		// Hide: guard against last visible pane.
		if t.visibleCountFromState() <= 1 {
			t.agents.error = "cannot hide last visible pane"
			return
		}
		if t.layoutState.Hidden == nil {
			t.layoutState.Hidden = make(map[string]bool)
		}
		t.layoutState.Hidden[name] = true
	}
	t.recalcGrid(false)
	t.applyLayout()
	t.saveLayoutIfConfigured()
}

// agentsTogglePin toggles the pinned state for the selected pane.
func (t *TUI) agentsTogglePin() {
	if t.agents.selected < 0 || t.agents.selected >= len(t.panes) {
		return
	}
	name := paneKey(t.panes[t.agents.selected])

	if t.layoutState.Pinned == nil {
		t.layoutState.Pinned = make(map[string]bool)
	}
	if t.layoutState.Pinned[name] {
		delete(t.layoutState.Pinned, name)
		if lp, ok := t.panes[t.agents.selected].(*Pane); ok {
			lp.SetPinned(false)
		}
	} else {
		t.layoutState.Pinned[name] = true
		if lp, ok := t.panes[t.agents.selected].(*Pane); ok {
			lp.SetPinned(true)
		}
	}
	t.saveLayoutIfConfigured()
}

// agentsRevealAll unhides all agents.
func (t *TUI) agentsRevealAll() {
	t.layoutState.Hidden = make(map[string]bool)
	t.recalcGrid(false)
	t.applyLayout()
	t.saveLayoutIfConfigured()
}

// agentsResetOrder resets pane order to the config-declared role order
// from initech.yaml. Falls back to current order if no config is available.
func (t *TUI) agentsResetOrder() {
	if t.project == nil || len(t.project.Roles) == 0 {
		t.agents.error = "no config role order available"
		return
	}
	t.layoutState.Order = make([]string, len(t.project.Roles))
	copy(t.layoutState.Order, t.project.Roles)
	reorderPanes(t.panes, t.layoutState.Order)
	t.applyLayout()
	t.saveLayoutIfConfigured()
	// Reset selection and scroll to top after reorder.
	t.agents.selected = 0
	t.agents.scrollOffset = 0
	t.agents.moving = false
}

// agentsPersistOrder snapshots the current pane order into layoutState and persists.
func (t *TUI) agentsPersistOrder() {
	order := make([]string, len(t.panes))
	for i, p := range t.panes {
		order[i] = paneKey(p)
	}
	t.layoutState.Order = order
	t.applyLayout()
	t.saveLayoutIfConfigured()
}

// renderAgents draws the centered floating agent management modal.
func (t *TUI) renderAgents() {
	s := t.screen
	sw, sh := s.Size()

	// Compute box dimensions.
	boxW := agentsBoxW
	if sw-4 < boxW {
		boxW = sw - 4
	}
	if boxW < 20 {
		boxW = 20
	}
	boxH := agentsBoxH
	if sh-4 < boxH {
		boxH = sh - 4
	}
	if boxH < 8 {
		boxH = 8
	}

	startX := (sw - boxW) / 2
	startY := (sh - boxH) / 2
	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}

	bgStyle := tcell.StyleDefault.Background(tcell.NewRGBColor(20, 20, 20)).Foreground(tcell.ColorSilver)
	borderStyle := bgStyle.Foreground(tcell.ColorGray)
	titleStyle := bgStyle.Foreground(tcell.ColorDodgerBlue).Bold(true)
	headerStyle := bgStyle.Bold(true).Foreground(tcell.ColorWhite)
	normalStyle := bgStyle.Foreground(tcell.ColorSilver)
	selectedStyle := tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite)
	movingStyle := tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorWhite).Bold(true)
	hiddenStyle := bgStyle.Foreground(tcell.ColorGray)
	helpStyle := bgStyle.Foreground(tcell.ColorGray)
	errorStyle := bgStyle.Foreground(tcell.ColorRed)
	scrollStyle := bgStyle.Foreground(tcell.ColorDodgerBlue)

	// Draw opaque background.
	for y := startY; y < startY+boxH && y < sh; y++ {
		for x := startX; x < startX+boxW && x < sw; x++ {
			s.SetContent(x, y, ' ', nil, bgStyle)
		}
	}

	// Draw border.
	// Corners.
	s.SetContent(startX, startY, '\u250c', nil, borderStyle)
	s.SetContent(startX+boxW-1, startY, '\u2510', nil, borderStyle)
	s.SetContent(startX, startY+boxH-1, '\u2514', nil, borderStyle)
	s.SetContent(startX+boxW-1, startY+boxH-1, '\u2518', nil, borderStyle)
	// Top and bottom edges.
	for x := startX + 1; x < startX+boxW-1 && x < sw; x++ {
		s.SetContent(x, startY, '\u2500', nil, borderStyle)
		s.SetContent(x, startY+boxH-1, '\u2500', nil, borderStyle)
	}
	// Left and right edges.
	for y := startY + 1; y < startY+boxH-1 && y < sh; y++ {
		s.SetContent(startX, y, '\u2502', nil, borderStyle)
		s.SetContent(startX+boxW-1, y, '\u2502', nil, borderStyle)
	}

	// Interior content width.
	innerW := boxW - 2 // exclude left/right border
	innerX := startX + 1

	// drawLine writes a string inside the box at the given row, clipped to innerW.
	drawLine := func(y int, text string, style tcell.Style) {
		for i, ch := range text {
			if i >= innerW {
				break
			}
			s.SetContent(innerX+i, y, ch, nil, style)
		}
	}

	// fillRow fills a full interior row with a style (for selected/moving highlight).
	fillRow := func(y int, style tcell.Style) {
		for x := innerX; x < innerX+innerW; x++ {
			s.SetContent(x, y, ' ', nil, style)
		}
	}

	// Title centered in top border.
	title := " initech agents "
	if t.agents.moving && t.agents.selected >= 0 && t.agents.selected < len(t.panes) {
		title = fmt.Sprintf(" moving %s ", t.panes[t.agents.selected].Name())
	}
	titleStart := startX + (boxW-len([]rune(title)))/2
	if titleStart < startX+1 {
		titleStart = startX + 1
	}
	for i, ch := range title {
		if titleStart+i < startX+boxW-1 {
			s.SetContent(titleStart+i, startY, ch, nil, titleStyle)
		}
	}

	// Interior rows: row 0 = startY+1
	iy := startY + 1

	// Header.
	header := fmt.Sprintf(" %-3s %-3s %-12s %-5s %s", "#", "VIS", "AGENT", "PIN", "STATUS")
	drawLine(iy, header, headerStyle)
	iy++

	// Separator.
	for x := innerX; x < innerX+innerW; x++ {
		s.SetContent(x, iy, '\u2500', nil, borderStyle)
	}
	iy++

	// Viewport: rows from iy to bottom border minus 3 (error + help + border).
	vpBottom := startY + boxH - 3 // last row for agent content
	vpHeight := vpBottom - iy
	if vpHeight < 1 {
		vpHeight = 1
	}

	// Clamp scroll offset.
	n := len(t.panes)
	maxScroll := n - vpHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if t.agents.scrollOffset > maxScroll {
		t.agents.scrollOffset = maxScroll
	}
	if t.agents.scrollOffset < 0 {
		t.agents.scrollOffset = 0
	}

	// Scroll indicators.
	hasAbove := t.agents.scrollOffset > 0
	hasBelow := t.agents.scrollOffset+vpHeight < n

	if hasAbove {
		s.SetContent(startX+boxW-2, iy, '\u2191', nil, scrollStyle) // up arrow
	}

	// Agent rows.
	for vi := 0; vi < vpHeight; vi++ {
		idx := t.agents.scrollOffset + vi
		if idx >= n {
			break
		}
		row := iy + vi

		p := t.panes[idx]
		name := p.Name()
		hidden := t.layoutState.Hidden[paneKey(p)]
		pinned := t.layoutState.Pinned[paneKey(p)]
		act := p.Activity()
		bead := p.BeadID()

		vis := "[x]"
		if hidden {
			vis = "[ ]"
		}
		pin := "   "
		if pinned {
			pin = "[P]"
		}
		status := act.String()
		if bead != "" {
			status = fmt.Sprintf("%s (%s)", act.String(), bead)
		}

		marker := " "
		if idx == t.agents.selected && t.agents.moving {
			marker = ">"
		}

		line := fmt.Sprintf("%s%2d  %s %-12s %s  %s", marker, idx+1, vis, name, pin, status)

		style := normalStyle
		if idx == t.agents.selected {
			if t.agents.moving {
				style = movingStyle
			} else {
				style = selectedStyle
			}
			fillRow(row, style)
		} else if hidden {
			style = hiddenStyle
		}

		drawLine(row, line, style)
	}

	if hasBelow {
		belowY := iy + vpHeight - 1
		if belowY < startY+boxH-1 {
			s.SetContent(startX+boxW-2, belowY, '\u2193', nil, scrollStyle) // down arrow
		}
	}

	// Error line (second-to-last interior row).
	errY := startY + boxH - 3
	if t.agents.error != "" {
		drawLine(errY, " "+t.agents.error, errorStyle)
	}

	// Help line (last interior row).
	helpY := startY + boxH - 2
	help := " Up/Down move  Space hide  Enter grab  p pin  A all  R reset  Esc close"
	drawLine(helpY, help, helpStyle)
}
