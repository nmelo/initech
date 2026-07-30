// quickgrid.go implements the Option+G / Option+L quick grid/live dimension
// popup (ini-dvy5): a small centered box where typing two digits reshapes
// the layout immediately, no Enter. Digits are columns then rows, matching
// :grid CxR and :live CxR exactly -- the popup builds the same "CxR" string
// those commands already parse and calls cmdGrid/cmdLive directly, so this
// file adds no new layout logic, only an input affordance over it.
package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
)

// openQuickGrid opens the quick grid/live dimension popup. live selects
// which command the two typed digits apply on submit: false for Option+G
// (cmdGrid), true for Option+L (cmdLive).
func (t *TUI) openQuickGrid(live bool) {
	t.quickGrid.active = true
	t.quickGrid.live = live
	t.quickGrid.firstDigit = 0
}

// handleQuickGridKey handles keyboard input while the quick grid popup is
// open. The popup is digit-only: 1-9 accumulate into columns then rows,
// '0' and any other non-digit rune are inert (spec: ignore, don't close,
// don't apply), Backspace clears the first digit, and Esc/Ctrl+C cancel
// with no layout change. Another modal's chord (an Alt combo, or backtick
// for the command bar) closes this popup rather than stacking modals,
// matching how every other modal in this codebase swallows an unrecognized
// key instead of re-dispatching to open a different one in the same
// keystroke.
func (t *TUI) handleQuickGridKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.quickGrid.active = false
		return false
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		t.quickGrid.firstDigit = 0
		return false
	case tcell.KeyRune:
		r := ev.Rune()
		if ev.Modifiers()&tcell.ModAlt != 0 {
			t.quickGrid.active = false
			return false
		}
		if r == '`' && ev.Modifiers() == 0 {
			t.quickGrid.active = false
			return false
		}
		if r < '1' || r > '9' {
			return false
		}
		digit := int(r - '0')
		if t.quickGrid.firstDigit == 0 {
			t.quickGrid.firstDigit = digit
			return false
		}
		cols, rows := t.quickGrid.firstDigit, digit
		live := t.quickGrid.live
		t.quickGrid.active = false
		t.quickGrid.firstDigit = 0
		gridArg := fmt.Sprintf("%dx%d", cols, rows)
		if live {
			t.cmdLive([]string{"live", gridArg})
		} else {
			t.cmdGrid([]string{"grid", gridArg})
		}
		return false
	default:
		// Arrows, function keys, etc.: inert, same as any other non-digit.
		return false
	}
}

// renderQuickGrid draws the centered floating popup. Content shown:
// which mode will apply (GRID or LIVE), and the first digit once typed
// (as "N columns" -- the only state that's ever actually seen on screen,
// since the second digit applies and closes the popup within the same
// keystroke's render, before the next frame draws).
func (t *TUI) renderQuickGrid() {
	s := t.screen
	sw, sh := s.Size()

	boxW, boxH := 27, 7
	if boxW > sw-2 {
		boxW = sw - 2
	}
	if boxH > sh-2 {
		boxH = sh - 2
	}
	if boxW < 15 || boxH < 5 {
		return // Terminal too small for the popup; silently skip rather than corrupt the frame.
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

	innerW := boxW - 2
	innerX := startX + 1

	// drawCentered writes text horizontally centered within the box's
	// interior on row y.
	drawCentered := func(y int, text string, style tcell.Style) {
		runes := []rune(text)
		start := innerX + (innerW-len(runes))/2
		if start < innerX {
			start = innerX
		}
		for i, ch := range runes {
			if start+i >= innerX+innerW {
				break
			}
			s.SetContent(start+i, y, ch, nil, style)
		}
	}

	mode := "GRID"
	if t.quickGrid.live {
		mode = "LIVE"
	}
	drawCentered(startY, " "+mode+" ", titleStyle)

	var status string
	if t.quickGrid.firstDigit == 0 {
		status = "type columns, then rows"
	} else {
		status = fmt.Sprintf("%d columns × ? rows", t.quickGrid.firstDigit)
	}
	drawCentered(startY+2, status, bgStyle)

	drawCentered(startY+boxH-2, "esc cancel", helpStyle)
}
