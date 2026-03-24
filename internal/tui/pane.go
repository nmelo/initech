// Package tui implements a terminal multiplexer with PTY management,
// VT emulation via charmbracelet/x/vt, and a tcell-based rendering engine.
package tui

import (
	"image/color"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/creack/pty"
	"github.com/gdamore/tcell/v2"
)

// Pane represents a terminal pane backed by a PTY process.
// It uses a SafeEmulator from charmbracelet/x/vt for terminal emulation.
type Pane struct {
	name string
	ptmx *os.File
	cmd  *exec.Cmd
	emu  *vt.SafeEmulator
	mu   sync.Mutex
	alive bool
	region Region
}

// Region defines a rectangular area on screen (outer bounds including border).
type Region struct {
	X, Y, W, H int
}

// InnerSize returns the usable terminal area (full width, minus 1 row for title bar).
func (r Region) InnerSize() (cols, rows int) {
	cols = r.W
	rows = r.H - 1
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return
}

// PaneConfig describes how to launch a pane's process.
type PaneConfig struct {
	Name    string   // Display name (role name).
	Command []string // Command + args. Empty means use $SHELL.
	Dir     string   // Working directory. Empty means inherit.
	Env     []string // Extra env vars (KEY=VALUE). TERM is always set.
}

// NewPane creates a terminal pane running the configured command (or $SHELL).
func NewPane(cfg PaneConfig, rows, cols int) (*Pane, error) {
	argv := cfg.Command
	if len(argv) == 0 {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		argv = []string{shell, "-l"}
	}

	emu := vt.NewSafeEmulator(cols, rows)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	cmd.Env = append(cmd.Env, cfg.Env...)
	if cfg.Dir != "" {
		cmd.Dir = cfg.Dir
	}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
	if err != nil {
		return nil, err
	}

	p := &Pane{
		name:  cfg.Name,
		ptmx:  ptmx,
		cmd:   cmd,
		emu:   emu,
		alive: true,
	}

	// Read PTY output and feed the emulator.
	go p.readLoop()

	// Read emulator responses (DSR, DA) and write them back to the PTY.
	go p.responseLoop()

	return p, nil
}

func (p *Pane) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			p.emu.Write(buf[:n])
		}
		if err != nil {
			p.mu.Lock()
			p.alive = false
			p.mu.Unlock()
			return
		}
	}
}

