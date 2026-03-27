package tui

import (
	"fmt"
	"log/slog"
	"os"
	osexec "os/exec"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	iexec "github.com/nmelo/initech/internal/exec"
)

// LayoutMode determines how panes are arranged on screen.
type LayoutMode int

const (
	LayoutFocus LayoutMode = iota // Single pane, full screen.
	LayoutGrid                    // Arbitrary NxM grid.
	Layout2Col                    // Main pane left, stacked right.
)

// AgentInfo describes an agent for the status overlay.
type AgentInfo struct {
	Name            string
	Status          string        // Display text: activity string or bead ID.
	Activity        ActivityState // Actual activity state for dot color.
	Visible         bool
	IdleWithBacklog bool // True when idle with ready beads in the backlog.
	BacklogCount    int  // Number of ready beads (when IdleWithBacklog is true).
}

// cmdModal holds command modal state.
type cmdModal struct {
	active bool
	buf    []rune
	error  string // Shown briefly after a bad command.

	// Tab completion state.
	tabBuf  string // Buffer content at last Tab press (double-Tab detection).
	tabHint string // Completion hint line shown above the input bar; empty = no hint.

	// Destructive command confirmation state.
	pendingConfirm string    // Command waiting for Enter-to-confirm ("quit", "remove eng1", "restart eng2").
	confirmExpiry  time.Time // When the confirmation prompt auto-expires.
	confirmMsg     string    // Human-readable confirmation prompt text.
}

// topModal holds activity monitor (top) modal state.
type topModal struct {
	active    bool
	selected  int
	data      []topEntry
	cacheTime time.Time
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
	panes       []*Pane
	layoutState LayoutState // Single source of truth for layout intent.
	plan        RenderPlan  // Current frame's render instructions.

	// Tracked screen dimensions for detecting resize.
	lastW, lastH int

	// Project root for .initech/layout.yaml persistence. Empty disables auto-save.
	projectRoot string

	// projectName is shown in the overlay title ("Agents (initech)").
	// Comes from initech.yaml's project field. Empty falls back to "Agents".
	projectName string

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
	sel       mouseSelection // Mouse text selection.
	quitCh   chan struct{} // Closed by IPC quit action to signal event loop exit.
	quitOnce sync.Once   // Guards single close of quitCh; prevents concurrent-quit panics.

	// ipcCh is the dispatch channel for IPC goroutines that need to access
	// TUI state (t.panes, layoutState) safely from outside the main event loop.
	// Nil in test contexts that don't set up the channel (runOnMain falls back
	// to direct execution when nil).
	ipcCh chan ipcAction

	// Build version for crash reports.
	version string

	// Resource management gate. When false, all resource management
	// (memory monitor, auto-suspend policy) is dormant.
	autoSuspend       bool
	pressureThreshold int

	// Agent event system.
	agentEvents   chan AgentEvent // Buffered channel for semantic events from detection modules.
	notifications []notification // Active notifications for rendering.
	eventLog      []AgentEvent   // Persistent log of all events (last 100 or last 60 min).
}

// applyLayout recomputes the render plan from the current layout state
// and resizes panes whose regions changed.
func (t *TUI) applyLayout() {
	var w, h int
	if t.screen != nil {
		w, h = t.screen.Size()
	} else {
		w, h = 200, 60 // Fallback for tests without a screen.
	}
	t.plan = computeLayout(t.layoutState, t.panes, w, h)

	// Cancel any in-progress mouse selection. Layout changes invalidate
	// the pane index and region the selection was tracking.
	t.sel.active = false

	// Write validated focus back to layoutState so it stays consistent.
	if t.plan.ValidatedFocus != "" {
		t.layoutState.Focused = t.plan.ValidatedFocus
	}

	// Resize panes whose regions changed (skip if no screen, e.g. in tests).
	if t.screen == nil {
		return
	}
	for _, pr := range t.plan.Panes {
		old := pr.Pane.region
		if old != pr.Region {
			pr.Pane.region = pr.Region
			cols, rows := pr.Region.InnerSize()
			pr.Pane.Resize(rows, cols)
		}
	}
}

