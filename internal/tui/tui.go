package tui

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

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

// TUI is the main terminal multiplexer. It owns the tcell screen,
// a set of terminal panes, and handles input routing, layout, and rendering.
type TUI struct {
	screen  tcell.Screen
	panes   []*Pane
	focused int
	layout  LayoutMode
	gridCols int // Used when layout == LayoutGrid.
	gridRows int
	zoomed  bool // When true, focused pane is full screen regardless of layout.
	overlay bool

	// Tracked screen dimensions for detecting resize.
	lastW, lastH int


	// Command modal state.
	cmdActive bool
	cmdBuf    []rune
	cmdError  string // Shown briefly after a bad command.

	// Mouse selection state.
	selActive bool
	selPane   int // Index of the pane being selected in.
	selStartX int // Start position in pane-local content coordinates.
	selStartY int
	selEndX   int // Current end position.
	selEndY   int
}

// Config controls what agents the TUI launches.
type Config struct {
	Agents      []PaneConfig // One entry per agent pane.
	ProjectName string       // Used for socket path.
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

	n := len(cfg.Agents)
	gridCols, gridRows := autoGrid(n)

	// Pre-read screen dimensions so the first render doesn't trigger a
	// spurious relayout (which would resize PTYs during Claude's init).
	initW, initH := screen.Size()

	t := &TUI{
		screen:   screen,
		layout:   LayoutGrid,
		gridCols: gridCols,
		gridRows: gridRows,
		overlay:  true,
		lastW:    initW,
		lastH:    initH,
	}

	// Start IPC socket server for inter-agent messaging.
	sockPath := SocketPath(cfg.ProjectName)
	if err := t.startIPC(sockPath); err != nil {
		return fmt.Errorf("start IPC: %w", err)
	}

	// Calculate initial layout.
	w, h := screen.Size()
	regions := t.calcRegions(w, h)

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
	if t.cmdActive {
		return
	}
	mx, my := ev.Position()

	switch {
	case ev.Buttons()&tcell.Button1 != 0 && !t.selActive:
		// Button1 press: start selection and focus the pane.
		for i, p := range t.panes {
			if !p.Visible() {
				continue
			}
			r := p.region
			if mx >= r.X && mx < r.X+r.W && my >= r.Y && my < r.Y+r.H {
				t.focused = i
				// Convert to pane-local content coordinates (below title bar).
				lx := mx - r.X
				ly := my - r.Y - 1 // -1 for title bar
				if ly < 0 {
					ly = 0
				}
				t.selActive = true
				t.selPane = i
				t.selStartX = lx
				t.selStartY = ly
				t.selEndX = lx
				t.selEndY = ly
				return
			}
		}

	case ev.Buttons()&tcell.Button1 != 0 && t.selActive:
		// Drag: update selection end.
		if t.selPane < len(t.panes) {
			r := t.panes[t.selPane].region
			lx := mx - r.X
			ly := my - r.Y - 1
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
			t.selEndX = lx
			t.selEndY = ly
		}

	case ev.Buttons() == tcell.ButtonNone && t.selActive:
		// Release: copy selection to clipboard and clear.
		t.copySelection()
		t.selActive = false

	case ev.Buttons()&tcell.WheelUp != 0:
		// Scroll back into history for the pane under cursor.
		for i, p := range t.panes {
			r := p.region
			if mx >= r.X && mx < r.X+r.W && my >= r.Y && my < r.Y+r.H {
				t.focused = i
				p.ScrollUp(3)
				return
			}
		}

	case ev.Buttons()&tcell.WheelDown != 0:
		// Scroll toward live view for the pane under cursor.
		for i, p := range t.panes {
			r := p.region
			if mx >= r.X && mx < r.X+r.W && my >= r.Y && my < r.Y+r.H {
				t.focused = i
				p.ScrollDown(3)
				return
			}
		}
	}
}

