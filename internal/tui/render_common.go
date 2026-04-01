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

// renderRibbon draws the bottom ribbon: solid black background, title badge,
// and optional bead ID. Returns the column position after the last element.
func renderRibbon(s *clampedScreen, r Region, title string, titleStyle tcell.Style, bead string) int {
	ribbonY := r.Y + r.H - 1

	blackStyle := tcell.StyleDefault.Background(trueBlack)
	for x := r.X; x < r.X+r.W; x++ {
		s.SetContent(x, ribbonY, ' ', nil, blackStyle)
	}

	col := r.X + 1
	for _, ch := range title {
		if col < r.X+r.W {
			s.SetContent(col, ribbonY, ch, nil, titleStyle)
			col++
		}
	}

	if bead != "" {
		beadStr := "| " + bead + " "
		beadStyle := tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorDarkCyan)
		for _, ch := range beadStr {
			if col < r.X+r.W {
				s.SetContent(col, ribbonY, ch, nil, beadStyle)
				col++
			}
		}
	}

	return col
}

// renderCellRow draws a single emulator row to the screen at position (x, y).
func renderCellRow(s *clampedScreen, emu *vt.SafeEmulator, x, y, emuRow, cols int, dimmed bool) {
	if !dimmed {
		for c := 0; c < cols; c++ {
			cell := emu.CellAt(c, emuRow)
			ch, style := uvCellToTcell(cell)
			s.SetContent(x+c, y, ch, nil, style)
		}
	} else {
		for c := 0; c < cols; c++ {
			cell := emu.CellAt(c, emuRow)
			ch, style := uvCellToTcell(cell)
			style = dimStyle(style)
			s.SetContent(x+c, y, ch, nil, style)
		}
	}
}

// renderCells draws terminal content from the emulator, starting at emuStartRow.
func renderCells(s *clampedScreen, r Region, emu *vt.SafeEmulator, dimmed bool, emuStartRow int) {
	innerCols, innerRows := r.InnerSize()
	emuRows := emu.Height()
	for row := 0; row < innerRows; row++ {
		emuRow := emuStartRow + row
		if emuRow < 0 || emuRow >= emuRows {
			continue
		}
		renderCellRow(s, emu, r.X, r.Y+row, emuRow, innerCols, dimmed)
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
