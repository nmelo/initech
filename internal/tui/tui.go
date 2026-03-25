package tui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/gdamore/tcell/v2"
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
	Name    string
	Status  string // "running", "idle"
	Visible bool
}

// cmdModal holds command modal state.
type cmdModal struct {
	active bool
	buf    []rune
	error  string // Shown briefly after a bad command.
}

// topModal holds activity monitor (top) modal state.
type topModal struct {
	active    bool
	selected  int
	data      []topEntry
	cacheTime time.Time
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

	cmd cmdModal      // Command input bar.
	top topModal      // Activity monitor overlay.
	sel mouseSelection // Mouse text selection.
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
// No-op if projectRoot is empty. Errors are silently ignored.
func (t *TUI) saveLayoutIfConfigured() {
	if t.projectRoot == "" {
		return
	}
	SaveLayout(t.projectRoot, t.layoutState)
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
	Agents      []PaneConfig // One entry per agent pane.
	ProjectName string       // Used for socket path.
	ProjectRoot string       // Project root for .initech/ layout persistence.
	ResetLayout bool         // Ignore saved layout and start with defaults.
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
	t := &TUI{
		screen:      screen,
		layoutState: layoutState,
		lastW:       initW,
		lastH:       initH,
		projectRoot: cfg.ProjectRoot,
	}

	// Initialize quit channel for IPC quit action.
	quitCh = make(chan struct{})

	// Start IPC socket server for inter-agent messaging.
	sockPath := SocketPath(cfg.ProjectName)
	ipcCleanup, err := t.startIPC(sockPath)
	if err != nil {
		return fmt.Errorf("start IPC: %w", err)
	}
	defer ipcCleanup()

	// Compute initial regions for pane creation.
	ls := t.layoutState
	regions := gridRegions(ls.GridCols, ls.GridRows, len(cfg.Agents),
		initW, initH, ls.ColWeights, ls.RowWeights)

	// Inject the socket path into every agent's environment.
	for i := range cfg.Agents {
		cfg.Agents[i].Env = append(cfg.Agents[i].Env, "INITECH_SOCKET="+sockPath)
	}

	// Create panes.
	for i, acfg := range cfg.Agents {
		r := regions[i%len(regions)]
		cols, rows := r.InnerSize()
		p, err := NewPane(acfg, rows, cols)
		if err != nil {
			for _, existing := range t.panes {
				existing.Close()
			}
			return fmt.Errorf("create pane %q: %w", acfg.Name, err)
		}
		p.region = r
		t.panes = append(t.panes, p)
	}

	// Now that panes exist, compute the full render plan.
	t.applyLayout()
	defer func() {
		for _, p := range t.panes {
			p.Close()
		}
	}()

	// Poll tcell events in a goroutine.
	eventCh := make(chan tcell.Event, 64)
	go func() {
		for {
			ev := screen.PollEvent()
			if ev == nil {
				return
			}
			eventCh <- ev
		}
	}()

	// Render at ~30 fps.
	ticker := time.NewTicker(33 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case ev := <-eventCh:
			if t.handleEvent(ev) {
				return nil
			}
		case <-ticker.C:
			t.render()
		case <-quitCh:
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
	}
	return false
}

func (t *TUI) handleMouse(ev *tcell.EventMouse) {
	if t.cmd.active {
		return
	}
	mx, my := ev.Position()

	switch {
	case ev.Buttons()&tcell.Button1 != 0 && !t.sel.active:
		// Button1 press: forward to pane and start TUI selection.
		for i, p := range t.panes {
			if t.layoutState.Hidden[p.name] {
				continue
			}
			r := p.region
			if mx >= r.X && mx < r.X+r.W && my >= r.Y && my < r.Y+r.H {
				if t.layoutState.Focused != p.name {
					t.layoutState.Focused = p.name
					t.applyLayout()
				}
				// Convert to pane-local content coordinates.
				lx := mx - r.X
				ly := my - r.Y
				if ly < 0 {
					ly = 0
				}
				// Forward click to emulator (no-op if mouse reporting is off).
				t.forwardMouseEvent(p, lx, ly, uv.MouseLeft, false, false, ev.Modifiers())
				// Snapshot contentOffset so copy uses the same mapping
				// regardless of content reflow during the drag.
				sr, ro := p.contentOffset()
				t.sel.active = true
				t.sel.pane = i
				t.sel.startX = lx
				t.sel.startY = ly
				t.sel.endX = lx
				t.sel.endY = ly
				t.sel.startRow = sr
				t.sel.renderOffset = ro
				return
			}
		}

	case ev.Buttons()&tcell.Button1 != 0 && t.sel.active:
		// Drag: update selection end and forward motion.
		if t.sel.pane < len(t.panes) {
			p := t.panes[t.sel.pane]
			r := p.region
			lx := mx - r.X
			ly := my - r.Y
			cols, rows := r.InnerSize()
			if lx < 0 {
				lx = 0
			}
			if lx >= cols {
				lx = cols - 1
			}
			if ly < 0 {
				ly = 0
			}
			if ly >= rows {
				ly = rows - 1
			}
			t.forwardMouseEvent(p, lx, ly, uv.MouseLeft, true, false, ev.Modifiers())
			t.sel.endX = lx
			t.sel.endY = ly
		}

	case ev.Buttons() == tcell.ButtonNone && t.sel.active:
		// Release: forward to pane, copy selection, and clear.
		if t.sel.pane < len(t.panes) {
			p := t.panes[t.sel.pane]
			r := p.region
			lx := mx - r.X
			ly := my - r.Y
			if ly < 0 {
				ly = 0
			}
			t.forwardMouseEvent(p, lx, ly, uv.MouseNone, false, true, ev.Modifiers())
		}
		t.copySelection()
		t.sel.active = false

	case ev.Buttons()&tcell.Button2 != 0:
		// Middle click: forward to focused pane only.
		t.forwardMouseToFocused(mx, my, uv.MouseMiddle, false, false, ev.Modifiers())

	case ev.Buttons()&tcell.Button3 != 0:
		// Right click: forward to focused pane only.
		t.forwardMouseToFocused(mx, my, uv.MouseRight, false, false, ev.Modifiers())

	case ev.Buttons()&tcell.WheelUp != 0:
		// Scroll back into history for the pane under cursor.
		for _, p := range t.panes {
			if t.layoutState.Hidden[p.name] {
				continue
			}
			r := p.region
			if mx >= r.X && mx < r.X+r.W && my >= r.Y && my < r.Y+r.H {
				t.layoutState.Focused = p.name
				p.ScrollUp(3)
				return
			}
		}

	case ev.Buttons()&tcell.WheelDown != 0:
		// Scroll toward live view for the pane under cursor.
		for _, p := range t.panes {
			if t.layoutState.Hidden[p.name] {
				continue
			}
			r := p.region
			if mx >= r.X && mx < r.X+r.W && my >= r.Y && my < r.Y+r.H {
				t.layoutState.Focused = p.name
				p.ScrollDown(3)
				return
			}
		}
	}
}

// forwardMouseEvent translates pane-local content coordinates to emulator
// coordinates and sends the mouse event. The emulator silently drops the
// event if the child hasn't enabled mouse reporting.
func (t *TUI) forwardMouseEvent(p *Pane, lx, ly int, button uv.MouseButton, isMotion, isRelease bool, mods tcell.ModMask) {
	startRow, renderOffset := p.contentOffset()
	emuY := startRow + (ly - renderOffset)
	emuX := lx
	if emuY < 0 {
		emuY = 0
	}
	if emuX < 0 {
		emuX = 0
	}

	var mod uv.KeyMod
	if mods&tcell.ModShift != 0 {
		mod |= uv.ModShift
	}
	if mods&tcell.ModAlt != 0 {
		mod |= uv.ModAlt
	}
	if mods&tcell.ModCtrl != 0 {
		mod |= uv.ModCtrl
	}

	m := uv.Mouse{X: emuX, Y: emuY, Button: button, Mod: mod}
	switch {
	case isRelease:
		p.ForwardMouse(uv.MouseReleaseEvent(m))
	case isMotion:
		p.ForwardMouse(uv.MouseMotionEvent(m))
	default:
		p.ForwardMouse(uv.MouseClickEvent(m))
	}
}

// forwardMouseToFocused forwards a mouse event to the focused pane if the
// click is within its region.
func (t *TUI) forwardMouseToFocused(mx, my int, button uv.MouseButton, isMotion, isRelease bool, mods tcell.ModMask) {
	p := t.focusedPane()
	if p == nil {
		return
	}
	r := p.region
	if mx < r.X || mx >= r.X+r.W || my < r.Y || my >= r.Y+r.H {
		return
	}
	lx := mx - r.X
	ly := my - r.Y
	if ly < 0 {
		ly = 0
	}
	t.forwardMouseEvent(p, lx, ly, button, isMotion, isRelease, mods)
}

// copySelection extracts selected text from the pane's emulator and copies to clipboard.
func (t *TUI) copySelection() {
	if t.sel.pane >= len(t.panes) {
		return
	}
	p := t.panes[t.sel.pane]

	// Normalize selection bounds (start may be after end).
	r0, c0, r1, c1 := t.sel.startY, t.sel.startX, t.sel.endY, t.sel.endX
	if r0 > r1 || (r0 == r1 && c0 > c1) {
		r0, c0, r1, c1 = r1, c1, r0, c0
	}

	cols, rows := p.region.InnerSize()
	if r1 >= rows {
		r1 = rows - 1
	}

	// Use the contentOffset snapshot from mouse-down time, not the current
	// offset. This prevents content reflow during the drag from shifting
	// the copied text.
	startRow := t.sel.startRow
	renderOffset := t.sel.renderOffset
	emuRows := p.emu.Height()

	var buf strings.Builder
	for row := r0; row <= r1; row++ {
		emuRow := startRow + (row - renderOffset)
		if emuRow < 0 || emuRow >= emuRows {
			continue
		}

		startCol := 0
		endCol := cols
		if row == r0 {
			startCol = c0
		}
		if row == r1 {
			endCol = c1 + 1
		}
		if endCol > cols {
			endCol = cols
		}

		// Collect characters from the emulator.
		var line strings.Builder
		for col := startCol; col < endCol; col++ {
			cell := p.emu.CellAt(col, emuRow)
			if cell != nil && cell.Content != "" {
				line.WriteString(cell.Content)
			} else {
				line.WriteByte(' ')
			}
		}

		// Trim trailing spaces per line.
		text := strings.TrimRight(line.String(), " ")
		buf.WriteString(text)
		if row < r1 {
			buf.WriteByte('\n')
		}
	}

	text := buf.String()
	if text == "" {
		return
	}

	// Copy to macOS clipboard via pbcopy.
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	cmd.Run()
}

func (t *TUI) handleKey(ev *tcell.EventKey) bool {
	// Top modal intercepts all input when active.
	if t.top.active {
		return t.handleTopKey(ev)
	}

	// Command modal intercepts all input when active.
	if t.cmd.active {
		return t.handleCmdKey(ev)
	}

	// Clear any lingering error message on next keypress.
	t.cmd.error = ""

	// Backtick opens the command modal.
	if ev.Key() == tcell.KeyRune && ev.Rune() == '`' && ev.Modifiers() == 0 {
		t.cmd.active = true
		t.cmd.buf = t.cmd.buf[:0]
		t.cmd.error = ""
		return false
	}

	// Alt-key combos are TUI shortcuts.
	if ev.Modifiers()&tcell.ModAlt != 0 {
		switch ev.Key() {
		case tcell.KeyLeft:
			t.cycleFocus(-1)
			return false
		case tcell.KeyRight:
			t.cycleFocus(1)
			return false
		case tcell.KeyUp:
			t.cycleFocus(-1)
			return false
		case tcell.KeyDown:
			t.cycleFocus(1)
			return false
		case tcell.KeyRune:
			switch ev.Rune() {
			case '1':
				t.layoutState.Mode = LayoutFocus
				t.layoutState.Zoomed = false
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case '2':
				t.layoutState.Mode = LayoutGrid
				t.layoutState.GridCols = 2
				t.layoutState.GridRows = 2
				t.layoutState.Zoomed = false
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case '3':
				t.layoutState.Mode = LayoutGrid
				t.layoutState.GridCols = 3
				t.layoutState.GridRows = 3
				t.layoutState.Zoomed = false
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case '4':
				t.layoutState.Mode = Layout2Col
				t.layoutState.Zoomed = false
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case 's':
				t.layoutState.Overlay = !t.layoutState.Overlay
				return false
			case 'z':
				t.layoutState.Zoomed = !t.layoutState.Zoomed
				t.applyLayout()
				t.saveLayoutIfConfigured()
				return false
			case 'q':
				return true
			}
		}
	}

	// Everything else goes to the focused pane.
	if fp := t.focusedPane(); fp != nil {
		fp.SendKey(ev)
	}
	return false
}

// handleCmdKey processes key events while the command modal is open.
func (t *TUI) handleCmdKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.cmd.active = false
		t.cmd.buf = t.cmd.buf[:0]
		return false
	case tcell.KeyEnter:
		cmd := strings.TrimSpace(string(t.cmd.buf))
		t.cmd.active = false
		t.cmd.buf = t.cmd.buf[:0]
		return t.execCmd(cmd)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(t.cmd.buf) > 0 {
			t.cmd.buf = t.cmd.buf[:len(t.cmd.buf)-1]
		}
		return false
	case tcell.KeyRune:
		// Backtick while empty closes the modal.
		if ev.Rune() == '`' && len(t.cmd.buf) == 0 {
			t.cmd.active = false
			return false
		}
		t.cmd.buf = append(t.cmd.buf, ev.Rune())
		return false
	}
	return false
}

