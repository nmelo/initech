// pane_render.go contains the Render method and visual conversion helpers for
// drawing a pane's terminal content and ribbon onto the tcell screen.
package tui

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"
	"unicode/utf8"

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

	// Badge style: focused = white on DodgerBlue box, unfocused = gray on true black.
	var titleStyle tcell.Style
	if focused {
		titleStyle = tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorBlack).Bold(true)
	} else {
		titleStyle = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.ColorGray).Bold(true)
	}

	// Pane badge: "N name" with optional status indicators.
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

	renderRibbon(s, r, title, titleStyle, p.BeadID())

	// Terminal content (starts at Y+1, fills full width).
	innerCols, innerRows := r.InnerSize()

	// Hold renderMu for the entire cell-reading phase to prevent tearing
	// from concurrent readLoop writes (ini-45m) and resize buffer
	// reorganization (ini-ipr). Read emuRows inside the lock so it matches
	// the buffer state we'll be reading from.
	p.renderMu.Lock()
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
		pos := p.emu.CursorPosition()

		if !p.emu.IsAltScreen() {
			// Extract the cursor row text as the session description.
			// Only update if non-empty (resizes temporarily clear the cursor row).
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
		statusZoneStart := pos.Y - 4
		if statusZoneStart < 0 {
			statusZoneStart = 0
		}

		for row := 0; row < innerRows; row++ {
			emuRow := startRow + (row - renderOffset)
			if emuRow < 0 || emuRow >= emuRows {
				continue
			}

			// In the status bar zone, blank stale CUF bleed-through on
			// rows that contain the status bar separator (ini-cp3).
			if emuRow >= statusZoneStart && emuRow <= pos.Y && rowContainsStatusBar(p.emu, emuRow, innerCols) {
				renderStatusBarRow(s, p.emu, r.X, r.Y+row, emuRow, innerCols, dimmed)
			} else {
				renderCellRow(s, p.emu, r.X, r.Y+row, emuRow, innerCols, dimmed)
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

		renderCursor(s, r, p.emu, focused, sel, startRow-renderOffset)
	}

	p.renderMu.Unlock()

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
	const baseBrightness = 35.0
	baseStyle := tcell.StyleDefault.Foreground(tcell.NewRGBColor(35, 35, 35))
	y := r.Y

	if p.Activity() != StateRunning {
		for x := r.X; x < r.X+r.W; x++ {
			s.SetContent(x, y, '\u2500', nil, baseStyle)
		}
		return
	}

	// KITT scanner: gaussian brightness peak sweeping left-right (~3s cycle).
	w := r.W
	cycle := (w - 1) * 2
	if cycle <= 0 {
		return
	}
	const kittCycleSec = 4.0
	elapsed := time.Since(p.kittEpoch).Seconds()
	frac := math.Mod(elapsed, kittCycleSec) / kittCycleSec
	pos := frac * float64(cycle)
	if pos >= float64(w) {
		pos = float64(cycle) - pos
	}

	for dx := 0; dx < w; dx++ {
		dist := float64(dx) - pos
		brightness := 85.0 * math.Exp(-dist*dist*0.15)
		if brightness < baseBrightness {
			s.SetContent(r.X+dx, y, '\u2500', nil, baseStyle)
		} else {
			b := int32(brightness)
			s.SetContent(r.X+dx, y, '\u2500', nil, tcell.StyleDefault.Foreground(tcell.NewRGBColor(b, b, b)))
		}
	}
}

// renderStatusBarRow renders a single emulator row with CUF bleed-through
// suppression. Claude Code uses cursor-forward (ESC[1C) to skip cells when
// rewriting its status bar, leaving stale uncolored text in the gaps. This
// function blanks uncolored non-space characters that sit within 2 columns of
// a colored character (the real status bar content has explicit Fg colors).
// cufCellInfo holds per-cell data for the CUF bleed-through heuristic.
type cufCellInfo struct {
	ch      rune
	style   tcell.Style
	colored bool
}

// cufCells is a reusable buffer for renderStatusBarRow, avoiding per-call
// allocation. Grows to the widest pane and stays there.
var cufCells []cufCellInfo

func renderStatusBarRow(s *clampedScreen, emu *vt.SafeEmulator, screenX, screenY, emuRow, cols int, dimmed bool) {
	if cap(cufCells) < cols {
		cufCells = make([]cufCellInfo, cols*2)
	}
	cells := cufCells[:cols]
	for col := 0; col < cols; col++ {
		cell := emu.CellAt(col, emuRow)
		ch, style := uvCellToTcell(cell)
		cells[col] = cufCellInfo{ch, style, cell != nil && cell.Style.Fg != nil}
	}
	// Blank uncolored non-space cells near colored cells (stale CUF artifacts).
	for col := 0; col < cols; col++ {
		if !cells[col].colored && cells[col].ch != ' ' {
			nearColored := false
			for d := 1; d <= 2; d++ {
				if col-d >= 0 && cells[col-d].colored {
					nearColored = true
					break
				}
				if col+d < cols && cells[col+d].colored {
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
	for col := 0; col < cols; col++ {
		st := cells[col].style
		if dimmed {
			st = dimStyle(st)
		}
		s.SetContent(screenX+col, screenY, cells[col].ch, nil, st)
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

	ch, _ := utf8.DecodeRuneInString(cell.Content)
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