// copySelection extracts selected text from the pane's emulator and copies to clipboard.
func (t *TUI) copySelection() {
	if t.selPane >= len(t.panes) {
		return
	}
	p := t.panes[t.selPane]

	// Normalize selection bounds (start may be after end).
	r0, c0, r1, c1 := t.selStartY, t.selStartX, t.selEndY, t.selEndX
	if r0 > r1 || (r0 == r1 && c0 > c1) {
		r0, c0, r1, c1 = r1, c1, r0, c0
	}

	cols, rows := p.region.InnerSize()
	if r1 >= rows {
		r1 = rows - 1
	}

	var buf strings.Builder
	for row := r0; row <= r1; row++ {
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
			cell := p.emu.CellAt(col, row)
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
	// Command modal intercepts all input when active.
	if t.cmdActive {
		return t.handleCmdKey(ev)
	}

	// Clear any lingering error message on next keypress.
	t.cmdError = ""

	// Backtick opens the command modal.
	if ev.Key() == tcell.KeyRune && ev.Rune() == '`' && ev.Modifiers() == 0 {
		t.cmdActive = true
		t.cmdBuf = t.cmdBuf[:0]
		t.cmdError = ""
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
				t.layout = LayoutFocus
				t.zoomed = false
				t.relayout()
				return false
			case '2':
				t.setGrid(2, 2)
				return false
			case '3':
				t.setGrid(3, 3)
				return false
			case '4':
				t.layout = Layout2Col
				t.zoomed = false
				t.relayout()
				return false
			case 's':
				t.overlay = !t.overlay
				return false
			case 'z':
				t.zoomed = !t.zoomed
				t.relayout()
				return false
			case 'q':
				return true
			}
		}
	}

	// Everything else goes to the focused pane.
	if t.focused >= 0 && t.focused < len(t.panes) {
		t.panes[t.focused].SendKey(ev)
	}
	return false
}

// handleCmdKey processes key events while the command modal is open.
func (t *TUI) handleCmdKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape:
		t.cmdActive = false
		t.cmdBuf = t.cmdBuf[:0]
		return false
	case tcell.KeyEnter:
		cmd := strings.TrimSpace(string(t.cmdBuf))
		t.cmdActive = false
		t.cmdBuf = t.cmdBuf[:0]
		return t.execCmd(cmd)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(t.cmdBuf) > 0 {
			t.cmdBuf = t.cmdBuf[:len(t.cmdBuf)-1]
		}
		return false
	case tcell.KeyRune:
		// Backtick while empty closes the modal.
		if ev.Rune() == '`' && len(t.cmdBuf) == 0 {
			t.cmdActive = false
			return false
		}
		t.cmdBuf = append(t.cmdBuf, ev.Rune())
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
			// No argument: auto-calculate optimal grid.
			c, r := autoGrid(t.visibleCount())
			t.setGrid(c, r)
			return false
		}
		cols, rows, ok := parseGrid(parts[1], t.visibleCount())
		if !ok {
			t.cmdError = fmt.Sprintf("invalid grid %q, use CxR or just C (e.g. 3x3, 4)", parts[1])
			return false
		}
		t.setGrid(cols, rows)

	case "focus":
		if len(parts) < 2 {
			// No argument: switch to focus mode on current pane.
			t.layout = LayoutFocus
			t.zoomed = false
			t.relayout()
			return false
		}
		name := parts[1]
		for i, p := range t.panes {
			if p.name == name {
				t.focused = i
				t.layout = LayoutFocus
				t.zoomed = false
				t.relayout()
				return false
			}
		}
		t.cmdError = fmt.Sprintf("unknown agent %q", name)

	case "zoom":
		t.zoomed = !t.zoomed
		t.relayout()

	case "panel":
		t.overlay = !t.overlay

	case "main":
		t.layout = Layout2Col
		t.zoomed = false
		t.relayout()

	case "restart", "r":
		if err := t.restartFocused(); err != nil {
			t.cmdError = fmt.Sprintf("restart failed: %v", err)
		}

	case "quit", "q":
		return true

	default:
		t.cmdError = fmt.Sprintf("unknown command %q", parts[0])
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
	return c, r, true
}

func (t *TUI) setGrid(cols, rows int) {
	t.layout = LayoutGrid
	t.gridCols = cols
	t.gridRows = rows
	t.zoomed = false
	t.relayout()
}

func (t *TUI) handleResize() {
	t.screen.Sync()
	t.relayout()
}

