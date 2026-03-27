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
	StateRunning   ActivityState = iota // Claude is processing.
	StateIdle                           // Waiting for input.
	StateDead                           // Process has exited; pane is no longer alive.
	StateSuspended                      // Auto-suspended by resource policy. Eligible for resume.
)

// String returns a human-readable label for the state.
func (s ActivityState) String() string {
	switch s {
	case StateRunning:
		return "running"
	case StateIdle:
		return "idle"
	case StateDead:
		return "dead"
	case StateSuspended:
		return "suspended"
	}
	return "unknown"
}

// JournalEntry represents a parsed JSONL entry from a Claude Code session.
type JournalEntry struct {
	Type      string    // "user", "assistant", "progress", "system", "last-prompt", etc.
	Content   string    // Text content (assistant message, tool output). Capped at 4KB.
	ToolName  string    // For tool_use/tool_result: which tool was called.
	ExitCode  int       // For Bash tool results: exit code if available.
	Timestamp time.Time // When this entry was written.
}

const (
	journalRingSize = 20   // Number of recent entries to keep per pane.
	maxContentLen   = 4096 // Max content bytes per JournalEntry.
)

// Pane represents a terminal pane backed by a PTY process.
// It uses a SafeEmulator from charmbracelet/x/vt for terminal emulation.
type Pane struct {
	cfg           PaneConfig // Original config for restart.
	name          string
	ptmx          *os.File
	cmd           *exec.Cmd
	pid           int              // Cached PID from process start (avoids race with restart).
	emu           *vt.SafeEmulator
	mu            sync.Mutex
	sendMu        sync.Mutex       // Serializes IPC send operations to prevent keystroke interleaving.
	alive          bool
	visible        bool           // Whether this pane is shown in the layout. Hidden panes keep running.
	activity       ActivityState  // Current state: running when PTY bytes flowed recently, else idle.
	lastOutputTime time.Time      // Last time readLoop received bytes from the PTY.
	lastIdleNotify time.Time      // Last time an EventAgentIdleWithBead was emitted.
	journal        []JournalEntry // Ring buffer of recent JSONL entries (cap journalRingSize).
	jsonlDir      string             // Directory to search for session JSONL files.
	eventCh       chan<- AgentEvent  // Emits detected semantic events to the TUI. May be nil.
	safeGo        func(func())     // Launches a goroutine with panic recovery. Set by TUI after creation.
	goWg          sync.WaitGroup  // Tracks goroutines launched by Start(). Wait in Close().
	sessionDesc   string       // Session description extracted from cursor row.
	beadID        string       // Current bead ID (e.g., "ini-bhk.3"). Empty = no bead.
	beadTitle     string       // Bead title for top modal display.
	stallReported   bool           // True after emitting stall event. Reset on new activity.
	stuckReported   bool           // True after emitting stuck event. Reset on success.
	dedupEvents     *dedup           // Dedup state for emitted events.
	scrollOffset    int              // Rows scrolled back from live view (0 = live).
	idleWithBacklog bool             // True when idle and ready beads exist in the backlog.
	backlogCount    int              // Number of ready beads at last idle-with-backlog detection.
	memoryRSS       int64            // RSS in kilobytes, updated by memory monitor goroutine.
	suspended       bool             // True when auto-suspend policy has stopped this pane.
	messageQueue    []QueuedMessage  // Messages waiting for resume. Capped at maxMessageQueue.
	pinned          bool             // Pinned agents are never auto-suspended.
	resumeGrace     time.Time        // Until this time, post-resume grace period is active.
	resumeMu        sync.Mutex       // Serializes concurrent resume attempts for this pane.
	region          Region
}

// Region defines a rectangular area on screen (outer bounds including border).
type Region struct {
	X, Y, W, H int
}

// InnerSize returns the renderable content area (full width, minus 1 row for bottom ribbon).
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
	} else if containsArg(cfg.Command, "--continue") {
		// --continue fails when no prior session exists (first launch,
		// hot-added agent, deleted session). Build a shell fallback:
		//   claude --continue ... || claude ...
		// The "||" operator is POSIX sh syntax; using $SHELL here would fail
		// for fish/tcsh users since those shells use different operators.
		// /bin/sh is guaranteed POSIX-compliant on all Unix systems.
		primary := shellQuoteArgs(cfg.Command)
		fallback := shellQuoteArgs(removeArg(cfg.Command, "--continue"))
		cmd = exec.Command("/bin/sh", "-l", "-c", primary+" || "+fallback)
	} else {
		// Execute directly without a shell. The login shell wrapper (shell -l)
		// is still used to initialize the PTY environment (stty, $PATH, etc.)
		// but exec replaces it with the target process, preventing shell injection.
		quoted := shellQuoteArgs(cfg.Command)
		cmd = exec.Command(shell, "-l", "-c", "exec "+quoted)
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

	// Cache PID at creation time so it can be read without locking cmd.Process.
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	p := &Pane{
		cfg:         cfg,
		name:        cfg.Name,
		ptmx:        ptmx,
		cmd:         cmd,
		pid:         pid,
		emu:         emu,
		alive:       true,
		visible:     true,
		activity:    StateIdle,
		jsonlDir:    jsonlDir,
		dedupEvents: newDedup(),
	}

	return p, nil
}

