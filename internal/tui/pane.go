// Package tui implements a terminal multiplexer with PTY management,
// VT emulation via charmbracelet/x/vt, and a tcell-based rendering engine.
package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
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

const codexPermissionScanRows = 10

var codexPermissionPromptPatterns = []string{
	"press enter to confirm or esc to cancel",
	"1. yes, proceed",
	"1. yes (y)",
	"2. yes, and don't ask again",
	"2. yes, and dont ask again",
	"yes, and don't ask again",
	"yes, and dont ask again",
}

// PaneView abstracts pane behavior so both local panes (Pane) and future
// network-backed panes (RemotePane) can be used interchangeably by the TUI.
type PaneView interface {
	Name() string
	Host() string // "" for local panes.
	IsAlive() bool
	IsSuspended() bool
	IsPinned() bool
	Activity() ActivityState
	LastOutputTime() time.Time
	BeadID() string
	SessionDesc() string
	IdleWithBacklog() bool
	BacklogCount() int
	Emulator() *vt.SafeEmulator
	GetRegion() Region
	SetBead(id, title string)
	SendKey(ev *tcell.EventKey)
	SendText(text string, enter bool)
	AgentType() string
	SubmitKey() string // "" or "enter" (default), "ctrl+enter".
	Render(screen tcell.Screen, focused bool, dimmed bool, index int, sel Selection)
	Resize(rows, cols int)
	Close()
}

// paneKey returns a unique identifier for a PaneView. Local panes use their
// bare name ("eng1"). Remote panes include the host prefix ("workbench:eng1").
// This prevents name collisions when a local pane and remote pane share an
// agent name (e.g. both have "eng1").
func paneKey(p PaneView) string {
	if h := p.Host(); h != "" {
		return h + ":" + p.Name()
	}
	return p.Name()
}

// Compile-time assertion: Pane implements PaneView.
var _ PaneView = (*Pane)(nil)

// Pane represents a terminal pane backed by a PTY process.
// It uses a SafeEmulator from charmbracelet/x/vt for terminal emulation.
type Pane struct {
	cfg              PaneConfig // Original config for restart.
	name             string
	ptmx             *os.File
	cmd              *exec.Cmd
	pid              int // Cached PID from process start (avoids race with restart).
	emu              *vt.SafeEmulator
	mu               sync.Mutex
	renderMu         sync.Mutex // Serializes readLoop writes with Render cell reads to prevent tearing.
	sendMu           sync.Mutex // Serializes IPC send operations to prevent keystroke interleaving.
	networkSink      io.Writer  // Optional: readLoop tees PTY bytes here for network streaming.
	sinkMu           sync.Mutex // Protects networkSink assignment.
	alive            bool
	visible          bool              // Whether this pane is shown in the layout. Hidden panes keep running.
	activity         ActivityState     // Current state: running when PTY bytes flowed recently, else idle.
	lastOutputTime   time.Time         // Last time readLoop received bytes from the PTY.
	lastIdleNotify   time.Time         // Last time an EventAgentIdleWithBead was emitted.
	journal          []JournalEntry    // Ring buffer of recent JSONL entries (cap journalRingSize).
	jsonlDir         string            // Directory to search for session JSONL files.
	eventCh          chan<- AgentEvent // Emits detected semantic events to the TUI. May be nil.
	safeGo           func(func())      // Launches a goroutine with panic recovery. Set by TUI after creation.
	goWg             sync.WaitGroup    // Tracks goroutines launched by Start(). Wait in Close().
	sessionDesc      string            // Session description extracted from cursor row.
	beadID           string            // Current bead ID (e.g., "ini-bhk.3"). Empty = no bead.
	beadTitle        string            // Bead title for top modal display.
	stallReported    bool              // True after emitting stall event. Reset on new activity.
	stuckReported    bool              // True after emitting stuck event. Reset on success.
	dedupEvents      *dedup            // Dedup state for emitted events.
	startedAt        time.Time         // When this pane's process was started. Used to filter stale JSONL.
	scrollOffset     int               // Rows scrolled back from live view (0 = live).
	idleWithBacklog  bool              // True when idle and ready beads exist in the backlog.
	backlogCount     int               // Number of ready beads at last idle-with-backlog detection.
	memoryRSS        int64             // RSS in kilobytes, updated by memory monitor goroutine.
	suspended        bool              // True when auto-suspend policy has stopped this pane.
	messageQueue     []QueuedMessage   // Messages waiting for resume. Capped at maxMessageQueue.
	pinned           bool              // Pinned agents are never auto-suspended.
	resumeGrace      time.Time         // Until this time, post-resume grace period is active.
	resumeMu         sync.Mutex        // Serializes concurrent resume attempts for this pane.
	kittEpoch        time.Time         // Reference time for KITT scanner animation phase.
	agentType        string            // Semantic agent type: claude-code, codex, or generic.
	noBracketedPaste bool              // True when injectText should use typed input instead of bracketed paste.
	submitKey        string            // Key sequence to submit: "" or "enter" (Enter), "ctrl+enter" (Ctrl+Enter).
	region           Region
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
	Name             string   // Display name (role name).
	Command          []string // Command + args. Empty means use $SHELL.
	Dir              string   // Working directory. Empty means inherit.
	Env              []string // Extra env vars (KEY=VALUE). TERM is always set.
	AgentType        string   // Semantic agent type: claude-code (default), codex, or generic.
	NoBracketedPaste bool     // Final resolved injection mode. True uses typed input instead of bracketed paste.
	SubmitKey        string   // Key sequence to submit input: "enter" (default) or "ctrl+enter".
}

