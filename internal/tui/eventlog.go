// eventlog.go implements the event log modal: a centered floating scrollable
// history of all agent events from the last 60 minutes (up to 100 events).
// Opened with the "log" or "events" command. Read-only; no actions.
// Visual style matches the agents modal (agents.go) for consistency.

package tui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
)

// eventLogChromeRows is the number of rows consumed by modal chrome:
// title border (1) + blank line (1) + help line (1) + bottom border (1).
const eventLogChromeRows = 4

// handleEventLogKey handles keyboard input while the event log modal is open.
// Returns false always (event log never quits the TUI).
func (t *TUI) handleEventLogKey(ev *tcell.EventKey) bool {
	maxOff := t.eventLogMaxOffset()
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.eventLogM.active = false
	case tcell.KeyUp:
		if t.eventLogM.scrollOffset < maxOff {
			t.eventLogM.scrollOffset++
		}
	case tcell.KeyDown:
		if t.eventLogM.scrollOffset > 0 {
			t.eventLogM.scrollOffset--
		}
	case tcell.KeyPgUp:
		t.eventLogM.scrollOffset += t.eventLogVisibleRows()
		if t.eventLogM.scrollOffset > maxOff {
			t.eventLogM.scrollOffset = maxOff
		}
	case tcell.KeyPgDn:
		t.eventLogM.scrollOffset -= t.eventLogVisibleRows()
		if t.eventLogM.scrollOffset < 0 {
			t.eventLogM.scrollOffset = 0
		}
	case tcell.KeyHome:
		t.eventLogM.scrollOffset = maxOff
	case tcell.KeyEnd:
		t.eventLogM.scrollOffset = 0
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'k':
			if t.eventLogM.scrollOffset < maxOff {
				t.eventLogM.scrollOffset++
			}
		case 'j':
			if t.eventLogM.scrollOffset > 0 {
				t.eventLogM.scrollOffset--
			}
		case 'q':
			t.eventLogM.active = false
		case '`':
			t.eventLogM.active = false
		}
	}
	return false
}

// eventLogVisibleRows returns the number of event rows visible inside the modal.
func (t *TUI) eventLogVisibleRows() int {
	if t.screen == nil {
		return 10
	}
	_, sh := t.screen.Size()
	boxH := sh * 7 / 10
	if boxH < 10 {
		boxH = 10
	}
	if sh-4 < boxH {
		boxH = sh - 4
	}
	rows := boxH - eventLogChromeRows
	if rows < 1 {
		rows = 1
	}
	return rows
}

// eventLogMaxOffset returns the maximum scroll offset (scrolled all the way to top).
func (t *TUI) eventLogMaxOffset() int {
	visible := t.eventLogVisibleRows()
	max := len(t.eventLog) - visible
	if max < 0 {
		max = 0
	}
	return max
}