// responseLoop reads encoded sequences from the emulator (responses to
// DSR, DA, SendKey, etc.) and writes them to the PTY.
func (p *Pane) responseLoop() {
	buf := make([]byte, 256)
	for {
		n, err := p.emu.Read(buf)
		if n > 0 {
			p.ptmx.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// SendKey translates a tcell key event into a charmbracelet KeyPressEvent
// and sends it through the emulator, which encodes it for the PTY.
func (p *Pane) SendKey(ev *tcell.EventKey) {
	kpe := tcellKeyToUV(ev)
	p.emu.SendKey(kpe)
}

// Resize updates the emulator and PTY dimensions.
func (p *Pane) Resize(rows, cols int) {
	p.emu.Resize(cols, rows)
	pty.Setsize(p.ptmx, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
}

// Render draws the pane's title bar and terminal content onto the tcell screen.
func (p *Pane) Render(s tcell.Screen, focused bool) {
	r := p.region
	if r.W < 1 || r.H < 2 {
		return
	}

	// Title bar (1 row at the top of the region).
	titleBg := tcell.ColorBlack
	nameBg := tcell.ColorGray
	nameFg := tcell.ColorBlack
	if focused {
		nameBg = tcell.ColorDodgerBlue
		nameFg = tcell.ColorBlack
	}
	titleStyle := tcell.StyleDefault.Background(nameBg).Foreground(nameFg).Bold(true)
	fillStyle := tcell.StyleDefault.Background(titleBg).Foreground(tcell.ColorDarkCyan)

	// Fill title bar background.
	for x := r.X; x < r.X+r.W; x++ {
		s.SetContent(x, r.Y, '\u2500', nil, fillStyle)
	}

	// Pane name.
	title := " " + p.name + " "
	if !p.IsAlive() {
		title = " " + p.name + " [dead] "
		titleStyle = tcell.StyleDefault.Background(tcell.ColorDarkRed).Foreground(tcell.ColorBlack).Bold(true)
	}
	for i, ch := range title {
		if r.X+1+i < r.X+r.W {
			s.SetContent(r.X+1+i, r.Y, ch, nil, titleStyle)
		}
	}

	// Terminal content (starts at Y+1, fills full width).
	innerCols, innerRows := r.InnerSize()
	for row := 0; row < innerRows; row++ {
		for col := 0; col < innerCols; col++ {
			cell := p.emu.CellAt(col, row)
			ch, style := uvCellToTcell(cell)
			s.SetContent(r.X+col, r.Y+1+row, ch, nil, style)
		}
	}

	// Cursor.
	if focused {
		pos := p.emu.CursorPosition()
		if pos.X < innerCols && pos.Y < innerRows {
			cx := r.X + pos.X
			cy := r.Y + 1 + pos.Y
			cell := p.emu.CellAt(pos.X, pos.Y)
			ch, _ := uvCellToTcell(cell)
			cursorStyle := tcell.StyleDefault.Background(tcell.ColorWhite).Foreground(tcell.ColorBlack)
			s.SetContent(cx, cy, ch, nil, cursorStyle)
		}
	}
}

// IsAlive returns whether the pane's shell process is still running.
func (p *Pane) IsAlive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.alive
}

// Close terminates the PTY and kills the process.
func (p *Pane) Close() {
	p.emu.Close()
	p.ptmx.Close()
	if p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	p.cmd.Wait()
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

// tcellKeyToUV translates a tcell key event to a charmbracelet KeyPressEvent.
func tcellKeyToUV(ev *tcell.EventKey) uv.KeyPressEvent {
	var mod uv.KeyMod
	if ev.Modifiers()&tcell.ModCtrl != 0 {
		mod |= uv.ModCtrl
	}
	if ev.Modifiers()&tcell.ModAlt != 0 {
		mod |= uv.ModAlt
	}
	if ev.Modifiers()&tcell.ModShift != 0 {
		mod |= uv.ModShift
	}

	switch ev.Key() {
	case tcell.KeyRune:
		r := ev.Rune()
		return uv.KeyPressEvent(uv.Key{Code: r, Text: string(r), Mod: mod})
	case tcell.KeyEnter:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter, Mod: mod})
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyBackspace, Mod: mod})
	case tcell.KeyTab:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyTab, Mod: mod})
	case tcell.KeyEscape:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyEscape, Mod: mod})
	case tcell.KeyUp:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyUp, Mod: mod})
	case tcell.KeyDown:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyDown, Mod: mod})
	case tcell.KeyRight:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyRight, Mod: mod})
	case tcell.KeyLeft:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyLeft, Mod: mod})
	case tcell.KeyHome:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyHome, Mod: mod})
	case tcell.KeyEnd:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyEnd, Mod: mod})
	case tcell.KeyDelete:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyDelete, Mod: mod})
	case tcell.KeyPgUp:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyPgUp, Mod: mod})
	case tcell.KeyPgDn:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyPgDown, Mod: mod})
	case tcell.KeyInsert:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyInsert, Mod: mod})
	case tcell.KeyF1:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyF1, Mod: mod})
	case tcell.KeyF2:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyF2, Mod: mod})
	case tcell.KeyF3:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyF3, Mod: mod})
	case tcell.KeyF4:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyF4, Mod: mod})
	case tcell.KeyF5:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyF5, Mod: mod})
	case tcell.KeyF6:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyF6, Mod: mod})
	case tcell.KeyF7:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyF7, Mod: mod})
	case tcell.KeyF8:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyF8, Mod: mod})
	case tcell.KeyF9:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyF9, Mod: mod})
	case tcell.KeyF10:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyF10, Mod: mod})
	case tcell.KeyF11:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyF11, Mod: mod})
	case tcell.KeyF12:
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyF12, Mod: mod})
	default:
		// Ctrl+letter: tcell Key values 1-26 map to Ctrl+A through Ctrl+Z.
		if ev.Key() >= tcell.KeyCtrlA && ev.Key() <= tcell.KeyCtrlZ {
			letter := rune('a' + ev.Key() - tcell.KeyCtrlA)
			return uv.KeyPressEvent(uv.Key{Code: letter, Mod: mod | uv.ModCtrl})
		}
	}

	// Fallback: space.
	return uv.KeyPressEvent(uv.Key{Code: uv.KeySpace})
}

// Ensure io.Writer is implemented (used by readLoop calling emu.Write).
var _ io.Writer = (*vt.SafeEmulator)(nil)
