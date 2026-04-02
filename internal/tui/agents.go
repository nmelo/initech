// agents.go implements the agent management modal. It displays all agents in
// current display order with visibility, pin state, activity, and bead info.
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

// openAgentsModal initializes and opens the agent management modal.
func (t *TUI) openAgentsModal() {
	t.agents.active = true
	t.agents.selected = 0
	t.agents.moving = false
	t.agents.error = ""
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
	// Reset selection to top after reorder.
	t.agents.selected = 0
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

// renderAgents draws the full-screen agent management modal.
func (t *TUI) renderAgents() {
	s := t.screen
	sw, sh := s.Size()

	titleStyle := tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorBlack).Bold(true)
	headerStyle := tcell.StyleDefault.Bold(true).Foreground(tcell.ColorWhite)
	normalStyle := tcell.StyleDefault.Foreground(tcell.ColorSilver)
	selectedStyle := tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite)
	movingStyle := tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorWhite).Bold(true)
	hiddenStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)
	helpStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)
	errorStyle := tcell.StyleDefault.Foreground(tcell.ColorRed)

	// Title bar.
	title := " initech agents "
	if t.agents.moving {
		sel := t.agents.selected
		if sel >= 0 && sel < len(t.panes) {
			title = fmt.Sprintf(" moving %s ", t.panes[sel].Name())
		}
	}
	for x := 0; x < sw; x++ {
		s.SetContent(x, 0, ' ', nil, titleStyle)
	}
	titleStart := (sw - len([]rune(title))) / 2
	if titleStart < 0 {
		titleStart = 0
	}
	for i, ch := range title {
		if titleStart+i < sw {
			s.SetContent(titleStart+i, 0, ch, nil, titleStyle)
		}
	}

	// Header row.
	y := 2
	header := fmt.Sprintf("  %-4s %-3s %-12s %-5s %s", "#", "VIS", "AGENT", "PIN", "STATUS")
	for i, ch := range header {
		if i < sw {
			s.SetContent(i, y, ch, nil, headerStyle)
		}
	}
	y++
	// Separator.
	for x := 0; x < sw; x++ {
		s.SetContent(x, y, '\u2500', nil, tcell.StyleDefault.Foreground(tcell.ColorGray))
	}
	y++

	// Agent rows.
	for i, p := range t.panes {
		if y >= sh-3 {
			break
		}

		name := p.Name()
		hidden := t.layoutState.Hidden[paneKey(p)]
		pinned := t.layoutState.Pinned[paneKey(p)]
		act := p.Activity()
		bead := p.BeadID()

		// Visibility checkbox.
		vis := "[x]"
		if hidden {
			vis = "[ ]"
		}

		// Pin badge.
		pin := "   "
		if pinned {
			pin = "[P]"
		}

		// Status with bead.
		status := act.String()
		if bead != "" {
			status = fmt.Sprintf("%s (%s)", act.String(), bead)
		}

		// Row marker.
		marker := "  "
		if i == t.agents.selected && t.agents.moving {
			marker = "> "
		}

		line := fmt.Sprintf("%s%2d  %s %-12s %s  %s", marker, i+1, vis, name, pin, status)

		// Pick style.
		style := normalStyle
		if i == t.agents.selected {
			if t.agents.moving {
				style = movingStyle
			} else {
				style = selectedStyle
			}
		} else if hidden {
			style = hiddenStyle
		}

		// Fill row background for selected/moving row.
		if i == t.agents.selected {
			for x := 0; x < sw; x++ {
				s.SetContent(x, y, ' ', nil, style)
			}
		}

		for j, ch := range line {
			if j < sw {
				s.SetContent(j, y, ch, nil, style)
			}
		}
		y++
	}

	// Error line (above help).
	if t.agents.error != "" {
		errY := sh - 2
		for i, ch := range t.agents.error {
			if 2+i < sw {
				s.SetContent(2+i, errY, ch, nil, errorStyle)
			}
		}
	}

	// Help line at bottom.
	help := "  Up/Down move  Space toggle visible  Enter grab/drop  p pin  A reveal all  R reset order  Esc close"
	for i, ch := range help {
		if i < sw {
			s.SetContent(i, sh-1, ch, nil, helpStyle)
		}
	}
}