// renderEventLog draws the centered floating event log modal.
func (t *TUI) renderEventLog() {
	s := t.screen
	sw, sh := s.Size()

	// Compute box dimensions: 80% width, 70% height, with minimums.
	boxW := sw * 8 / 10
	if boxW < 60 {
		boxW = 60
	}
	if sw-4 < boxW {
		boxW = sw - 4
	}
	if boxW < 20 {
		boxW = 20
	}
	boxH := sh * 7 / 10
	if boxH < 10 {
		boxH = 10
	}
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
	helpStyle := bgStyle.Foreground(tcell.ColorGray)
	emptyStyle := bgStyle.Foreground(tcell.ColorDarkGray)
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
		runes := []rune(text)
		for i, ch := range runes {
			if i >= innerW {
				break
			}
			s.SetContent(innerX+i, y, ch, nil, style)
		}
	}

	// Title centered in top border.
	count := len(t.eventLog)
	var titleText string
	if count == 0 {
		titleText = " Events "
	} else {
		titleText = fmt.Sprintf(" Events (%d) ", count)
	}
	titleStart := startX + (boxW-len([]rune(titleText)))/2
	if titleStart < startX+1 {
		titleStart = startX + 1
	}
	for i, ch := range titleText {
		if titleStart+i < startX+boxW-1 {
			s.SetContent(titleStart+i, startY, ch, nil, titleStyle)
		}
	}

	// Content area: starts after top border + blank line.
	vpStartY := startY + 1
	vpHeight := boxH - eventLogChromeRows
	if vpHeight < 1 {
		vpHeight = 1
	}

	if count == 0 {
		msg := "No events recorded"
		msgY := vpStartY + vpHeight/2
		msgX := innerX + (innerW-len([]rune(msg)))/2
		if msgX < innerX {
			msgX = innerX
		}
		for i, ch := range msg {
			if msgX+i < innerX+innerW {
				s.SetContent(msgX+i, msgY, ch, nil, emptyStyle)
			}
		}
		helpY := startY + boxH - 2
		drawLine(helpY, " Esc close", helpStyle)
		return
	}

	// Clamp scroll.
	maxOff := t.eventLogMaxOffset()
	if t.eventLogM.scrollOffset > maxOff {
		t.eventLogM.scrollOffset = maxOff
	}

	// Compute visible window. eventLog is oldest-first, newest-last.
	// scrollOffset=0 means showing the tail (newest at bottom).
	startIdx := len(t.eventLog) - vpHeight - t.eventLogM.scrollOffset
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := startIdx + vpHeight
	if endIdx > len(t.eventLog) {
		endIdx = len(t.eventLog)
	}

	hasAbove := t.eventLogM.scrollOffset < maxOff
	hasBelow := t.eventLogM.scrollOffset > 0

	// Scroll indicators.
	if hasAbove {
		s.SetContent(startX+boxW-2, vpStartY, '\u2191', nil, scrollStyle)
	}
	if hasBelow {
		belowY := vpStartY + vpHeight - 1
		if belowY < startY+boxH-1 {
			s.SetContent(startX+boxW-2, belowY, '\u2193', nil, scrollStyle)
		}
	}

	// Render event rows.
	today := time.Now()
	for row := 0; row < vpHeight && startIdx+row < endIdx; row++ {
		ev := t.eventLog[startIdx+row]
		style := eventLogRowStyle(ev.Type, bgStyle)

		var ts string
		if ev.Time.Format("2006-01-02") != today.Format("2006-01-02") {
			ts = ev.Time.Format("01/02 15:04:05")
		} else {
			ts = ev.Time.Format("15:04:05")
		}

		// Format: " HH:MM:SS  agent     type            detail"
		line := fmt.Sprintf(" %-14s  %-8s  %-18s  %s", ts, ev.Pane, ev.Type, ev.Detail)
		runes := []rune(line)
		if len(runes) > innerW {
			runes = runes[:innerW]
		}
		y := vpStartY + row
		for i, ch := range runes {
			s.SetContent(innerX+i, y, ch, nil, style)
		}
	}

	// Help line + scroll position (last interior row before bottom border).
	helpY := startY + boxH - 2
	help := " j/k scroll  PgUp/PgDn fast  Esc close"
	drawLine(helpY, help, helpStyle)

	// Scroll position indicator right-aligned.
	if count > vpHeight {
		bottomVisible := len(t.eventLog) - t.eventLogM.scrollOffset
		topVisible := bottomVisible - vpHeight + 1
		if topVisible < 1 {
			topVisible = 1
		}
		pos := fmt.Sprintf("%d-%d/%d ", topVisible, bottomVisible, count)
		posStart := innerX + innerW - len([]rune(pos))
		for i, ch := range pos {
			if posStart+i >= innerX && posStart+i < innerX+innerW {
				s.SetContent(posStart+i, helpY, ch, nil, scrollStyle)
			}
		}
	}
}

// eventLogRowStyle returns the tcell style for a given event type,
// using the modal background color for visual consistency.
func eventLogRowStyle(et EventType, bg tcell.Style) tcell.Style {
	switch et {
	case EventBeadCompleted:
		return bg.Foreground(tcell.ColorGreen)
	case EventBeadFailed, EventAgentStuck:
		return bg.Foreground(tcell.ColorRed)
	case EventAgentStalled:
		return bg.Foreground(tcell.ColorYellow)
	case EventBeadClaimed:
		return bg.Foreground(tcell.ColorDodgerBlue)
	case EventAgentSuspended, EventAgentResumed:
		return bg.Foreground(tcell.ColorDodgerBlue)
	case EventAgentIdle:
		return bg.Foreground(tcell.ColorGray)
	default:
		return bg.Foreground(tcell.ColorSilver)
	}
}
