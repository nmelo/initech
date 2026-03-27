// pane_render.go contains the Render method and visual conversion helpers for
// drawing a pane's terminal content and ribbon onto the tcell screen.
package tui

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/gdamore/tcell/v2"
)

// Selection describes a text selection range in pane-local content coordinates.
type Selection struct {
	Active         bool
	StartX, StartY int
	EndX, EndY     int
}

// clampedScreen wraps tcell.Screen and clips all SetContent calls to a region.
// Prevents pane content from ever rendering outside its assigned area.
type clampedScreen struct {
	tcell.Screen
	r Region
}

func (c *clampedScreen) SetContent(x, y int, ch rune, comb []rune, style tcell.Style) {
	if x >= c.r.X && x < c.r.X+c.r.W && y >= c.r.Y && y < c.r.Y+c.r.H {
		c.Screen.SetContent(x, y, ch, comb, style)
	}
}

func (c *clampedScreen) GetContent(x, y int) (rune, []rune, tcell.Style, int) {
	if x >= c.r.X && x < c.r.X+c.r.W && y >= c.r.Y && y < c.r.Y+c.r.H {
		return c.Screen.GetContent(x, y)
	}
	return ' ', nil, tcell.StyleDefault, 1
}

// Render draws the pane's bottom ribbon and terminal content onto the tcell screen.
// When dimmed is true, foreground colors are reduced to ~70% brightness.
// The index parameter is the 1-based pane number shown in the ribbon badge.
// All writes are clamped to the pane's region to prevent bleed-through.
func (p *Pane) Render(screen tcell.Screen, focused bool, dimmed bool, index int, sel Selection) {
	r := p.region
	if r.W < 1 || r.H < 2 {
		return
	}

	// Clamp all writes to the pane's region.
	s := &clampedScreen{Screen: screen, r: r}

	// Bottom ribbon (1 row at the bottom of the region).
	// Use true black (#000000) not palette ColorBlack, which terminals often
	// render as the same dark gray as the default background.
	trueBlack := tcell.NewRGBColor(0, 0, 0)
	ribbonY := r.Y + r.H - 1

	// Fill ribbon row with solid black background.
	blackStyle := tcell.StyleDefault.Background(trueBlack)
	for x := r.X; x < r.X+r.W; x++ {
		s.SetContent(x, ribbonY, ' ', nil, blackStyle)
	}

	// Badge style: focused = white on DodgerBlue box, unfocused = gray on true black.
	var titleStyle tcell.Style
	if focused {
		titleStyle = tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorBlack).Bold(true)
	} else {
		titleStyle = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorGray).Bold(true)
	}

	// Pane badge: "N name" with optional bead ID and status indicators.
	title := fmt.Sprintf(" %d %s ", index, p.name)
	if p.IsSuspended() {
		title = fmt.Sprintf(" %d %s [susp] ", index, p.name)
		titleStyle = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorDodgerBlue).Bold(true)
	} else if !p.IsAlive() {
		title = fmt.Sprintf(" %d %s [dead] ", index, p.name)
		titleStyle = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorRed).Bold(true)
	} else if p.scrollOffset > 0 {
		title = fmt.Sprintf(" %d %s [+%d] ", index, p.name, p.scrollOffset)
		titleStyle = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorYellow).Bold(true)
	}
	col := r.X + 1
	for _, ch := range title {
		if col < r.X+r.W {
			s.SetContent(col, ribbonY, ch, nil, titleStyle)
			col++
		}
	}
	// Append bead ID in dark cyan after the name badge.
	bead := p.BeadID()
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

	// Terminal content (starts at Y+1, fills full width).
	innerCols, innerRows := r.InnerSize()
	emuRows := p.emu.Height()

	if p.scrollOffset > 0 {
		// Scrollback mode: render from the combined scrollback + screen buffer.
		scrollbackLen := p.emu.ScrollbackLen()
		totalVirtual := scrollbackLen + emuRows

		// The bottom of the view window (exclusive).
		viewBottom := totalVirtual - p.scrollOffset
		if viewBottom < 0 {
			viewBottom = 0
		}
		viewTop := viewBottom - innerRows
		if viewTop < 0 {
			viewTop = 0
		}

		for row := 0; row < innerRows; row++ {
			vRow := viewTop + row
			if vRow >= viewBottom {
				continue
			}
			for col := 0; col < innerCols; col++ {
				var cell *uv.Cell
				if vRow < scrollbackLen {
					cell = p.emu.ScrollbackCellAt(col, vRow)
				} else {
					cell = p.emu.CellAt(col, vRow-scrollbackLen)
				}
				ch, style := uvCellToTcell(cell)
				if dimmed {
					style = dimStyle(style)
				}
				s.SetContent(r.X+col, r.Y+row, ch, nil, style)
			}
		}
	}

	// These variables are used by both the live rendering and cursor logic below.
	startRow, renderOffset := p.contentOffset()

	if p.scrollOffset == 0 {
		// Live mode: anchor content to the bottom of the pane.
		if !p.emu.IsAltScreen() {
			// Extract the cursor row text as the session description.
			// Only update if non-empty (resizes temporarily clear the cursor row).
			pos := p.emu.CursorPosition()
			if pos.Y < emuRows {
				var desc strings.Builder
				for col := 0; col < innerCols; col++ {
					cell := p.emu.CellAt(col, pos.Y)
					if cell != nil && cell.Content != "" {
						desc.WriteString(cell.Content)
					} else {
						desc.WriteByte(' ')
					}
				}
				trimmed := strings.TrimSpace(desc.String())
				// Only use as description if it looks like real text, not
				// Claude's status bar (which contains │ separators).
				if trimmed != "" && !strings.Contains(trimmed, "\u2502") {
					p.mu.Lock()
					p.sessionDesc = trimmed
					p.mu.Unlock()
				}
			}
		}
		// Determine status bar zone for CUF bleed-through fix.
		// Only apply the fix near the cursor (last 4 rows of content).
		pos := p.emu.CursorPosition()
		statusZoneStart := pos.Y - 4
		if statusZoneStart < 0 {
			statusZoneStart = 0
		}

		for row := 0; row < innerRows; row++ {
			emuRow := startRow + (row - renderOffset)
			if emuRow < 0 || emuRow >= emuRows {
				continue
			}

			// In the status bar zone, fix CUF bleed-through: Claude Code
			// uses cursor-forward (ESC[1C) to skip cells when rewriting
			// its status bar, leaving stale content in gaps. Only apply
			// the fix to rows that actually contain the status bar separator
			// (│ U+2502). Input rows lack this character and must not be
			// filtered, or typed text adjacent to autocomplete ghost text
			// gets blanked (ini-cp3).
			if emuRow >= statusZoneStart && emuRow <= pos.Y && rowContainsStatusBar(p.emu, emuRow, innerCols) {
				type cellInfo struct {
					ch      rune
					style   tcell.Style
					colored bool
				}
				cells := make([]cellInfo, innerCols)
				for col := 0; col < innerCols; col++ {
					cell := p.emu.CellAt(col, emuRow)
					ch, style := uvCellToTcell(cell)
					colored := cell != nil && cell.Style.Fg != nil
					cells[col] = cellInfo{ch, style, colored}
				}
				for col := 0; col < innerCols; col++ {
					if !cells[col].colored && cells[col].ch != ' ' {
						nearColored := false
						for d := 1; d <= 2; d++ {
							if col-d >= 0 && cells[col-d].colored {
								nearColored = true
								break
							}
							if col+d < innerCols && cells[col+d].colored {
								nearColored = true
								break
							}
						}
						if nearColored {
							cells[col].ch = ' '
							cells[col].style = tcell.StyleDefault
						}
					}
				}
				for col := 0; col < innerCols; col++ {
					st := cells[col].style
					if dimmed {
						st = dimStyle(st)
					}
					s.SetContent(r.X+col, r.Y+row, cells[col].ch, nil, st)
				}
			} else if emuRow == pos.Y {
				// Cursor (prompt) row: blank uncolored non-space cells to the
				// right of the cursor position. These are stale cells left by
				// CUF (cursor forward) that moved past them without erasing —
				// e.g. "Claude Code" or the session name appearing as ghost
				// text on the prompt line (ini-7md). Colored cells (autocomplete
				// suggestions rendered with an explicit Fg color) are preserved.
				for col := 0; col < innerCols; col++ {
					cell := p.emu.CellAt(col, emuRow)
					ch, style := uvCellToTcell(cell)
					if col > pos.X && ch != ' ' && (cell == nil || cell.Style.Fg == nil) {
						ch = ' '
						style = tcell.StyleDefault
					}
					if dimmed {
						style = dimStyle(style)
					}
					s.SetContent(r.X+col, r.Y+row, ch, nil, style)
				}
			} else {
				// Normal row: render directly.
				for col := 0; col < innerCols; col++ {
					cell := p.emu.CellAt(col, emuRow)
					ch, style := uvCellToTcell(cell)
					if dimmed {
						style = dimStyle(style)
					}
					s.SetContent(r.X+col, r.Y+row, ch, nil, style)
				}
			}
		}
	}

	// Selection and cursor are only drawn in live mode (not scrollback).
	if p.scrollOffset == 0 {
		// Selection highlight (yellow background, black text).
		if sel.Active {
			r0, c0, r1, c1 := sel.StartY, sel.StartX, sel.EndY, sel.EndX
			if r0 > r1 || (r0 == r1 && c0 > c1) {
				r0, c0, r1, c1 = r1, c1, r0, c0
			}
			selBg := tcell.ColorYellow
			if dimmed {
				selBg = tcell.ColorOlive // Muted highlight for dimmed panes.
			}
			selStyle := tcell.StyleDefault.Background(selBg).Foreground(tcell.ColorBlack)
			for row := r0; row <= r1 && row < innerRows; row++ {
				emuRow := startRow + (row - renderOffset)
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
					cell := p.emu.CellAt(col, emuRow)
					ch := ' '
					if cell != nil && cell.Content != "" {
						ch = []rune(cell.Content)[0]
					}
					s.SetContent(r.X+col, r.Y+row, ch, nil, selStyle)
				}
			}
		}

		// Cursor (skip if selection is active to avoid visual conflict).
		if focused && !sel.Active {
			pos := p.emu.CursorPosition()
			visRow := pos.Y - startRow + renderOffset
			if pos.X >= 0 && pos.X < innerCols && visRow >= 0 && visRow < innerRows {
				cx := r.X + pos.X
				cy := r.Y + visRow
				cell := p.emu.CellAt(pos.X, pos.Y)
				ch, _ := uvCellToTcell(cell)
				cursorStyle := tcell.StyleDefault.Background(tcell.ColorWhite).Foreground(tcell.ColorBlack)
				s.SetContent(cx, cy, ch, nil, cursorStyle)
			}
		}
	}

	// Activity bar on the top edge of the pane (ini-lw0). Overlays row 0
	// of the content area. Running panes get a KITT scanner sweep; all
	// other states get a static dim line.
	p.renderActivityBar(s, r)
}