// Start launches the pane's background goroutines (PTY reader, response loop,
// JSONL watcher). Must be called after safeGo and eventCh are wired. If safeGo
// is nil, falls back to bare goroutine launches.
func (p *Pane) Start() {
	launch := p.safeGo
	if launch == nil {
		launch = func(fn func()) { go fn() }
	}
	count := 2 // readLoop + responseLoop.
	if p.jsonlDir != "" {
		count++
	}
	p.goWg.Add(count)
	launch(func() { defer p.goWg.Done(); p.readLoop() })
	launch(func() { defer p.goWg.Done(); p.responseLoop() })
	if p.jsonlDir != "" {
		launch(func() { defer p.goWg.Done(); p.watchJSONL() })
	}
}

func (p *Pane) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			p.mu.Lock()
			p.lastOutputTime = time.Now()
			p.mu.Unlock()
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

// SendPaste writes a bracketed paste marker to the PTY.
// On start=true it writes \x1b[200~ (paste start); on start=false it writes
// \x1b[201~ (paste end). The child process uses these delimiters to
// distinguish pasted content from typed keystrokes.
// No-op if the PTY is not open.
func (p *Pane) SendPaste(start bool) {
	if p.ptmx == nil {
		return
	}
	if start {
		p.ptmx.Write([]byte("\x1b[200~")) //nolint:errcheck
	} else {
		p.ptmx.Write([]byte("\x1b[201~")) //nolint:errcheck
	}
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

}

// ForwardMouse sends a mouse event to the emulator with pane-local content
// coordinates translated to emulator coordinates. The emulator silently
// drops the event if the child process hasn't enabled mouse reporting.
func (p *Pane) ForwardMouse(ev uv.MouseEvent) {
	p.emu.SendMouse(ev)
}

// contentOffset computes the mapping from screen-local content rows to
// emulator rows for bottom-anchored (non-alt-screen) content. In alt-screen
// mode or scrollback mode, the mapping is identity (both return 0).
//
// Usage: emuRow = startRow + (screenRow - renderOffset)
func (p *Pane) contentOffset() (startRow, renderOffset int) {
	if p.scrollOffset > 0 || p.emu.IsAltScreen() {
		return 0, 0
	}

	innerCols, innerRows := p.region.InnerSize()
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
	if contentEnd > innerRows {
		// Content overflows the pane: scroll to show the bottom.
		startRow = contentEnd - innerRows
	}
	// When content fits within the pane, render from the top (no offset).
	return
}

// IsAlive returns whether the pane's shell process is still running.
func (p *Pane) IsAlive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.alive
}

// Visible returns whether the pane is included in the current layout.
// Hidden panes keep their emulator running at their last visible size.
func (p *Pane) Visible() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.visible
}

// SetVisible controls whether the pane appears in the layout.
// Hiding a pane does not stop its process or resize its emulator.
func (p *Pane) SetVisible(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.visible = v
}

// IsSuspended returns whether the pane has been stopped by the auto-suspend
// policy. A suspended pane is distinct from dead (crashed) and will
// auto-resume when a message arrives.
func (p *Pane) IsSuspended() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.suspended
}

// SetSuspended marks the pane as suspended or resumed by the auto-suspend
// policy.
func (p *Pane) SetSuspended(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.suspended = v
}

// SessionDesc returns the session description extracted from Claude's cursor row.
func (p *Pane) SessionDesc() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessionDesc
}

// BeadID returns the currently assigned bead ID, or empty string.
func (p *Pane) BeadID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.beadID
}

// SetBead sets the current bead ID and title.
func (p *Pane) SetBead(id, title string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.beadID = id
	p.beadTitle = title
}

// RecentEntries returns a copy of the recent JSONL entries ring buffer.
func (p *Pane) RecentEntries() []JournalEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]JournalEntry, len(p.journal))
	copy(cp, p.journal)
	return cp
}