// NewPane creates a terminal pane running the configured command (or $SHELL).
func NewPane(cfg PaneConfig, rows, cols int) (*Pane, error) {
	emu := vt.NewSafeEmulator(cols, rows)
	agentType := cfg.AgentType
	if agentType == "" {
		agentType = config.AgentTypeClaudeCode
	}
	submitKey := cfg.SubmitKey
	if submitKey == "" {
		submitKey = config.DefaultSubmitKey(agentType)
	}

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
		cfg:              cfg,
		name:             cfg.Name,
		ptmx:             ptmx,
		cmd:              cmd,
		pid:              pid,
		emu:              emu,
		alive:            true,
		visible:          true,
		activity:         StateIdle,
		jsonlDir:         jsonlDir,
		dedupEvents:      newDedup(),
		kittEpoch:        time.Now(),
		agentType:        agentType,
		noBracketedPaste: cfg.NoBracketedPaste,
		submitKey:        submitKey,
	}

	return p, nil
}

// Start launches the pane's background goroutines (PTY reader, response loop,
// JSONL watcher). Must be called after safeGo and eventCh are wired. If safeGo
// is nil, falls back to bare goroutine launches.
func (p *Pane) Start() {
	p.startedAt = time.Now()

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
	buf := make([]byte, 32*1024) // Match PTY buffer size for fewer syscalls.
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			data := buf[:n]

			p.mu.Lock()
			p.lastOutputTime = time.Now()
			p.mu.Unlock()

			// Write to emulator under renderMu. Using a larger read buffer
			// (32KB) coalesces multiple PTY writes into fewer emulator writes,
			// reducing the tearing window between renderMu unlock and the
			// next lock cycle (ini-jrj8).
			p.renderMu.Lock()
			p.emu.Write(data)
			p.renderMu.Unlock()

			// Tee to network sink if connected. Separate from emu.Write so
			// network backpressure cannot stall local rendering.
			p.sinkMu.Lock()
			sink := p.networkSink
			p.sinkMu.Unlock()
			if sink != nil {
				sink.Write(data)
			}
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
	// Shift+Enter: write CSI-u encoded ESC[13;2u directly to the PTY in a
	// single atomic Write call. Claude Code's ink parser (parse-keypress.ts)
	// has a CSI_U_RE regex that decodes this as Shift+Enter, which inserts a
	// newline instead of submitting. The charmbracelet VT emulator doesn't
	// support kitty keyboard protocol, so we bypass it for this key combo.
	//
	// Claude Code assumes kitty keyboard is active based on TERM_PROGRAM
	// (inherited from the outer terminal). It sends CSI > 1 u to stdout,
	// which the emulator ignores, but the input parser still accepts CSI-u
	// sequences. The 50ms ESC disambiguation timeout in App.tsx means all 7
	// bytes must arrive in a single read() on stdin. A single ptmx.Write()
	// guarantees this for small writes on a PTY.
	if ev.Key() == tcell.KeyEnter && ev.Modifiers()&tcell.ModShift != 0 {
		if p.ptmx != nil {
			p.ptmx.Write([]byte("\x1b[13;2u"))
		}
		return
	}
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

// Resize updates the emulator and PTY dimensions. Holds renderMu to serialize
// with readLoop writes and Render cell reads, preventing garbled output when
// the buffer is reorganized during zoom or layout changes (ini-ipr).
func (p *Pane) Resize(rows, cols int) {
	p.renderMu.Lock()
	p.emu.Resize(cols, rows)
	p.renderMu.Unlock()
	pty.Setsize(p.ptmx, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
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

// Name returns the pane's display name (role name).
func (p *Pane) Name() string {
	return p.name
}

// Host returns the hostname for this pane. Local panes always return "".
func (p *Pane) Host() string {
	return ""
}

// Emulator returns the pane's terminal emulator for cell-level access.
func (p *Pane) Emulator() *vt.SafeEmulator {
	return p.emu
}

// SubmitKey returns the configured submit key sequence for this pane.
func (p *Pane) SubmitKey() string { return p.submitKey }

// AgentType returns the configured semantic agent type for this pane.
func (p *Pane) AgentType() string { return p.agentType }

// SendText injects text into the pane using the harness-appropriate local
// delivery path. Claude panes use bracketed paste; raw-input panes like Codex
// write the body directly to the PTY and delay submit to avoid paste-burst
// suppression. Acquires sendMu to serialize concurrent sends.
func (p *Pane) SendText(text string, enter bool) {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	sendPaneTextLocked(p, text, enter)
}

// sendSubmitKey sends the appropriate submit key sequence to an emulator
// based on the configured submit key. Default ("" or "enter") sends Enter.
// "ctrl+enter" sends Ctrl+Enter for agents like Codex that use it for submit.
func sendSubmitKey(emu *vt.SafeEmulator, key string) {
	switch key {
	case "ctrl+enter":
		emu.SendKey(uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter, Mod: uv.ModCtrl}))
	default:
		emu.SendKey(uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter}))
	}
}