// execCmd parses and executes a command string. Returns true if the TUI should quit.
func (t *TUI) execCmd(cmd string) bool {
	if cmd == "" {
		return false
	}

	parts := strings.Fields(cmd)
	switch parts[0] {
	case "grid":
		if len(parts) < 2 {
			visCount := t.visibleCountFromState()
			c, r := autoGrid(visCount)
			t.layoutState.Mode = LayoutGrid
			t.layoutState.GridCols = c
			t.layoutState.GridRows = r
			t.layoutState.Zoomed = false
			t.applyLayout()
			t.saveLayoutIfConfigured()
			return false
		}
		visCount := t.visibleCountFromState()
		cols, rows, ok := parseGrid(parts[1], visCount)
		if !ok {
			t.cmd.error = fmt.Sprintf("invalid grid %q, use CxR or just C (e.g. 3x3, 4)", parts[1])
			return false
		}
		t.layoutState.Mode = LayoutGrid
		t.layoutState.GridCols = cols
		t.layoutState.GridRows = rows
		t.layoutState.Zoomed = false
		t.applyLayout()
		t.saveLayoutIfConfigured()

	case "focus":
		if len(parts) < 2 {
			t.layoutState.Mode = LayoutFocus
			t.layoutState.Zoomed = false
			t.applyLayout()
			t.saveLayoutIfConfigured()
			return false
		}
		name := parts[1]
		if t.findPaneByName(name) == nil {
			t.cmd.error = fmt.Sprintf("unknown agent %q", name)
			return false
		}
		t.layoutState.Focused = name
		t.layoutState.Mode = LayoutFocus
		t.layoutState.Zoomed = false
		t.applyLayout()
		t.saveLayoutIfConfigured()

	case "zoom":
		t.layoutState.Zoomed = !t.layoutState.Zoomed
		t.applyLayout()
		t.saveLayoutIfConfigured()

	case "panel":
		t.layoutState.Overlay = !t.layoutState.Overlay

	case "main":
		t.layoutState.Mode = Layout2Col
		t.layoutState.Zoomed = false
		t.applyLayout()
		t.saveLayoutIfConfigured()

	case "show":
		if len(parts) < 2 {
			t.cmd.error = "usage: show <name> or show all"
			return false
		}
		if parts[1] == "all" {
			t.layoutState.Hidden = make(map[string]bool)
			t.autoRecalcGrid()
			t.saveLayoutIfConfigured()
			return false
		}
		if t.findPaneByName(parts[1]) == nil {
			t.cmd.error = fmt.Sprintf("unknown agent %q", parts[1])
			return false
		}
		delete(t.layoutState.Hidden, parts[1])
		t.autoRecalcGrid()
		t.saveLayoutIfConfigured()

	case "hide":
		if len(parts) < 2 {
			t.cmd.error = "usage: hide <name>"
			return false
		}
		if parts[1] == "all" {
			t.cmd.error = "cannot hide all panes"
			return false
		}
		if t.findPaneByName(parts[1]) == nil {
			t.cmd.error = fmt.Sprintf("unknown agent %q", parts[1])
			return false
		}
		if t.layoutState.Hidden[parts[1]] {
			return false // Already hidden.
		}
		if t.visibleCountFromState() <= 1 {
			t.cmd.error = "cannot hide last visible pane"
			return false
		}
		if t.layoutState.Hidden == nil {
			t.layoutState.Hidden = make(map[string]bool)
		}
		t.layoutState.Hidden[parts[1]] = true
		t.autoRecalcGrid()
		t.saveLayoutIfConfigured()

	case "view":
		if len(parts) < 2 {
			t.cmd.error = "usage: view <name1> [name2] ..."
			return false
		}
		for _, name := range parts[1:] {
			if t.findPaneByName(name) == nil {
				t.cmd.error = fmt.Sprintf("unknown agent %q", name)
				return false
			}
		}
		show := make(map[string]bool, len(parts)-1)
		for _, name := range parts[1:] {
			show[name] = true
		}
		// Check that at least one pane will be visible.
		visCount := 0
		for _, p := range t.panes {
			if show[p.name] {
				visCount++
			}
		}
		if visCount == 0 {
			t.cmd.error = "view must include at least one valid pane"
			return false
		}
		hidden := make(map[string]bool)
		for _, p := range t.panes {
			if !show[p.name] {
				hidden[p.name] = true
			}
		}
		t.layoutState.Hidden = hidden
		t.autoRecalcGrid()
		t.saveLayoutIfConfigured()

	case "layout":
		if len(parts) < 2 {
			t.cmd.error = "usage: layout reset"
			return false
		}
		switch parts[1] {
		case "reset":
			if t.projectRoot != "" {
				DeleteLayout(t.projectRoot)
			}
			names := make([]string, len(t.panes))
			for i, p := range t.panes {
				names[i] = p.name
			}
			t.layoutState = DefaultLayoutState(names)
			t.applyLayout()
		default:
			t.cmd.error = fmt.Sprintf("unknown layout subcommand %q", parts[1])
		}

	case "restart", "r":
		if err := t.restartFocused(); err != nil {
			t.cmd.error = fmt.Sprintf("restart failed: %v", err)
		}

	case "top", "ps":
		t.top.active = true
		t.top.selected = 0
		t.top.cacheTime = time.Time{} // Force fresh data.

	case "quit", "q":
		return true

	default:
		t.cmd.error = fmt.Sprintf("unknown command %q", parts[0])
	}
	return false
}

