package tui

import (
	"strings"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
)

// LayoutMode determines how panes are arranged on screen.
type LayoutMode int

const (
	LayoutFocus LayoutMode = iota // Single pane, full screen.
	LayoutGrid                    // Arbitrary NxM grid.
	Layout2Col                    // Focused pane left (40%), grid of others right (60%).
	LayoutLive                    // Dynamic pane rotation by activity conviction score.
)

// AgentInfo describes an agent for the status overlay.
type AgentInfo struct {
	Name       string
	Status     string        // Display text: activity string or bead ID.
	Activity   ActivityState // Actual activity state for dot color.
	Visible    bool
	Protected  bool // True when agent is protected from auto-suspend.
	LivePinned bool // True when agent is pinned to a live mode slot.
	Remote     bool // True for agents on remote peers.
	Divider    bool // True for a machine-divider row (Name holds the machine); not an agent.
}

// cmdModal holds command modal state.
type cmdModal struct {
	active bool
	buf    []rune
	cursor int    // 0-based cursor position within buf (0 = before first rune).
	error  string // Shown briefly after a bad command.

	// Error auto-clear: when error is set, errorExpiry tracks when to clear it.
	// Zero value means the expiry hasn't been stamped yet (stamped lazily on
	// the first render tick after error is set, so callers don't need to
	// remember to set it).
	errorExpiry time.Time

	// Tab completion state.
	tabBuf  string // Buffer content at last Tab press (double-Tab detection).
	tabHint string // Completion hint line shown above the input bar; empty = no hint.

	// Fuzzy command suggestions (shown while typing the command keyword).
	suggestions []string // Top matches for the first word being typed; empty = no hint.

	// Destructive command confirmation state.
	pendingConfirm string    // Command waiting for Enter-to-confirm ("quit", "remove eng1", "restart eng2").
	confirmExpiry  time.Time // When the confirmation prompt auto-expires.
	confirmMsg     string    // Human-readable confirmation prompt text.
}

// topModal holds activity monitor (top) modal state.
type topModal struct {
	active       bool
	selected     int
	scrollOffset int
	data         []topEntry
	cacheTime    time.Time
}

// eventLogModal holds event log modal state.
type eventLogModal struct {
	active       bool
	scrollOffset int // lines scrolled up from the bottom; 0 = at bottom (latest)
}

// helpModal holds help reference card state.
type helpModal struct {
	active       bool
	scrollOffset int
}

// agentsModal holds state for the agent management modal.
type agentsModal struct {
	active    bool
	selected  int    // Grid: index into t.panes of the selected/grabbed pane.
	moving    bool   // True when a row is grabbed for reorder.
	error     string // Inline error message (e.g., "cannot hide last visible pane").
	searching bool   // True when / has been pressed and search is active.
	searchBuf []rune // Current search input.
	// preSearchSelected is the selection at the moment / was pressed,
	// restored on Esc (spec: "Esc restores the pre-search selection").
	// Enter deliberately does NOT restore it -- Enter keeps the selection
	// the search reached.
	preSearchSelected int

	// Group creation (ini-2rc): 'g' opens a name prompt in the search bar's
	// visual language. Mutually exclusive with searching.
	creatingGroup bool
	groupNameBuf  []rune

	// expanded lifts this window's display scope for the modal (ini-9isx):
	// the default view is this window's own agents, and 'a' reveals the whole
	// fleet arranged by window so agents can be moved between monitors. It is
	// VIEW state and nothing else -- moves still route through window-1
	// authority (la97), and it resets on close so a window never reopens in a
	// state the operator did not choose this time.
	expanded bool
}

// welcomeOverlay is shown once on first launch, then never again.
type welcomeOverlay struct {
	active    bool
	expiresAt time.Time
}

// quickGridModal holds state for the Option+G / Option+L quick-dimension
// popup (ini-dvy5): type two digits and the layout applies immediately, no
// Enter. Digits are columns then rows, matching :grid/:live's own CxR
// convention exactly -- there is one dimension order in the product, not two.
type quickGridModal struct {
	active bool
	live   bool // true for Option+L (apply via cmdLive); false for Option+G (cmdGrid).
	// firstDigit is the first digit typed (columns). 0 means not yet typed --
	// valid digits are 1-9, so 0 is unambiguous as "empty". The second digit
	// is consumed and submitted in the same keystroke it arrives in, so no
	// separate field is needed for it.
	firstDigit int
}

// mouseSelection holds mouse text selection state.
type mouseSelection struct {
	active       bool
	pane         int // Index of the pane being selected in.
	startX       int // Start position in pane-local content coordinates.
	startY       int
	endX         int // Current end position.
	endY         int
	startRow     int // contentOffset snapshot at mouse-down.
	renderOffset int

	// swallowed records that this gesture's press was NOT forwarded to the
	// child because the pane was unfocused at the time (ini-pzx0, focus-first).
	// It is the GESTURE's memory, and it has to be, because the alternative --
	// re-testing "is this pane focused" in the drag and release cases -- splits
	// the pair exactly when the rule fires: focus lands at press, so by release
	// the pane IS focused and the release would forward alone. A child that
	// validates press/release identity before completing a click then sees an
	// orphan release, which is the ini-82k mismatched-pair defect from the
	// other side. Decided once, at press; obeyed by every event after it.
	swallowed bool
}

