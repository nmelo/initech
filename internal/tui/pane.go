// Package tui implements a terminal multiplexer with PTY management,
// VT emulation via charmbracelet/x/vt, and a tcell-based rendering engine.
package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
)

// ActivityState describes what an agent is doing based on JSONL session tailing.
type ActivityState int

const (
	StateRunning   ActivityState = iota // Claude is processing.
	StateIdle                           // Sitting at the prompt with nothing to do.
	StateDead                           // Process has exited; pane is no longer alive.
	StateSuspended                      // Auto-suspended by resource policy. Eligible for resume.
	// StateWaitingInput means a blocking dialog is open and the OPERATOR is the
	// thing being waited on -- a question, a permission prompt. Distinct from
	// StateIdle (nothing to do) and from StateRunning (working), and NOT
	// derivable from either: both are computed from PTY byte recency, and a
	// dialog that repaints keeps producing bytes, which is exactly why the
	// recorded codex gap shows a permission prompt reading as "running" despite
	// no work happening. Appended rather than inserted so the existing constants
	// keep their values (ini-2x8.1).
	StateWaitingInput
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
	case StateWaitingInput:
		return "waiting"
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

const codexReadyPollInterval = 50 * time.Millisecond
const codexReadyTimeout = 10 * time.Second
const codexReadyStableDuration = 500 * time.Millisecond

var codexNotReadyPromptPatterns = []string{
	"do you trust the contents of this directory",
	"press enter to continue",
	"1. yes, continue",
	"2. no, quit",
	"booting mcp server",
}

var codexTrustPromptPatterns = []string{
	"do you trust the contents of this directory",
	"press enter to continue",
	"1. yes, continue",
	"2. no, quit",
}

// PaneView abstracts pane behavior so both local panes (Pane) and future
// network-backed panes (RemotePane) can be used interchangeably by the TUI.
type PaneView interface {
	Name() string
	Host() string // "" for local panes.
	IsAlive() bool
	IsSuspended() bool
	IsProtected() bool
	Activity() ActivityState
	LastOutputTime() time.Time
	BeadID() string
	BeadIDs() []string
	SessionDesc() string
	Emulator() *vt.SafeEmulator
	GetRegion() Region
	SetBead(id, title string)
	SetBeads(ids []string)
	SendKey(ev *tcell.EventKey)
	SendText(text string, enter bool)
	// FlushPaste delivers a buffered bracketed paste to the agent, UNSUBMITTED.
	//
	// On the contract rather than reached by type assertion (ini-a9d8): the
	// assertion in handlePaste silently discarded every paste into a viewer
	// window for two years, because a *RemotePane simply failed it and there
	// was nothing to notice. A method here cannot be skipped by a new pane
	// kind -- it will not compile without an answer.
	FlushPaste(content []byte)
	AgentType() string
	SubmitKey() string // "" or "enter" (default), "ctrl+enter".
	ActiveRunStart() time.Time
	ActiveRunBytes() int64
	LastMessageReceived() time.Time
	LastEventTime() time.Time
	Render(screen tcell.Screen, focused bool, dimmed bool, index int, sel Selection)
	Resize(rows, cols int)
	Close()
}

// paneKey returns a unique identifier for a PaneView. Local panes use their
// bare name ("eng1"). Remote panes include the host prefix ("workbench:eng1").
// This prevents name collisions when a local pane and remote pane share an
// agent name (e.g. both have "eng1").
// fleetNumbered is implemented by panes that carry a fleet-canonical number
// (ini-6m4). The agents modal displays THIS number, not the pane's position in
// t.panes: positions are per-window state (each window applies its own saved
// order), so numbering by position gave two windows two numberings for the
// same fleet, and grab-by-number would have acted on different agents per
// window. The canonical order is window 1's pane creation order -- which the
// window server snapshots BEFORE layout reordering and serves to viewers as
// the ordered hello_ok agent list, so both sides can number from the same
// sequence without a wire change.
type fleetNumbered interface {
	FleetIdx() int
}

// fleetIdxOf returns the pane's fleet-canonical index, falling back to the
// caller's local index for panes that predate or don't carry one.
func fleetIdxOf(p PaneView, localIdx int) int {
	if fn, ok := p.(fleetNumbered); ok {
		if i := fn.FleetIdx(); i >= 0 {
			return i
		}
	}
	return localIdx
}

// FleetIdx implements fleetNumbered. Negative means never stamped.
func (p *Pane) FleetIdx() int { return p.fleetNum - 1 }

// SetFleetIdx stamps the fleet-canonical number. Called once per pane, on the
// main goroutine, at session start (creation order) or hot-add.
func (p *Pane) SetFleetIdx(i int) { p.fleetNum = i + 1 }

// agentKey is THE ONE NAMED TRANSLATION EDGE (ini-yc03): the single place a
// pane's observer-relative form becomes the agent's canonical identity, and
// the only identity any fleet-scoped structure may be keyed by.
//
// WHAT IS OBSERVER-RELATIVE AND WHAT IS IDENTITY. A pane's host prefix means
// two different things depending on which host it names:
//
//   - The WINDOW-1 ALIAS is presentation. A viewer reaches window 1's agents
//     through a peer literally named "window1", so the same agent is "eng1" to
//     window 1 and "window1:eng1" to a viewer. That prefix describes the
//     OBSERVER, not the agent, and is stripped here.
//   - A REAL CROSS-MACHINE HOST is identity. "workbench:eng1" and a local
//     "eng1" are different agents on different machines, and stripping that
//     prefix would collapse them into one. It survives.
//
// WHY THE EDGE IS THE IDENTITY FUNCTION ITSELF rather than a translator called
// at each boundary: ini-6m4, xq4r, qkwc and i7fr were four fences around four
// fields while the divergence boundary spanned every field, present and
// future. A per-call translation is another fence. Making the identity
// canonical at the point it is COMPUTED means every structure downstream is
// canonical by construction, and a new fleet-scoped field cannot forget to
// translate because there is nothing left to translate.
//
// This generalizes fleetStoreKey, which performed exactly this strip for the
// fleet store alone -- one field's fence, now the whole boundary.
// observerAliasPrefix is THE ONE PLACE the window-1 observer alias is spelled.
// Every strip goes through canonicalStoreKey and every identity through
// agentKey, so a second spelling of this prefix anywhere is a second
// translation edge -- which ini-yc03's source guard fails on.
const observerAliasPrefix = WindowOnePeerName + ":"

// canonicalStoreKey is the string-level form of agentKey's rule, for keys read
// off disk or the wire where no PaneView exists. It strips the window-1
// observer alias and nothing else: a real cross-machine host prefix is part of
// the agent's identity and survives.
func canonicalStoreKey(key string) string {
	return strings.TrimPrefix(key, observerAliasPrefix)
}

func agentKey(p PaneView) string {
	h := p.Host()
	if h == "" || h == WindowOnePeerName {
		return p.Name()
	}
	return h + ":" + p.Name()
}

// Compile-time assertion: Pane implements PaneView.
var _ PaneView = (*Pane)(nil)

// Pane represents a terminal pane backed by a PTY process.
// It uses a SafeEmulator from charmbracelet/x/vt for terminal emulation.
type Pane struct {
	cfg  PaneConfig // Original config for restart.
	name string
	// onSuspendedMessage fires when SendText queues for a suspended pane
	// (ini-g7fl): the TUI wires it to resume-on-message, because the pane
	// cannot respawn itself and entry points must not each remember to.
	onSuspendedMessage    func(*Pane)
	fleetNum              int // Fleet-canonical number PLUS ONE (ini-6m4); zero value = unstamped, so struct-literal construction (tests, fakes) falls back to local numbering instead of reading as "stamped at 0". See fleetNumbered.
	ptmx                  xpty.Pty
	cmd                   *exec.Cmd
	pid                   int // Cached PID from process start (avoids race with restart).
	emu                   *vt.SafeEmulator
	mu                    sync.Mutex
	renderMu              sync.Mutex // Serializes readLoop writes with Render cell reads to prevent tearing.
	sendMu                sync.Mutex // Serializes IPC send operations to prevent keystroke interleaving.
	networkSink           io.Writer  // Optional: readLoop tees PTY bytes here for network streaming.
	sinkMu                sync.Mutex // Protects networkSink assignment.
	alive                 bool
	visible               bool              // Whether this pane is shown in the layout. Hidden panes keep running.
	activity              ActivityState     // Current state: running when PTY bytes flowed recently, else idle.
	lastOutputTime        time.Time         // Last time readLoop received bytes from the PTY.
	tintUntil             time.Time         // Hold deadline for the running-pane background tint (ini-zmzg). Bumped while StateRunning; the tint shows until this passes, giving the bg its own hysteresis window decoupled from the 2s dot/KITT signal.
	lastIdleNotify        time.Time         // Last time an EventAgentIdleWithBead was emitted.
	idleWithBeadThreshold time.Duration     // Silence duration before idle-with-bead fires. 0 = disabled.
	idleBeadNotified      bool              // True after idle-with-bead fires. Reset when output resumes.
	beadAssignedAt        time.Time         // When the current bead was assigned. Grace period starts here.
	waitingSince          time.Time         // When the currently-open blocking dialog was first seen. Zero = not waiting.
	waitingPreview        string            // What to show for this agent in the needs-input list. Empty is allowed.
	waitingTier           WaitingTier       // Confidence in the current wait. Zero value is the SILENT tier, deliberately.
	waitingModalSeen      bool              // The screen has confirmed this wait's dialog, so the screen may also retire it.
	attn                  *attentionSignal  // Mailbox the OSC 777 handler writes into. Leaf-locked; see attention_detect.go.
	journal               []JournalEntry    // Ring buffer of recent JSONL entries (cap journalRingSize).
	jsonlDir              string            // Directory to search for session JSONL files.
	eventCh               chan<- AgentEvent // Emits detected semantic events to the TUI. May be nil.
	safeGo                func(func())      // Launches a goroutine with panic recovery. Set by TUI after creation.
	goWg                  sync.WaitGroup    // Tracks goroutines launched by Start(). Wait in Close().
	sessionDesc           string            // Session description extracted from cursor row.
	beadIDs               []string          // Current bead IDs. Nil = no beads. First is primary.
	beadTitle             string            // Bead title for top modal display.
	stallReported         bool              // True after emitting stall event. Reset on new activity.
	stuckReported         bool              // True after emitting stuck event. Reset on success.
	dedupEvents           *dedup            // Dedup state for emitted events.
	startedAt             time.Time         // When this pane's process was started. Used to filter stale JSONL.
	scrollOffset          int               // Rows scrolled back from live view (0 = live).
	resizeSettleFrames    int               // Render frames remaining to skip after resize.
	resizeSettleDeadline  time.Time         // Hard deadline: skip content rendering until this time.
	scrollAnchorLen       int               // Scrollback length when user last scrolled. Used to compensate for new output.
	memoryRSS             int64             // RSS in kilobytes, updated by memory monitor goroutine.
	suspended             bool              // True when auto-suspend policy has stopped this pane.
	messageQueue          []QueuedMessage   // Messages waiting for resume or modal-close. Capped at maxMessageQueue.
	idlePromptSince       time.Time         // When the pane last began rendering its idle composer (ini-gbqc).
	pendingSubmit         *pendingSubmit    // A submit the belt withheld, waiting for the composer to repaint (ini-vpwg).
	waking                bool              // A wake is in flight (ini-zffi). Guards against a burst of keystrokes each launching a respawn, and drives the "waking" pane display.
	modalDraining         bool              // True while a modal-close queue drain is in flight (guarded by p.mu).
	protected             bool              // Protected agents are never auto-suspended.
	resumeGrace           time.Time         // Until this time, post-resume grace period is active.
	resumeMu              sync.Mutex        // Serializes concurrent resume attempts for this pane.
	kittEpoch             time.Time         // Reference time for KITT scanner animation phase.
	agentType             string            // Semantic agent type: claude-code, codex, or generic.
	noBracketedPaste      bool              // True when injectText should use typed input instead of bracketed paste.
	submitKey             string            // Key sequence to submit: "" or "enter" (Enter), "ctrl+enter" (Ctrl+Enter).
	region                Region
	activeRunStart        time.Time // Set on idle->running edge, cleared on running->idle.
	activeRunBytes        int64     // Bytes received since last idle->running edge.
	lastMessageReceived   time.Time // Updated when injectText delivers a message to this pane.
	lastEventTime         time.Time // Updated when an AgentEvent fires for this pane.

	// lastAltScreen is the child's alt-screen state as of the most recent
	// resize (ini-y97). Compared against p.emu.IsAltScreen() on every PTY
	// write (checkAltScreenTransition) to detect entry/exit and trigger the
	// PTY/emulator resize each requires. Zero value (false) matches a
	// freshly-started shell, which is not in alt-screen mode.
	lastAltScreen bool

	// dialogOpen latches an application-declared blocking dialog (ini-zjhg).
	// Raised from OSC 777, cleared by operator input; see modal_detect.go for
	// why the guard reads this rather than the attention row, and why the clear
	// is input-driven rather than screen-driven.
	//
	// A plain bool, deliberately: the attention MAILBOX (p.attn) is a pointer
	// that a directly-constructed Pane can leave nil, and a guard with an
	// optional dependency has an "absent" branch that silently degrades to the
	// old screen-only behaviour. A value field has no absent case to fall back
	// through -- the zero value is simply "no dialog declared".
	dialogOpen bool
	// dialogOpenAt is when the latch was raised, and dialogCorroborated records
	// whether SIGHT has confirmed it since (ini-9gvn). A latch raised falsely can
	// never earn its clear -- no dialog ever renders to be seen closing -- so
	// without these it is permanent, survives a restart, and holds the fleet's
	// mail forever. Measured live: eng2's pane deferred every send for ~30
	// minutes and through a restart.
	dialogOpenAt       time.Time
	dialogCorroborated bool
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

// TerminalSize returns the dimensions given to the VT emulator and PTY.
// Subtracts 2 rows from region height: 1 for the bottom ribbon, 1 for the
// top activity bar. The child process sees these dimensions via LINES and
// SIGWINCH, so content it renders fits entirely within the visible area.
func (r Region) TerminalSize() (cols, rows int) {
	cols = r.W
	rows = r.H - 2
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
	BeadsEnabled     bool     // When false, skip bead detection (detectBeadClaim, detectCompletion, detectStall).
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

	cmd := buildPaneCmd(cfg, rows, cols)

	ptmx, err := xpty.NewPty(cols, rows)
	if err != nil {
		return nil, fmt.Errorf("create pty: %w", err)
	}
	if err := ptmx.Start(cmd); err != nil {
		ptmx.Close()
		return nil, fmt.Errorf("start command: %w", err)
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
		cfg:  cfg,
		name: cfg.Name,

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
		attn:             &attentionSignal{},
	}

	// Wire tier-1 attention detection (ini-2x8.2) BEFORE Start() spins up
	// readLoop. Registering an OSC handler mutates the emulator's handler map,
	// which Write reads -- doing it on a pane that is already reading is a data
	// race, not just a timing question.
	registerAttentionOSC(p)

	return p, nil
}

// NewParkedPane builds a pane in the suspended state WITHOUT starting a
// process: no fork, no PTY, no goroutines — construction cost is one emulator
// allocation. It is byte-for-byte the state parkPaneSuspended leaves behind
// (suspended=true, activity=StateSuspended, dead child), so every existing
// wake path — queued message, keystroke, `initech resume` — works on it
// unchanged via resumePane, which replaces the pane wholesale.
//
// Used at launch for agents whose suspension was persisted across the restart
// and for agents deferred by the spawn stagger. Cannot fail: there is nothing
// to exec.
func NewParkedPane(cfg PaneConfig, rows, cols int) *Pane {
	emu := vt.NewSafeEmulator(cols, rows)
	agentType := cfg.AgentType
	if agentType == "" {
		agentType = config.AgentTypeClaudeCode
	}
	submitKey := cfg.SubmitKey
	if submitKey == "" {
		submitKey = config.DefaultSubmitKey(agentType)
	}
	return &Pane{
		cfg:              cfg,
		name:             cfg.Name,
		emu:              emu,
		alive:            false,
		visible:          true,
		suspended:        true,
		activity:         StateSuspended,
		dedupEvents:      newDedup(),
		kittEpoch:        time.Now(),
		agentType:        agentType,
		noBracketedPaste: cfg.NoBracketedPaste,
		submitKey:        submitKey,
		attn:             &attentionSignal{},
	}
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
			p.activeRunBytes += int64(n)
			p.mu.Unlock()

			p.renderMu.Lock()
			p.emu.Write(data)
			p.checkAltScreenTransition()
			p.renderMu.Unlock()

			// Re-deliver any sends that were deferred while a modal was open,
			// now that the latest output is on screen and the modal may have
			// closed (ini-7txh). Cheap no-op when the queue is empty.
			p.maybeDrainModalQueue()

			// A submit the belt withheld waits here for the composer to
			// repaint (ini-vpwg). Same hook as the modal drain and for the
			// same reason: new output IS the repaint, so this is the moment
			// the composer's state is worth re-reading. Cheap no-op when
			// nothing is pending.
			p.maybeRetryWithheldSubmit()

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
	// The operator is acting on this pane, which is the only way an open dialog
	// gets answered -- so retire the dialog latch (ini-zjhg). This is the
	// operator input path; injected message bytes go straight to the PTY and do
	// NOT come through here, which is what keeps a drain from clearing the latch
	// that is gating it.
	p.noteOperatorInput()

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

// FlushPaste writes the entire paste content to the PTY in a single operation,
// wrapped in bracketed paste markers (unless noBracketedPaste is set). Uses
// sendMu to serialize with concurrent IPC sends and prevent interleaving.
// For large pastes (>64KB), the content is written in chunks to avoid blocking
// the PTY write buffer for extended periods.
func (p *Pane) FlushPaste(content []byte) {
	if p.ptmx == nil || len(content) == 0 {
		return
	}

	p.sendMu.Lock()
	defer p.sendMu.Unlock()

	if p.noBracketedPaste {
		p.writePTYChunked(content)
		return
	}

	p.ptmx.Write([]byte("\x1b[200~")) //nolint:errcheck
	p.writePTYChunked(content)
	p.ptmx.Write([]byte("\x1b[201~")) //nolint:errcheck
}

// writePTYChunked writes data to the PTY, splitting into 64KB chunks for
// large payloads to avoid blocking the PTY write buffer indefinitely.
func (p *Pane) writePTYChunked(data []byte) {
	const chunkSize = 64 * 1024
	for len(data) > 0 {
		n := len(data)
		if n > chunkSize {
			n = chunkSize
		}
		p.ptmx.Write(data[:n]) //nolint:errcheck
		data = data[n:]
	}
}

// resizeSettleCount is the number of render frames to skip after a resize.
// One frame is insufficient: the child process needs to receive SIGWINCH,
// re-layout, and emit new-geometry output before the emulator content is valid.
const resizeSettleCount = 3

// resizeSettleDuration is the minimum wall-clock time to suppress content
// rendering after a resize, covering slow child redraws.
const resizeSettleDuration = 150 * time.Millisecond

// Resize updates the PTY and emulator dimensions to fit the given VISIBLE
// rows/cols. Resizes the PTY first so the child process receives the size
// change before the emulator buffer reorganizes. Holds renderMu across both
// operations to prevent readLoop from writing old-geometry PTY output into a
// new-geometry emulator buffer (ini-yah).
func (p *Pane) Resize(rows, cols int) {
	p.renderMu.Lock()
	defer p.renderMu.Unlock()
	p.resizeLocked(rows, cols)
}

// ResizeExact sizes the PTY and emulator to exactly rows x cols, with no
// scrollback growth factor. The 3x inflation exists so a LOCAL renderer can
// window scrolling content (see resizeLocked); a headless daemon has no
// renderer — its viewers do — and inflating there means the child paints its
// bottom rows (Claude's composer) at a height the viewer's true-sized
// emulator does not have: the input box vanished behind the hub's chrome
// (ini-ap3i, operator-observed 2026-08-15). Two emulators sharing one byte
// stream must share one size belief, and the viewer's is the true one.
func (p *Pane) ResizeExact(rows, cols int) {
	p.renderMu.Lock()
	defer p.renderMu.Unlock()
	if rows < 1 {
		rows = 1
	}
	cols = effectiveEmuCols(cols)
	if p.ptmx != nil {
		p.ptmx.Resize(cols, rows)
	}
	p.emu.Resize(cols, rows)
	p.resizeSettleFrames = resizeSettleCount
	p.resizeSettleDeadline = time.Now().Add(resizeSettleDuration)
	p.lastAltScreen = p.emu.IsAltScreen()
}

// resizeLocked performs the actual PTY/emulator resize to fit the given
// VISIBLE rows/cols. Caller must hold renderMu.
//
// The emulator/PTY height is NOT simply the visible height: whether it gets
// inflated depends on the child's CURRENT screen mode, checked fresh here
// every call rather than cached, since a resize can itself be the trigger
// that needs to react to a mode that just changed (see
// checkAltScreenTransition).
//
//   - Normal (non-alt-screen) child: inflate to effectiveEmuRows(rows) -- a
//     TALLER virtual screen so a live region (an AskUserQuestion modal, an
//     in-progress response) that exceeds the visible window renders into the
//     emulator in full rather than clipping. The render path windows the
//     bottom `rows` lines (pane_render live mode, contentOffset's bottom
//     anchor) and scrolling reveals the clipped top (ini-44hp).
//   - Alt-screen child (vim, htop, Claude Code's "tui":"fullscreen"): use the
//     TRUE visible rows, unmodified. An alt-screen child draws ABSOLUTELY
//     across whatever height it is told it has -- there is no bottom-anchor
//     to reveal a clipped portion the way there is for normal scrolling
//     content, so a 3x-inflated report can never fit a 1x visible window no
//     matter how the render path windows it. The child must be told the
//     truth (ini-y97).
func (p *Pane) resizeLocked(rows, cols int) {
	emuRows := rows
	if !p.emu.IsAltScreen() {
		emuRows = effectiveEmuRows(rows)
	}
	// Clamp cols to a sane bound (ini-hup3): an out-of-range column count from a
	// resize control message (e.g. cols=MaxInt32, or a large finite value)
	// otherwise flows straight into the emulator buffer's make(Line, width),
	// panicking with "makeslice: len out of range" or allocating multiple GB and
	// OOMing the daemon. Mirrors the effectiveEmuRows cap on the row axis.
	cols = effectiveEmuCols(cols)
	if p.ptmx != nil {
		p.ptmx.Resize(cols, emuRows)
	}
	p.emu.Resize(cols, emuRows)
	p.resizeSettleFrames = resizeSettleCount
	p.resizeSettleDeadline = time.Now().Add(resizeSettleDuration)
	p.lastAltScreen = p.emu.IsAltScreen()
}

// checkAltScreenTransition detects a change in the child's alt-screen mode
// since the last check and, if one occurred, resizes the PTY/emulator to
// match (see resizeLocked): true visible dimensions on entry, the inflated
// scrollable-live-region height on exit. Called from readLoop after every
// PTY write, so the pane's own on-screen rectangle doesn't need to have
// changed -- the trigger is the child's rendering mode, not a layout event.
// Caller must hold renderMu (readLoop already does, around emu.Write).
//
// On ENTRY specifically, also clears scrollOffset/scrollAnchorLen (ini-i3v).
// Wheel input while alt-screen is active is forwarded to the child instead
// of mutating scrollOffset (see mouse.go), so scrollOffset cannot accumulate
// DURING alt-screen -- but a pane that was already mid-scrollback in normal
// mode before its child switched to alt-screen would otherwise carry that
// stale, invisible-during-alt-screen offset forward. Without this reset,
// exiting alt-screen would silently drop the pane into that stale scrollback
// position instead of resuming the live view.
func (p *Pane) checkAltScreenTransition() {
	nowAlt := p.emu.IsAltScreen()
	if nowAlt == p.lastAltScreen {
		return
	}
	if nowAlt {
		p.scrollOffset = 0
		p.scrollAnchorLen = 0
	}
	cols, rows := p.region.TerminalSize()
	p.resizeLocked(rows, cols)
}

// emuRowsGrowthFactor multiplies a pane's visible height to get its emulator
// height. 3x covers the confirmed worst case (a tall AskUserQuestion modal of
// ~25-30 rows in a 10-row pane) with headroom; see ini-44hp.
const emuRowsGrowthFactor = 3

// emuRowsCap bounds per-pane emulator memory regardless of visible height.
const emuRowsCap = 300

// emuColsCap bounds per-pane emulator width regardless of the requested column
// count, so a malicious or buggy resize can't panic (makeslice) or allocate
// unbounded memory in the emulator buffer (ini-hup3). Generous vs. real
// terminals (~80-400 cols) while keeping a width x height buffer tiny.
const emuColsCap = 1000

// effectiveEmuCols clamps a requested column count to [1, emuColsCap].
func effectiveEmuCols(cols int) int {
	if cols < 1 {
		return 1
	}
	if cols > emuColsCap {
		return emuColsCap
	}
	return cols
}

// effectiveEmuRows returns the emulator/PTY height for a pane whose VISIBLE
// height is visibleRows. The emulator is grown to emuRowsGrowthFactor x the
// visible height (capped at emuRowsCap, and never smaller than the visible
// height) so the child renders its full live region into a scrollable buffer
// instead of clipping it to the small pane (ini-44hp).
func effectiveEmuRows(visibleRows int) int {
	if visibleRows < 1 {
		visibleRows = 1
	}
	emuRows := emuRowsGrowthFactor * visibleRows
	if emuRows > emuRowsCap {
		emuRows = emuRowsCap
	}
	if emuRows < visibleRows {
		emuRows = visibleRows
	}
	return emuRows
}

// ForwardMouse sends a mouse event to the emulator with pane-local content
// coordinates translated to emulator coordinates. The emulator silently
// drops the event if the child process hasn't enabled mouse reporting.
func (p *Pane) ForwardMouse(ev uv.MouseEvent) {
	// Operator input, same as SendKey: it retires the dialog latch (ini-zjhg).
	// A click is a live candidate for answering a dialog outright -- ini-zjhg AC
	// item 4 measures whether it selects an option row directly -- so it must
	// not be treated as more innocent than a keystroke.
	p.noteOperatorInput()
	p.emu.SendMouse(ev)
}

// maxScrollOffset returns the largest meaningful scrollOffset. Beyond this
// value the view window would extend past the top of the virtual buffer
// (scrollback + screen). The formula is scrollbackLen + emuHeight - termRows.
func (p *Pane) maxScrollOffset() int {
	scrollbackLen := p.emu.ScrollbackLen()
	emuHeight := p.emu.Height()
	termRows := emuHeight
	if p.region.H > 2 {
		_, termRows = p.region.TerminalSize()
	}
	max := scrollbackLen + emuHeight - termRows
	if max < 0 {
		max = 0
	}
	return max
}

// applyScrollAnchor adjusts scrollOffset to compensate for new scrollback
// lines added since the user scrolled. Must be called before any cell drawing
// so the view window uses the corrected offset.
func (p *Pane) applyScrollAnchor() {
	if p.scrollOffset > 0 && p.scrollAnchorLen > 0 {
		delta := p.emu.ScrollbackLen() - p.scrollAnchorLen
		if delta > 0 {
			p.scrollOffset += delta
			p.scrollAnchorLen = p.emu.ScrollbackLen()
		}
	}
	if max := p.maxScrollOffset(); p.scrollOffset > max {
		p.scrollOffset = max
	}
}

// contentOffset computes the mapping from screen-local content rows to
// emulator rows for bottom-anchored (non-alt-screen) content. In alt-screen
// mode the mapping is identity (both return 0). In scrollback mode, startRow
// is the virtual row (scrollback + screen combined) of the view window top.
//
// Usage: emuRow = startRow + (screenRow - renderOffset)
func (p *Pane) contentOffset() (startRow, renderOffset int) {
	if p.emu.IsAltScreen() {
		return 0, 0
	}
	if p.scrollOffset > 0 {
		scrollbackLen := p.emu.ScrollbackLen()
		totalVirtual := scrollbackLen + p.emu.Height()
		_, termRows := p.region.TerminalSize()
		viewBottom := totalVirtual - p.scrollOffset
		if viewBottom < 0 {
			viewBottom = 0
		}
		viewTop := viewBottom - termRows
		if viewTop < 0 {
			viewTop = 0
		}
		return viewTop, 0
	}

	innerCols, termRows := p.region.TerminalSize()
	// Anchor to the bottom of the FULL drawn screen, not the cursor row. The
	// emulator may be taller than the visible window (ini-44hp); claude parks
	// its cursor mid-screen and renders its status bar BELOW the cursor, so a
	// cursor-bounded scan (pos.Y-1) anchors too high and clips the active area
	// off the bottom window. Scanning the whole screen finds the true last drawn
	// row. For a non-taller emulator (emuHeight==termRows) the result is
	// identical: contentEnd<=termRows, so startRow stays 0.
	scanEnd := p.emu.Height() - 1
	if scanEnd < 0 {
		scanEnd = 0
	}
	lastContent := 0
	for row := scanEnd; row >= 0; row-- {
		empty := true
		for col := 0; col < innerCols; col++ {
			// CellValueAt copies under lock (ini-wizq): contentOffset is
			// called both under p.renderMu (from Render) and with no lock at
			// all (from mouse.go on every mouse event), so it cannot rely on
			// the caller holding renderMu.
			cell, ok := p.emu.CellValueAt(col, row)
			if ok && cell.Content != "" && cell.Content != " " {
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
	if contentEnd > termRows {
		// Content overflows the pane: scroll to show the bottom.
		startRow = contentEnd - termRows
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

// virtualCellAt returns the cell at virtual row vRow (scrollback + screen
// combined). vRow in [0, scrollbackLen) reads from scrollback; vRow in
// [scrollbackLen, scrollbackLen+emuRows) reads from the live screen buffer.
//
// The returned pointer aliases live emulator/scrollback memory (ini-wizq): safe
// only under a lock that excludes concurrent Write/Reflow, which today means
// p.renderMu. Render (via renderSelectionVirtual) holds renderMu, so its use
// there is correct. Any other caller must use virtualCellValueAt instead.
func (p *Pane) virtualCellAt(col, vRow int) *uv.Cell {
	scrollbackLen := p.emu.ScrollbackLen()
	if vRow < scrollbackLen {
		return p.emu.ScrollbackCellAt(col, vRow)
	}
	return p.emu.CellAt(col, vRow-scrollbackLen)
}

// virtualCellValueAt is the lock-free-safe counterpart to virtualCellAt: it
// returns a COPY of the cell, safe to read without holding renderMu or any
// other lock (ini-wizq). Use this from any caller that is not already
// serialized against emulator writes by renderMu — e.g. the mouse selection
// copy path, which runs on the main goroutine without renderMu.
func (p *Pane) virtualCellValueAt(col, vRow int) (uv.Cell, bool) {
	scrollbackLen := p.emu.ScrollbackLen()
	if vRow < scrollbackLen {
		return p.emu.ScrollbackCellValueAt(col, vRow)
	}
	return p.emu.CellValueAt(col, vRow-scrollbackLen)
}

// SubmitKey returns the configured submit key sequence for this pane.
func (p *Pane) SubmitKey() string { return p.submitKey }

// AgentType returns the configured semantic agent type for this pane.
func (p *Pane) AgentType() string { return p.agentType }

// SendText injects text into the pane using the harness-appropriate local
// delivery path. Claude panes use bracketed paste; raw-input panes like Codex
// write the body directly to the PTY and delay submit to avoid paste-burst
// suppression. The Codex ready-wait runs before acquiring sendMu so it does
// not block concurrent sends.
func (p *Pane) SendText(text string, enter bool) {
	p.mu.Lock()
	p.lastMessageReceived = time.Now()
	suspended := p.suspended
	cb := p.onSuspendedMessage
	p.mu.Unlock()

	// SUSPENSION GUARD AT THE PRIMITIVE (ini-g7fl). A suspended pane's
	// process is gone and its PTY is closed; writing there is silent loss.
	// The guard lived only in the IPC handler, and three other entry points
	// (forward_send, the daemon's two send sites) called this primitive
	// directly -- window-2 and cross-machine sends to a suspended agent
	// vanished with success reported upstream (ini-9imx, measured). Guarding
	// HERE means every present and future caller inherits queue-on-suspended
	// without knowing suspension exists: structural, not per-caller vigilance.
	if suspended {
		if dropped := p.EnqueueMessage(text, enter); dropped {
			EmitEvent(p.eventCh, AgentEvent{
				Type:   EventAgentStalled,
				Pane:   p.name,
				Detail: "Message queue full, oldest message dropped.",
				Time:   time.Now(),
			})
		}
		// Resume-on-message, via the callback the TUI wires at pane
		// creation/adoption -- the pane cannot resume itself (respawn needs
		// TUI state), and entry points must not each remember to trigger it.
		if cb != nil {
			cb(p)
		}
		return
	}
	waitForCodexReadyIfNeeded(p)
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	sendPaneTextLocked(p, text, enter)
}

// WakeOnStreamInput is the remote twin of wakeSuspendedPaneFromKeystroke's
// swallow-and-wake: input arriving over a window stream at a suspended pane
// is a WAKE GESTURE, not text — the process is gone and its PTY is closed,
// so writing would be silent loss, and delivering the bytes post-wake would
// forge input the operator aimed at a parked pane, not at whatever prompt
// the respawned process shows. Returns true when the input was consumed as
// a gesture; the pump must not write it anywhere.
func (p *Pane) WakeOnStreamInput() bool {
	p.mu.Lock()
	suspended := p.suspended
	cb := p.onSuspendedMessage
	p.mu.Unlock()
	if !suspended {
		return false
	}
	if cb != nil {
		cb(p)
	}
	return true
}

// SetOnSuspendedMessage wires the resume-on-message trigger (ini-g7fl). Called
// by the TUI when it adopts a pane; fired (on the caller's goroutine) when a
// message arrives for a suspended pane, after the message is safely queued.
func (p *Pane) SetOnSuspendedMessage(fn func(*Pane)) {
	p.mu.Lock()
	p.onSuspendedMessage = fn
	p.mu.Unlock()
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

func emulatorBottomText(emu *vt.SafeEmulator, lines int) string {
	cols := emu.Width()
	rows := emu.Height()
	if lines <= 0 || lines > rows {
		lines = rows
	}
	start := rows - lines

	var buf strings.Builder
	for row := start; row < rows; row++ {
		// RowText copies the row under a single lock, so this cannot observe
		// a torn cell from a concurrent readLoop write (ini-wizq).
		buf.WriteString(strings.TrimRight(emu.RowText(row, cols), " "))
		buf.WriteByte('\n')
	}
	return buf.String()
}

func isCodexReadyPrompt(text string) bool {
	normalized := strings.ToLower(text)
	normalized = strings.ReplaceAll(normalized, "’", "'")
	for _, pattern := range codexNotReadyPromptPatterns {
		if strings.Contains(compactPromptText(normalized), compactPromptText(pattern)) {
			return false
		}
	}

	lines := strings.Split(normalized, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		for _, prompt := range []string{"›", ">"} {
			if line == prompt {
				return true
			}
			if strings.HasPrefix(line, prompt+" ") {
				rest := strings.TrimSpace(strings.TrimPrefix(line, prompt))
				if rest == "" {
					return true
				}
				if rest[0] >= '0' && rest[0] <= '9' {
					return false
				}
				return true
			}
		}
		return false
	}
	return false
}

func isCodexTrustPrompt(text string) bool {
	normalized := strings.ToLower(text)
	normalized = strings.ReplaceAll(normalized, "’", "'")
	for _, pattern := range codexTrustPromptPatterns {
		if !strings.Contains(compactPromptText(normalized), compactPromptText(pattern)) {
			return false
		}
	}
	return true
}

func compactPromptText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
}

func (p *Pane) isCodexReadyForSend() bool {
	p.mu.Lock()
	alive := p.alive
	lastOutput := p.lastOutputTime
	p.mu.Unlock()
	if !alive || time.Since(lastOutput) < ptyIdleTimeout {
		return false
	}

	p.renderMu.Lock()
	text := emulatorBottomText(p.emu, p.emu.Height())
	p.renderMu.Unlock()
	return isCodexReadyPrompt(text)
}

func (p *Pane) waitForCodexReady(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	trustAccepted := false
	var readySince time.Time
	for {
		p.renderMu.Lock()
		text := emulatorBottomText(p.emu, p.emu.Height())
		p.renderMu.Unlock()
		if isCodexTrustPrompt(text) && !trustAccepted && p.ptmx != nil {
			_, _ = p.ptmx.Write([]byte("\r"))
			trustAccepted = true
		}
		if p.isCodexReadyForSend() {
			if readySince.IsZero() {
				readySince = time.Now()
			} else if time.Since(readySince) >= codexReadyStableDuration {
				return true
			}
		} else {
			readySince = time.Time{}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(codexReadyPollInterval)
	}
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

// IsProtected reports whether the operator has protected this pane from
// auto-suspension. Protected panes are always kept running.
func (p *Pane) IsProtected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.protected
}

// SetProtected marks the pane as protected (true) or unprotected (false).
func (p *Pane) SetProtected(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.protected = v
}

// SessionDesc returns the session description extracted from Claude's cursor row.
func (p *Pane) SessionDesc() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessionDesc
}

// BeadID returns the first (primary) bead ID, or empty string.
func (p *Pane) BeadID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.beadIDs) == 0 {
		return ""
	}
	return p.beadIDs[0]
}

// BeadIDs returns all assigned bead IDs.
func (p *Pane) BeadIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.beadIDs) == 0 {
		return nil
	}
	out := make([]string, len(p.beadIDs))
	copy(out, p.beadIDs)
	return out
}

// BeadTitle returns the title of the primary bead, or empty string.
func (p *Pane) BeadTitle() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.beadTitle
}

// SetBead sets a single bead ID (backward compat). Pass "" to clear.
// When assigning a non-empty bead, resets the idle-with-bead grace window
// so the notification doesn't fire on stale lastOutputTime (ini-t42).
func (p *Pane) SetBead(id, title string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id == "" {
		p.beadIDs = nil
	} else {
		p.beadIDs = []string{id}
		p.beadAssignedAt = time.Now()
		p.idleBeadNotified = false
	}
	p.beadTitle = title
}

// SetBeads sets multiple bead IDs. Pass nil to clear.
// When assigning non-empty beads, resets the idle-with-bead grace window
// so the notification doesn't fire on stale lastOutputTime (ini-t42).
func (p *Pane) SetBeads(ids []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(ids) == 0 {
		p.beadIDs = nil
	} else {
		p.beadIDs = make([]string, len(ids))
		copy(p.beadIDs, ids)
		p.beadAssignedAt = time.Now()
		p.idleBeadNotified = false
	}
	p.beadTitle = ""
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

// SetWaitingInput marks the pane as blocked on the operator at the LIST-ONLY
// tier -- it appears in the needs-input list and stays silent.
//
// Silent is the default on purpose. The operator's standing rule is that a false
// chime is a defect, and that when a tier's confidence is in doubt the row
// renders while the chime stays quiet. Making the quiet tier the zero value
// means a detector has to opt IN to making noise, so a new detector added later
// cannot become audible by forgetting to say anything.
func (p *Pane) SetWaitingInput(preview string) {
	p.SetWaitingInputTier(preview, WaitingTierListOnly)
}

// SetWaitingInputTier marks the pane as blocked on the operator at an explicit
// tier. Idempotent on the timestamp: a detector that re-asserts the same open
// dialog every tick must not restart the wait clock, or the list's durations
// never advance and the 2-minute chime reminder never comes due. Only the FIRST
// call since the last clear sets waitingSince.
//
// preview and tier are refreshed on every call, so a detector that learns more
// later -- a screen scrape arriving after the edge signal, or a hook confirming
// what a heuristic guessed -- can upgrade the row without costing it the wait it
// has already accumulated.
func (p *Pane) SetWaitingInputTier(preview string, tier WaitingTier) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waitingSince.IsZero() {
		p.waitingSince = time.Now()
	}
	p.waitingPreview = preview
	p.waitingTier = tier
}

// WaitingTierOf returns the tier of the current wait. Meaningless when the pane
// is not waiting.
func (p *Pane) WaitingTierOf() WaitingTier {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitingTier
}

// ClearWaitingInput marks the pane as no longer blocked on the operator.
// Safe to call when it was not waiting.
func (p *Pane) ClearWaitingInput() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.waitingSince = time.Time{}
	p.waitingPreview = ""
	p.waitingTier = WaitingTierListOnly
	p.waitingModalSeen = false
}

// WaitingInput reports whether the operator is being waited on, since when, and
// what to show. since is zero exactly when waiting is false.
func (p *Pane) WaitingInput() (waiting bool, since time.Time, preview string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.waitingSince.IsZero(), p.waitingSince, p.waitingPreview
}

// ActiveRunStart returns when the current running streak began.
func (p *Pane) ActiveRunStart() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.activeRunStart
}

// ActiveRunBytes returns bytes received during the current running streak.
func (p *Pane) ActiveRunBytes() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.activeRunBytes
}