// renderActivityBar draws a 1-row activity indicator on the top edge of the
// pane. Running panes show a KITT scanner effect (gaussian brightness peak
// sweeping left-right). All other states show a static dim horizontal line.
func (p *Pane) renderActivityBar(s *clampedScreen, r Region) {
	if r.W < 1 {
		return
	}
	bg := tcell.NewRGBColor(24, 24, 24)
	baseBrightness := int32(35)
	baseColor := tcell.NewRGBColor(baseBrightness, baseBrightness, baseBrightness)
	baseStyle := tcell.StyleDefault.Background(bg).Foreground(baseColor)
	y := r.Y // top row of the pane

	if p.Activity() != StateRunning {
		// Static dim bar for idle/dead/suspended panes.
		for x := r.X; x < r.X+r.W; x++ {
			s.SetContent(x, y, '\u2500', nil, baseStyle)
		}
		return
	}

	// KITT scanner: gaussian brightness peak sweeping left-right.
	// Cycle period: ~2.5 seconds. Position computed from wall clock.
	w := r.W
	cycle := (w - 1) * 2
	if cycle <= 0 {
		return
	}
	// 2.5s per full cycle at 33ms per tick = ~76 ticks.
	// Use fractional position from time for smooth sub-cell movement.
	elapsed := time.Since(p.kittEpoch).Seconds()
	ticksPerCycle := 2.5
	frac := math.Mod(elapsed, ticksPerCycle) / ticksPerCycle
	rawPos := frac * float64(cycle)
	pos := rawPos
	if pos >= float64(w) {
		pos = float64(cycle) - pos // bounce back
	}

	for dx := 0; dx < w; dx++ {
		dist := float64(dx) - pos
		// Gaussian falloff: ~3-5 cells wide bright segment.
		brightness := 85.0 * math.Exp(-dist*dist*0.15)
		if brightness < float64(baseBrightness) {
			brightness = float64(baseBrightness)
		}
		b := int32(brightness)
		c := tcell.NewRGBColor(b, b, b)
		s.SetContent(r.X+dx, y, '\u2500', nil, tcell.StyleDefault.Background(bg).Foreground(c))
	}
}