// Activity returns the current activity state based on JSONL session tailing.
func (p *Pane) Activity() ActivityState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.activity
}

// IdleWithBacklog returns true when the pane is idle and ready beads exist.
func (p *Pane) IdleWithBacklog() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.idleWithBacklog
}

// BacklogCount returns the number of ready beads at the last idle-with-backlog detection.
func (p *Pane) BacklogCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.backlogCount
}

// SetIdleWithBacklog marks the pane as idle with n ready beads in the backlog.
func (p *Pane) SetIdleWithBacklog(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.idleWithBacklog = true
	p.backlogCount = n
}

// ClearIdleWithBacklog clears the idle-with-backlog indicator.
func (p *Pane) ClearIdleWithBacklog() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.idleWithBacklog = false
	p.backlogCount = 0
}

// MemoryRSS returns the pane's last polled RSS in kilobytes.
// Returns 0 if the memory monitor has not yet polled or the process is dead.
func (p *Pane) MemoryRSS() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.memoryRSS
}

// setMemoryRSS updates the pane's cached RSS value. Called by the memory
// monitor goroutine.
func (p *Pane) setMemoryRSS(kb int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.memoryRSS = kb
}

// IsPinned returns whether this pane is protected from auto-suspension.
func (p *Pane) IsPinned() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pinned
}

// Pin marks this pane as protected from auto-suspension.
func (p *Pane) Pin() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pinned = true
}

// Unpin removes auto-suspension protection from this pane.
func (p *Pane) Unpin() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pinned = false
}

// LastOutputTime returns the last time PTY output was received.
func (p *Pane) LastOutputTime() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastOutputTime
}

// InResumeGrace returns true if the pane is within the post-resume grace
// period. During this window the pane is exempt from auto-suspend and
// idle-with-bead notifications.
func (p *Pane) InResumeGrace() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return time.Now().Before(p.resumeGrace)
}

// SetResumeGrace sets the post-resume grace period expiration.
func (p *Pane) SetResumeGrace(until time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resumeGrace = until
}

// watchJSONL polls for the newest session JSONL file in the pane's project
// directory, reads new entries incrementally, and updates both the journal
// ring buffer and the activity state.
func (p *Pane) watchJSONL() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastFile string
	var lastOffset int64

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
			continue
		}

		// File rotation: new file -> reset offset.
		if file != lastFile {
			lastFile = file
			lastOffset = 0
		}

		// Check file size. Truncation (size < offset) -> reset.
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		size := info.Size()
		if size < lastOffset {
			lastOffset = 0
		}
		if size == lastOffset {
			p.runDetectors(nil) // stall/stuck checks run every tick
			continue
		}

		// Read new entries from offset.
		entries, newOffset := recentJSONLEntries(file, lastOffset)
		lastOffset = newOffset

		if len(entries) > 0 {
			p.mu.Lock()
			// Append to ring buffer.
			for _, e := range entries {
				if len(p.journal) >= journalRingSize {
					p.journal = p.journal[1:]
				}
				p.journal = append(p.journal, e)
			}
			p.mu.Unlock()

			// Bead auto-detection: check new entries for bd claim/clear signals.
			if p.eventCh != nil {
				p.applyBeadDetection(entries)
			}
		}

		p.runDetectors(entries)
	}
}

// applyBeadDetection runs detectBeadClaim on new entries and applies the result:
// sets or clears the pane's bead display and emits an event if appropriate.
// Must be called outside p.mu (it acquires the lock internally via SetBead).
func (p *Pane) applyBeadDetection(entries []JournalEntry) {
	beadID, clear := detectBeadClaim(entries)
	switch {
	case clear:
		p.SetBead("", "")
	case beadID != "":
		p.SetBead(beadID, "")
		EmitEvent(p.eventCh, AgentEvent{
			Type:   EventBeadClaimed,
			Pane:   p.name,
			BeadID: beadID,
			Detail: p.name + " claimed " + beadID,
		})
	}
}

// ptyIdleTimeout is how long to wait after the last PTY byte before declaring
// an agent idle. Claude Code's spinner runs at 10-30fps during all active
// states (thinking, tool execution, generation). A 2-second gap in output
// means the agent is genuinely idle at the prompt.
const ptyIdleTimeout = 2 * time.Second

// idleNotifyCooldown is the minimum time between EventAgentIdleWithBead
// emissions for a single pane. Prevents notification spam from burst output
// patterns that straddle the idle threshold.
const idleNotifyCooldown = 60 * time.Second

