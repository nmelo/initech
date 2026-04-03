// help.go renders the help reference card as a centered floating modal.
// Opened by typing "help" or "?" in the command modal.
// Closed by pressing Esc, backtick, or q.
package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// helpLines is the static help content. Update when commands change.
var helpLines = []string{
	"Keybindings",
	"  Alt+Left/Right   Navigate between panes",
	"  Alt+1            Focus mode (single pane)",
	"  Alt+2            2x2 grid",
	"  Alt+3            3x3 grid",
	"  Alt+4            Main + stacked layout",
	"  Alt+a            Agent management modal",
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
	"  agents           Manage visibility, order, and pinning (Alt+a).",
	"  layout reset     Reset to auto-calculated defaults.",
	"  restart (r)      Kill and relaunch focused pane.",
	"  patrol           Bulk peek all agents.",
	"  top (ps)         Activity monitor (PID, memory, status).",
	"  add <name>       Add a new agent pane.",
	"  remove (rm) <n>  Remove an agent pane.",
	"  mcp              MCP server status and connection info.",
	"  help (?)         This screen.",
	"  quit (q)         Exit initech.",
	"",
	"Command bar editing",
	"  Ctrl+A / Home    Move to beginning of line",
	"  Ctrl+E / End     Move to end of line",
	"  Ctrl+B / Left    Move back one character",
	"  Ctrl+F / Right   Move forward one character",
	"  Ctrl+W           Delete word left of cursor",
	"  Ctrl+U           Delete to beginning of line",
	"  Ctrl+K           Delete to end of line",
	"  Ctrl+D / Delete  Delete character at cursor",
	"  Backspace        Delete character left of cursor",
	"",
	"found a bug? open an issue. have a fix? even better.",
	"github.com/nmelo/initech",
}

// helpBoxW and helpBoxH are the target floating box dimensions.
const helpBoxW = 94
const helpBoxH = 38

// helpChromeRows is the number of rows used by modal chrome
// (title border, spacer, footer help, bottom border).
const helpChromeRows = 3

// helpMaxOffset returns the maximum scroll offset for the help modal.
func (t *TUI) helpMaxOffset() int {
	vp := t.helpViewportHeight()
	max := len(helpLines) - vp
	if max < 0 {
		max = 0
	}
	return max
}

// helpViewportHeight returns the number of visible content rows inside the box.
func (t *TUI) helpViewportHeight() int {
	if t.screen == nil {
		return 1
	}
	_, sh := t.screen.Size()
	boxH := helpBoxH
	if sh-4 < boxH {
		boxH = sh - 4
	}
	if boxH < helpChromeRows+1 {
		return 1
	}
	return boxH - helpChromeRows
}

// renderHelp draws the centered floating help reference card.
func (t *TUI) renderHelp() {
	s := t.screen
	sw, sh := s.Size()

	// Compute box dimensions.
	boxW := helpBoxW
	if sw-4 < boxW {
		boxW = sw - 4
	}
	if boxW < 20 {
		boxW = 20
	}
	boxH := helpBoxH
	if sh-4 < boxH {
		boxH = sh - 4
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

	bgStyle := tcell.StyleDefault.Background(tcell.NewRGBColor(20, 20, 20)).Foreground(tcell.ColorSilver)
	borderStyle := bgStyle.Foreground(tcell.ColorGray)
	titleStyle := bgStyle.Foreground(tcell.ColorDodgerBlue).Bold(true)
	headerStyle := bgStyle.Foreground(tcell.ColorYellow).Bold(true)
	bodyStyle := bgStyle.Foreground(tcell.ColorSilver)
	helpStyle := bgStyle.Foreground(tcell.ColorGray)
	scrollStyle := bgStyle.Foreground(tcell.ColorDodgerBlue)

	// Draw opaque background.
	for y := startY; y < startY+boxH && y < sh; y++ {
		for x := startX; x < startX+boxW && x < sw; x++ {
			s.SetContent(x, y, ' ', nil, bgStyle)
		}
	}

	// Draw border.
	s.SetContent(startX, startY, '\u250c', nil, borderStyle)
	s.SetContent(startX+boxW-1, startY, '\u2510', nil, borderStyle)
	s.SetContent(startX, startY+boxH-1, '\u2514', nil, borderStyle)
	s.SetContent(startX+boxW-1, startY+boxH-1, '\u2518', nil, borderStyle)
	for x := startX + 1; x < startX+boxW-1 && x < sw; x++ {
		s.SetContent(x, startY, '\u2500', nil, borderStyle)
		s.SetContent(x, startY+boxH-1, '\u2500', nil, borderStyle)
	}
	for y := startY + 1; y < startY+boxH-1 && y < sh; y++ {
		s.SetContent(startX, y, '\u2502', nil, borderStyle)
		s.SetContent(startX+boxW-1, y, '\u2502', nil, borderStyle)
	}

	innerW := boxW - 2
	innerX := startX + 1

	drawLine := func(y int, text string, style tcell.Style) {
		for i, ch := range text {
			if i >= innerW {
				break
			}
			s.SetContent(innerX+i, y, ch, nil, style)
		}
	}

	// Title centered in top border.
	title := " initech help "
	if t.version != "" {
		title = fmt.Sprintf(" initech help (v%s) ", t.version)
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

	// Content viewport: starts at startY+1, ends before footer row.
	vpStartY := startY + 1
	vpHeight := boxH - helpChromeRows
	if vpHeight < 1 {
		vpHeight = 1
	}

	// Clamp scroll.
	maxScroll := len(helpLines) - vpHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if t.help.scrollOffset > maxScroll {
		t.help.scrollOffset = maxScroll
	}

	hasAbove := t.help.scrollOffset > 0
	hasBelow := t.help.scrollOffset+vpHeight < len(helpLines)

	// Scroll indicators.
	if hasAbove {
		s.SetContent(startX+boxW-2, vpStartY, '\u2191', nil, scrollStyle)
	}

	// Content rows.
	for row := 0; row < vpHeight; row++ {
		lineIdx := row + t.help.scrollOffset
		if lineIdx >= len(helpLines) {
			break
		}
		line := helpLines[lineIdx]
		y := vpStartY + row

		isFooter := strings.HasPrefix(line, "found a bug") || strings.HasPrefix(line, "github.com")
		style := bodyStyle
		if isFooter {
			footerBg := tcell.NewRGBColor(15, 20, 45)
			style = tcell.StyleDefault.Background(footerBg).Foreground(tcell.ColorYellow)
			// Fill interior with footer bg.
			for x := innerX; x < innerX+innerW; x++ {
				s.SetContent(x, y, ' ', nil, style)
			}
			drawLine(y, " "+line, style)
		} else {
			if len(line) > 0 && line[0] != ' ' {
				style = headerStyle
			}
			drawLine(y, " "+line, style)
		}
	}

	if hasBelow {
		belowY := vpStartY + vpHeight - 1
		if belowY < startY+boxH-1 {
			s.SetContent(startX+boxW-2, belowY, '\u2193', nil, scrollStyle)
		}
	}

	// Footer help (last interior row).
	helpY := startY + boxH - 2
	hint := " j/k scroll  Esc close"
	drawLine(helpY, hint, helpStyle)
}