func (t *TUI) cycleFocus(delta int) {
	n := len(t.panes)
	if n == 0 {
		return
	}
	// Skip hidden panes. Try every pane at most once.
	next := t.focused
	for i := 0; i < n; i++ {
		next = (next + delta + n) % n
		if t.panes[next].Visible() {
			t.focused = next
			return
		}
	}
}

// visiblePanes returns only the panes that are currently shown in the layout.
func (t *TUI) visiblePanes() []*Pane {
	vis := make([]*Pane, 0, len(t.panes))
	for _, p := range t.panes {
		if p.Visible() {
			vis = append(vis, p)
		}
	}
	return vis
}

// allPanes returns every pane regardless of visibility.
func (t *TUI) allPanes() []*Pane {
	return t.panes
}

// visibleCount returns the number of visible panes.
func (t *TUI) visibleCount() int {
	n := 0
	for _, p := range t.panes {
		if p.Visible() {
			n++
		}
	}
	return n
}

// paneIndex returns the index of a pane in t.panes, or -1 if not found.
func (t *TUI) paneIndex(p *Pane) int {
	for i, pp := range t.panes {
		if pp == p {
			return i
		}
	}
	return -1
}

// restartFocused kills the focused pane's process and starts a new one.
func (t *TUI) restartFocused() error {
	if t.focused < 0 || t.focused >= len(t.panes) {
		return fmt.Errorf("no pane focused")
	}
	old := t.panes[t.focused]
	r := old.region
	cols, rows := r.InnerSize()

	// Kill the old process.
	old.Close()

	// Create a new pane with the same config and region.
	p, err := NewPane(old.cfg, rows, cols)
	if err != nil {
		return err
	}
	p.region = r
	t.panes[t.focused] = p
	return nil
}

func (t *TUI) relayout() {
	w, h := t.screen.Size()
	regions := t.calcRegions(w, h)
	// Only visible panes get new regions and resizes.
	// Hidden panes keep their emulator running at their last visible size.
	vis := t.visiblePanes()
	for i, p := range vis {
		if i < len(regions) {
			r := regions[i]
			p.region = r
			cols, rows := r.InnerSize()
			p.Resize(rows, cols)
		}
	}
}

