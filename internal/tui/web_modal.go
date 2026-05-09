// web_modal.go renders the Web Companion modal showing server status, URL,
// and available endpoints. Opened via the `web` command.
package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
)

const webBoxW = 94
const webBoxH = 28

// handleWebKey processes key events while the web modal is open.
func (t *TUI) handleWebKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.webM.active = false
		return false
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q', '`':
			t.webM.active = false
			return false
		case 'c':
			t.webCopyURL()
			return false
		}
	}
	return false
}

// renderWebModal draws the web companion modal.
func (t *TUI) renderWebModal() {
	s := t.screen
	sw, sh := s.Size()

	boxW := webBoxW
	if sw-4 < boxW {
		boxW = sw - 4
	}
	if boxW < 30 {
		boxW = 30
	}
	boxH := webBoxH
	if sh-4 < boxH {
		boxH = sh - 4
	}
	if boxH < 10 {
		boxH = 10
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
	labelStyle := bgStyle.Foreground(tcell.ColorWhite).Bold(true)
	greenStyle := bgStyle.Foreground(tcell.ColorGreen)
	dimStyle := bgStyle.Foreground(tcell.ColorGray)
	codeStyle := bgStyle.Foreground(tcell.ColorYellow)
	helpStyle := bgStyle.Foreground(tcell.ColorGray)

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
	title := " initech web "
	titleStart := startX + (boxW-len([]rune(title)))/2
	for i, ch := range title {
		if titleStart+i < startX+boxW-1 {
			s.SetContent(titleStart+i, startY, ch, nil, titleStyle)
		}
	}

	iy := startY + 2

	disabled := t.webPort == 0

	// Status line.
	if disabled {
		drawLine(iy, " Status     disabled (set --web-port to enable)", dimStyle)
		helpY := startY + boxH - 2
		drawLine(helpY, " [Esc] close", helpStyle)
		return
	}

	host := mcpLANIP()
	url := fmt.Sprintf("http://%s:%d", host, t.webPort)

	drawLine(iy, fmt.Sprintf(" Status     running on 0.0.0.0:%d", t.webPort), greenStyle)
	iy += 2

	// URL.
	drawLine(iy, " URL        ", labelStyle)
	col := innerX + 12
	for _, ch := range url {
		if col >= innerX+innerW {
			break
		}
		s.SetContent(col, iy, ch, nil, codeStyle)
		col++
	}
	iy += 2

	// Endpoints.
	drawLine(iy, " Endpoints", labelStyle)
	iy++
	for x := innerX + 1; x < innerX+innerW-1; x++ {
		s.SetContent(x, iy, '\u2500', nil, borderStyle)
	}
	iy++

	endpoints := []struct {
		path string
		desc string
	}{
		{"/", "Web companion SPA"},
		{"/api/panes", "Agent list (JSON)"},
		{"/ws/pane/<name>", "PTY byte stream (WebSocket)"},
	}
	for _, ep := range endpoints {
		line := fmt.Sprintf("   %-22s %s", ep.path, ep.desc)
		drawLine(iy, line, dimStyle)
		iy++
	}

	// Help line.
	helpY := startY + boxH - 2
	drawLine(helpY, " [c] copy URL   [Esc] close", helpStyle)
}

// webCopyURL copies the web companion URL to clipboard via OSC 52.
func (t *TUI) webCopyURL() {
	if t.webPort == 0 {
		return
	}
	host := mcpLANIP()
	url := fmt.Sprintf("http://%s:%d", host, t.webPort)
	fmt.Print(buildOSC52(url))
}
