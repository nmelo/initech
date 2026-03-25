// Package tui implements a terminal multiplexer with PTY management,
// VT emulation via charmbracelet/x/vt, and a tcell-based rendering engine.
package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/creack/pty"
	"github.com/gdamore/tcell/v2"
)

// ActivityState describes what an agent is doing based on JSONL session tailing.
type ActivityState int

const (
	StateRunning ActivityState = iota // Claude is processing.
	StateIdle                         // Waiting for input.
)

// String returns a human-readable label for the state.
func (s ActivityState) String() string {
	switch s {
	case StateRunning:
		return "running"
	case StateIdle:
		return "idle"
	}
	return "unknown"
}

// Pane represents a terminal pane backed by a PTY process.
// It uses a SafeEmulator from charmbracelet/x/vt for terminal emulation.
type Pane struct {
	cfg           PaneConfig // Original config for restart.
	name          string
	ptmx          *os.File
	cmd           *exec.Cmd
	emu           *vt.SafeEmulator
	mu            sync.Mutex
	alive         bool
	activity      ActivityState // Current state from JSONL tailing.
	lastJsonlType string       // Last JSONL entry type seen.
	lastJsonlTime time.Time    // When we last saw a file change.
	jsonlDir      string       // Directory to search for session JSONL files.
	sessionDesc   string       // Session description extracted from cursor row.
	scrollOffset  int          // Rows scrolled back from live view (0 = live).
	region        Region
}

// Region defines a rectangular area on screen (outer bounds including border).
type Region struct {
	X, Y, W, H int
}

// InnerSize returns the renderable content area (full width, minus 1 row for title bar).
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
	emu := vt.NewSafeEmulator(cols, rows)

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	var cmd *exec.Cmd
	if len(cfg.Command) == 0 {
		// No command: run an interactive login shell.
		cmd = exec.Command(shell, "-l")
	} else {
		// Run via login shell + exec so the terminal gets proper stty
		// initialization before Claude starts (Claude depends on a
		// shell-initialized PTY for correct terminal size detection).
		cmdStr := strings.Join(cfg.Command, " ")
		cmd = exec.Command(shell, "-l", "-c", "exec "+cmdStr)
	}
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		fmt.Sprintf("LINES=%d", rows),
		fmt.Sprintf("COLUMNS=%d", cols),
	)
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

	// Determine the JSONL session directory for this pane.
	// Standard Claude: ~/.claude/projects/<encoded-cwd>/
	// CCS: $CLAUDE_CONFIG_DIR/projects/<encoded-cwd>/
	jsonlDir := ""
	if cfg.Dir != "" {
		encodedCwd := encodePathForClaude(cfg.Dir)
		configDir := os.Getenv("CLAUDE_CONFIG_DIR")
		if configDir == "" {
			home, _ := os.UserHomeDir()
			configDir = filepath.Join(home, ".claude")
		}
		jsonlDir = filepath.Join(configDir, "projects", encodedCwd)
	}

	p := &Pane{
		cfg:      cfg,
		name:     cfg.Name,
		ptmx:     ptmx,
		cmd:      cmd,
		emu:      emu,
		alive:    true,
		activity: StateIdle,
		jsonlDir: jsonlDir,
	}

	// Read PTY output and feed the emulator.
	go p.readLoop()

	// Read emulator responses (DSR, DA) and write them back to the PTY.
	go p.responseLoop()

	// Watch JSONL session files for activity state.
	if jsonlDir != "" {
		go p.watchJSONL()
	}

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
			p.activity = StateIdle
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

// Selection describes a text selection range in pane-local content coordinates.
type Selection struct {
	Active         bool
	StartX, StartY int
	EndX, EndY     int
}

// Render draws the pane's title bar and terminal content onto the tcell screen.
func (p *Pane) Render(s tcell.Screen, focused bool, sel Selection) {
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

	// Fill title bar with horizontal line.
	for x := r.X; x < r.X+r.W; x++ {
		s.SetContent(x, r.Y, '\u2500', nil, fillStyle)
	}

	// Pane name + session description.
	title := " " + p.name + " "
	if !p.IsAlive() {
		title = " " + p.name + " [dead] "
		titleStyle = tcell.StyleDefault.Background(tcell.ColorDarkRed).Foreground(tcell.ColorBlack).Bold(true)
	} else if p.scrollOffset > 0 {
		title = fmt.Sprintf(" %s [+%d] ", p.name, p.scrollOffset)
		titleStyle = tcell.StyleDefault.Background(tcell.ColorYellow).Foreground(tcell.ColorBlack).Bold(true)
	}
	for i, ch := range title {
		if r.X+1+i < r.X+r.W {
			s.SetContent(r.X+1+i, r.Y, ch, nil, titleStyle)
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
				s.SetContent(r.X+col, r.Y+1+row, ch, nil, style)
			}
		}
	}

	// These variables are used by both the live rendering and cursor logic below.
	renderOffset := 0 // screen rows to skip before rendering emulator row 0
	startRow := 0     // first emulator row to render

	if p.scrollOffset == 0 {
		// Live mode: anchor content to the bottom of the pane.
		// Claude Code uses inline mode and renders from the top, leaving
		// empty rows below the cursor.
		if !p.emu.IsAltScreen() {
			// Anchor content to the bottom. Scan up to 1 row BEFORE the cursor
			// for the content extent. The cursor row often has Claude's session
			// description which we extract for the title bar instead.
			pos := p.emu.CursorPosition()
			scanEnd := pos.Y - 1
			if scanEnd < 0 {
				scanEnd = 0
			}
			lastContent := 0
			for row := scanEnd; row >= 0; row-- {
				empty := true
				for col := 0; col < innerCols; col++ {
					cell := p.emu.CellAt(col, row)
					if cell != nil && cell.Content != "" && cell.Content != " " {
						empty = false
						break
					}
				}
				if !empty {
					lastContent = row
					break
				}
			}
			contentEnd := lastContent + 1
			if contentEnd < innerRows {
				renderOffset = innerRows - contentEnd
			} else if contentEnd > innerRows {
				startRow = contentEnd - innerRows
			}

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
		for row := 0; row < innerRows; row++ {
			emuRow := startRow + (row - renderOffset)
			if emuRow < 0 || emuRow >= emuRows {
				continue
			}
			for col := 0; col < innerCols; col++ {
				cell := p.emu.CellAt(col, emuRow)
				ch, style := uvCellToTcell(cell)
				s.SetContent(r.X+col, r.Y+1+row, ch, nil, style)
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
			selStyle := tcell.StyleDefault.Background(tcell.ColorYellow).Foreground(tcell.ColorBlack)
			for row := r0; row <= r1 && row < innerRows; row++ {
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
					cell := p.emu.CellAt(col, row)
					ch := ' '
					if cell != nil && cell.Content != "" {
						ch = []rune(cell.Content)[0]
					}
					s.SetContent(r.X+col, r.Y+1+row, ch, nil, selStyle)
				}
			}
		}

		// Cursor (skip if selection is active to avoid visual conflict).
		if focused && !sel.Active {
			pos := p.emu.CursorPosition()
			visRow := pos.Y - startRow + renderOffset
			if pos.X >= 0 && pos.X < innerCols && visRow >= 0 && visRow < innerRows {
				cx := r.X + pos.X
				cy := r.Y + 1 + visRow
				cell := p.emu.CellAt(pos.X, pos.Y)
				ch, _ := uvCellToTcell(cell)
				cursorStyle := tcell.StyleDefault.Background(tcell.ColorWhite).Foreground(tcell.ColorBlack)
				s.SetContent(cx, cy, ch, nil, cursorStyle)
			}
		}
	}

}

