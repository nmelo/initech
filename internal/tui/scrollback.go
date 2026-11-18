package tui

// scrollback.go -- the wheel-scroll rules, shared by every pane kind (ini-di8h).
//
// Window 1 has two rules and picks between them by the child: a fullscreen
// (alt-screen) child is sent the wheel and scrolls itself (ini-i3v); any other
// child ignores the wheel, so initech scrolls the pane's own history. A viewer
// window has everything both rules need -- its own emulator on the same byte
// stream, and a raw input path to the child -- and wired neither until this
// bead.
//
// The rules live here, as functions over an emulator and a row count, rather
// than as methods on one pane type, so "same step, same live-return" is one
// implementation rather than a promise two implementations keep separately.

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// wheelScrollRows is how far one wheel notch moves the view, in rows.
const wheelScrollRows = 3

// drawnRows includes the last nonblank row, even when the program parks
// the cursor above its status line. Call with the emulator read lock held.
func drawnRows(e *vt.Emulator) int {
	for y := e.Height() - 1; y >= 0; y-- {
		for x := 0; x < e.Width(); x++ {
			cell := e.CellAt(x, y)
			if cell != nil && cell.Content != "" && cell.Content != " " {
				return y + 1
			}
		}
	}
	return 0
}

// liveViewEnd is the bottom of a short tile's viewport. Inline programs
// can leave unused terminal rows below their prompt. Fullscreen programs
// address the entire terminal.
func liveViewEnd(e *vt.Emulator, visibleRows int) int {
	if e.Height() == shortTileTerminalRows && visibleRows <= shortPaneRows && !e.IsAltScreen() {
		return drawnRows(e)
	}
	return e.Height()
}

// maxScrollOffsetFor includes both retained history and clipped live rows.
func maxScrollOffsetFor(e *vt.Emulator, termRows int) int {
	return max(0, e.ScrollbackLen()+liveViewEnd(e, termRows)-termRows)
}

// scrollbackViewTop maps the scroll position to a scrollback + screen row.
func scrollbackViewTop(e *vt.Emulator, termRows, scrollOffset int) int {
	return max(0, maxScrollOffsetFor(e, termRows)-scrollOffset)
}

// scrollAnchor holds a pane's position in its own history. Zero offset is the
// live view. anchorLen is the scrollback length when the offset was last set,
// so output arriving while the operator reads does not yank the view.
type scrollAnchor struct {
	offset    int
	anchorLen int
}

// up moves the view into history and clamps it to the buffer.
func (a *scrollAnchor) up(n int, e *vt.Emulator, termRows int) {
	a.offset += n
	if max := maxScrollOffsetFor(e, termRows); a.offset > max {
		a.offset = max
	}
	a.anchorLen = e.ScrollbackLen()
}

// down moves the view toward live. At zero the anchor is dropped so new output
// scrolls the pane again.
func (a *scrollAnchor) down(n int) {
	a.offset -= n
	if a.offset <= 0 {
		a.offset = 0
		a.anchorLen = 0
	}
}

// compensate shifts the offset by the scrollback grown since the anchor was
// taken, so a scrolled view keeps showing the same lines while the child keeps
// writing. Call it before drawing.
func (a *scrollAnchor) compensate(e *vt.Emulator, termRows int) {
	if a.offset > 0 && a.anchorLen > 0 {
		if delta := e.ScrollbackLen() - a.anchorLen; delta > 0 {
			a.offset += delta
			a.anchorLen = e.ScrollbackLen()
		}
	}
	if max := maxScrollOffsetFor(e, termRows); a.offset > max {
		a.offset = max
	}
}

// renderScrollbackRows draws the view window from the combined scrollback +
// screen buffer into region, whose top-left is visual row 0. viewTop is the
// virtual row shown at the top. The caller holds the emulator read region.
func renderScrollbackRows(s *clampedScreen, region Region, e *vt.Emulator, dimmed bool, tint tcell.Color, viewTop, termCols, termRows int) {
	scrollbackLen := e.ScrollbackLen()
	totalVirtual := scrollbackLen + e.Height()
	for row := 0; row < termRows; row++ {
		vRow := viewTop + row
		if vRow < 0 || vRow >= totalVirtual {
			continue
		}
		for col := 0; col < termCols; col++ {
			cell := virtualCellOn(e, col, vRow)
			ch, style := uvCellToTcell(cell)
			style = tintStyle(style, tint)
			if dimmed {
				style = dimStyle(style)
			}
			s.SetContent(region.X+col, region.Y+row, ch, nil, style)
		}
	}
}

// uvKeyMods converts tcell modifiers to the emulator's own modifier set, so a
// forwarded wheel carries the modifiers the child would have seen.
func uvKeyMods(mods tcell.ModMask) uv.KeyMod {
	var mod uv.KeyMod
	if mods&tcell.ModShift != 0 {
		mod |= uv.ModShift
	}
	if mods&tcell.ModAlt != 0 {
		mod |= uv.ModAlt
	}
	if mods&tcell.ModCtrl != 0 {
		mod |= uv.ModCtrl
	}
	return mod
}