// calcRegions computes pane regions for the current layout.
// Regions are calculated for visible panes only, filling the screen.
// The last row may have fewer columns and those panes expand to fill the width.
func (t *TUI) calcRegions(screenW, screenH int) []Region {
	n := t.visibleCount()
	if n == 0 {
		return nil
	}

	if t.zoomed || t.layout == LayoutFocus {
		return []Region{{X: 0, Y: 0, W: screenW, H: screenH}}
	}

	switch t.layout {
	case LayoutGrid:
		return calcPaneGrid(t.gridCols, t.gridRows, n, screenW, screenH)
	case Layout2Col:
		return calcMainVertical(n, screenW, screenH)
	default:
		return calcPaneGrid(t.gridCols, t.gridRows, n, screenW, screenH)
	}
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
// Full rows have gridCols columns. The last row gets the remaining panes,
// and those panes expand to fill the full width.
func calcPaneGrid(gridCols, gridRows, numPanes, screenW, screenH int) []Region {
	if numPanes <= 0 {
		return nil
	}

	// Number of rows actually needed.
	rows := (numPanes + gridCols - 1) / gridCols
	if rows > gridRows {
		rows = gridRows
	}

	// Row heights: divide screen evenly.
	cellH := screenH / rows
	extraH := screenH - cellH*rows

	regions := make([]Region, 0, numPanes)
	y := 0
	placed := 0
	for r := 0; r < rows && placed < numPanes; r++ {
		h := cellH
		if r < extraH {
			h++
		}

		// How many panes in this row?
		colsThisRow := gridCols
		remaining := numPanes - placed
		if remaining < gridCols {
			colsThisRow = remaining
		}

		// Column widths for this row.
		cellW := screenW / colsThisRow
		extraW := screenW - cellW*colsThisRow

		x := 0
		for c := 0; c < colsThisRow; c++ {
			w := cellW
			if c < extraW {
				w++
			}
			regions = append(regions, Region{X: x, Y: y, W: w, H: h})
			x += w
			placed++
		}
		y += h
	}
	return regions
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
func (t *TUI) render() {
	s := t.screen

	// Detect dimension changes (font resize, window resize).
	w, h := s.Size()
	if w != t.lastW || h != t.lastH {
		t.lastW = w
		t.lastH = h
		t.relayout()
	}

	s.Clear()

	if t.zoomed || t.layout == LayoutFocus {
		if t.focused >= 0 && t.focused < len(t.panes) && t.panes[t.focused].Visible() {
			sel := t.selectionFor(t.focused)
			t.panes[t.focused].Render(s, true, sel)
		}
	} else {
		vis := t.visiblePanes()
		regions := t.calcRegions(t.screenSize())
		for i, p := range vis {
			if i < len(regions) {
				idx := t.paneIndex(p)
				sel := t.selectionFor(idx)
				p.Render(s, idx == t.focused, sel)
			}
		}
		// Draw thin black vertical dividers between columns.
		t.renderGridDividers(regions)
		// Draw focused pane borders AFTER dividers so they aren't overwritten.
		t.renderFocusBorder()
	}

	if t.overlay {
		t.renderOverlay()
	}

	// Command modal or error message at the bottom.
	if t.cmdActive {
		t.renderCmdLine()
	} else if t.cmdError != "" {
		t.renderCmdError()
	}

	s.Show()
}

func (t *TUI) screenSize() (int, int) {
	return t.screen.Size()
}

// selectionFor returns the selection state for a given pane index.
func (t *TUI) selectionFor(paneIdx int) Selection {
	if !t.selActive || t.selPane != paneIdx {
		return Selection{}
	}
	return Selection{
		Active: true,
		StartX: t.selStartX, StartY: t.selStartY,
		EndX: t.selEndX, EndY: t.selEndY,
	}
}

// renderFocusBorder highlights the focused pane's left and right edges.
// Instead of drawing U+2502 (which overwrites content like the prompt character),
// it tints the background of existing cells to DodgerBlue, preserving their content.
// Called after renderGridDividers so the tint isn't overwritten by dividers.
func (t *TUI) renderFocusBorder() {
	if t.focused < 0 || t.focused >= len(t.panes) {
		return
	}
	p := t.panes[t.focused]
	if !p.Visible() {
		return
	}
	r := p.region
	s := t.screen
	for y := r.Y + 1; y < r.Y+r.H; y++ {
		// Left edge: preserve content, tint background.
		mainc, combc, style, _ := s.GetContent(r.X, y)
		s.SetContent(r.X, y, mainc, combc, style.Background(tcell.ColorDodgerBlue))
		// Right edge.
		mainc, combc, style, _ = s.GetContent(r.X+r.W-1, y)
		s.SetContent(r.X+r.W-1, y, mainc, combc, style.Background(tcell.ColorDodgerBlue))
	}
}

// renderGridDividers draws thin black vertical lines between pane columns.
// Each row may have different column boundaries, so dividers are drawn per-row.
func (t *TUI) renderGridDividers(regions []Region) {
	if len(regions) < 2 {
		return
	}
	s := t.screen
	divStyle := tcell.StyleDefault.Foreground(tcell.ColorBlack)

	// Group regions by row (same Y value).
	type rowInfo struct {
		y, h int
		xs   []int // X positions > 0 (column boundaries within this row)
	}
	rowMap := make(map[int]*rowInfo)
	for _, r := range regions {
		ri, ok := rowMap[r.Y]
		if !ok {
			ri = &rowInfo{y: r.Y, h: r.H}
			rowMap[r.Y] = ri
		}
		if r.X > 0 {
			ri.xs = append(ri.xs, r.X)
		}
	}

	for _, ri := range rowMap {
		for _, x := range ri.xs {
			for y := ri.y; y < ri.y+ri.h; y++ {
				s.SetContent(x-1, y, '\u2502', nil, divStyle)
			}
		}
	}
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
	for i, ch := range t.cmdBuf {
		if 2+i < sw {
			s.SetContent(2+i, y, ch, nil, textStyle)
		}
	}

	// Cursor.
	cursorPos := 2 + len(t.cmdBuf)
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
	msg := " " + t.cmdError
	for i, ch := range msg {
		if i < sw {
			s.SetContent(i, y, ch, nil, errStyle)
		}
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
		vis := p.Visible()
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
		if i == t.focused {
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