// IsAlive returns whether the pane's shell process is still running.
func (p *Pane) IsAlive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.alive
}

// SessionDesc returns the session description extracted from Claude's cursor row.
func (p *Pane) SessionDesc() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessionDesc
}

// Activity returns the current activity state based on JSONL session tailing.
func (p *Pane) Activity() ActivityState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.activity
}

// watchJSONL polls for the newest session JSONL file in the pane's project
// directory and tails the last line to determine activity state.
func (p *Pane) watchJSONL() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastFile string
	var lastSize int64

	for {
		<-ticker.C

		p.mu.Lock()
		alive := p.alive
		p.mu.Unlock()
		if !alive {
			return
		}

		// Find the most recently modified .jsonl file in the session dir.
		file := newestJSONL(p.jsonlDir)
		if file == "" {
			continue // No session file yet, stay in starting state.
		}

		// Check if file changed.
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		size := info.Size()
		fileChanged := file != lastFile || size != lastSize

		if fileChanged {
			lastFile = file
			lastSize = size

			lastType := lastJSONLType(file)
			if lastType != "" {
				p.mu.Lock()
				p.lastJsonlType = lastType
				p.lastJsonlTime = time.Now()
				p.mu.Unlock()
			}
		}

		// Update activity state (runs every tick, even without file changes,
		// so the assistant→idle timeout works).
		p.mu.Lock()
		switch p.lastJsonlType {
		case "user", "progress", "assistant":
			// Running if the JSONL file was recently updated, idle if stale.
			// Without this timeout, a pane whose last entry is "user" or
			// "progress" stays stuck in running state forever.
			if time.Since(p.lastJsonlTime) > 5*time.Second {
				p.activity = StateIdle
			} else {
				p.activity = StateRunning
			}
		default:
			// last-prompt, system, agent-color, agent-name, etc. = idle.
			p.activity = StateIdle
		}
		p.mu.Unlock()
	}
}

// newestJSONL finds the most recently modified .jsonl file in dir (non-recursive,
// excludes subdirectories like subagents/).
func newestJSONL(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var newest string
	var newestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = filepath.Join(dir, e.Name())
		}
	}
	return newest
}

// lastJSONLType reads the last line of a JSONL file and returns its "type" field.
func lastJSONLType(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	// Seek near end and scan for last complete line.
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	size := info.Size()

	// Read the last 8KB (should be more than enough for the last JSONL entry).
	readSize := int64(8192)
	if readSize > size {
		readSize = size
	}
	f.Seek(size-readSize, 0)

	var lastLine string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) != "" {
			lastLine = line
		}
	}
	if lastLine == "" {
		return ""
	}

	var entry struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(lastLine), &entry) != nil {
		return ""
	}
	return entry.Type
}

// encodePathForClaude converts an absolute path to Claude's directory encoding
// (slashes replaced with dashes, e.g. /Users/foo/bar -> -Users-foo-bar).
func encodePathForClaude(path string) string {
	return strings.ReplaceAll(path, string(filepath.Separator), "-")
}

// ScrollUp moves the viewport up (into scrollback history) by n rows.
func (p *Pane) ScrollUp(n int) {
	maxOffset := p.emu.ScrollbackLen()
	p.scrollOffset += n
	if p.scrollOffset > maxOffset {
		p.scrollOffset = maxOffset
	}
}

// ScrollDown moves the viewport down (toward live output) by n rows.
// When scrollOffset reaches 0, the pane returns to live view.
func (p *Pane) ScrollDown(n int) {
	p.scrollOffset -= n
	if p.scrollOffset < 0 {
		p.scrollOffset = 0
	}
}

// InScrollback returns true when the pane is viewing scrollback history.
func (p *Pane) InScrollback() bool {
	return p.scrollOffset > 0
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
