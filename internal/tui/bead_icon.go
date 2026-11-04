package tui

// bead_icon.go is the bead state icon (ini-z9t): one character for a bead's
// bd status, drawn left of the bead id in the pane ribbon, where the bar was,
// and left of each effort row's id in the Agents overlay.

// beadStateIcons is the one table, as Nelson settled it. Each glyph is one
// cell by displayWidth (the ribbon's layout cannot move). Every glyph renders
// in Ghostty 1.4.0 with TX-02 (some through its fallback faces: ☾ and ❄
// from Menlo, ○ ◎ ◆ ✓ from JetBrains Mono). If one stops rendering, its
// fallback is the one-line change, on Nelson's word:
//   - ☾ falls back to 'z'
//   - ◎ falls back to '⊙'
//   - ◆ falls back to '+'
var beadStateIcons = map[string]rune{
	"open":              '○',
	"in_progress":       '▶',
	"ready_for_qa":      '◎', // fallback '⊙'
	"in_qa":             '◎', // fallback '⊙'
	"qa_passed":         '◆', // fallback '+'
	"ready_to_ship":     '◆', // fallback '+'
	"waiting":           '☾', // fallback 'z'
	"blocked":           '■',
	"waiting_on_nelson": '?',
	"deferred":          '❄',
	"closed":            '✓',
}

// beadStateFallbacks are the fallbacks above, for the width test.
var beadStateFallbacks = []rune{'z', '⊙', '+'}

// beadBar is what stands before the bead id when its status is not in the
// table or has not been read yet.
const beadBar = '|'

// beadStateIcon is status's icon; ok is false for a status not in the table
// and for "" (not read yet).
func beadStateIcon(status string) (icon rune, ok bool) {
	icon, ok = beadStateIcons[status]
	return icon, ok
}