// dimStyle returns a dimmed version of a tcell.Style for unfocused panes.
// Colors are scaled to ~70% brightness. Default fg becomes dark gray.
func dimStyle(style tcell.Style) tcell.Style {
	fg, bg, attrs := style.Decompose()
	return tcell.StyleDefault.
		Foreground(dimColor(fg)).
		Background(bg).
		Attributes(attrs)
}

// dimColor reduces a tcell.Color to ~70% brightness.
func dimColor(c tcell.Color) tcell.Color {
	if c == tcell.ColorDefault {
		return tcell.ColorDimGray
	}
	// For any color, extract RGB and scale down.
	r, g, b := c.RGB()
	return tcell.NewRGBColor(int32(r)*7/10, int32(g)*7/10, int32(b)*7/10)
}

// rowContainsStatusBar checks if an emulator row contains the vertical box
// drawing character │ (U+2502), which is the definitive marker for Claude Code's
// status bar. Used to restrict the CUF bleed-through heuristic to status bar
// rows only, preventing it from blanking typed text on input rows.
func rowContainsStatusBar(emu *vt.SafeEmulator, row, cols int) bool {
	for col := 0; col < cols; col++ {
		cell := emu.CellAt(col, row)
		if cell != nil {
			for _, r := range cell.Content {
				if r == '\u2502' {
					return true
				}
			}
		}
	}
	return false
}

