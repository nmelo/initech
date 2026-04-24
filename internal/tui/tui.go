package tui

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/mcp"
	"github.com/nmelo/initech/internal/slackchat"
	"github.com/nmelo/initech/internal/telemetry"
	"github.com/nmelo/initech/internal/update"
	"github.com/nmelo/initech/internal/web"
)

// LayoutMode determines how panes are arranged on screen.
type LayoutMode int

const (
	LayoutFocus LayoutMode = iota // Single pane, full screen.
	LayoutGrid                    // Arbitrary NxM grid.
	Layout2Col                    // Main pane left, stacked right.
	LayoutLive                    // Dynamic pane rotation by activity conviction score.
)

// AgentInfo describes an agent for the status overlay.
type AgentInfo struct {
	Name            string
	Status          string        // Display text: activity string or bead ID.
	Activity        ActivityState // Actual activity state for dot color.
	Visible         bool
	Protected       bool // True when agent is protected from auto-suspend.
	LivePinned      bool // True when agent is pinned to a live mode slot.
	Remote          bool // True for agents on remote peers.
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

// mcpModal holds MCP setup modal state.
type mcpModal struct {
	active        bool
	tokenRevealed bool
	revealExpiry  time.Time // auto-hide token after 10 seconds
}

// webModal holds Web Companion modal state.
type webModal struct {
	active bool
}

// agentsModal holds state for the agent management modal.
type agentsModal struct {
	active       bool
	selected     int    // Currently highlighted row index (into filtered list when searching).
	scrollOffset int    // First visible row in the viewport.
	moving       bool   // True when a row is grabbed for reorder.
	error        string // Inline error message (e.g., "cannot hide last visible pane").
	searching    bool   // True when / has been pressed and search is active.
	searchBuf    []rune // Current search input.
	filtered     []int  // Indices into t.panes matching the search. Nil = no filter active.
}

// welcomeOverlay is shown once on first launch, then never again.
type welcomeOverlay struct {
	active    bool
	expiresAt time.Time
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
}

// TUI is the main terminal multiplexer. It owns the tcell screen,
// a set of terminal panes, and handles input routing, layout, and rendering.
type TUI struct {
	screen      tcell.Screen
	panes       []PaneView
	layoutState LayoutState // Single source of truth for layout intent.
	plan        RenderPlan  // Current frame's render instructions.

	// Tracked screen dimensions for detecting resize.
	lastW, lastH int

	// Project root for .initech/layout.yaml persistence. Empty disables auto-save.
	projectRoot string

	// projectName is shown in the overlay title ("Agents (initech)").
	// Comes from initech.yaml's project field. Empty falls back to "Agents".
	projectName string

	// project holds the full config for cross-machine peer name lookup and
	// remote connection routing. Nil when no config is loaded (tests).
	project *config.Project

	// telemetry is the PostHog telemetry client. Nil when disabled.
	telemetry      *telemetry.Client
	liveTracked    bool // true after first live_mode_activated event

	// sockPath is the IPC socket this TUI is listening on. Used to inject
	// INITECH_SOCKET into hot-added panes.
	sockPath string

	// paneConfigBuilder builds a PaneConfig for a new role at runtime.
	// Set from Config.PaneConfigBuilder. Nil disables the add command.
	paneConfigBuilder func(name string) (PaneConfig, error)

	cmd       cmdModal       // Command input bar.
	top       topModal       // Activity monitor overlay.
	eventLogM eventLogModal  // Event log history modal.
	help      helpModal      // Help reference card modal.
	mcpM      mcpModal       // MCP setup modal.
	webM      webModal       // Web companion modal.
	agents    agentsModal    // Agent management modal.
	welcome   welcomeOverlay // First-launch keybinding hints.
	sel       mouseSelection // Mouse text selection.
	quitCh    chan struct{}  // Closed by IPC quit action to signal event loop exit.
	quitOnce  sync.Once      // Guards single close of quitCh; prevents concurrent-quit panics.

	// ipcCh is the dispatch channel for IPC goroutines that need to access
	// TUI state (t.panes, layoutState) safely from outside the main event loop.
	// Nil in test contexts that don't set up the channel (runOnMain falls back
	// to direct execution when nil).
	ipcCh chan ipcAction

	// Build version for crash reports.
	version     string
	renderCount int // Frame counter for periodic render heartbeat logging.

	// lastRenderAt stores the UnixNano timestamp of the last completed render.
	// Updated atomically by render(), read by the watchdog goroutine.
	lastRenderAt atomic.Int64

	// Resource management gate. When false, all resource management
	// (memory monitor, auto-suspend policy) is dormant.
	autoSuspend       bool
	pressureThreshold int
	systemMemAvail    int64 // Available system RAM in KB, updated by memory monitor.
	systemMemTotal    int64 // Total system RAM in KB, queried once at startup.

	// Update notification. Set via runOnMain when background check finds a newer version.
	updateAvailable string // e.g. "0.24.0". Empty = no update or check not done.

	// Status bar tip cycling.
	tipIndex    int       // Current index into statusTips.
	tipRotateAt time.Time // When the next tip rotation should happen.

	// Battery monitoring for status bar.
	batteryPercent  int  // 0-100, or -1 if no battery detected.
	batteryCharging bool // True when plugged in and charging.

	// Claude Code quota percentage scraped from an agent's status bar.
	// -1 means not available (no pane showed a quota, or all panes dead).
	quotaPercent int
	quotaPollAt  time.Time // Next time to poll for quota.

	// MCP server runtime state for the setup modal.
	mcpToken string // Active bearer token (empty if MCP disabled).
	mcpBind  string // Bind address (e.g. "0.0.0.0").
	mcpPort  int    // Configured port (0 = disabled).

	// Web companion runtime state for the web modal.
	webPort         int               // Configured port (0 = disabled).
	webEventProvider *tuiEventProvider // For broadcasting events to web subscribers. Nil if web disabled.
	webhookCh        chan AgentEvent             // Fan-out channel for webhook HTTP POSTs. Nil if webhook disabled.
	webhookURL       string                     // Webhook URL from config, for IPC notify action.
	slackEventCh     chan slackchat.ResponderEvent // Fan-out channel for Slack responder. Nil if Slack disabled.

	// Paste buffering: accumulate characters between EventPaste start/end,
	// then flush as one atomic PTY write with bracketed paste markers.
	// Turns O(N) renders into O(1) for large pastes.
	pasting  bool   // True between EventPaste(start) and EventPaste(end).
	pasteBuf []byte // Accumulated paste characters.

	// Timer store for scheduled sends.
	timers *TimerStore

	// Agent event system.
	agentEvents   chan AgentEvent // Buffered channel for semantic events from detection modules.
	notifications []notification  // Active notifications for rendering.
	eventLog      []AgentEvent    // Persistent log of all events (last 100 or last 60 min).

	// Live Mode: persistent engine for anti-thrashing across render frames.
	// Nil when not in live mode. Created by cmdLive/Alt+5, destroyed on mode switch.
	liveEngine   *LiveEngine
	lastLiveTick time.Time // Throttles live-mode applyLayout to 1-second cadence.
}

// logPanesMutation is temporary DEBUG logging. Logs every mutation of t.panes
// with a call-site tag, old count, new count, and names.
func (t *TUI) logPanesMutation(site string, oldLen int) {
	names := make([]string, len(t.panes))
	for i, p := range t.panes {
		names[i] = paneKey(p)
	}
	LogInfo("panes-mutation", site, "old", oldLen, "new", len(t.panes), "names", fmt.Sprintf("%v", names))
}

// applyLayout recomputes the render plan from the current layout state
// and resizes panes whose regions changed. The bottom row is reserved
// for the persistent status bar and excluded from pane layout.
func (t *TUI) applyLayout() {
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
		livePanes := make([]PaneView, 0, len(t.panes))
		for _, p := range t.panes {
			if !t.layoutState.Hidden[paneKey(p)] {
				livePanes = append(livePanes, p)
			}
		}
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

	t.plan = computeLayout(t.layoutState, t.panes, w, paneH)
	LogInfo("applyLayout", "layout applied", "panes", len(t.plan.Panes), "w", w, "h", paneH)

	// Cancel in-progress mouse selection only if the tracked pane's region
	// changed. Live mode ticks applyLayout every second; clearing selection
	// unconditionally makes click-drag copy impossible in live mode.
	if t.sel.active && t.sel.pane < len(t.panes) {
		pk := paneKey(t.panes[t.sel.pane])
		stillValid := false
		for _, pr := range t.plan.Panes {
			if paneKey(pr.Pane) == pk && pr.Region == t.panes[t.sel.pane].GetRegion() {
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

// trackLiveModeActivated sends a one-time telemetry event for live mode.
func (t *TUI) trackLiveModeActivated() {
	if t.telemetry == nil || t.liveTracked {
		return
	}
	t.liveTracked = true
	mode := "CxR"
	if t.layoutState.LiveAuto {
		mode = "auto"
	}
	visCount := 0
	for _, p := range t.panes {
		if !t.layoutState.Hidden[paneKey(p)] {
			visCount++
		}
	}
	t.telemetry.Track("live_mode_activated", map[string]any{
		"mode":        mode,
		"agent_count": visCount,
	})
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
		visCount := t.visibleCountFromState()
		if visCount < 1 {
			visCount = len(t.panes)
		}
		cols, rows := autoGrid(visCount)
		numSlots = cols * rows
	}
	t.liveEngine = NewLiveEngine(numSlots, t.layoutState.LivePinned, roles)
}

// onLiveSwap compares previous and current slot assignments. If any slot
// changed to a different agent, emits an EventLiveSwap. The event flows
// through the standard fan-out (event log, webhook, MCP) but is suppressed
// from toasts (too frequent). No direct radio POST; the webhook sink
// handles external notification.
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
	// Snapshot current pane order into layoutState before persisting.
	t.layoutState.Order = make([]string, len(t.panes))
	for i, p := range t.panes {
		t.layoutState.Order[i] = paneKey(p)
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
		if paneKey(p) == key {
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

// checkForUpdate triggers a manual version check (Alt+u). Runs the check
// in a background goroutine to avoid blocking the main event loop.
func (t *TUI) checkForUpdate() {
	t.cmd.error = "Checking for updates..."
	t.safeGo(func() {
		info, err := update.CheckForUpdate(context.Background(), t.version)
		t.runOnMain(func() {
			if err != nil {
				t.cmd.error = "Update check failed: " + err.Error()
				return
			}
			if info == nil {
				t.cmd.error = "Up to date (v" + t.version + ")"
				return
			}
			t.updateAvailable = info.Version
			t.cmd.error = "v" + info.Version + " available - " + update.UpdateInstruction()
		})
	})
}

// Config controls what agents the TUI launches.
type Config struct {
	Agents            []PaneConfig                          // One entry per agent pane.
	ProjectName       string                                // Used for socket path.
	ProjectRoot       string                                // Project root for .initech/ layout persistence.
	ResetLayout       bool                                  // Ignore saved layout and start with defaults.
	Verbose           bool                                  // Enable DEBUG-level logging (default: INFO).
	Version           string                                // Build version for crash reports.
	AutoSuspend       bool                                  // Enable resource-aware auto-suspend/resume.
	PressureThreshold int                                   // RSS percentage threshold (0 uses default 85).
	PaneConfigBuilder func(name string) (PaneConfig, error) // Optional factory for hot-add. Nil disables add command.
	Project           *config.Project                       // Full project config. Used for remote peer connections.
	UpdateResult      <-chan string                         // Receives newer version string from background check. Nil = no check.
	WebPort           int                                   // Port for the web companion server. 0 = disabled.
	WebhookURL        string                                // HTTP endpoint for agent event POSTs. Empty = disabled.
	SlackAppToken     string                                // Slack app-level token for Socket Mode. Empty = disabled.
	SlackBotToken     string                                // Slack bot token for Web API calls. Empty = disabled.
	McpPort           int                                   // Port for the MCP server. 0 = disabled.
	McpToken          string                                // Bearer token for MCP auth. Empty = auto-generate.
	McpBind           string                                // Bind address for MCP server. Empty = "0.0.0.0".
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
	checkPreviousCrash(cfg.ProjectRoot)

	LogInfo("tui", "starting", "version", cfg.Version, "pid", os.Getpid(), "agents", len(cfg.Agents), "verbose", cfg.Verbose)

	// Write PID file. The deferred remove fires only on clean exits; an absent
	// cleanup at startup means the previous run exited uncleanly.
	pidCleanup := writePIDFile(cfg.ProjectRoot)
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
	sigCleanup := installSignalHandlers(screen, quitCh, sp, pidPath)
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

	initW, initH := screen.Size()
	t := &TUI{
		screen:            screen,
		layoutState:       layoutState,
		lastW:             initW,
		lastH:             initH,
		projectRoot:       cfg.ProjectRoot,
		projectName:       cfg.ProjectName,
		webhookURL:        cfg.WebhookURL,
		project:           cfg.Project,
		version:           cfg.Version,
		sockPath:          sp,
		paneConfigBuilder: cfg.PaneConfigBuilder,
		autoSuspend:       cfg.AutoSuspend,
		pressureThreshold: cfg.PressureThreshold,
		tipRotateAt:       time.Now().Add(tipRotationInterval),
		batteryPercent:    -1,
		quotaPercent:      -1,
		quotaPollAt:       time.Now().Add(5 * time.Second), // first poll after 5s startup
		quitCh:            quitCh,
		ipcCh:             make(chan ipcAction, 32),
		agentEvents:       make(chan AgentEvent, 64),
		timers:            NewTimerStore(filepath.Join(cfg.ProjectRoot, ".initech", "timers.json")),
	}

	// Initialize telemetry if enabled.
	if cfg.Project == nil || cfg.Project.IsTelemetryEnabled() {
		t.telemetry = telemetry.Init(cfg.Version)
		agentTypes := make(map[string]bool, len(cfg.Agents))
		for _, a := range cfg.Agents {
			agentTypes[a.AgentType] = true
		}
		types := make([]string, 0, len(agentTypes))
		for at := range agentTypes {
			if at != "" {
				types = append(types, at)
			}
		}
		t.telemetry.Track("session_started", map[string]any{
			"agent_count": len(cfg.Agents),
			"agent_types": types,
		})
	}
	defer func() {
		if t.telemetry != nil {
			t.telemetry.Track("session_ended", map[string]any{
				"duration_seconds": int(t.telemetry.Duration().Seconds()),
				"agents_started":  len(t.panes),
			})
			t.telemetry.Shutdown()
		}
	}()

	// Show update notification on stderr after TUI exits.
	defer func() {
		if t.updateAvailable != "" {
			fmt.Fprintf(os.Stderr, "\nA new version of initech is available: v%s -> v%s\n  Update: %s\n\n",
				t.version, t.updateAvailable, update.UpdateInstruction())
		}
	}()

	// Show welcome overlay on first launch (no saved layout).
	if firstLaunch {
		t.welcome = welcomeOverlay{active: true, expiresAt: time.Now().Add(10 * time.Second)}
	}

	// Start IPC socket server for inter-agent messaging.
	sockPath := sp
	ipcCleanup, err := t.startIPC(sockPath)
	if err != nil {
		LogError("ipc", "socket bind failed", "path", sockPath, "err", err)
		return fmt.Errorf("start IPC: %w", err)
	}
	LogInfo("ipc", "listening", "path", sockPath)
	defer ipcCleanup()

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

	// Create panes.
	for i, acfg := range cfg.Agents {
		r := regions[i%len(regions)]
		cols, rows := r.TerminalSize()
		p, err := NewPane(acfg, rows, cols)
		if err != nil {
			LogError("pane", "launch failed", "name", acfg.Name, "err", err)
			for _, existing := range t.panes {
				existing.Close()
			}
			return fmt.Errorf("create pane %q: %w", acfg.Name, err)
		}
		p.region = r
		p.eventCh = t.agentEvents
		p.safeGo = t.safeGo
		p.Start()
		old := len(t.panes)
		t.panes = append(t.panes, p)
		t.logPanesMutation("create-pane", old)
		LogDebug("pane", "created", "name", acfg.Name, "dir", acfg.Dir)
	}

	// Connect to remote peers asynchronously. The peerManager handles both
	// initial connection and reconnection in background goroutines. The TUI
	// renders immediately with local-only panes; remote panes appear once
	// connected via handlePeerUpdate on the main goroutine.
	if cfg.Project != nil && len(cfg.Project.Remotes) > 0 {
		pm := newPeerManager(cfg.Project, func(peerName string, panes []PaneView) {
			t.runOnMain(func() {
				t.handlePeerUpdate(peerName, panes)
			})
		}, func(target, text string, enter bool) error {
			// Deliver forwarded message to local pane.
			var pv PaneView
			t.runOnMain(func() { pv = t.findPaneByName(target) })
			if pv == nil {
				return fmt.Errorf("agent %q not found", target)
			}
			pv.SendText(text, enter)
			return nil
		}, t.quitCh)
		defer func() {
			done := make(chan struct{})
			go func() {
				pm.wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				LogWarn("tui", "peerManager wait timed out after 3s, forcing exit")
			}
		}()
	}

	// Sync pinned state from layout to panes.
	for _, p := range t.panes {
		if t.layoutState.Protected[paneKey(p)] {
			if lp, ok := p.(*Pane); ok {
				lp.SetProtected(true)
			}
		}
	}

	// Apply saved pane order from layout.yaml.
	if len(t.layoutState.Order) > 0 {
		reorderPanes(t.panes, t.layoutState.Order)
	}

	// Initialize live engine if the restored layout is in live mode.
	// Without this, liveEngine is nil and applyLayout falls through to
	// computeLayout's stateless fallback which only sees visible panes.
	if t.layoutState.Mode == LayoutLive {
		if t.layoutState.LivePinned == nil {
			t.layoutState.LivePinned = make(map[string]int)
		}
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
			for _, p := range t.panes {
				p.Close()
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			LogWarn("tui", "pane cleanup timed out after 3s, forcing exit")
		}
	}()

	// Fire any overdue timers from a previous session that missed their window
	// (e.g., initech was restarted after a timer's FireAt).
	t.fireTimers()

	// Start memory monitor when auto-suspend is enabled.
	if t.autoSuspend {
		t.startMemoryMonitor()
	}

	// Start web companion server when configured.
	if cfg.WebPort > 0 {
		t.webPort = cfg.WebPort
		webCtx, webCancel := context.WithCancel(context.Background())
		lister := &tuiPaneLister{t: t}
		subscriber := &tuiPaneSubscriber{t: t}
		stateProvider := &tuiStateProvider{t: t}
		eventProvider := &tuiEventProvider{t: t}
		t.webEventProvider = eventProvider
		paneWriter := &tuiPaneWriter{t: t}
		pinToggler := &tuiPinToggler{t: t}
		webSrv := web.NewServer(cfg.WebPort, lister, subscriber, stateProvider, eventProvider, paneWriter, pinToggler, nil)
		go func() {
			if err := webSrv.Start(webCtx); err != nil {
				LogError("web", "server exited with error", "err", err)
			}
		}()
		LogInfo("web", "companion server starting", "port", cfg.WebPort)
		defer func() {
			webCancel()
			shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
			webSrv.Shutdown(shutCtx)
			shutCancel()
		}()
	}

	// Start webhook event sink when configured.
	if cfg.WebhookURL != "" {
		webhookCtx, webhookCancel := context.WithCancel(context.Background())
		t.webhookCh = make(chan AgentEvent, 64)
		t.safeGo(func() {
			startWebhookSink(webhookCtx, cfg.WebhookURL, cfg.ProjectName, t.webhookCh)
		})
		LogInfo("webhook", "event sink starting", "url", cfg.WebhookURL)
		defer webhookCancel()
	}

	// Start Slack Socket Mode client and responder when tokens are configured.
	if cfg.SlackAppToken != "" && cfg.SlackBotToken != "" {
		slackCtx, slackCancel := context.WithCancel(context.Background())
		host := &tuiSlackHost{t: t}
		var allowedUsers []string
		if cfg.Project != nil {
			allowedUsers = cfg.Project.Slack.AllowedUsers
		}
		sc := slackchat.NewClient(cfg.SlackAppToken, cfg.SlackBotToken, host, allowedUsers, nil)
		if cfg.Project != nil {
			sc.SetThreadContext(cfg.Project.Slack.IsThreadContextEnabled())
		}
		t.safeGo(func() { sc.Run(slackCtx) })

		// Start the completion responder.
		responseMode := "completion"
		if cfg.Project != nil {
			responseMode = cfg.Project.Slack.ResponseMode
		}
		t.slackEventCh = make(chan slackchat.ResponderEvent, 64)
		peeker := &tuiPanePeeker{t: t}
		resp := slackchat.NewResponder(sc.API(), sc.Tracker(), peeker, responseMode, nil)
		t.safeGo(func() { resp.Run(slackCtx, t.slackEventCh) })

		LogInfo("slack", "Socket Mode client and responder starting")
		defer slackCancel()
	}

	// Start MCP server when configured.
	if cfg.McpPort > 0 {
		mcpToken := cfg.McpToken
		if mcpToken == "" {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				LogError("mcp", "failed to generate token", "err", err)
			} else {
				mcpToken = base64.RawURLEncoding.EncodeToString(b)
			}
		}
		if mcpToken != "" {
			mcpBind := cfg.McpBind
			if mcpBind == "" {
				mcpBind = config.DefaultMcpBind
			}
			// Store MCP state for the setup modal.
			t.mcpToken = mcpToken
			t.mcpBind = mcpBind
			t.mcpPort = cfg.McpPort

			mcpHost := &tuiMCPHost{t: t}
			mcpSrv := mcp.NewServer(cfg.McpPort, mcpBind, mcpToken, mcpHost, nil)
			mcpCtx, mcpCancel := context.WithCancel(context.Background())
			go func() {
				if err := mcpSrv.Start(mcpCtx); err != nil {
					LogError("mcp", "server exited with error", "err", err)
				}
			}()
			LogInfo("mcp", "server starting", "addr", fmt.Sprintf("%s:%d", mcpBind, cfg.McpPort), "token", mcpToken)
			defer func() {
				mcpCancel()
				shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
				mcpSrv.Shutdown(shutCtx)
				shutCancel()
			}()
		}
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

	// Wire background update check result into the TUI.
	if cfg.UpdateResult != nil {
		t.safeGo(func() {
			if ver, ok := <-cfg.UpdateResult; ok && ver != "" {
				t.runOnMain(func() { t.updateAvailable = ver })
			}
		})
	}

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
			t.pollQuota()
			t.fireTimers()
			if t.layoutState.Mode == LayoutLive && time.Since(t.lastLiveTick) >= time.Second {
				t.lastLiveTick = time.Now()
				t.applyLayout()
			}
		case <-t.quitCh:
			return nil
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
func (t *TUI) handlePeerUpdate(peerName string, newPanes []PaneView) {
	LogInfo("peer-update", "start", "peer", peerName, "new_panes", len(newPanes), "current_panes", len(t.panes))

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

	// Add new panes (nil = peer went offline, nothing to add).
	if len(newPanes) > 0 {
		for _, p := range newPanes {
			if vp, ok := p.(interface{ SetVisible(bool) }); ok {
				vp.SetVisible(!t.layoutState.Hidden[paneKey(p)])
			}
		}
		kept = append(kept, newPanes...)
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
	oldLen := len(t.panes)
	t.panes = kept
	t.logPanesMutation("peer-update", oldLen)
	if len(t.layoutState.Order) > 0 {
		reorderPanes(t.panes, t.layoutState.Order)
	}
	LogInfo("peer-update", "panes-updated", "peer", peerName, "total_panes", len(kept))
	t.recalcGrid(true)
	LogInfo("peer-update", "done", "peer", peerName, "plan_panes", len(t.plan.Panes))
}

// calcMainVertical creates a layout with a large pane on the left
// and stacked panes on the right.
func calcMainVertical(n, screenW, screenH int) []Region {
	if n <= 1 {
		return []Region{{X: 0, Y: 0, W: screenW, H: screenH}}
	}

	leftW := screenW * 60 / 100
	rightW := screenW - leftW
	rightCount := n - 1
	if rightCount < 1 {
		rightCount = 1
	}

	regions := make([]Region, 0, n)
	regions = append(regions, Region{X: 0, Y: 0, W: leftW, H: screenH})

	cellH := screenH / rightCount
	extraH := screenH - cellH*rightCount
	y := 0
	for i := 0; i < rightCount; i++ {
		h := cellH
		if i < extraH {
			h++
		}
		regions = append(regions, Region{X: leftW, Y: y, W: rightW, H: h})
		y += h
	}
	return regions
}

// render draws all visible panes, the overlay, and the command modal.
// It consumes the pre-computed RenderPlan without making layout decisions.