// saveLayoutIfConfigured persists the current layout to disk.
// No-op if projectRoot is empty.
func (t *TUI) saveLayoutIfConfigured() {
	if t.projectRoot == "" {
		return
	}
	if err := SaveLayout(t.projectRoot, t.layoutState); err != nil {
		LogWarn("layout", "save failed", "err", err)
	}
}

// focusedPane returns the currently focused pane, or nil.
func (t *TUI) focusedPane() *Pane {
	name := t.layoutState.Focused
	for _, p := range t.panes {
		if p.name == name {
			return p
		}
	}
	return nil
}

// Config controls what agents the TUI launches.
type Config struct {
	Agents            []PaneConfig                    // One entry per agent pane.
	ProjectName       string                          // Used for socket path.
	ProjectRoot       string                          // Project root for .initech/ layout persistence.
	ResetLayout       bool                            // Ignore saved layout and start with defaults.
	Verbose           bool                            // Enable DEBUG-level logging (default: INFO).
	Version           string                          // Build version for crash reports.
	AutoSuspend       bool                            // Enable resource-aware auto-suspend/resume.
	PressureThreshold int                             // RSS percentage threshold (0 uses default 85).
	PaneConfigBuilder func(name string) (PaneConfig, error) // Optional factory for hot-add. Nil disables add command.
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
	// a valid screen to Fini() on signal receipt. quitCh is created now so
	// it can be passed to both the signal handler and the TUI struct below.
	quitCh := make(chan struct{})
	sigCleanup := installSignalHandlers(screen, quitCh)
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
	if !cfg.ResetLayout && cfg.ProjectRoot != "" {
		if saved, ok := LoadLayout(cfg.ProjectRoot, agentNames); ok {
			layoutState = saved
		} else {
			layoutState = DefaultLayoutState(agentNames)
		}
	} else {
		layoutState = DefaultLayoutState(agentNames)
	}

	initW, initH := screen.Size()
	sp := SocketPath(cfg.ProjectRoot, cfg.ProjectName)
	t := &TUI{
		screen:            screen,
		layoutState:       layoutState,
		lastW:             initW,
		lastH:             initH,
		projectRoot:       cfg.ProjectRoot,
		projectName:       cfg.ProjectName,
		version:           cfg.Version,
		sockPath:          sp,
		paneConfigBuilder: cfg.PaneConfigBuilder,
		autoSuspend:       cfg.AutoSuspend,
		pressureThreshold: cfg.PressureThreshold,
		quitCh:            quitCh,
		ipcCh:             make(chan ipcAction, 32),
		agentEvents:       make(chan AgentEvent, 64),
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

	// Compute initial regions for pane creation.
	ls := t.layoutState
	regions := gridRegions(ls.GridCols, ls.GridRows, len(cfg.Agents),
		initW, initH, ls.ColWeights, ls.RowWeights)

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
		cols, rows := r.InnerSize()
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
		t.panes = append(t.panes, p)
		LogDebug("pane", "created", "name", acfg.Name, "dir", acfg.Dir)
	}

	// Now that panes exist, compute the full render plan.
	t.applyLayout()
	defer func() {
		for _, p := range t.panes {
			p.Close()
		}
	}()

	// Start idle-with-backlog detection if bd is available.
	if _, err := osexec.LookPath("bd"); err == nil {
		t.safeGo(func() { t.watchBacklog(&iexec.DefaultRunner{}) })
	}

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

	// Render at ~30 fps.
	ticker := time.NewTicker(33 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case ev := <-eventCh:
			if t.handleEvent(ev) {
				return nil
			}
		case ae := <-t.agentEvents:
			t.handleAgentEvent(ae)
		case op := <-t.ipcCh:
			// Execute IPC-dispatched closures on the main goroutine so they
			// can safely access t.panes and other unsynchronised TUI state.
			op.fn()
			close(op.done)
		case <-ticker.C:
			t.pruneNotifications()
			t.pruneConfirmation()
			t.render()
		case <-t.quitCh:
			return nil
		}
	}
}

func (t *TUI) handleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventKey:
		return t.handleKey(ev)
	case *tcell.EventMouse:
		t.handleMouse(ev)
	case *tcell.EventResize:
		t.handleResize()
	case *tcell.EventPaste:
		if p := t.focusedPane(); p != nil {
			p.SendPaste(ev.Start())
		}
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