// parseGrid parses "CxR" or just "C" (auto-calculating rows from numPanes).
func parseGrid(s string, numPanes int) (cols, rows int, ok bool) {
	s = strings.ToLower(s)
	if strings.Contains(s, "x") {
		parts := strings.SplitN(s, "x", 2)
		if len(parts) != 2 {
			return 0, 0, false
		}
		c, err1 := strconv.Atoi(parts[0])
		r, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || c < 1 || r < 1 || c > 10 || r > 10 {
			return 0, 0, false
		}
		return c, r, true
	}
	// Just a column count; auto-calculate rows.
	c, err := strconv.Atoi(s)
	if err != nil || c < 1 || c > 10 {
		return 0, 0, false
	}
	r := (numPanes + c - 1) / c
	if r < 1 {
		r = 1
	}
	return c, r, true
}

// findPaneByName returns the pane with the given name, or nil.
func (t *TUI) findPaneByName(name string) *Pane {
	for _, p := range t.panes {
		if p.name == name {
			return p
		}
	}
	return nil
}

// visibleCountFromState returns the number of visible panes based on layoutState.
func (t *TUI) visibleCountFromState() int {
	n := 0
	for _, p := range t.panes {
		if !t.layoutState.Hidden[p.name] {
			n++
		}
	}
	return n
}

