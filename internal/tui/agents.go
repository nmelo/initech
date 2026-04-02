// agents.go implements the agent management modal. It displays all agents in
// current display order with visibility, pin state, activity, and bead info.
// Opened via backtick+agents command or Alt+a shortcut.
package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
)

// openAgentsModal initializes and opens the agent management modal.
func (t *TUI) openAgentsModal() {
	t.agents.active = true
	t.agents.selected = 0
}

// handleAgentsKey processes key events while the agents modal is open.
func (t *TUI) handleAgentsKey(ev *tcell.EventKey) bool {
	n := len(t.panes)
	if n == 0 {
		t.agents.active = false
		return false
	}

	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.agents.active = false
		return false
	case tcell.KeyUp:
		if t.agents.selected > 0 {
			t.agents.selected--
		}
		return false
	case tcell.KeyDown:
		if t.agents.selected < n-1 {
			t.agents.selected++
		}
		return false
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q', '`':
			t.agents.active = false
			return false
		case 'j':
			if t.agents.selected < n-1 {
				t.agents.selected++
			}
			return false
		case 'k':
			if t.agents.selected > 0 {
				t.agents.selected--
			}
			return false
		}
	}
	return false
}

// renderAgents draws the full-screen agent management modal.
func (t *TUI) renderAgents() {
	s := t.screen
	sw, sh := s.Size()

	titleStyle := tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorBlack).Bold(true)
	headerStyle := tcell.StyleDefault.Bold(true).Foreground(tcell.ColorWhite)
	normalStyle := tcell.StyleDefault.Foreground(tcell.ColorSilver)
	selectedStyle := tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite)
	hiddenStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)
	helpStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)

	// Title bar.
	title := " initech agents "
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
		if y >= sh-2 {
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

		line := fmt.Sprintf("  %2d  %s %-12s %s  %s", i+1, vis, name, pin, status)

		// Pick style.
		style := normalStyle
		if i == t.agents.selected {
			style = selectedStyle
		} else if hidden {
			style = hiddenStyle
		}

		// Fill row background for selected row.
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

	// Help line at bottom.
	help := "  Up/Down move   Esc/q close"
	for i, ch := range help {
		if i < sw {
			s.SetContent(i, sh-1, ch, nil, helpStyle)
		}
	}
}
