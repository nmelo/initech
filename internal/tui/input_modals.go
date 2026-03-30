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

// handleReorderKey processes key events while the reorder modal is open.
func (t *TUI) handleReorderKey(ev *tcell.EventKey) bool {
	n := len(t.reorder.items)
	if n == 0 {
		t.reorder.active = false
		return false
	}

	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		// Cancel: discard changes.
		t.reorder.active = false
		return false

	case tcell.KeyEnter:
		// Toggle pick/drop.
		t.reorder.moving = !t.reorder.moving
		return false

	case tcell.KeyRune:
		switch ev.Rune() {
		case ' ':
			// Confirm: apply the new order.
			t.reorder.active = false
			t.layoutState.Order = make([]string, len(t.reorder.items))
			copy(t.layoutState.Order, t.reorder.items)
			reorderPanes(t.panes, t.layoutState.Order)
			t.applyLayout()
			t.saveLayoutIfConfigured()
			return false

		case 'j':
			if t.reorder.moving {
				// Move picked item down.
				if t.reorder.cursor < n-1 {
					i := t.reorder.cursor
					t.reorder.items[i], t.reorder.items[i+1] = t.reorder.items[i+1], t.reorder.items[i]
					t.reorder.cursor++
				}
			} else {
				// Move cursor down.
				if t.reorder.cursor < n-1 {
					t.reorder.cursor++
				}
			}
			return false

		case 'k':
			if t.reorder.moving {
				// Move picked item up.
				if t.reorder.cursor > 0 {
					i := t.reorder.cursor
					t.reorder.items[i], t.reorder.items[i-1] = t.reorder.items[i-1], t.reorder.items[i]
					t.reorder.cursor--
				}
			} else {
				// Move cursor up.
				if t.reorder.cursor > 0 {
					t.reorder.cursor--
				}
			}
			return false
		}

	case tcell.KeyDown:
		if t.reorder.moving {
			if t.reorder.cursor < n-1 {
				i := t.reorder.cursor
				t.reorder.items[i], t.reorder.items[i+1] = t.reorder.items[i+1], t.reorder.items[i]
				t.reorder.cursor++
			}
		} else if t.reorder.cursor < n-1 {
			t.reorder.cursor++
		}
		return false

	case tcell.KeyUp:
		if t.reorder.moving {
			if t.reorder.cursor > 0 {
				i := t.reorder.cursor
				t.reorder.items[i], t.reorder.items[i-1] = t.reorder.items[i-1], t.reorder.items[i]
				t.reorder.cursor--
			}
		} else if t.reorder.cursor > 0 {
			t.reorder.cursor--
		}
		return false
	}
	return false
}