// autoRecalcGrid recalculates grid dimensions for the current visible count
// and applies the layout.
func (t *TUI) autoRecalcGrid() {
	if t.layoutState.Mode == LayoutGrid {
		c, r := autoGrid(t.visibleCountFromState())
		t.layoutState.GridCols = c
		t.layoutState.GridRows = r
	}
	t.applyLayout()
}

func (t *TUI) handleResize() {
	t.screen.Sync()
	t.applyLayout()
}

func (t *TUI) cycleFocus(delta int) {
	n := len(t.panes)
	if n == 0 {
		return
	}
	// Find current focused index.
	cur := 0
	for i, p := range t.panes {
		if p.name == t.layoutState.Focused {
			cur = i
			break
		}
	}
	// Skip hidden panes. Try every pane at most once.
	next := cur
	for i := 0; i < n; i++ {
		next = (next + delta + n) % n
		if !t.layoutState.Hidden[t.panes[next].name] {
			t.layoutState.Focused = t.panes[next].name
			t.applyLayout()
			return
		}
	}
}


// restartFocused kills the focused pane's process and starts a new one.
func (t *TUI) restartFocused() error {
	fp := t.focusedPane()
	if fp == nil {
		return fmt.Errorf("no pane focused")
	}
	idx := -1
	for i, p := range t.panes {
		if p == fp {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("focused pane not found")
	}
	cols := fp.emu.Width()
	rows := fp.emu.Height()
	fp.Close()

	p, err := NewPane(fp.cfg, rows, cols)
	if err != nil {
		return err
	}
	t.panes[idx] = p
	t.applyLayout()
	return nil
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
func (t *TUI) render() {
	s := t.screen

	// Detect dimension changes (font resize, window resize).
	w, h := s.Size()
	if w != t.lastW || h != t.lastH {
		t.lastW = w
		t.lastH = h
		t.applyLayout()
	}

	s.Clear()

	if t.top.active {
		// Full-screen activity monitor replaces pane rendering.
		t.renderTop()
		s.Show()
		return
	}

	// Draw panes from the render plan. No visibility checks needed.
	for _, pr := range t.plan.Panes {
		sel := t.selectionForPane(pr.Pane)
		pr.Pane.Render(s, pr.Focused, pr.Dimmed, pr.Index, sel)
	}

	// Draw dividers from the render plan.
	divStyle := tcell.StyleDefault.Foreground(tcell.ColorBlack)
	for _, d := range t.plan.Dividers {
		if d.Vertical {
			for y := d.Y; y < d.Y+d.Len; y++ {
				s.SetContent(d.X, y, '\u2502', nil, divStyle)
			}
		}
	}

	if t.layoutState.Overlay {
		t.renderOverlay()
	}

	// Command modal or error message at the bottom.
	if t.cmd.active {
		t.renderCmdLine()
	} else if t.cmd.error != "" {
		t.renderCmdError()
	}

	s.Show()
}


// selectionFor returns the selection state for a given pane index.
func (t *TUI) selectionFor(paneIdx int) Selection {
	if !t.sel.active || t.sel.pane != paneIdx {
		return Selection{}
	}
	return Selection{
		Active: true,
		StartX: t.sel.startX, StartY: t.sel.startY,
		EndX: t.sel.endX, EndY: t.sel.endY,
	}
}

// selectionForPane returns the selection state for a given pane.
func (t *TUI) selectionForPane(p *Pane) Selection {
	if !t.sel.active {
		return Selection{}
	}
	if t.sel.pane >= 0 && t.sel.pane < len(t.panes) && t.panes[t.sel.pane] == p {
		return Selection{
			Active: true,
			StartX: t.sel.startX, StartY: t.sel.startY,
			EndX: t.sel.endX, EndY: t.sel.endY,
		}
	}
	return Selection{}
}



// renderCmdLine draws the command input bar at the bottom of the screen.
func (t *TUI) renderCmdLine() {
	s := t.screen
	sw, sh := s.Size()
	y := sh - 1

	// Background for the entire line.
	bgStyle := tcell.StyleDefault.Background(tcell.ColorDarkSlateGray)
	for x := 0; x < sw; x++ {
		s.SetContent(x, y, ' ', nil, bgStyle)
	}

	// Prompt.
	promptStyle := bgStyle.Foreground(tcell.ColorYellow).Bold(true)
	s.SetContent(0, y, '>', nil, promptStyle)
	s.SetContent(1, y, ' ', nil, bgStyle)

	// Input text.
	textStyle := bgStyle.Foreground(tcell.ColorWhite)
	for i, ch := range t.cmd.buf {
		if 2+i < sw {
			s.SetContent(2+i, y, ch, nil, textStyle)
		}
	}

	// Cursor.
	cursorPos := 2 + len(t.cmd.buf)
	if cursorPos < sw {
		cursorStyle := tcell.StyleDefault.Background(tcell.ColorWhite).Foreground(tcell.ColorBlack)
		s.SetContent(cursorPos, y, ' ', nil, cursorStyle)
	}

	// Hint text on the right.
	hint := "Enter:run  Esc:cancel"
	hintStyle := bgStyle.Foreground(tcell.ColorGray)
	hintStart := sw - len(hint) - 1
	if hintStart > cursorPos+2 {
		for i, ch := range hint {
			s.SetContent(hintStart+i, y, ch, nil, hintStyle)
		}
	}
}

// renderCmdError draws an error message at the bottom of the screen.
func (t *TUI) renderCmdError() {
	s := t.screen
	sw, sh := s.Size()
	y := sh - 1

	errStyle := tcell.StyleDefault.Background(tcell.ColorDarkRed).Foreground(tcell.ColorWhite)
	for x := 0; x < sw; x++ {
		s.SetContent(x, y, ' ', nil, errStyle)
	}
	msg := " " + t.cmd.error
	for i, ch := range msg {
		if i < sw {
			s.SetContent(i, y, ch, nil, errStyle)
		}
	}
}

// ── Activity monitor (top) modal ─────────────────────────────────────

// topEntry holds process info for one pane.
type topEntry struct {
	Name    string
	PID     int
	Comm    string // Process name from ps.
	Command string // Launch command from config.
	RSS     int64  // Resident memory in KB.
	Status  string // running, idle, dead, hidden.
}

// refreshTopData queries ps for each pane and caches the result.
func (t *TUI) refreshTopData() {
	if time.Since(t.top.cacheTime) < 2*time.Second && len(t.top.data) > 0 {
		return
	}
	entries := make([]topEntry, len(t.panes))
	for i, p := range t.panes {
		e := topEntry{
			Name:    p.name,
			Command: strings.Join(p.cfg.Command, " "),
			Status:  p.Activity().String(),
		}
		if t.layoutState.Hidden[p.name] {
			e.Status += " [hidden]"
		}
		if p.pid > 0 {
			e.PID = p.pid
			// Query ps for RSS and process name.
			out, err := exec.Command("ps", "-o", "rss=,comm=", "-p",
				fmt.Sprintf("%d", e.PID)).Output()
			if err == nil {
				fields := strings.Fields(strings.TrimSpace(string(out)))
				if len(fields) >= 1 {
					if rss, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
						e.RSS = rss
					}
				}
				if len(fields) >= 2 {
					e.Comm = filepath.Base(fields[1])
				}
			}
		}
		entries[i] = e
	}
	t.top.data = entries
	t.top.cacheTime = time.Now()
}

// handleTopKey handles input while the top modal is active.
func (t *TUI) handleTopKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		t.top.active = false
		return false
	case tcell.KeyUp:
		if t.top.selected > 0 {
			t.top.selected--
		}
		return false
	case tcell.KeyDown:
		if t.top.selected < len(t.panes)-1 {
			t.top.selected++
		}
		return false
	case tcell.KeyRune:
		switch ev.Rune() {
		case '`':
			t.top.active = false
			return false
		case 'r':
			if t.top.selected >= 0 && t.top.selected < len(t.panes) {
				p := t.panes[t.top.selected]
				idx := t.top.selected
				// Use emulator dimensions, not stale region (hidden panes
				// have regions from when they were last visible).
				cols := p.emu.Width()
				rows := p.emu.Height()
				p.Close()
				np, err := NewPane(p.cfg, rows, cols)
				if err == nil {
					t.panes[idx] = np
					t.applyLayout() // Assigns correct region.
				}
				t.top.cacheTime = time.Time{} // Force refresh.
			}
			return false
		case 'k':
			if t.top.selected >= 0 && t.top.selected < len(t.panes) {
				p := t.panes[t.top.selected]
				if p.cmd != nil && p.cmd.Process != nil {
					p.cmd.Process.Kill()
				}
				t.top.cacheTime = time.Time{}
			}
			return false
		case 'h':
			if t.top.selected >= 0 && t.top.selected < len(t.panes) {
				name := t.panes[t.top.selected].name
				if t.layoutState.Hidden[name] {
					delete(t.layoutState.Hidden, name)
				} else {
					if t.visibleCountFromState() > 1 {
						if t.layoutState.Hidden == nil {
							t.layoutState.Hidden = make(map[string]bool)
						}
						t.layoutState.Hidden[name] = true
					}
				}
				t.autoRecalcGrid()
				t.top.cacheTime = time.Time{}
			}
			return false
		case 'q':
			t.top.active = false
			return false
		}
	}
	return false
}