// TUI is the main terminal multiplexer. It owns the tcell screen,
// a set of terminal panes, and handles input routing, layout, and rendering.
type TUI struct {
	screen      tcell.Screen
	panes       []PaneView
	layoutState LayoutState // Single source of truth for layout intent.
	plan        RenderPlan  // Current frame's render instructions.

	// ownershipSnap is the partition decision's inputs, frozen by the main
	// loop so the daemon's connection goroutines can answer a handshake
	// without waiting on it (ini-x5ob).
	// lastModalMaint rate-limits the modal guard's housekeeping (ini-9gvn).
	lastModalMaint time.Time

	ownershipMu   sync.Mutex
	ownershipSnap *ownershipInputs

	// layoutPresets holds the resolved Alt/Option 1–5 layout shortcuts,
	// parsed and default-filled from cfg.Project.LayoutPresets at startup.
	// Index 0 = Alt+1 ... index 4 = Alt+5. Zero value (all-grid) is replaced
	// by defaultLayoutPresets() for any TUI built without explicit resolution.
	layoutPresets [presetSlots]LayoutPreset

	// Tracked screen dimensions for detecting resize.
	lastW, lastH int

	// Project root for .initech/layout.yaml persistence. Empty disables auto-save.
	projectRoot string

	// assignment is the group-to-window store (ini-9ka.4), loaded once and
	// cached for the modal's tier rendering and the m/grab interactions.
	assignment *WindowAssignment

	// fleet is the session-GLOBAL hidden/protected/live-pinned store
	// (ini-9ka.10). Loaded once and cached. Only window 1 writes it;
	// secondary windows mutate via the set_fleet_state control command.
	fleet *FleetState

	// projectName is shown in the overlay title ("Agents (initech)").
	// Comes from initech.yaml's project field. Empty falls back to "Agents".
	projectName string

	// project holds the full config for cross-machine peer name lookup and
	// remote connection routing. Nil when no config is loaded (tests).
	project *config.Project

	// sockPath is the IPC socket this TUI is listening on. Used to inject
	// INITECH_SOCKET into hot-added panes.
	sockPath string

	// Multi-monitor render state (ini-9ka.6). windowID is this window's
	// identity in the assignment model -- WindowOne for the session owner,
	// "window-N" for a secondary window. These are zero for an ordinary
	// single-window session, which is what makes the render filter a no-op
	// there rather than a new code path. The assignment store itself is the
	// shared `assignment` field declared above (ini-9ka.4/.5) -- one store per
	// session, read by both the modal and the render filter.
	windowID string

	// paneOwnership is the ownership map: canonical agent key -> owning window
	// id (ini-x5ob). On window 1 it is the authority's own computation; on a
	// secondary it is what window 1 served, and it is the ONLY thing that
	// window consults to decide which panes it renders.
	paneOwnership map[string]string
	// ownershipServed records whether a secondary has ever been served an
	// ownership map. Before that it renders nothing rather than guessing.
	ownershipServed bool
	// lastServedTo is the attached-window set at the last ownership broadcast,
	// so a newly attached window is served even when the map itself did not
	// change (window 1 only).
	lastServedTo map[string]bool
	windowSrv    *windowServer
	liveness     *windowLivenessTracker

	// agentStatus is the last bead/description broadcast per agent, so window
	// 1 emits only on genuine change (ini-9ka.11). Descriptions are
	// recomputed every frame; without this diff the control stream would
	// carry a full status push at frame rate.
	agentStatus map[string]agentStatusSnapshot

	// paneConfigBuilder builds a PaneConfig for a new role at runtime.
	// Set from Config.PaneConfigBuilder. Nil disables the add command.
	paneConfigBuilder func(name string) (PaneConfig, error)

	cmd       cmdModal       // Command input bar.
	top       topModal       // Activity monitor overlay.
	eventLogM eventLogModal  // Event log history modal.
	help      helpModal      // Help reference card modal.
	agents    agentsModal    // Agent management modal.
	quickGrid quickGridModal // Quick grid/live dimension popup (Option+G/L).
	welcome   welcomeOverlay // First-launch keybinding hints.

	// attentionConsent is the one-time attention-hooks consent question for
	// EXISTING projects (ini-2x8.6). Window 1 only.
	attentionConsent attentionConsentModal

	// onAttentionConsent persists the recorded answer and, when granted, runs
	// the install. Injected by the cmd layer so this package stays free of
	// config-writing and agent-settings deps.
	onAttentionConsent func(granted bool)
	sel                mouseSelection // Mouse text selection.
	quitCh             chan struct{}  // Closed by IPC quit action to signal event loop exit.
	quitOnce           sync.Once      // Guards single close of quitCh; prevents concurrent-quit panics.

	// ipcCh is the dispatch channel for IPC goroutines that need to access
	// TUI state (t.panes, layoutState) safely from outside the main event loop.
	// Nil in test contexts that don't set up the channel (runOnMain falls back
	// to direct execution when nil).
	ipcCh chan ipcAction

	// mainGoroutineID identifies the goroutine running Run()'s own event
	// loop, captured once at construction (tui.go) before any other
	// goroutine starts. runOnMain compares against this to detect a
	// command-bar handler calling in from the main goroutine itself, rather
	// than an IPC or background goroutine, and skip the channel round trip
	// that would otherwise deadlock (ini-jesh). Zero value (unset) in test
	// contexts that never call Run() — the guard cannot fire for them.
	mainGoroutineID uint64

	// Build version for crash reports.
	version     string
	renderCount int // Frame counter for periodic render heartbeat logging.

	// Attention system (ini-2x8). chime is nil in tests that do not care about
	// sound; attentionChimes tolerates that. attentionSound is the normalised
	// config value ("bell" or "none"). chimeSeen tracks which waits have already
	// been announced, keyed by paneKey.
	chime          Chimer
	attentionSound string
	chimeSeen      map[string]chimeState

	// exitReason, when non-nil, is returned by Run after the screen is
	// restored -- an operator-facing explanation for a self-initiated exit
	// (ini-jhm6: an evicted window must SAY why it closed, in a sane shell,
	// not paint it over a dying TUI). Set on the main goroutine before
	// requesting quit.
	exitReason error

	// peerConnected is the last REPORTED connection state per peer, so notices
	// fire on the transition rather than on every reconnect attempt (ini-1ch).
	// The retry loop is by design; an unbounded stack of identical notices for
	// one underlying fact is not.
	peerConnected map[string]bool

	// lastRenderAt stores the UnixNano timestamp of the last completed render.
	// Updated atomically by render(), read by the watchdog goroutine.
	lastRenderAt atomic.Int64

	// Resource management gate. When false, all resource management
	// (memory monitor, auto-suspend policy) is dormant.
	autoSuspend       bool
	pressureThreshold int
	systemMemAvail    int64 // Available system RAM in KB, updated by memory monitor.
	systemMemTotal    int64 // Total system RAM in KB, queried once at startup.

	// Status bar tip cycling.
	tipIndex    int       // Current index into statusTips.
	tipRotateAt time.Time // When the next tip rotation should happen.

	// Battery monitoring for status bar. mu protects both fields against
	// concurrent reads (renderer paint, tests) and writes (background poller).
	mu              sync.Mutex
	batteryPercent  int  // 0-100, or -1 if no battery detected.
	batteryCharging bool // True when plugged in and charging.

	// batteryPollerDone is closed by startBatteryPoller once its background
	// goroutine has fully exited (or immediately if no goroutine was spawned
	// because the machine has no battery). Test-observability only: lets
	// tests wait for the poller to actually stop touching readBatteryFn/
	// newBatteryTicker before restoring those test stubs — closing quitCh
	// alone only requests a stop, it doesn't confirm one.
	batteryPollerDone chan struct{}

	// Paste buffering: accumulate characters between EventPaste start/end,
	// then flush as one atomic PTY write with bracketed paste markers.
	// Turns O(N) renders into O(1) for large pastes.
	pasting  bool   // True between EventPaste(start) and EventPaste(end).
	pasteBuf []byte // Accumulated paste characters.

	// Agent event system.
	agentEvents   chan AgentEvent // Buffered channel for semantic events from detection modules.
	notifications []notification  // Active notifications for rendering.
	eventLog      []AgentEvent    // Persistent log of all events (last 100 or last 60 min).

	// Overlay geometry cached by renderOverlay for mouse hit-testing.
	overlayBounds struct {
		x, y       int // Panel top-left corner.
		agentCount int // Number of agent rows rendered.
	}

	// Live Mode: persistent engine for anti-thrashing across render frames.
	// Nil when not in live mode. Created by cmdLive/Alt+5, destroyed on mode switch.
	liveEngine   *LiveEngine
	lastLiveTick time.Time // Throttles live-mode applyLayout to 1-second cadence.

	// Option+F focus split (ini-vtki). Non-nil while the split is active via
	// Option+F specifically; holds what to restore on toggle-off. See
	// focus_split.go.
	focusSplitPrev *focusSplitSnapshot
}

// logPanesMutation is temporary DEBUG logging. Logs every mutation of t.panes
// with a call-site tag, old count, new count, and names.
func (t *TUI) logPanesMutation(site string, oldLen int) {
	names := make([]string, len(t.panes))
	for i, p := range t.panes {
		names[i] = agentKey(p)
	}
	LogInfo("panes-mutation", site, "old", oldLen, "new", len(t.panes), "names", fmt.Sprintf("%v", names))
}

