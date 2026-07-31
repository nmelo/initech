// render_common.go contains rendering helpers shared between Pane and RemotePane.
// These eliminate duplicate ribbon, cell, and cursor rendering logic.
package tui

import (
	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// trueBlack is #000000, distinct from palette ColorBlack which terminals often
// render as a dark gray matching the default background.
var trueBlack = tcell.NewRGBColor(0, 0, 0)

// runningSquareStyle is the green running indicator drawn at the ribbon's
// reserved leading cell (ini-z9a3, recolored ini-8a7). Always green
// regardless of focus. Deliberately NOT red: red also means [dead] on this
// same badge, and ColorRed here would make the two indistinguishable except
// by position — a real collision, not a style preference (ini-8a7). Green
// instead matches the hue family of the running-pane background tint, which
// is driven by the same tintUntil field the square reads — but NOT the
// tint's literal RGB (#0c120e, pane_render.go's defaultRunningTintColor):
// that value is a deliberately near-black background wash, not a legible
// foreground color, so reusing it here would reproduce the exact
// invisible-glyph failure ini-6o3 exists to prevent, just in a different
// color. Do not "fix" this back to red or to the literal tint RGB.
var runningSquareStyle = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorGreen).Bold(true)

// renderRibbon draws the bottom ribbon: solid black background, title badge,
// and optional bead ID with title. running draws a full-height green block at
// the ribbon's leading cell (reserved unconditionally by the col := r.X+1
// title start below, so the badge never shifts whether or not the square is
// shown). Returns the column position after the last element.
func renderRibbon(s *clampedScreen, r Region, title string, titleStyle tcell.Style, beadID, beadTitle string, running bool) int {
	ribbonY := r.Y + r.H - 1

	blackStyle := tcell.StyleDefault.Background(trueBlack)
	for x := r.X; x < r.X+r.W; x++ {
		s.SetContent(x, ribbonY, ' ', nil, blackStyle)
	}

	if running {
		s.SetContent(r.X, ribbonY, '█', nil, runningSquareStyle)
	}

	col := r.X + 1
	for _, ch := range title {
		if col < r.X+r.W {
			s.SetContent(col, ribbonY, ch, nil, titleStyle)
			col++
		}
	}

	if beadID != "" {
		beadStyle := tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorDarkCyan)
		maxCol := r.X + r.W

		// Build bead display: "| id: title " or "| id " if no title.
		beadStr := "| " + beadID
		if beadTitle != "" {
			beadStr += ": " + beadTitle
		}
		beadStr += " "

		// Truncate with ellipsis if it exceeds available width.
		avail := maxCol - col
		beadRunes := []rune(beadStr)
		if len(beadRunes) > avail && avail > 6 {
			beadRunes = append(beadRunes[:avail-1], '\u2026')
		}

		for _, ch := range beadRunes {
			if col < maxCol {
				s.SetContent(col, ribbonY, ch, nil, beadStyle)
				col++
			}
		}
	}

	return col
}

// renderCellRow draws a single emulator row to the screen at position (x, y).
// tint is the running-pane background wash applied to default-bg cells only
// (tcell.ColorDefault = no tint). Tint is applied before dimming so it survives
// the unfocused-pane dim (dimStyle preserves bg).
func renderCellRow(s *clampedScreen, emu *vt.SafeEmulator, x, y, emuRow, cols int, dimmed bool, tint tcell.Color) {
	for c := 0; c < cols; c++ {
		cell := emu.CellAt(c, emuRow)
		ch, style := uvCellToTcell(cell)
		style = tintStyle(style, tint)
		if dimmed {
			style = dimStyle(style)
		}
		s.SetContent(x+c, y, ch, nil, style)
	}
}

// renderCells draws terminal content from the emulator, starting at emuStartRow.
// tint applies the running-pane background wash to default-bg cells.
func renderCells(s *clampedScreen, r Region, emu *vt.SafeEmulator, dimmed bool, emuStartRow int, tint tcell.Color) {
	innerCols, innerRows := r.InnerSize()
	emuRows := emu.Height()
	for row := 0; row < innerRows; row++ {
		emuRow := emuStartRow + row
		if emuRow < 0 || emuRow >= emuRows {
			continue
		}
		renderCellRow(s, emu, r.X, r.Y+row, emuRow, innerCols, dimmed, tint)
	}
}

