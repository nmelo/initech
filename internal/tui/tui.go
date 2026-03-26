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