// applyLayout recomputes the render plan from the current layout state
// and resizes panes whose regions changed. The bottom row is reserved
// for the persistent status bar and excluded from pane layout.
func (t *TUI) applyLayout() {
	// PUBLISH BEFORE PLANNING (ini-x5ob). Window 1 recomputes ownership here,
	// on the path every layout change already runs through, and serves it when
	// it differs. Placing it at the one seam that ALWAYS runs -- rather than
	// at each of the several mutation sites -- is what makes a missed trigger
	// structurally impossible: any change that could alter ownership must
	// re-plan, and re-planning republishes. It is cheap: a map build and a
	// comparison, no I/O, and the broadcast fires only on difference.
	t.publishPaneOwnership()

	var w, h int
	if t.screen != nil {
		w, h = t.screen.Size()
	} else {
		w, h = 200, 60 // Fallback for tests without a screen.
	}
	// Reserve 2 rows below panes: spacer (h-2) + tip/command line (h-1).
	paneH := h - 2
	if paneH < 1 {
		paneH = 1
	}
	// Tick live engine before computing layout so LiveSlots are fresh.
	// Exclude manually hidden panes so the engine only scores and assigns
	// agents that the operator wants visible. Hidden means hidden.
	if t.layoutState.Mode == LayoutLive && t.liveEngine != nil {
		// The live rotation's universe is THIS WINDOW'S panes (assignment-
		// aware, ini-xq4r AC 3), minus hidden. Feeding t.panes rotated agents
		// that had moved to another window; worse, a PIN for a departed agent
		// held its slot forever -- a dangling reservation rotating nothing.
		// The pin itself is global and survives the move (it re-applies where
		// the agent now renders); only this window's slot releases, which is
		// why engine.Pinned is re-derived per tick as global-state ∩ this
		// window's pane set rather than mutated anywhere.
		livePanes, pinned := t.liveTickInputs()
		t.liveEngine.Pinned = pinned
		LogInfo("applyLayout", "live-tick-input", "total", len(t.panes), "visible", len(livePanes))
		prev := make([]string, len(t.liveEngine.Slots))
		copy(prev, t.liveEngine.Slots)
		if t.layoutState.LiveAuto {
			t.layoutState.LiveSlots = t.liveEngine.TickAuto(livePanes, time.Now())
		} else {
			t.layoutState.LiveSlots = t.liveEngine.Tick(livePanes, time.Now())
		}
		t.onLiveSwap(prev, t.liveEngine.Slots)
	}

	// Raise fold-back / restore notices before computing the plan, so the
	// notice and the pane movement it describes land in the same frame
	// (ini-9ka.6 wiring ini-9ka.7's transitions).
	t.noticeWindowTransitions()
	t.broadcastAgentStatusChanges()

	t.plan = computeLayout(t.layoutState, t.visiblePanesForWindow(), w, paneH)
	LogInfo("applyLayout", "layout applied", "panes", len(t.plan.Panes), "w", w, "h", paneH)

	// Cancel in-progress mouse selection only if the tracked pane's region
	// changed. Live mode ticks applyLayout every second; clearing selection
	// unconditionally makes click-drag copy impossible in live mode.
	if t.sel.active && t.sel.pane < len(t.panes) {
		pk := agentKey(t.panes[t.sel.pane])
		stillValid := false
		for _, pr := range t.plan.Panes {
			if agentKey(pr.Pane) == pk && pr.Region == t.panes[t.sel.pane].GetRegion() {
				stillValid = true
				break
			}
		}
		if !stillValid {
			t.sel.active = false
		}
	}

	// Write validated focus back to layoutState so it stays consistent.
	if t.plan.ValidatedFocus != "" {
		t.layoutState.Focused = t.plan.ValidatedFocus
	}

	// Resize panes whose regions changed (skip if no screen, e.g. in tests).
	if t.screen == nil {
		return
	}
	for i, pr := range t.plan.Panes {
		old := pr.Pane.GetRegion()
		if old != pr.Region {
			if lp, ok := pr.Pane.(*Pane); ok {
				lp.region = pr.Region
			} else if rp, ok := pr.Pane.(*RemotePane); ok {
				rp.region = pr.Region
			}
			oldCols, oldRows := old.TerminalSize()
			cols, rows := pr.Region.TerminalSize()
			LogInfo("applyLayout", "resizing pane", "idx", i, "name", pr.Pane.Name(),
				"oldRows", oldRows, "oldCols", oldCols, "newRows", rows, "newCols", cols)
			pr.Pane.Resize(rows, cols)
			LogInfo("applyLayout", "resize done", "idx", i, "name", pr.Pane.Name())
		}
	}
	LogInfo("applyLayout", "all resizes complete")
}

// initLiveEngine creates a persistent LiveEngine for live mode.
// When numSlots is 0, the slot count is derived from the visible pane
// count via autoGrid so the live grid is square-ish for the agents
// actually on screen. A non-zero numSlots overrides (for explicit
// `:live CxR` dimensions). In LiveAuto mode, starts with zero slots
// since TickAuto manages the slot list dynamically.
func (t *TUI) initLiveEngine(numSlots int) {
	var roles []string
	if t.project != nil {
		roles = t.project.Roles
	}
	if t.layoutState.LiveAuto {
		// Auto mode: start with zero slots; TickAuto manages the slot list dynamically.
		t.liveEngine = NewLiveEngine(0, t.layoutState.LivePinned, roles)
		return
	}
	if numSlots < 1 {
		if t.layoutState.GridExplicit && t.layoutState.GridCols > 0 && t.layoutState.GridRows > 0 {
			numSlots = t.layoutState.GridCols * t.layoutState.GridRows
		} else {
			visCount := t.visibleCountFromState()
			if visCount < 1 {
				visCount = len(t.panes)
			}
			cols, rows := autoGrid(visCount)
			numSlots = cols * rows
		}
	}
	t.liveEngine = NewLiveEngine(numSlots, t.layoutState.LivePinned, roles)
}

// onLiveSwap compares previous and current slot assignments. If any slot
// changed to a different agent, emits an EventLiveSwap. The event flows
// through the standard fan-out (event log) but is suppressed from toasts
// (too frequent).
func (t *TUI) onLiveSwap(prev, curr []string) {
	var swapped string
	var prevAgent string
	var slotIdx int
	for i := 0; i < len(curr) && i < len(prev); i++ {
		if prev[i] != curr[i] && curr[i] != "" && prev[i] != "" {
			swapped = curr[i]
			prevAgent = prev[i]
			slotIdx = i
			break
		}
	}
	if swapped == "" {
		return
	}

	t.handleAgentEvent(AgentEvent{
		Type:   EventLiveSwap,
		Pane:   swapped,
		Detail: fmt.Sprintf("%s swapped into slot %d (was %s)", swapped, slotIdx, prevAgent),
	})
}

// saveLayoutIfConfigured persists the current layout to disk.
// No-op if projectRoot is empty.
func (t *TUI) saveLayoutIfConfigured() {
	if t.projectRoot == "" {
		return
	}
	// THE GUARD IN THE PRIMITIVE (ini-la97). This is the only production
	// writer of layout.yaml, so gating here means a viewer process carries no
	// write path to project-root layout state at all -- docs/systemdesign.md's
	// single-writer architecture face, and the reason ini-i7fr's cascade
	// started here (a viewer rewriting window 1's file in the viewer's own key
	// space through this very call).
	//
	// A viewer losing its ARRANGEMENT here is intended, not collateral:
	// arrangement is viewer-local and session-scoped, and starts fresh on
	// reattach (pm ruling 2026-08-14; retirement tracked in ini-qodm). The
	// FLEET-SCOPED half of this file -- groups and group_of -- does not just
	// get dropped for a viewer: it routes to window 1 instead, via
	// agentsPersistGrouping.
	if !t.isFleetAuthority() {
		return
	}
	// Snapshot current pane order into layoutState before persisting.
	t.layoutState.Order = make([]string, len(t.panes))
	for i, p := range t.panes {
		t.layoutState.Order[i] = agentKey(p)
	}
	if err := SaveLayout(t.projectRoot, t.layoutState); err != nil {
		LogWarn("layout", "save failed", "err", err)
	}
}

// focusedPane returns the currently focused pane, or nil.
// Uses paneKey (host:name for remote, name for local) to avoid collisions
// when a local and remote pane share the same agent name.
func (t *TUI) focusedPane() PaneView {
	key := t.layoutState.Focused
	for _, p := range t.panes {
		if agentKey(p) == key {
			return p
		}
	}
	return nil
}

