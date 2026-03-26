// help.go renders the full-screen help reference card modal.
// Opened by typing "help" or "?" in the command modal.
// Closed by pressing Esc, backtick, or q.
package tui

import "github.com/gdamore/tcell/v2"

// helpLines is the static help content. Update when commands change.
var helpLines = []string{
	"Keybindings",
	"  Alt+Left/Right   Navigate between panes",
	"  Alt+1            Focus mode (single pane)",
	"  Alt+2            2x2 grid",
	"  Alt+3            3x3 grid",
	"  Alt+4            Main + stacked layout",
	"  Alt+z            Zoom/unzoom focused pane",
	"  Alt+s            Toggle agent overlay",
	"  Alt+q            Quit",
	"",
	"Commands  (press ` to open)",
	"  grid [CxR]       Set grid layout (e.g. grid 3x2). No arg = auto.",
	"  focus [name]     Full-screen on a pane. No arg = current pane.",
	"  zoom             Toggle zoom on focused pane.",
	"  panel            Toggle agent status overlay.",
	"  main             Main pane left + stacked right.",
	"  show <name|all>  Show a hidden pane (or all).",
	"  hide <name>      Hide a pane from the grid.",
	"  view <n1> [n2]   Show only named panes, hide rest.",
	"  layout reset     Reset to auto-calculated defaults.",
	"  restart (r)      Kill and relaunch focused pane.",
	"  patrol           Bulk peek all agents, copy to clipboard.",
	"  top (ps)         Activity monitor (PID, memory, status).",
	"  add <name>       Add a new agent pane.",
	"  remove (rm) <n>  Remove an agent pane.",
	"  help (?)         This screen.",
	"  quit (q)         Exit initech.",
}

// renderHelp draws the full-screen help reference card.
func (t *TUI) renderHelp() {
	s := t.screen
	sw, sh := s.Size()
	if sw < 20 || sh < 5 {
		drawField(s, 0, 0, sw, "Terminal too small for help", tcell.StyleDefault.Foreground(tcell.ColorRed))
		return
	}

	titleStyle := tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorBlack).Bold(true)
	headerStyle := tcell.StyleDefault.Foreground(tcell.ColorYellow).Bold(true)
	bodyStyle := tcell.StyleDefault.Foreground(tcell.ColorSilver)
	helpStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)

	// Title bar.
	title := " initech help "
	for x := 0; x < sw; x++ {
		s.SetContent(x, 0, ' ', nil, titleStyle)
	}
	for i, ch := range title {
		if i < sw {
			s.SetContent(i, 0, ch, nil, titleStyle)
		}
	}

	// Content area: rows 1 to sh-2.
	contentRows := sh - 2
	if contentRows < 1 {
		contentRows = 1
	}

	// Cap scroll so we don't go past the end.
	maxScroll := len(helpLines) - contentRows
	if maxScroll < 0 {
		maxScroll = 0
	}
	if t.help.scrollOffset > maxScroll {
		t.help.scrollOffset = maxScroll
	}

	for row := 0; row < contentRows; row++ {
		lineIdx := row + t.help.scrollOffset
		if lineIdx >= len(helpLines) {
			break
		}
		line := helpLines[lineIdx]
		y := row + 1

		// Section headers (no leading space) use yellow; body lines use silver.
		style := bodyStyle
		if len(line) > 0 && line[0] != ' ' {
			style = headerStyle
		}
		for x := 0; x < sw; x++ {
			s.SetContent(x, y, ' ', nil, tcell.StyleDefault)
		}
		for i, ch := range line {
			if 1+i < sw {
				s.SetContent(1+i, y, ch, nil, style)
			}
		}
	}

	// Help hint at the bottom.
	hint := "  [j/k] scroll  [Esc/q/`] close"
	if maxScroll > 0 && t.help.scrollOffset < maxScroll {
		hint = "  [j/k] scroll  [Esc/q/`] close  (more below)"
	}
	for x := 0; x < sw; x++ {
		s.SetContent(x, sh-1, ' ', nil, tcell.StyleDefault)
	}
	for i, ch := range hint {
		if i < sw {
			s.SetContent(i, sh-1, ch, nil, helpStyle)
		}
	}
}