// updateActivity derives activity state from PTY output recency.
// Called per pane on every render tick. Detects running->idle edge transitions
// and emits EventAgentIdleWithBead when the pane holds a bead and the cooldown
// has elapsed.
func (p *Pane) updateActivity() {
	p.mu.Lock()
	defer p.mu.Unlock()

	prev := p.activity
	if !p.alive {
		if p.suspended {
			p.activity = StateSuspended
		} else {
			p.activity = StateDead
		}
		return
	}
	if time.Since(p.lastOutputTime) < ptyIdleTimeout {
		p.activity = StateRunning
	} else {
		p.activity = StateIdle
	}

	// Detect running->idle edge with a bead assigned and cooldown elapsed.
	if prev == StateRunning && p.activity == StateIdle &&
		p.beadID != "" && p.eventCh != nil &&
		time.Since(p.lastIdleNotify) > idleNotifyCooldown {
		p.lastIdleNotify = time.Now()
		EmitEvent(p.eventCh, AgentEvent{
			Type:   EventAgentIdleWithBead,
			Pane:   p.name,
			BeadID: p.beadID,
			Detail: p.beadID,
		})
	}

}

// runDetectors runs all event detectors (completion, stall, stuck) and emits
// discovered events to p.eventCh. newEntries contains entries read since the
// last tick; pass nil when no new data arrived (stall/stuck still check every
// tick). Safe to call from watchJSONL goroutine only.
func (p *Pane) runDetectors(newEntries []JournalEntry) {
	if p.eventCh == nil || p.dedupEvents == nil {
		return
	}

	// Read protected fields atomically.
	p.mu.Lock()
	beadID := p.beadID
	journal := make([]JournalEntry, len(p.journal))
	copy(journal, p.journal)
	stallReported := p.stallReported
	stuckReported := p.stuckReported
	p.mu.Unlock()

	// Derive last JSONL entry time from the ring buffer for stall detection.
	var lastTime time.Time
	if len(journal) > 0 {
		lastTime = journal[len(journal)-1].Timestamp
	}

	// Completion/claimed/failed detection on new entries only.
	if len(newEntries) > 0 {
		for _, ev := range detectCompletion(newEntries, p.name) {
			if p.dedupEvents.shouldEmit(ev) {
				EmitEvent(p.eventCh, ev)
			}
		}
		// New activity clears the stall state so the next silence
		// triggers a fresh stall notification rather than staying silent.
		if stallReported {
			p.mu.Lock()
			p.stallReported = false
			p.mu.Unlock()
		}
	}

	// Stuck detection on full journal (every tick).
	if ev := detectStuck(journal, p.name); ev != nil {
		if !stuckReported {
			p.mu.Lock()
			p.stuckReported = true
			p.mu.Unlock()
			if p.dedupEvents.shouldEmit(*ev) {
				EmitEvent(p.eventCh, *ev)
			}
		}
	} else if stuckReported {
		// Error loop cleared (success seen). Reset so next loop triggers again.
		p.mu.Lock()
		p.stuckReported = false
		p.mu.Unlock()
	}

	// Stall detection (every tick).
	if ev := detectStall(lastTime, beadID, p.name, DefaultStallThreshold); ev != nil {
		if !stallReported {
			p.mu.Lock()
			p.stallReported = true
			p.mu.Unlock()
			if p.dedupEvents.shouldEmit(*ev) {
				EmitEvent(p.eventCh, *ev)
			}
		}
	}

	// Periodically prune the dedup map to avoid unbounded growth.
	p.dedupEvents.prune()
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

// recentJSONLEntries reads new JSONL entries from the given file starting at
// sinceOffset. Returns parsed entries and the new file offset. Handles partial
// lines at the boundary by only consuming complete lines.
func recentJSONLEntries(path string, sinceOffset int64) ([]JournalEntry, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, sinceOffset
	}
	defer f.Close()

	if sinceOffset > 0 {
		if _, err := f.Seek(sinceOffset, 0); err != nil {
			return nil, sinceOffset
		}
	}

	var entries []JournalEntry
	// Use ReadBytes instead of bufio.Scanner so we count the actual bytes
	// consumed including the line terminator. Scanner adds a fixed +1 for the
	// newline, which drifts on CRLF files (each line needs +2). ReadBytes
	// returns the terminator as part of the slice, so len(lineBytes) is exact
	// for both LF and CRLF files.
	reader := bufio.NewReaderSize(f, 256*1024)
	bytesRead := sinceOffset

	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err == io.EOF {
			// Partial line at EOF — incomplete line, don't advance offset.
			break
		}
		if err != nil {
			break
		}
		bytesRead += int64(len(lineBytes))
		line := strings.TrimRight(string(lineBytes), "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}
		entry := parseJSONLEntry(line)
		if entry.Type != "" {
			entries = append(entries, entry)
		}
	}

	return entries, bytesRead
}