// drainRemotePanes calls DrainData on every RemotePane, including hidden ones.
// This prevents network data from accumulating in dataCh when a remote pane is
// not visible in the layout (hidden panes skip Render entirely).
func (t *TUI) drainRemotePanes() {
	for _, p := range t.panes {
		if rp, ok := p.(*RemotePane); ok {
			rp.DrainData()
		}
	}
}

// Config controls what agents the TUI launches.
type Config struct {
	Agents             []PaneConfig                          // One entry per agent pane.
	ProjectName        string                                // Used for socket path.
	ProjectRoot        string                                // Project root for .initech/ layout persistence.
	ResetLayout        bool                                  // Ignore saved layout and start with defaults.
	Verbose            bool                                  // Enable DEBUG-level logging (default: INFO).
	Version            string                                // Build version for crash reports.
	AutoSuspend        bool                                  // Enable resource-aware auto-suspend/resume.
	PressureThreshold  int                                   // RSS percentage threshold (0 uses default 85).
	PaneConfigBuilder  func(name string) (PaneConfig, error) // Optional factory for hot-add. Nil disables add command.
	Project            *config.Project                       // Full project config. Used for remote peer connections.
	OnAttentionConsent func(granted bool)                    // Persists the one-time consent answer (ini-2x8.6). Nil disables the modal's write-back.
}

// DefaultConfig returns a config with standard shell-only agents.
func DefaultConfig() Config {
	names := []string{"super", "eng1", "eng2", "qa1"}
	agents := make([]PaneConfig, len(names))
	for i, n := range names {
		agents[i] = PaneConfig{Name: n}
	}
	return Config{Agents: agents}
}