// maybeApproveCodexPermissionPrompt scans the bottom rows of the emulator for
// a Codex permission prompt and, if found, writes "p" to the PTY to approve
// and remember the choice. It only applies to no-bracketed-paste panes.
func (p *Pane) maybeApproveCodexPermissionPrompt() bool {
	if !p.noBracketedPaste || p.ptmx == nil {
		return false
	}

	p.renderMu.Lock()
	text := emulatorBottomText(p.emu, codexPermissionScanRows)
	p.renderMu.Unlock()
	if !isCodexPermissionPrompt(text) {
		return false
	}

	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	if !p.noBracketedPaste || p.ptmx == nil {
		return false
	}
	_, err := p.ptmx.Write([]byte("p"))
	return err == nil
}

func emulatorBottomText(emu *vt.SafeEmulator, lines int) string {
	cols := emu.Width()
	rows := emu.Height()
	if lines <= 0 || lines > rows {
		lines = rows
	}
	start := rows - lines

	var buf strings.Builder
	for row := start; row < rows; row++ {
		var line strings.Builder
		for col := 0; col < cols; col++ {
			cell := emu.CellAt(col, row)
			if cell != nil && cell.Content != "" {
				line.WriteString(cell.Content)
			} else {
				line.WriteByte(' ')
			}
		}
		buf.WriteString(strings.TrimRight(line.String(), " "))
		buf.WriteByte('\n')
	}
	return buf.String()
}

func isCodexPermissionPrompt(text string) bool {
	normalized := strings.ToLower(text)
	normalized = strings.ReplaceAll(normalized, "’", "'")
	for _, pattern := range codexPermissionPromptPatterns {
		if strings.Contains(normalized, pattern) {
			return true
		}
	}
	return false
}

// GetRegion returns the pane's screen region.
func (p *Pane) GetRegion() Region {
	return p.region
}

// SetNetworkSink sets the writer that receives a copy of all PTY output.
// Used by the daemon to stream bytes to a connected client. The sink
// receives bytes after the emulator, so network backpressure cannot stall
// local rendering.
func (p *Pane) SetNetworkSink(w io.Writer) {
	p.sinkMu.Lock()
	p.networkSink = w
	p.sinkMu.Unlock()
}

// ClearNetworkSink removes the network sink. Safe to call if no sink is set.
func (p *Pane) ClearNetworkSink() {
	p.sinkMu.Lock()
	p.networkSink = nil
	p.sinkMu.Unlock()
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

// IsPinned reports whether the operator has pinned this pane to prevent
// auto-suspension. Pinned panes are always kept running.
func (p *Pane) IsPinned() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pinned
}

// SetPinned marks the pane as pinned (true) or unpinned (false).
func (p *Pane) SetPinned(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pinned = v
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
	case tcell.KeyBacktab:
		// Shift+Tab: tcell reports this as a distinct key, not Tab+Shift.
		return uv.KeyPressEvent(uv.Key{Code: uv.KeyTab, Mod: mod | uv.ModShift})
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

// quotaRe matches "N% of" in the Claude Code status bar (e.g. "75% of limit").
var quotaRe = regexp.MustCompile(`(\d{1,3})%\s+of`)

// ScrapeQuota reads the emulator's status bar rows and extracts the quota
// percentage ("N% of limit"). Returns 0-100 on success, -1 if not found.
// Skips panes in alt-screen mode (vim, less) where the status bar is hidden.
func (p *Pane) ScrapeQuota() int {
	if p.emu == nil || p.emu.IsAltScreen() {
		return -1
	}
	cols := p.emu.Width()
	rows := p.emu.Height()
	if cols < 10 || rows < 2 {
		return -1
	}

	// Scan the last 4 rows for a status bar (contains U+2502 separator).
	for row := rows - 1; row >= rows-4 && row >= 0; row-- {
		if !rowContainsStatusBar(p.emu, row, cols) {
			continue
		}
		// Extract text content from this row.
		var sb strings.Builder
		for col := 0; col < cols; col++ {
			cell := p.emu.CellAt(col, row)
			if cell != nil && cell.Content != "" {
				sb.WriteString(cell.Content)
			} else {
				sb.WriteByte(' ')
			}
		}
		line := sb.String()
		if m := quotaRe.FindStringSubmatch(line); m != nil {
			if pct, err := strconv.Atoi(m[1]); err == nil && pct >= 0 && pct <= 100 {
				return pct
			}
		}
	}
	return -1
}

// Ensure io.Writer is implemented (used by readLoop calling emu.Write).
var _ io.Writer = (*vt.SafeEmulator)(nil)
