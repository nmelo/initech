package tui

// attention_render.go paints the needs-input list and gathers the rows it draws
// from (ini-2x8.1). All layout lives in attention.go; this file only positions
// finished lines and colours them.

import (
	"time"

	"github.com/gdamore/tcell/v2"
)

// waitingRows collects every pane currently blocked on the operator.
//
// Reads through the PaneView interface so REMOTE panes are included on the same
// footing as local ones -- the coordinator's dialogs are caught exactly like a
// worker's, which is the point the spec makes twice, and both motivating
// instances were super's.
func (t *TUI) waitingRows() []WaitingRow {
	var rows []WaitingRow
	for _, p := range t.panes {
		wp, ok := p.(waitingPane)
		if !ok {
			continue
		}
		waiting, since, preview := wp.WaitingInput()
		if !waiting {
			continue
		}
		// paneDisplayName drops the window-1 observer alias and keeps a real
		// cross-machine host (ini-9isx). The list itself stays FLEET-WIDE:
		// this loop walks t.panes, never the window-scoped set, because
		// attention is never scoped -- an agent waiting on the operator must
		// be discoverable from every window, whichever monitor it renders on.
		// That is a normative rule in docs/spec.md, not an implementation
		// convenience, and it is the one thing display scoping must never
		// reach into.
		rows = append(rows, WaitingRow{Name: paneDisplayName(p), Preview: preview, Since: since})
	}
	return rows
}

// waitingPane is the narrow slice of a pane the attention list needs. Kept as
// its own interface rather than widened into PaneView so pane kinds that cannot
// detect a dialog (and remote kinds until their tier is decided) simply do not
// implement it, instead of every kind having to return a stub.
type waitingPane interface {
	WaitingInput() (bool, time.Time, string)
}

// renderAttention draws the needs-input list at the top left, or draws NOTHING
// AT ALL when no agent is waiting.
//
// The empty case returns before touching the screen and before reserving any
// space: the hidden-when-empty rule is that a no-waiting session renders exactly
// as it did before this feature existed, and the census check against the
// shipped release is what holds that honest.
func (t *TUI) renderAttention() {
	if t.screen == nil {
		return
	}
	sw, sh := t.screen.Size()
	lines := attentionLines(t.waitingRows(), time.Now(), sw)
	if len(lines) == 0 {
		return
	}

	const px, py = 1, 1
	bg := tcell.NewRGBColor(40, 20, 20)
	border := tcell.StyleDefault.Foreground(tcell.ColorOrange).Background(bg)
	text := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(bg)

	for i, line := range lines {
		y := py + i
		if y >= sh {
			break
		}
		style := text
		if i == 0 || i == len(lines)-1 {
			style = border
		}
		x := px
		for _, ch := range line {
			if x >= sw {
				break
			}
			// Box-drawing runes take the border colour even on content rows, so
			// the frame reads as one shape rather than a stack of coloured bars.
			chStyle := style
			if isBoxRune(ch) {
				chStyle = border
			}
			t.screen.SetContent(x, y, ch, nil, chStyle)
			x += runeCells(ch)
		}
	}
}

// isBoxRune reports whether ch is one of the frame's box-drawing characters.
func isBoxRune(ch rune) bool {
	switch ch {
	case '┌', '┐', '└', '┘', '─', '│':
		return true
	}
	return false
}

// runeCells is how many terminal columns ch occupies. Advancing by 1 per rune
// would smear the box on any question containing a wide glyph.
func runeCells(ch rune) int {
	w := displayWidth(string(ch))
	if w < 1 {
		return 1
	}
	return w
}