// Run starts the TUI event loop. Blocks until the user quits.
func Run(cfg Config) error {
	// Redirect stderr to .initech/stderr.log BEFORE screen.Init() puts the
	// terminal in raw mode. This captures cgo/native crash stack traces that
	// would otherwise be lost in the garbled terminal buffer.
	stderrCleanup := redirectStderr(cfg.ProjectRoot)
	defer stderrCleanup()

	// A secondary window is a VIEWER: it renders another window's agents and
	// owns none of the project-root state that identifies a running session.
	// The socket, the PID file and the signal-time cleanup of both are keyed on
	// the PROJECT ROOT, not on the window, so every one of them is a way for a
	// viewer to damage window 1's session from the same directory (ini-civ).
	// Computed once, here, so the ownership question is asked in one place
	// rather than rediscovered at each door.
	viewer := isViewerSession(cfg)

	screen, err := tcell.NewScreen()
	if err != nil {
		return fmt.Errorf("create screen: %w", err)
	}
	if err := screen.Init(); err != nil {
		return fmt.Errorf("init screen: %w", err)
	}
	screen.SetTitle(fmt.Sprintf("initech - %s", cfg.ProjectName))
	screen.EnableMouse()
	screen.EnablePaste()
	// Suppress the OS hardware caret; initech draws its own focus-indicator
	// cell. On Windows the OS cursor tracks every SetContent and visibly
	// hops; on Unix it's a no-op (the cursor was already idle). State is
	// restored by screen.Fini() on clean exit and panic recovery.
	screen.HideCursor()
	defer screen.Fini()

	// Initialize structured logging before anything else.
	logLevel := slog.LevelInfo
	if cfg.Verbose {
		logLevel = slog.LevelDebug
	}
	logCleanup := InitLogger(cfg.ProjectRoot, logLevel)
	defer logCleanup()

	// Check for unclean exit from a previous run before logging the start
	// message, so the warning appears immediately before the new-session header.
	// Window 1 only: "did the previous session crash?" is the session owner's
	// question, and checkPreviousCrash ANSWERS it by os.Remove()ing a PID file
	// it judges stale. A viewer has no use for the answer and no business
	// making that judgement about a file it does not own -- the fourth door on
	// this bead, and the reason round 2 gates every project-root MUTATION in
	// one place rather than fixing them as they are found (ini-civ).
	if !viewer {
		checkPreviousCrash(cfg.ProjectRoot)
	}

	LogInfo("tui", "starting", "version", cfg.Version, "pid", os.Getpid(), "agents", len(cfg.Agents), "verbose", cfg.Verbose)

	// Write PID file. The deferred remove fires only on clean exits; an absent
	// cleanup at startup means the previous run exited uncleanly.
	// A viewer must not write the PID file: the path is project-scoped, so it
	// would overwrite window 1's PID with its own for as long as it runs, and
	// its clean-exit cleanup would then DELETE the file outright -- leaving a
	// live window 1 with no PID file at all, which is what `initech status` and
	// `initech down` read to find the session (ini-civ round 2).
	pidCleanup := func() {}
	if !viewer {
		pidCleanup = writePIDFile(cfg.ProjectRoot)
	}
	defer pidCleanup()

	// Deferred exit log: fires on any return from Run() that is not os.Exit().
	// Covers normal quit and error returns. Signals and panics log themselves.
	defer LogInfo("tui", "exiting", "pid", os.Getpid())

	// Panic recovery: restore terminal and write crash log before exiting.
	defer func() {
		if r := recover(); r != nil {
			LogError("tui", "panic", "value", fmt.Sprint(r))
			screen.Fini() // Restore terminal first (idempotent).
			report := crashLog(cfg.ProjectRoot, cfg.Version, r)
			fmt.Fprint(os.Stderr, report)
			os.Exit(1)
		}
	}()

	// Install OS signal handlers. Must happen after screen.Init() so we have
	// a valid screen to Fini() on signal receipt. Pass socket and PID file
	// paths so the handler can remove them before os.Exit (defers don't run
	// on os.Exit, leaving stale files that block restart — ini-db1).
	quitCh := make(chan struct{})
	sp := SocketPath(cfg.ProjectRoot, cfg.ProjectName)
	pidPath := filepath.Join(cfg.ProjectRoot, ".initech", pidFileName)
	// A viewer passes NO cleanup paths. The handler os.Remove()s whatever it is
	// given before os.Exit, and SIGHUP is the NATURAL viewer exit -- closing the
	// terminal window. Handing it window 1's socket and PID file meant the
	// ordinary way to close a second monitor deleted the live session's IPC
	// socket: window 1 kept rendering while `initech status` reported nothing
	// running and fleet messaging was dead until restart (ini-civ round 2,
	// measured by qa1 on a real two-window rig).
	cleanupPaths := []string{sp, pidPath}
	if viewer {
		cleanupPaths = nil
	}
	sigCleanup := installSignalHandlers(screen, quitCh, cleanupPaths...)
	defer sigCleanup()

	// Build layout state from config.
	agentNames := make([]string, len(cfg.Agents))
	for i, a := range cfg.Agents {
		agentNames[i] = a.Name
	}

	// Delete saved layout when --reset-layout is requested.
	if cfg.ResetLayout && cfg.ProjectRoot != "" {
		DeleteLayout(cfg.ProjectRoot)
	}

	// Restore saved layout if available, otherwise use defaults.
	var layoutState LayoutState
	firstLaunch := false
	if !cfg.ResetLayout && cfg.ProjectRoot != "" {
		if saved, ok := LoadLayout(cfg.ProjectRoot, agentNames); ok {
			layoutState = saved
		} else {
			layoutState = DefaultLayoutState(agentNames)
			firstLaunch = true
		}
	} else {
		layoutState = DefaultLayoutState(agentNames)
	}

	// Resolve Alt+1–7 layout presets from config (default-filled, never fails).
	var rawPresets map[string]string
	if cfg.Project != nil {
		rawPresets = cfg.Project.LayoutPresets
	}
	layoutPresets, presetWarnings := ResolvePresets(rawPresets)
	for _, w := range presetWarnings {
		LogWarn("layout-presets", w)
	}

	// Resolve the running-pane tint color from config (ini-eo2d). Set once here
	// before the render loop starts; backgroundTint reads it on the main render
	// goroutine. Invalid values fall back to the default with a warning.
	var rawTint string
	if cfg.Project != nil {
		rawTint = cfg.Project.RunningPaneTint
	}
	resolvedTint, tintWarn := resolveRunningTintColor(rawTint)
	runningTintColor = resolvedTint
	if tintWarn != "" {
		LogWarn("running-tint", tintWarn)
	}

	initW, initH := screen.Size()
	t := &TUI{
		screen:            screen,
		layoutState:       layoutState,
		layoutPresets:     layoutPresets,
		lastW:             initW,
		lastH:             initH,
		projectRoot:       cfg.ProjectRoot,
		projectName:       cfg.ProjectName,
		project:           cfg.Project,
		version:           cfg.Version,
		sockPath:          sp,
		paneConfigBuilder: cfg.PaneConfigBuilder,
		autoSuspend:       cfg.AutoSuspend,
		pressureThreshold: cfg.PressureThreshold,
		tipRotateAt:       time.Now().Add(tipRotationInterval),
		batteryPercent:    -1,
		quitCh:            quitCh,
		ipcCh:             make(chan ipcAction, 32),
		agentEvents:       make(chan AgentEvent, 64),
		mainGoroutineID:   currentGoroutineID(),
	}

	// Load session-global fleet state and project it onto layoutState
	// (ini-9ka.10). Must happen before the first applyLayout: Hidden feeds
	// visibility, Protected feeds auto-suspend policy, and LivePinned feeds
	// the live engine, so a frame rendered before this would briefly show
	// hidden agents. On a fresh upgrade this is also where the one-time
	// import from a legacy layout.yaml happens.
	t.fleetState()

	// Re-size the grid now that hidden panes are known. LoadLayout used to do
	// this itself, because Hidden lived in the layout file; it no longer does
	// (ini-9ka.10), so the auto-recalc has to happen HERE, after the global
	// projection lands. Without it a session that starts with agents hidden
	// would size its grid for the full fleet and render empty cells.
	// GridExplicit is respected inside recalcGrid, so an operator's chosen
	// CxR is still not overridden.
	t.recalcGrid(false)

	// One-time attention-hooks consent for an EXISTING project (ini-2x8.6).
	// Window 1 only; a project that already recorded an answer sees nothing,
	// which is what keeps this bead's census claim true.
	t.onAttentionConsent = cfg.OnAttentionConsent
	t.maybeStartAttentionConsent()

	// Show welcome overlay on first launch (no saved layout).
	if firstLaunch {
		t.welcome = welcomeOverlay{active: true, expiresAt: time.Now().Add(10 * time.Second)}
	}

	// Start IPC socket server for inter-agent messaging -- WINDOW 1 ONLY.
	//
	// A secondary window hosts zero local agents. The IPC socket exists so an
	// agent's own CLI (initech send/peek/bead) can reach the TUI that owns its
	// PTY, and every one of those agents lives in window 1. There is nothing in
	// a viewer for it to serve (ini-civ).
	//
	// Binding it here was fatal, not merely wasteful: the socket path is a
	// property of the PROJECT ROOT, not of the window, so a viewer started from
	// the same root collided with window 1's live socket and the single-instance
	// guard refused to start -- telling the operator to run 'initech down',
	// i.e. to kill the very fleet he was trying to get a second monitor onto.
	//
	// Skipping the CLEANUP matters as much as skipping the bind: ipcCleanup
	// unlinks the socket file, and a viewer must never unlink window 1's socket
	// on its way out. listenIPC also os.Remove()s a socket it finds
	// undialable, so a viewer that ran this path during a momentary stall in
	// window 1 would have deleted a socket that was about to work again.
	//
	// viewerProject already clears WindowListen with the comment "a viewer
	// serves nothing"; this is the same rule applied to the listener it missed.
	// sockPath stays defined for both windows: it is also what INITECH_SOCKET is
	// built from below. A viewer hosts no agents, so that loop is empty there,
	// but keeping the value identical means window 1's behaviour is untouched.
	sockPath := sp
	if !viewer {
		ipcCleanup, err := t.startIPC(sockPath)
		if err != nil {
			LogError("ipc", "socket bind failed", "path", sockPath, "err", err)
			return fmt.Errorf("start IPC: %w", err)
		}
		LogInfo("ipc", "listening", "path", sockPath)
		defer ipcCleanup()
	} else {
		LogInfo("ipc", "secondary window: not serving project IPC")
	}

	// Compute initial regions for pane creation. Reserve 2 rows below panes
	// (spacer + tip line), matching what applyLayout will compute.
	paneInitH := initH - 2
	if paneInitH < 1 {
		paneInitH = 1
	}
	ls := t.layoutState
	regions := gridRegions(ls.GridCols, ls.GridRows, len(cfg.Agents),
		initW, paneInitH, ls.ColWeights, ls.RowWeights)

	// Inject the socket path and agent name into every agent's environment.
	for i := range cfg.Agents {
		cfg.Agents[i].Env = append(cfg.Agents[i].Env,
			"INITECH_SOCKET="+sockPath,
			"INITECH_AGENT="+cfg.Agents[i].Name,
		)
	}

	// Compute idle-with-bead threshold from config (default 60s).
	beadThreshold := defaultIdleWithBeadThreshold
	if cfg.Project != nil {
		sec := cfg.Project.GetIdleWithBeadThreshold()
		beadThreshold = time.Duration(sec) * time.Second
	}

	// Attention chime (ini-2x8.3). Default-on audible surface: absent config
	// means "bell", and only an explicit attention.sound: none silences it.
	t.attentionSound = "bell"
	if cfg.Project != nil {
		t.attentionSound = cfg.Project.Attention.AttentionSound()
	}
	if t.peerConnected == nil {
		t.peerConnected = make(map[string]bool)
	}
	if t.chimeSeen == nil {
		t.chimeSeen = make(map[string]chimeState)
	}
	if t.chime == nil && t.screen != nil {
		t.chime = screenChimer{screen: t.screen}
	}

	// Create panes. Launch cost must track ACTIVE agents, not fleet size
	// (hover, 2026-08-15: 39 concurrent `claude --continue` boots in one 90ms
	// burst — +17GB RSS, load 3→216 for ninety seconds). Two rules, one
	// primitive: agents whose suspension was persisted cold-park (pane, no
	// process) and stay parked; everyone past the first batch cold-parks and
	// is booted by the stagger walker below. All wakes go through resumePane,
	// so a message to a not-yet-booted agent wakes it immediately — the
	// stagger delays idle boots, never work.
	suspendedAtExit := t.fleetState().SuspendedMap()
	names := make([]string, len(cfg.Agents))
	for i := range cfg.Agents {
		names[i] = cfg.Agents[i].Name
	}
	plan := startupSpawnPlan(names, suspendedAtExit, startupLiveBatch)
	var staggered []*Pane
	for i, acfg := range cfg.Agents {
		r := regions[i%len(regions)]
		cols, rows := r.TerminalSize()
		var p *Pane
		if plan[i] == spawnLive {
			live, err := NewPane(acfg, rows, cols)
			if err != nil {
				LogError("pane", "launch failed", "name", acfg.Name, "err", err)
				for _, existing := range t.panes {
					existing.Close()
				}
				return fmt.Errorf("create pane %q: %w", acfg.Name, err)
			}
			p = live
		} else {
			p = NewParkedPane(acfg, rows, cols)
		}
		p.region = r
		p.eventCh = t.agentEvents
		t.wireSuspendResume(p)
		p.safeGo = t.safeGo
		p.idleWithBeadThreshold = beadThreshold
		if plan[i] == spawnLive {
			p.Start()
		} else if plan[i] == spawnStaggered {
			staggered = append(staggered, p)
		}
		old := len(t.panes)
		t.panes = append(t.panes, p)
		t.logPanesMutation("create-pane", old)
		LogDebug("pane", "created", "name", acfg.Name, "dir", acfg.Dir, "mode", plan[i])
	}
	liveN, parkedN := 0, 0
	for _, m := range plan {
		switch m {
		case spawnLive:
			liveN++
		case spawnParked:
			parkedN++
		}
	}
	if parkedN > 0 || len(staggered) > 0 {
		LogInfo("pane", "launch plan",
			"live", liveN, "staggered", len(staggered), "parked", parkedN)
	}
	if len(staggered) > 0 {
		t.safeGo(func() { t.staggerStartPanes(staggered) })
	}

	// Multi-monitor: serve the pane-stream protocol in-process so secondary
	// windows can attach (ini-9ka.2). Started here, after the panes exist,
	// because attaching windows stream those panes.
	//
	// Gated on WindowListen being configured. A single-window fleet leaves it
	// empty and this block is skipped entirely -- no listener, no goroutine,
	// no artifact, no output -- so single-window sessions run today's code
	// path rather than a new one that merely behaves the same.
	if cfg.Project != nil && cfg.Project.WindowListen != "" {
		ws, wsCleanup, err := startWindowServer(cfg.Project, cfg.Version, localPanes(t.panes), t.safeGo, t.applyFleetStateCmd, t.applyGroupWindowCmd, t.applyGroupOfCmd, t.currentPaneOwnership,
			// Republish on window 1's OWN loop once the newcomer is
			// registered, so it is served the partition that includes it
			// (ini-x5ob). safeGo first: runOnMain waits for the main loop,
			// and this is called from a connection goroutine.
			func(peer string) {
				t.safeGo(func() {
					t.runOnMain(func() {
						// Publish THEN re-plan. Window 1 is a renderer as
						// well as the authority: deciding that a group is no
						// longer its own does not remove the panes from its
						// own plan, and skipping the re-plan leaves exactly
						// the duplicate render this bead exists to kill --
						// both windows drawing the same agents.
						//
						// applyLayout republishes at its top; that call finds
						// the map unchanged and returns, so this does not
						// recurse.
						t.publishPaneOwnership()
						t.applyLayout()
						// Forget the last-broadcast agent_status snapshots so
						// the next render re-broadcasts every agent's state.
						// The diff-based broadcast only fires on CHANGE, and a
						// suspended agent changes nothing ever again — a
						// window attaching after the park would otherwise
						// never learn it (the same blindness the operator hit
						// live between two already-open windows, one layer
						// later).
						t.agentStatus = nil
					})
				})
			})
		if err != nil {
			// Non-fatal: a secondary window is an enhancement, and failing to
			// bind it must not take down a session whose agents are already
			// running. That decision stands.
			//
			// WHAT CHANGED (ini-ikz3) IS PROMINENCE, NOT FATALITY. This was a
			// log line, and a log line is invisible to an operator looking at a
			// TUI. When hover's bind lost :9300 to initech, hover's session
			// looked completely healthy -- and hover's own window 2 then
			// attached to INITECH and rendered its agents. The operator had no
			// way to know the bind had failed, so the only visible symptom was
			// the wrong fleet on screen.
			//
			// A failure whose only symptom appears somewhere else, later,
			// belongs on screen at the moment it happens. The port is named
			// because "already in use" is the overwhelmingly likely cause and
			// the fix is choosing another one.
			LogError("window-server", "failed to start; secondary windows cannot attach",
				"addr", cfg.Project.WindowListen, "err", err)
			EmitEvent(t.agentEvents, AgentEvent{
				Type: EventAssignmentWriteRefused,
				Detail: fmt.Sprintf(
					"window server could NOT bind %s (%v) — secondary windows cannot attach to "+
						"this project. If another project is listening there, give this one a "+
						"different window_listen port.",
					cfg.Project.WindowListen, err),
				Time: time.Now(),
			})
		} else {
			defer wsCleanup()
			t.windowSrv = ws
		}
	}

	// Multi-monitor render state (ini-9ka.6). Loaded only when this session
	// participates in multi-window -- window 1 because it serves, a secondary
	// window because it was launched with --window N and so has a peer_name.
	// Left nil otherwise, which is what makes visiblePanesForWindow a no-op
	// for ordinary single-window sessions.
	if cfg.Project != nil && (cfg.Project.WindowListen != "" || isSecondaryWindowIdentity(cfg.Project.PeerName)) {
		if a, err := LoadAssignment(cfg.ProjectRoot, t.windowID); err != nil {
			// A corrupt store must not take down the session: fall back to
			// single-window rendering (window 1 shows everything) rather than
			// refusing to start with agents already running.
			LogError("assignment", "load failed; rendering all panes in this window", "err", err)
		} else {
			t.assignment = a
			t.windowID = WindowOne
			if isSecondaryWindowIdentity(cfg.Project.PeerName) {
				t.windowID = cfg.Project.PeerName
			}
			// The authority persists any legacy-identity heal ONCE
			// (ini-m495); a viewer's load stays read-only per civ.
			if t.windowID == WindowOne {
				a.PersistHealIfNeeded()
			}
			t.liveness = newWindowLivenessTracker()
			LogInfo("window", "multi-monitor rendering active", "window_id", t.windowID)
		}
	}

	// Connect to remote peers asynchronously. The peerManager handles both
	// initial connection and reconnection in background goroutines. The TUI
	// renders immediately with local-only panes; remote panes appear once
	// connected via handlePeerUpdate on the main goroutine.
	if cfg.Project != nil && len(cfg.Project.Remotes) > 0 {
		pm := newPeerManager(cfg.Project, func(peerName string, panes []PaneView, connected bool) {
			t.runOnMain(func() {
				t.handlePeerUpdate(peerName, panes, connected)
			})
		}, t.deliverForwardedSend, t.quitCh)
		// Session notices broadcast by window 1 must render here too
		// (ini-9ka.8): they describe the session's shape changing, not one
		// agent's activity.
		pm.SetOnAgentStatus(func(name string, beads []string, primary, desc string, ws WaitingState, suspended bool) {
			if len(beads) == 0 && primary != "" {
				beads = []string{primary} // Peer predates the plural field.
			}
			t.runOnMain(func() { t.applyAgentStatus(name, beads, desc, ws, suspended) })
		})
		pm.SetOnSessionNotice(func(text string) {
			t.runOnMain(func() { t.surfaceSessionNotice(text) })
		})
		// Ownership is served, never derived (ini-x5ob). Marshalled onto the
		// main loop for the same reason every other peer callback is: it
		// mutates render state.
		pm.SetOnPaneOwnership(func(owner map[string]string) {
			t.runOnMain(func() { t.applyServedPaneOwnership(owner) })
		})
		// Terminal eviction (ini-jhm6): another process took this window's
		// identity. Surface the reason as Run's return value -- printed after
		// screen.Fini restores the terminal -- and quit. Deliberately NO
		// project-root writes on this path (the civ rule): the winner owns
		// the session state now.
		pm.SetOnEvicted(func(peerName, reason string) {
			t.runOnMain(func() {
				// Name the TAKEN IDENTITY (this window's own, e.g. window-2),
				// not the server label the manager dialed (qa1's wording
				// FAIL: "another window1 took over" told the operator the
				// wrong window changed hands).
				t.exitReason = fmt.Errorf(
					"another %s took over this identity — this window is closing; rerun initech --window with the same number here to take it back (%s)",
					t.windowID, reason)
			})
			t.requestQuit()
		})
		// Stream-on-create: when a daemon announces a new agent stream
		// (configure_agent → stream_added), append the new RemotePane to
		// the live grid via runOnMain so it shows up in the next render.
		pm.SetOnPaneAdded(func(peerName string, pane PaneView) {
			t.runOnMain(func() {
				t.handlePeerPaneAdded(peerName, pane)
			})
		})
		// Removal twin: a pruned agent's pane leaves the grid live.
		pm.SetOnPaneRemoved(func(peerName, agentName string) {
			t.runOnMain(func() {
				for i, p := range t.panes {
					rp, ok := p.(*RemotePane)
					if ok && rp.Host() == peerName && rp.Name() == agentName {
						t.panes = append(t.panes[:i], t.panes[i+1:]...)
						t.logPanesMutation("peer-pane-removed", len(t.panes)+1)
						break
					}
				}
				t.recalcGrid(false)
				t.applyLayout()
			})
		})
		defer func() {
			done := make(chan struct{})
			go func() {
				pm.wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				LogWarn("tui", "peerManager wait timed out after 3s, forcing exit",
					"hostages", strings.Join(pm.activeLabels(), ", "))
			}
		}()
	}

	// Sync pinned state from layout to panes.
	for _, p := range t.panes {
		if t.layoutState.Protected[agentKey(p)] {
			if lp, ok := p.(*Pane); ok {
				lp.SetProtected(true)
			}
		}
	}

	stampFleetThenApplyOrder(t.panes, t.layoutState.Order)

	// Initialize live engine if the restored layout is in live mode.
	// Without this, liveEngine is nil and applyLayout falls through to
	// computeLayout's stateless fallback which only sees visible panes.
	if t.layoutState.Mode == LayoutLive {
		t.fleetState() // Populate the global projection.
		t.initLiveEngine(0)
	}

	// Now that panes exist, compute the full render plan.
	t.applyLayout()
	defer func() {
		// Close all panes with a hard deadline. RemotePane.Close has its own
		// 2s timeout per pane, but we cap the entire cleanup to 3s in case
		// many panes are stuck on dead yamux sessions simultaneously.
		done := make(chan struct{})
		go func() {
			// PARALLEL closes (ini-ap3i): with the busy-only SIGTERM grace in
			// Pane.Close, serial closes would pay one grace PER busy agent;
			// parallel closes pay max one grace total.
			var cwg sync.WaitGroup
			for _, p := range t.panes {
				cwg.Add(1)
				go func(pv PaneView) {
					defer cwg.Done()
					pv.Close()
				}(p)
			}
			cwg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			LogWarn("tui", "pane cleanup timed out after 4s, forcing exit")
		}
	}()

	// Guard the hallway, not the doors: whatever path exits the event loop
	// (IPC quit, keyboard :quit, eviction, a future return, a panic), quitCh
	// must be closed before the cleanup defers above start waiting — they
	// wait on goroutines whose only exit signal is that channel. Defers run
	// LIFO, so registering this AFTER them makes it run FIRST at unwind.
	// requestQuit is idempotent (quitOnce), so paths that already closed it
	// are unaffected.
	defer t.requestQuit()

	// Start memory monitor when auto-suspend is enabled.
	if t.autoSuspend {
		t.startMemoryMonitor()
	}

	// Start battery polling for status bar display.
	t.startBatteryPoller()

	// Poll tcell events in a goroutine.
	eventCh := make(chan tcell.Event, 64)
	t.safeGo(func() {
		for {
			ev := screen.PollEvent()
			if ev == nil {
				return
			}
			eventCh <- ev
		}
	})

	// Start render watchdog: if no render completes within 10s, dump all
	// goroutine stacks to crash.log for post-mortem analysis of silent freezes.
	go renderWatchdog(&t.lastRenderAt, 10*time.Second, t.projectRoot, t.version, t.quitCh)

	// Render at ~30 fps.
	ticker := time.NewTicker(33 * time.Millisecond)
	defer ticker.Stop()

	LogInfo("main-loop", "entering event loop", "ipcCh_cap", cap(t.ipcCh), "panes", len(t.panes))

	for {
		select {
		case ev := <-eventCh:
			if t.handleEvent(ev) {
				return nil
			}
		case ae := <-t.agentEvents:
			t.handleAgentEvent(ae)
		case op := <-t.ipcCh:
			LogInfo("main-loop", "processing ipcCh op")
			op.fn()
			LogInfo("main-loop", "op.fn returned, closing done channel")
			close(op.done)
			LogInfo("main-loop", "done channel closed, about to render")
		case <-ticker.C:
			// Periodic housekeeping (runs even if no events arrive).
			t.pruneNotifications()
			t.pruneConfirmation()
			t.pruneError()
			if t.welcome.active && time.Now().After(t.welcome.expiresAt) {
				t.welcome.active = false
			}
			t.rotateTip()
			if t.layoutState.Mode == LayoutLive && time.Since(t.lastLiveTick) >= time.Second {
				t.lastLiveTick = time.Now()
				t.applyLayout()
			}
		case <-t.quitCh:
			return t.exitReason
		}
		// Drain all remote panes (visible or hidden) so network data doesn't
		// accumulate in dataCh when a pane is hidden from the layout.
		t.drainRemotePanes()
		// Skip rendering while accumulating paste characters. The paste
		// flush in handlePaste triggers a single render when complete.
		// This turns O(N) renders into O(1) for large pastes.
		if !t.pasting {
			// Render after every select case, not just ticker.C. If another
			// channel (eventCh, agentEvents, ipcCh) is always ready, the
			// ticker.C case is starved by Go's random select and the screen
			// never updates. Rendering unconditionally guarantees the screen
			// reflects state changes within one event-loop cycle.
			t.render()
		}
	}
}

