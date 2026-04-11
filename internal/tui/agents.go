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
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

// agentsBoxW and agentsBoxH are the target floating box dimensions.
const agentsBoxW = 94
const agentsBoxH = 38

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
	t.agents.searching = false
	t.agents.searchBuf = nil
	t.agents.filtered = nil
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
		t.agents.searching = false
		t.agents.searchBuf = nil
		t.agents.filtered = nil
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

	// Search mode: route keys to search input.
	if t.agents.searching {
		return t.handleAgentsSearchKey(ev)
	}

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
		case '/':
			t.agents.searching = true
			t.agents.searchBuf = nil
			t.agents.moving = false
			t.agentsRefilter()
			return false
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
		default:
			// Number keys 0-9: pin selected agent to that live slot.
			if t.layoutState.Mode == LayoutLive && ev.Rune() >= '0' && ev.Rune() <= '9' {
				t.agentsLivePin(int(ev.Rune() - '0'))
				return false
			}
		}
	}
	return false
}

// agentsLivePin pins the selected agent to the given slot index in live mode.
func (t *TUI) agentsLivePin(slot int) {
	if t.agents.selected < 0 || t.agents.selected >= len(t.panes) {
		return
	}
	totalSlots := t.layoutState.GridCols * t.layoutState.GridRows
	if slot >= totalSlots {
		t.agents.error = fmt.Sprintf("slot %d does not exist (grid has %d slots)", slot, totalSlots)
		return
	}
	if t.layoutState.LivePinned == nil {
		t.layoutState.LivePinned = make(map[string]int)
	}
	name := paneKey(t.panes[t.agents.selected])
	// Remove any existing pin on this slot.
	for k, v := range t.layoutState.LivePinned {
		if v == slot {
			delete(t.layoutState.LivePinned, k)
		}
	}
	t.layoutState.LivePinned[name] = slot
	if t.liveEngine != nil {
		t.liveEngine.Pinned = t.layoutState.LivePinned
	}
	t.applyLayout()
	t.saveLayoutIfConfigured()
}

// handleAgentsSearchKey processes keys while in search mode.
func (t *TUI) handleAgentsSearchKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape:
		// Clear search and restore full list.
		t.agents.searching = false
		t.agents.searchBuf = nil
		t.agents.filtered = nil
		t.agents.selected = 0
		t.agents.scrollOffset = 0
		return false

	case tcell.KeyEnter:
		// Select from filtered list, then exit search.
		if t.agents.filtered != nil && len(t.agents.filtered) > 0 && t.agents.selected < len(t.agents.filtered) {
			t.agents.selected = t.agents.filtered[t.agents.selected]
		}
		t.agents.searching = false
		t.agents.searchBuf = nil
		t.agents.filtered = nil
		return false

	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(t.agents.searchBuf) > 0 {
			t.agents.searchBuf = t.agents.searchBuf[:len(t.agents.searchBuf)-1]
			t.agentsRefilter()
		}
		return false

	case tcell.KeyUp:
		if t.agents.selected > 0 {
			t.agents.selected--
		}
		t.agentsEnsureVisibleFiltered()
		return false

	case tcell.KeyDown:
		maxIdx := t.agentsFilteredCount() - 1
		if maxIdx < 0 {
			maxIdx = 0
		}
		if t.agents.selected < maxIdx {
			t.agents.selected++
		}
		t.agentsEnsureVisibleFiltered()
		return false

	case tcell.KeyRune:
		r := ev.Rune()
		if unicode.IsPrint(r) {
			t.agents.searchBuf = append(t.agents.searchBuf, r)
			t.agentsRefilter()
		}
		return false
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

// agentsRefilter recomputes the filtered index list from the current searchBuf.
// Empty search matches all agents. Resets selection and scroll when the list changes.
func (t *TUI) agentsRefilter() {
	query := strings.ToLower(string(t.agents.searchBuf))
	t.agents.filtered = nil
	for i, p := range t.panes {
		if query == "" || strings.Contains(strings.ToLower(p.Name()), query) {
			t.agents.filtered = append(t.agents.filtered, i)
		}
	}
	// Clamp selection.
	max := len(t.agents.filtered) - 1
	if max < 0 {
		max = 0
	}
	if t.agents.selected > max {
		t.agents.selected = max
	}
	t.agents.scrollOffset = 0
	t.agentsEnsureVisibleFiltered()
}

// agentsFilteredCount returns the number of visible agents (filtered or all).
func (t *TUI) agentsFilteredCount() int {
	if t.agents.filtered != nil {
		return len(t.agents.filtered)
	}
	return len(t.panes)
}