// renderTop draws the full-screen activity monitor table.
func (t *TUI) renderTop() {
	t.refreshTopData()
	s := t.screen
	sw, sh := s.Size()
	if sw < 40 || sh < 5 {
		drawField(s, 0, 0, sw, "Terminal too narrow for top", tcell.StyleDefault.Foreground(tcell.ColorRed))
		return
	}

	headerStyle := tcell.StyleDefault.Bold(true).Foreground(tcell.ColorWhite)
	normalStyle := tcell.StyleDefault.Foreground(tcell.ColorSilver)
	selectedStyle := tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite)
	totalStyle := tcell.StyleDefault.Foreground(tcell.ColorYellow).Bold(true)
	helpStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)

	// Column widths.
	nameW := 10
	pidW := 8
	commW := 12
	rssW := 10
	statusW := 16
	cmdW := sw - nameW - pidW - commW - rssW - statusW - 6 // 6 for spacing
	if cmdW < 10 {
		cmdW = 10
	}

	// Header row.
	y := 1
	drawRow := func(row int, style tcell.Style, name, pid, comm, cmd, rss, status string) {
		x := 1
		drawField(s, x, row, nameW, name, style)
		x += nameW + 1
		drawField(s, x, row, pidW, pid, style)
		x += pidW + 1
		drawField(s, x, row, commW, comm, style)
		x += commW + 1
		drawField(s, x, row, cmdW, cmd, style)
		x += cmdW + 1
		drawField(s, x, row, rssW, rss, style)
		x += rssW + 1
		drawField(s, x, row, statusW, status, style)
	}

	// Title.
	title := " initech top "
	titleStyle := tcell.StyleDefault.Background(tcell.ColorDodgerBlue).Foreground(tcell.ColorBlack).Bold(true)
	for i, ch := range title {
		if 1+i < sw {
			s.SetContent(1+i, 0, ch, nil, titleStyle)
		}
	}

	drawRow(y, headerStyle, "AGENT", "PID", "PROCESS", "COMMAND", "RSS", "STATUS")
	y++
	// Separator.
	for x := 1; x < sw-1; x++ {
		s.SetContent(x, y, '\u2500', nil, tcell.StyleDefault.Foreground(tcell.ColorGray))
	}
	y++

	var totalRSS int64
	for i, e := range t.top.data {
		style := normalStyle
		if i == t.top.selected {
			style = selectedStyle
		}

		pid := "-"
		if e.PID > 0 {
			pid = fmt.Sprintf("%d", e.PID)
		}
		comm := e.Comm
		if comm == "" {
			comm = "-"
		}
		cmd := e.Command
		if cmd == "" {
			cmd = "-"
		}
		rss := "-"
		if e.RSS > 0 {
			totalRSS += e.RSS
			if e.RSS > 1048576 {
				rss = fmt.Sprintf("%.1f GB", float64(e.RSS)/1048576)
			} else if e.RSS > 1024 {
				rss = fmt.Sprintf("%.0f MB", float64(e.RSS)/1024)
			} else {
				rss = fmt.Sprintf("%d KB", e.RSS)
			}
		}
		status := e.Status
		if status == "" {
			status = "-"
		}

		drawRow(y, style, e.Name, pid, comm, cmd, rss, status)
		y++
		if y >= sh-3 {
			break
		}
	}

	// Total row.
	y++
	totalStr := "-"
	if totalRSS > 0 {
		if totalRSS > 1048576 {
			totalStr = fmt.Sprintf("%.1f GB", float64(totalRSS)/1048576)
		} else {
			totalStr = fmt.Sprintf("%.0f MB", float64(totalRSS)/1024)
		}
	}
	alive := 0
	dead := 0
	for _, e := range t.top.data {
		if e.PID > 0 {
			alive++
		} else {
			dead++
		}
	}
	summary := fmt.Sprintf("Total: %s (%d alive, %d dead)", totalStr, alive, dead)
	for i, ch := range summary {
		if 1+i < sw {
			s.SetContent(1+i, y, ch, nil, totalStyle)
		}
	}

	// Help line at bottom.
	help := "  [r]estart  [k]ill  [h]ide/show  [q/Esc] close"
	for i, ch := range help {
		if 1+i < sw {
			s.SetContent(1+i, sh-1, ch, nil, helpStyle)
		}
	}
}