func (t *TUI) handleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventKey:
		// While buffering a paste, accumulate characters instead of
		// forwarding them per-key. This avoids O(N) renders.
		if t.pasting {
			t.bufferPasteKey(ev)
			return false
		}
		return t.handleKey(ev)
	case *tcell.EventMouse:
		t.handleMouse(ev)
	case *tcell.EventResize:
		t.handleResize()
	case *tcell.EventPaste:
		t.handlePaste(ev.Start())
	}
	return false
}

// autoGrid picks grid dimensions that minimize waste for n panes.
func autoGrid(n int) (cols, rows int) {
	switch {
	case n <= 1:
		return 1, 1
	case n <= 2:
		return 2, 1
	case n <= 4:
		return 2, 2
	case n <= 6:
		return 3, 2
	case n <= 8:
		return 4, 2
	case n <= 9:
		return 3, 3
	case n <= 12:
		return 4, 3
	default:
		cols = 4
		rows = (n + cols - 1) / cols
		return
	}
}

// calcPaneGrid generates exactly numPanes regions arranged in a grid.

// recalcGrid recomputes GridCols/GridRows from the current visible pane
// count and applies the layout. When force is true, the mode is switched to
// LayoutGrid (used after add/remove/remote-connect) unless the current mode
// is LayoutLive (live mode manages its own slot assignments via LiveEngine).
// When force is false, grid dimensions are only updated if the mode is
// already LayoutGrid or LayoutLive (used after visibility toggles that
// shouldn't force a mode change).
//
// When GridExplicit is true the user chose dimensions via :grid CxR or
// Alt+2/Alt+3. In that case we skip the auto-recalculation so peer updates
// and hot-adds don't overwrite the user's choice.
// deliverForwardedSend delivers a message forwarded from another window or
// machine to a local pane. Extracted from the peer-manager closure so the
// entry point is testable BY NAME (ini-g7fl, the i7fr lesson: a guard proven
// at one site says nothing about a site that does not route through it --
// this was one of the three sites that bypassed the suspension guard and
// silently lost window-2-originated sends). The suspension safety itself
// lives in SendText, which this inherits like every other caller.
func (t *TUI) deliverForwardedSend(target, text string, enter bool) error {
	var pv PaneView
	t.runOnMain(func() { pv = t.findPaneByName(target) })
	if pv == nil {
		return fmt.Errorf("agent %q not found", target)
	}
	pv.SendText(text, enter)
	return nil
}

