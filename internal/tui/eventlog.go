// eventlog.go implements the event log modal: a full-screen scrollable history
// of all agent events from the last 60 minutes (up to 100 events).
// Opened with the "log" or "events" command. Read-only; no actions.

package tui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
)

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

// eventLogVisibleRows returns the number of event rows visible in the modal.
func (t *TUI) eventLogVisibleRows() int {
	if t.screen == nil {
		return 10
	}
	_, sh := t.screen.Size()
	// Title (1) + blank (1) + help (1) + bottom padding (1) = 4 overhead rows.
	rows := sh - 4
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

// renderEventLog draws the full-screen event log modal.
func (t *TUI) renderEventLog() {
	s := t.screen
	sw, sh := s.Size()
	if sw < 40 || sh < 5 {
		drawField(s, 0, 0, sw, "Terminal too narrow for event log", tcell.StyleDefault.Foreground(tcell.ColorRed))
		return
	}

	titleStyle := tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorBlack).Bold(true)
	helpStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)
	emptyStyle := tcell.StyleDefault.Foreground(tcell.ColorDarkGray)

	// Title bar.
	count := len(t.eventLog)
	var titleText string
	if count == 0 {
		titleText = " Event Log (no events recorded) "
	} else {
		titleText = fmt.Sprintf(" Event Log (last 60 min, %d events) ", count)
	}
	// Center the title text.
	for i := 0; i < sw; i++ {
		s.SetContent(i, 0, ' ', nil, titleStyle)
	}
	titleStart := (sw - len([]rune(titleText))) / 2
	if titleStart < 0 {
		titleStart = 0
	}
	for i, ch := range titleText {
		if titleStart+i < sw {
			s.SetContent(titleStart+i, 0, ch, nil, titleStyle)
		}
	}

	if count == 0 {
		msg := "  No events recorded. Events appear here when agents complete, claim, fail, stall, or go idle."
		runes := []rune(msg)
		for i, ch := range runes {
			if i < sw {
				s.SetContent(i, 2, ch, nil, emptyStyle)
			}
		}
		help := "  Esc:close"
		for i, ch := range help {
			if i < sw {
				s.SetContent(i, sh-1, ch, nil, helpStyle)
			}
		}
		return
	}

	// Compute the visible window.
	// eventLog is ordered oldest-first, newest-last.
	// scrollOffset=0 means show the tail (latest events at the bottom of the viewport).
	visibleRows := t.eventLogVisibleRows()
	// Clamp scroll offset.
	maxOff := t.eventLogMaxOffset()
	if t.eventLogM.scrollOffset > maxOff {
		t.eventLogM.scrollOffset = maxOff
	}

	// The index of the first event to show.
	startIdx := len(t.eventLog) - visibleRows - t.eventLogM.scrollOffset
	if startIdx < 0 {
		startIdx = 0
	}
	endIdx := startIdx + visibleRows
	if endIdx > len(t.eventLog) {
		endIdx = len(t.eventLog)
	}

	// Render event rows starting at y=1 (below title).
	today := time.Now()
	for row, i := 1, startIdx; i < endIdx; i++ {
		ev := t.eventLog[i]
		style := eventLogStyle(ev.Type)

		// Timestamp: HH:MM, with date prefix for events from a different day.
		var ts string
		evDay := ev.Time.Format("2006-01-02")
		todayDay := today.Format("2006-01-02")
		if evDay != todayDay {
			ts = ev.Time.Format("01/02 15:04")
		} else {
			ts = ev.Time.Format("15:04")
		}

		// Format: "  HH:MM  [agent]  detail"
		line := fmt.Sprintf("  %-7s  [%-6s]  %s", ts, ev.Pane, ev.Detail)
		runes := []rune(line)
		if len(runes) > sw-1 {
			runes = append(runes[:sw-2], '\u2026')
		}
		for x, ch := range runes {
			s.SetContent(x, row, ch, nil, style)
		}
		row++
	}

	// Help line at the bottom.
	help := "  Esc:close  j/k:scroll  PgUp/PgDn:fast  Home/End:jump"
	for i, ch := range help {
		if i < sw {
			s.SetContent(i, sh-1, ch, nil, helpStyle)
		}
	}
}

// eventLogStyle returns the tcell style for a given event type.
func eventLogStyle(et EventType) tcell.Style {
	switch et {
	case EventBeadCompleted:
		return tcell.StyleDefault.Foreground(tcell.ColorGreen)
	case EventBeadFailed, EventAgentStuck:
		return tcell.StyleDefault.Foreground(tcell.ColorRed)
	case EventAgentStalled:
		return tcell.StyleDefault.Foreground(tcell.ColorYellow)
	case EventBeadClaimed:
		return tcell.StyleDefault.Foreground(tcell.ColorDodgerBlue)
	case EventAgentIdle:
		return tcell.StyleDefault.Foreground(tcell.ColorGray)
	default:
		return tcell.StyleDefault.Foreground(tcell.ColorSilver)
	}
}