// LastMessageReceived returns when a message was last delivered to this pane.
func (p *Pane) LastMessageReceived() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastMessageReceived
}

// LastEventTime returns when an AgentEvent last fired for this pane.
func (p *Pane) LastEventTime() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastEventTime
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
	p.scrollOffset += n
	if max := p.maxScrollOffset(); p.scrollOffset > max {
		p.scrollOffset = max
	}
	p.scrollAnchorLen = p.emu.ScrollbackLen()
}

// ScrollDown moves the viewport down (toward live output) by n rows.
// When scrollOffset reaches 0, the pane returns to live view.
func (p *Pane) ScrollDown(n int) {
	p.scrollOffset -= n
	if p.scrollOffset < 0 {
		p.scrollOffset = 0
	}
	// When returning to live edge, clear the anchor so auto-scroll resumes.
	if p.scrollOffset == 0 {
		p.scrollAnchorLen = 0
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
	// (The field is nil'd at the END of Close, after goWg.Wait -- see below.)
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
	// BUSY-ONLY GRACE (ini-ap3i, operator-decided 2026-08-15): a RUNNING
	// agent gets SIGTERM and up to 2s before the kill, so an in-flight child
	// command (a git push, a file write) can finish or die cleanly instead of
	// being orphaned mid-operation. IDLE agents — the overwhelmingly common
	// case at quit — keep the instant kill: Claude Code persists its
	// transcript incrementally, so there is nothing to save, and a blanket
	// grace would re-add the multi-second quit tax measured and removed the
	// same evening. If SIGTERM is unsupported (Windows) the grace is skipped
	// rather than waited out.
	//
	// THIS SIGNAL ERROR MEANS "CAPABILITY UNSUPPORTED", NOT "PROCESS DEAD",
	// and the distinction is why this site is correct while resumePane's was
	// not (ini-uop2). Reading the same error as liveness is what made every
	// Windows wake report a dead process; here the error only skips an
	// optional grace and the Kill/Wait below still run. Verified as intended,
	// unchanged by uop2, and deliberately NOT given a Windows grace path in
	// that bead.
	var waited chan struct{}
	if p.cmd != nil && p.cmd.Process != nil {
		if p.Activity() == StateRunning {
			if err := p.cmd.Process.Signal(syscall.SIGTERM); err == nil {
				waited = make(chan struct{})
				go func() { p.cmd.Wait(); close(waited) }() //nolint:errcheck
				select {
				case <-waited:
				case <-time.After(2 * time.Second):
				}
			}
		}
		p.cmd.Process.Kill() //nolint:errcheck // No-op if the grace already reaped it.
		if waited != nil {
			<-waited
		} else {
			p.cmd.Wait() //nolint:errcheck
		}
	}

	// Wait for all goroutines started by Start() to exit before touching
	// emu or ptmx fields, preventing data races detected by the race detector.
	p.goWg.Wait()
	// responseLoop has exited; safe to call emu.Close() now.
	if p.emu != nil {
		p.emu.Close()
	}
	// NIL THE PTY LAST (ini-g7fl), after every goroutine that read it has
	// exited: a closed-but-not-nil ptmx made later writes vanish into the
	// dead descriptor with the error discarded -- sendPaneTextLocked's
	// nil-guard existed and never fired (measured: queue=0, ptmx_nil=false,
	// message gone). With the field nil, any dead-pane write path that slips
	// past the suspension guard hits the explicit early-return instead of a
	// silent discard. The suspend path holds sendMu across this entire Close,
	// so no send can interleave with the teardown it serializes against.
	p.mu.Lock()
	p.ptmx = nil
	p.mu.Unlock()
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

// Ensure io.Writer is implemented (used by readLoop calling emu.Write).
var _ io.Writer = (*vt.SafeEmulator)(nil)

// QueuedMessageCount reports how many messages are waiting for this pane to
// resume. Read before a wake so a success message can name what the operator's
// action actually delivered: resumePane drains the queue as part of waking, so
// counting afterwards always reports zero (ini-zffi).
func (p *Pane) QueuedMessageCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.messageQueue)
}

// IsWaking reports whether a wake is currently in flight for this pane
// (ini-zffi). The display uses it so a parked pane visibly changes the moment
// the operator's gesture lands, rather than sitting on "[suspended]" for the
// ~1.1s the respawn takes and looking like the keystroke was ignored.
func (p *Pane) IsWaking() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waking
}