// requestQuit closes quitCh exactly once, from any goroutine.
func (t *TUI) requestQuit() {
	t.quitOnce.Do(func() { close(t.quitCh) })
}

func (t *TUI) recalcGrid(force bool) {
	if force && t.layoutState.Mode != LayoutLive {
		t.layoutState.Mode = LayoutGrid
	} else if t.layoutState.Mode != LayoutGrid && t.layoutState.Mode != LayoutLive {
		t.applyLayout()
		return
	}
	if !t.layoutState.GridExplicit {
		vis := t.visibleCountFromState()
		if vis > 0 {
			cols, rows := autoGrid(vis)
			t.layoutState.GridCols = cols
			t.layoutState.GridRows = rows
		}
	}
	t.applyLayout()
}

// handlePeerUpdate is called by the peer manager (via runOnMain) when a
// remote peer connects, reconnects, or goes offline. It swaps the old
// RemotePanes for the peer with new ones (or removes them on disconnect).
func (t *TUI) handlePeerUpdate(peerName string, newPanes []PaneView, connected bool) {
	LogInfo("peer-update", "start", "peer", peerName, "new_panes", len(newPanes),
		"current_panes", len(t.panes), "connected", connected)

	// Remove old panes for this peer. Close in a goroutine so goWg.Wait()
	// inside rp.Close() never blocks the main loop (the readLoop goroutine
	// may be stuck on a yamux stream read from a dead peer).
	kept := make([]PaneView, 0, len(t.panes))
	for _, p := range t.panes {
		if rp, ok := p.(*RemotePane); ok && rp.Host() == peerName {
			go rp.Close()
			continue
		}
		kept = append(kept, p)
	}

	for _, p := range newPanes {
		if vp, ok := p.(interface{ SetVisible(bool) }); ok {
			vp.SetVisible(!t.layoutState.Hidden[agentKey(p)])
		}
	}
	kept = append(kept, newPanes...)

	// Notify on the STATE CHANGE, not on the attempt (ini-1ch). The reconnect
	// loop retries by design, so an event per attempt is an unbounded stack of
	// identical notices for one underlying fact -- four of them on the
	// operator's screen. And the state is `connected`, NOT len(newPanes): a
	// peer connected with zero agents assigned to this window is healthy, and
	// reporting it as a disconnect is how a working attach announced itself as
	// a failure.
	// Initialised here rather than only at construction: TUIs are built by
	// several paths (tests included) and a nil map would panic on the write
	// below, turning a notice-discipline fix into a crash.
	if t.peerConnected == nil {
		t.peerConnected = make(map[string]bool)
	}
	if was, seen := t.peerConnected[peerName]; !seen || was != connected {
		t.peerConnected[peerName] = connected
		if connected {
			t.handleAgentEvent(AgentEvent{
				Type:   EventPeerConnected,
				Detail: fmt.Sprintf("%s connected (%d agents)", peerName, len(newPanes)),
			})
		} else {
			t.handleAgentEvent(AgentEvent{
				Type:   EventPeerDisconnected,
				Detail: fmt.Sprintf("%s disconnected", peerName),
			})
		}
	}
	oldLen := len(t.panes)
	t.panes = kept
	t.logPanesMutation("peer-update", oldLen)
	if len(t.layoutState.Order) > 0 {
		reorderPanes(t.panes, t.layoutState.Order)
	}
	// Keep the agents-modal selection state valid against the new pane set so a
	// stale filtered/selected index can't crash the render loop (ini-w7ym).
	t.agentsReconcile()
	// Drop the top modal's cached rows: a peer update can both shrink and
	// reorder t.panes, so the cached rows no longer describe it (ini-6gjg).
	t.topReconcile()
	// A (re)connect is a shape change this window slept through: reload the
	// assignment so a move made while detached is filtered from the first
	// frame (ini-xq4r). No-op on window 1, which owns the store.
	if connected {
		t.reloadAssignmentIfFollower()
		t.refreshMembershipIfFollower()
	}
	// Group the NEW panes BEFORE any layout runs (ini-6m4). visiblePanesForWindow
	// resolves each pane's window through GroupOf; panes that arrived in this
	// update have no entry yet, and an unknown pane defaults to window 1 -- so a
	// secondary window filtered out its OWN agents and planned zero panes on the
	// operator's fleet ('total_panes=8' followed by 'plan_panes=0' in his log).
	// Zero planned panes also meant no emulator ever got resized, which is what
	// armed the replay crash (ini-w6z). Measured: the same state planned eng1+eng2
	// correctly the moment ensureGroups had run.
	t.ensureGroups(false)
	LogInfo("peer-update", "panes-updated", "peer", peerName, "total_panes", len(kept))
	t.recalcGrid(true)
	LogInfo("peer-update", "done", "peer", peerName,
		"plan_panes", len(t.plan.Panes), "plan_set", planPaneSet(t.plan))
}