// drawField writes a string into a fixed-width column, truncating if needed.
func drawField(s tcell.Screen, x, y, width int, text string, style tcell.Style) {
	if width <= 0 {
		return
	}
	runes := []rune(text)
	if len(runes) > width {
		runes = runes[:width-1]
		runes = append(runes, '\u2026') // ellipsis
	}
	for i, ch := range runes {
		s.SetContent(x+i, y, ch, nil, style)
	}
}

// renderOverlay draws the floating agent status panel.
func (t *TUI) renderOverlay() {
	s := t.screen

	agents := make([]AgentInfo, len(t.panes))
	maxNameLen := 0
	hiddenCount := 0
	for i, p := range t.panes {
		state := p.Activity()
		vis := !t.layoutState.Hidden[p.name]
		agents[i] = AgentInfo{Name: p.name, Status: state.String(), Visible: vis}
		nameLen := len(p.name)
		if !vis {
			nameLen += 4 // " [h]"
			hiddenCount++
		}
		if nameLen > maxNameLen {
			maxNameLen = nameLen
		}
	}

	statusMaxLen := 8 // "thinking"
	panelW := 4 + maxNameLen + 1 + statusMaxLen + 2
	// Extra row for summary line when there are hidden panes.
	summaryRow := hiddenCount > 0
	panelH := len(agents) + 2
	if summaryRow {
		panelH++
	}

	sw, sh := s.Size()
	px := sw - panelW - 1
	py := 1
	if px < 0 {
		px = 0
	}
	if px+panelW > sw {
		panelW = sw - px
	}
	if py+panelH > sh {
		panelH = sh - py
	}
	if panelW < 10 || panelH < 3 {
		return
	}

	bgStyle := tcell.StyleDefault.Background(tcell.ColorDarkBlue)
	borderStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorDarkBlue)

	// Top border with title.
	s.SetContent(px, py, '\u250c', nil, borderStyle)
	title := " Agents "
	for i := 1; i < panelW-1; i++ {
		ch := '\u2500'
		if i-1 < len(title) {
			ch = rune(title[i-1])
		}
		s.SetContent(px+i, py, ch, nil, borderStyle)
	}
	s.SetContent(px+panelW-1, py, '\u2510', nil, borderStyle)

	// Agent rows.
	for i, a := range agents {
		if i+2 >= panelH {
			break
		}
		row := py + 1 + i

		s.SetContent(px, row, '\u2502', nil, borderStyle)
		for x := px + 1; x < px+panelW-1; x++ {
			s.SetContent(x, row, ' ', nil, bgStyle)
		}

		// Status dot (color per activity state).
		dot := '\u25cf'
		var dotColor tcell.Color
		switch a.Status {
		case "running":
			dotColor = tcell.ColorGreen
		case "idle":
			dot = '\u25cb'
			dotColor = tcell.ColorGray
		default:
			dotColor = tcell.ColorGray
		}
		s.SetContent(px+2, row, dot, nil, bgStyle.Foreground(dotColor))

		// Name (dimmed for hidden panes).
		nameStyle := bgStyle.Foreground(tcell.ColorWhite)
		if a.Name == t.layoutState.Focused {
			nameStyle = bgStyle.Foreground(tcell.ColorYellow).Bold(true)
		} else if !a.Visible {
			nameStyle = bgStyle.Foreground(tcell.ColorDarkGray)
		}
		col := px + 4
		for _, ch := range a.Name {
			if col < px+panelW-1 {
				s.SetContent(col, row, ch, nil, nameStyle)
			}
			col++
		}
		// Hidden marker.
		if !a.Visible {
			markerStyle := bgStyle.Foreground(tcell.ColorDarkGray)
			for _, ch := range " [h]" {
				if col < px+panelW-1 {
					s.SetContent(col, row, ch, nil, markerStyle)
				}
				col++
			}
		}

		// Status text.
		statusStyle := bgStyle.Foreground(tcell.ColorSilver)
		if !a.Visible {
			statusStyle = bgStyle.Foreground(tcell.ColorDarkGray)
		}
		statusCol := px + 4 + maxNameLen + 1
		for j, ch := range a.Status {
			if statusCol+j < px+panelW-1 {
				s.SetContent(statusCol+j, row, ch, nil, statusStyle)
			}
		}

		s.SetContent(px+panelW-1, row, '\u2502', nil, borderStyle)
	}

	// Summary line (only when hidden panes exist).
	if summaryRow {
		sumRow := py + 1 + len(agents)
		if sumRow+1 < py+panelH {
			s.SetContent(px, sumRow, '\u2502', nil, borderStyle)
			for x := px + 1; x < px+panelW-1; x++ {
				s.SetContent(x, sumRow, ' ', nil, bgStyle)
			}
			visCount := len(agents) - hiddenCount
			summary := fmt.Sprintf(" %d visible, %d hidden", visCount, hiddenCount)
			sumStyle := bgStyle.Foreground(tcell.ColorSilver)
			for j, ch := range summary {
				if px+1+j < px+panelW-1 {
					s.SetContent(px+1+j, sumRow, ch, nil, sumStyle)
				}
			}
			s.SetContent(px+panelW-1, sumRow, '\u2502', nil, borderStyle)
		}
	}

	// Bottom border.
	botRow := py + panelH - 1
	s.SetContent(px, botRow, '\u2514', nil, borderStyle)
	for i := 1; i < panelW-1; i++ {
		s.SetContent(px+i, botRow, '\u2500', nil, borderStyle)
	}
	s.SetContent(px+panelW-1, botRow, '\u2518', nil, borderStyle)
}