// renderSelection draws the yellow selection highlight over cells in the
// selected range. emuStartRow is the emulator row that maps to visual row 0.
func renderSelection(s *clampedScreen, r Region, emu *vt.SafeEmulator, sel Selection, dimmed bool, emuStartRow int) {
	if !sel.Active {
		return
	}
	innerCols, innerRows := r.InnerSize()
	emuRows := emu.Height()

	r0, c0, r1, c1 := sel.StartY, sel.StartX, sel.EndY, sel.EndX
	if r0 > r1 || (r0 == r1 && c0 > c1) {
		r0, c0, r1, c1 = r1, c1, r0, c0
	}
	selBg := tcell.ColorYellow
	if dimmed {
		selBg = tcell.ColorOlive
	}
	selStyle := tcell.StyleDefault.Background(selBg).Foreground(tcell.ColorBlack)
	for row := r0; row <= r1 && row < innerRows; row++ {
		emuRow := emuStartRow + row
		if emuRow < 0 || emuRow >= emuRows {
			continue
		}
		sc := 0
		ec := innerCols
		if row == r0 {
			sc = c0
		}
		if row == r1 {
			ec = c1 + 1
		}
		if ec > innerCols {
			ec = innerCols
		}
		for col := sc; col < ec; col++ {
			cell := emu.CellAt(col, emuRow)
			ch := ' '
			if cell != nil && cell.Content != "" {
				ch = []rune(cell.Content)[0]
			}
			s.SetContent(r.X+col, r.Y+row, ch, nil, selStyle)
		}
	}
}

// renderSelectionVirtual renders the selection highlight in scrollback mode,
// using the pane's virtualCellAt to read from the combined scrollback + screen
// buffer. viewTop is the virtual row at the top of the visible window.
func renderSelectionVirtual(s *clampedScreen, r Region, p *Pane, sel Selection, dimmed bool, viewTop int) {
	if !sel.Active {
		return
	}
	innerCols, innerRows := r.InnerSize()
	scrollbackLen := p.emu.ScrollbackLen()
	totalVirtual := scrollbackLen + p.emu.Height()

	r0, c0, r1, c1 := sel.StartY, sel.StartX, sel.EndY, sel.EndX
	if r0 > r1 || (r0 == r1 && c0 > c1) {
		r0, c0, r1, c1 = r1, c1, r0, c0
	}
	selBg := tcell.ColorYellow
	if dimmed {
		selBg = tcell.ColorOlive
	}
	selStyle := tcell.StyleDefault.Background(selBg).Foreground(tcell.ColorBlack)
	for row := r0; row <= r1 && row < innerRows; row++ {
		vRow := viewTop + row
		if vRow < 0 || vRow >= totalVirtual {
			continue
		}
		sc := 0
		ec := innerCols
		if row == r0 {
			sc = c0
		}
		if row == r1 {
			ec = c1 + 1
		}
		if ec > innerCols {
			ec = innerCols
		}
		for col := sc; col < ec; col++ {
			cell := p.virtualCellAt(col, vRow)
			ch := ' '
			if cell != nil && cell.Content != "" {
				ch = []rune(cell.Content)[0]
			}
			s.SetContent(r.X+col, r.Y+row, ch, nil, selStyle)
		}
	}
}

// renderCursor draws the cursor block if focused and no selection is active.
// emuStartRow is the emulator row that maps to visual row 0.
func renderCursor(s *clampedScreen, r Region, emu *vt.SafeEmulator, focused bool, sel Selection, emuStartRow int) {
	if !focused || sel.Active {
		return
	}
	innerCols, innerRows := r.InnerSize()
	pos := emu.CursorPosition()
	visRow := pos.Y - emuStartRow
	if pos.X >= 0 && pos.X < innerCols && visRow >= 0 && visRow < innerRows {
		cx := r.X + pos.X
		cy := r.Y + visRow
		cell := emu.CellAt(pos.X, pos.Y)
		ch, _ := uvCellToTcell(cell)
		cursorStyle := tcell.StyleDefault.Background(tcell.ColorWhite).Foreground(tcell.ColorBlack)
		s.SetContent(cx, cy, ch, nil, cursorStyle)
	}
}