// parseJSONLEntry parses a single JSONL line into a JournalEntry.
// Extracts type, content, tool name, and exit code from the nested structure.
func parseJSONLEntry(line string) JournalEntry {
	var raw struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(line), &raw) != nil {
		return JournalEntry{}
	}

	entry := JournalEntry{Type: raw.Type}
	if raw.Timestamp != "" {
		entry.Timestamp, _ = time.Parse(time.RFC3339Nano, raw.Timestamp)
	}

	// Parse content array for assistant messages (text blocks, tool_use).
	if len(raw.Message.Content) > 0 {
		var blocks []struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Name  string `json:"name"`
			Input struct {
				Command string `json:"command"`
			} `json:"input"`
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ExitCode *int   `json:"exit_code"`
			} `json:"content"`
		}
		if json.Unmarshal(raw.Message.Content, &blocks) == nil {
			for _, b := range blocks {
				switch b.Type {
				case "text":
					entry.Content = truncateContent(b.Text)
				case "tool_use":
					entry.ToolName = b.Name
					if b.Input.Command != "" {
						entry.Content = truncateContent(b.Input.Command)
					}
				case "tool_result":
					for _, c := range b.Content {
						if c.Type == "text" {
							entry.Content = truncateContent(c.Text)
						}
						if c.ExitCode != nil {
							entry.ExitCode = *c.ExitCode
						}
					}
				}
			}
		} else {
			// Content might be a plain string (user messages).
			var text string
			if json.Unmarshal(raw.Message.Content, &text) == nil {
				entry.Content = truncateContent(text)
			}
		}
	}

	return entry
}

// truncateContent caps a string at maxContentLen bytes to prevent memory bloat
// in the ring buffer.
func truncateContent(s string) string {
	if len(s) > maxContentLen {
		return s[:maxContentLen] + "...[truncated]"
	}
	return s
}

// encodePathForClaude converts an absolute path to Claude's directory encoding
// (slashes replaced with dashes, e.g. /Users/foo/bar -> -Users-foo-bar).
func encodePathForClaude(path string) string {
	return strings.ReplaceAll(path, string(filepath.Separator), "-")
}

// containsArg returns true if flag appears exactly in args.
// shellQuoteArgs shell-quotes each element of args and joins them with spaces.
// Each argument is wrapped in single quotes with any embedded single-quote
// characters escaped as '"'"', making the result safe to pass to sh -c.
// This prevents shell injection when user-supplied values appear in args.
func shellQuoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		// Replace ' with '"'"' (end quote, literal single quote, reopen quote)
		// then wrap the whole thing in single quotes.
		quoted[i] = "'" + strings.ReplaceAll(a, "'", "'\"'\"'") + "'"
	}
	return strings.Join(quoted, " ")
}

func containsArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// removeArg returns a copy of args with all occurrences of flag removed.
func removeArg(args []string, flag string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != flag {
			out = append(out, a)
		}
	}
	return out
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

// Close terminates the PTY, kills the process, and signals goroutines to exit.
func (p *Pane) Close() {
	// Signal watchJSONL and readLoop to exit.
	p.mu.Lock()
	p.alive = false
	p.mu.Unlock()

	// Close PTY first so readLoop's ptmx.Read() errors out immediately.
	if p.ptmx != nil {
		p.ptmx.Close()
	}
	// Close only the emulator's input pipe writer so responseLoop's blocking
	// e.pr.Read() returns EOF and the goroutine exits. We must NOT call
	// emu.Close() here because it writes e.closed=true which races with
	// responseLoop's concurrent Read() that also checks e.closed. After
	// goWg.Wait() confirms responseLoop has exited, it is safe to call
	// emu.Close() without any concurrent reader.
	if p.emu != nil {
		if pw, ok := p.emu.InputPipe().(*io.PipeWriter); ok {
			pw.CloseWithError(io.EOF)
		}
	}
	if p.cmd != nil {
		if p.cmd.Process != nil {
			p.cmd.Process.Kill()
		}
		p.cmd.Wait()
	}
	// Wait for all goroutines started by Start() to exit before touching
	// emu or ptmx fields, preventing data races detected by the race detector.
	p.goWg.Wait()
	// responseLoop has exited; safe to call emu.Close() now.
	if p.emu != nil {
		p.emu.Close()
	}
}


// dimStyle reduces the foreground brightness of a tcell.Style for focus dimming.
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