// uvCellToTcell converts a charmbracelet ultraviolet Cell to a rune + tcell.Style.
func uvCellToTcell(cell *uv.Cell) (rune, tcell.Style) {
	if cell == nil || cell.Content == "" {
		return ' ', tcell.StyleDefault
	}

	ch := []rune(cell.Content)[0]
	style := tcell.StyleDefault

	// Foreground color.
	if cell.Style.Fg != nil {
		style = style.Foreground(uvColorToTcell(cell.Style.Fg))
	}
	// Background color.
	if cell.Style.Bg != nil {
		style = style.Background(uvColorToTcell(cell.Style.Bg))
	}

	// Attributes.
	attrs := cell.Style.Attrs
	if attrs&uv.AttrBold != 0 {
		style = style.Bold(true)
	}
	if attrs&uv.AttrFaint != 0 {
		style = style.Dim(true)
	}
	if attrs&uv.AttrItalic != 0 {
		style = style.Italic(true)
	}
	if attrs&uv.AttrReverse != 0 {
		style = style.Reverse(true)
	}
	if attrs&uv.AttrStrikethrough != 0 {
		style = style.StrikeThrough(true)
	}
	if cell.Style.Underline != 0 {
		style = style.Underline(true)
	}

	return ch, style
}

// uvColorToTcell converts a Go color.Color (from ultraviolet) to a tcell.Color.
func uvColorToTcell(c color.Color) tcell.Color {
	if c == nil {
		return tcell.ColorDefault
	}

	switch v := c.(type) {
	case ansi.BasicColor:
		return tcell.PaletteColor(int(v))
	case ansi.IndexedColor:
		return tcell.PaletteColor(int(v))
	case ansi.RGBColor:
		return tcell.NewRGBColor(int32(v.R), int32(v.G), int32(v.B))
	default:
		// Generic color.Color: extract RGBA and convert.
		r, g, b, _ := c.RGBA()
		return tcell.NewRGBColor(int32(r>>8), int32(g>>8), int32(b>>8))
	}
}