// handlePeerPaneAdded inserts a single remote pane into the TUI when the
// daemon announces a stream-on-create (configure_agent → stream_added).
// Unlike handlePeerUpdate it does not replace the peer's existing panes —
// it adds a single new one to the end of the list.
func (t *TUI) handlePeerPaneAdded(peerName string, pane PaneView) {
	if vp, ok := pane.(interface{ SetVisible(bool) }); ok {
		vp.SetVisible(!t.layoutState.Hidden[agentKey(pane)])
	}
	oldLen := len(t.panes)
	t.panes = append(t.panes, pane)
	t.logPanesMutation("peer-pane-added", oldLen)
	if len(t.layoutState.Order) > 0 {
		reorderPanes(t.panes, t.layoutState.Order)
	}
	t.handleAgentEvent(AgentEvent{
		Type:   EventPeerConnected,
		Detail: fmt.Sprintf("%s: %s pushed", peerName, pane.Name()),
	})
	t.recalcGrid(true)
	LogInfo("peer-pane-added", "done", "peer", peerName, "agent", pane.Name())
}

// calcMainVertical creates a focus-split layout: a smaller pane on the left
// (40%) and every other pane reflowed as a grid on the right (60%). The
// caller is responsible for ordering the input pane list so the pane meant
// for the left slot is first (region 0) — this function is purely
// positional (ini-vtki: was 60/40 with the right side stacked; the ratio
// inverted and the right side became a grid so the focused pane keeps every
// other agent visible, not just a sliver of each).
func calcMainVertical(n, screenW, screenH int) []Region {
	if n <= 1 {
		return []Region{{X: 0, Y: 0, W: screenW, H: screenH}}
	}

	leftW := screenW * 40 / 100
	rightW := screenW - leftW
	rightCount := n - 1

	// Reserve the left pane's last column as the gutter for the divider
	// between it and the right grid (ini-czi) -- the right grid still
	// starts at the unshrunk leftW offset below, so the reserved column
	// falls exactly where computeDividers' X = nextX-1 already points.
	leftOwnedW := leftW - 1
	if leftOwnedW < 1 {
		leftOwnedW = 1
	}

	regions := make([]Region, 0, n)
	regions = append(regions, Region{X: 0, Y: 0, W: leftOwnedW, H: screenH})

	cols, rows := autoGrid(rightCount)
	rightRegions := gridRegions(cols, rows, rightCount, rightW, screenH, nil, nil)
	for _, r := range rightRegions {
		r.X += leftW
		regions = append(regions, r)
	}
	return regions
}

// render draws all visible panes, the overlay, and the command modal.
// It consumes the pre-computed RenderPlan without making layout decisions.
