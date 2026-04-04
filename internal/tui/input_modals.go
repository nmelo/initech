package tui

import "github.com/gdamore/tcell/v2"

// handleHelpKey processes key events while the help modal is open.
func (t *TUI) handleHelpKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.help.active = false
		return false
	case tcell.KeyUp:
		if t.help.scrollOffset > 0 {
			t.help.scrollOffset--
		}
		return false
	case tcell.KeyDown:
		if t.help.scrollOffset < t.helpMaxOffset() {
			t.help.scrollOffset++
		}
		return false
	case tcell.KeyRune:
		switch ev.Rune() {
		case '`', 'q':
			t.help.active = false
			return false
		case 'j':
			if t.help.scrollOffset < t.helpMaxOffset() {
				t.help.scrollOffset++
			}
			return false
		case 'k':
			if t.help.scrollOffset > 0 {
				t.help.scrollOffset--
			}
			return false
		}
	}
	return false
}