// agentsEnsureVisibleFiltered adjusts scrollOffset for the filtered list.
func (t *TUI) agentsEnsureVisibleFiltered() {
	if t.screen == nil {
		return // No screen in test context.
	}
	vp := t.agentsViewportHeight()
	if t.agents.searching {
		// Account for search bar row.
		vp--
		if vp < 1 {
			vp = 1
		}
	}
	if t.agents.selected < t.agents.scrollOffset {
		t.agents.scrollOffset = t.agents.selected
	}
	if t.agents.selected >= t.agents.scrollOffset+vp {
		t.agents.scrollOffset = t.agents.selected - vp + 1
	}
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

	// Search bar (shown when searching, takes one row before the header).
	searchStyle := bgStyle.Foreground(tcell.ColorYellow)
	if t.agents.searching {
		searchLine := fmt.Sprintf(" / %s_", string(t.agents.searchBuf))
		drawLine(iy, searchLine, searchStyle)
		iy++
	}

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

	// Build the display list: filtered indices when searching, all panes otherwise.
	var displayIndices []int
	if t.agents.searching && t.agents.filtered != nil {
		displayIndices = t.agents.filtered
	} else {
		displayIndices = make([]int, len(t.panes))
		for i := range t.panes {
			displayIndices[i] = i
		}
	}
	n := len(displayIndices)

	// Clamp scroll offset.
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

	// "No matches" message when searching with no results.
	if t.agents.searching && n == 0 {
		dimStyle := bgStyle.Foreground(tcell.ColorGray)
		drawLine(iy, "  no matches", dimStyle)
	}

	// Scroll indicators.
	hasAbove := t.agents.scrollOffset > 0
	hasBelow := t.agents.scrollOffset+vpHeight < n

	if hasAbove {
		s.SetContent(startX+boxW-2, iy, '\u2191', nil, scrollStyle) // up arrow
	}

	// Agent rows.
	for vi := 0; vi < vpHeight; vi++ {
		dispIdx := t.agents.scrollOffset + vi
		if dispIdx >= n {
			break
		}
		idx := displayIndices[dispIdx] // index into t.panes
		row := iy + vi

		p := t.panes[idx]
		name := p.Name()
		hidden := t.layoutState.Hidden[paneKey(p)]
		pk := paneKey(p)
		generalPinned := t.layoutState.Pinned[pk]
		_, livePinned := t.layoutState.LivePinned[pk]
		act := p.Activity()
		bead := p.BeadID()

		vis := "[x]"
		if hidden {
			vis = "[ ]"
		}
		pin := "   "
		if generalPinned || livePinned {
			pin = "[P]"
		}

		// Live mode: show slot info. Live-pinned shows P:N, dynamic shows D:N.
		// General-pinned agents not in LivePinned keep [P].
		if t.layoutState.Mode == LayoutLive {
			if liveSlot, lp := t.layoutState.LivePinned[pk]; lp {
				pin = fmt.Sprintf("P:%d", liveSlot)
			} else {
				// Check if agent is in a dynamic slot.
				for si, sn := range t.layoutState.LiveSlots {
					if sn == pk {
						if generalPinned {
							pin = fmt.Sprintf("P:%d", si)
						} else {
							pin = fmt.Sprintf("D:%d", si)
						}
						break
					}
				}
			}
		}

		status := act.String()
		_ = bead // Bead info shown in pane ribbon, not in modal rows.

		marker := " "
		if !t.agents.searching && idx == t.agents.selected && t.agents.moving {
			marker = ">"
		}

		line := fmt.Sprintf("%s%2d  %s %-12s %s  %s", marker, idx+1, vis, name, pin, status)
		prefix := fmt.Sprintf("%s%2d  %s ", marker, idx+1, vis)

		// Highlight: in search mode, selected refers to position in filtered list.
		isSelected := false
		if t.agents.searching {
			isSelected = dispIdx == t.agents.selected
		} else {
			isSelected = idx == t.agents.selected
		}

		style := normalStyle
		if isSelected {
			if !t.agents.searching && t.agents.moving {
				style = movingStyle
			} else {
				style = selectedStyle
			}
			fillRow(row, style)
		} else if hidden {
			style = hiddenStyle
		}

		drawLine(row, line, style)
		if hidden {
			nameStyle := style.Italic(true)
			nameCol := innerX + len([]rune(prefix))
			for _, ch := range name {
				if nameCol >= innerX+innerW {
					break
				}
				s.SetContent(nameCol, row, ch, nil, nameStyle)
				nameCol++
			}
		}
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
	if t.agents.searching {
		help = " Type to filter  Up/Down navigate  Enter select  Esc clear  / search"
	}
	drawLine(helpY, help, helpStyle)
}
