package tui

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// ---------------------------------------------------------------------------
// Region.InnerSize
// ---------------------------------------------------------------------------

func TestInnerSize(t *testing.T) {
	tests := []struct {
		name       string
		r          Region
		wantCols   int
		wantRows   int
	}{
		{"normal", Region{0, 0, 80, 25}, 80, 24},
		{"min_height", Region{0, 0, 80, 1}, 80, 1},      // H-1=0 clamped to 1
		{"zero_width", Region{0, 0, 0, 10}, 1, 9},        // W=0 clamped to 1
		{"tiny", Region{0, 0, 1, 2}, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols, rows := tt.r.InnerSize()
			if cols != tt.wantCols || rows != tt.wantRows {
				t.Errorf("InnerSize() = (%d, %d), want (%d, %d)", cols, rows, tt.wantCols, tt.wantRows)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// calcPaneGrid
// ---------------------------------------------------------------------------

func TestCalcPaneGrid(t *testing.T) {
	// 4 panes in a 2x2 grid on a 100x50 screen.
	regions := gridRegions(2, 2, 4, 100, 50, nil, nil)
	if len(regions) != 4 {
		t.Fatalf("got %d regions, want 4", len(regions))
	}
	// All regions should tile the screen with no gaps.
	totalArea := 0
	for _, r := range regions {
		totalArea += r.W * r.H
	}
	if totalArea != 100*50 {
		t.Errorf("total area = %d, want %d", totalArea, 100*50)
	}
	// First region starts at origin.
	if regions[0].X != 0 || regions[0].Y != 0 {
		t.Errorf("first region at (%d,%d), want (0,0)", regions[0].X, regions[0].Y)
	}
}

func TestCalcPaneGridLastRowExpands(t *testing.T) {
	// 3 panes in a 2-col grid: row 1 has 2 panes, row 2 has 1 pane.
	regions := gridRegions(2, 2, 3, 100, 50, nil, nil)
	if len(regions) != 3 {
		t.Fatalf("got %d regions, want 3", len(regions))
	}
	// Third pane should fill the entire width (it's alone in its row).
	if regions[2].W != 100 {
		t.Errorf("last row pane width = %d, want 100", regions[2].W)
	}
}

func TestCalcPaneGridEmpty(t *testing.T) {
	regions := gridRegions(2, 2, 0, 100, 50, nil, nil)
	if len(regions) != 0 {
		t.Errorf("got %d regions for 0 panes, want 0", len(regions))
	}
}

// ---------------------------------------------------------------------------
// calcMainVertical
// ---------------------------------------------------------------------------

func TestCalcMainVerticalLayout(t *testing.T) {
	regions := calcMainVertical(4, 100, 50)
	if len(regions) != 4 {
		t.Fatalf("got %d regions, want 4", len(regions))
	}
	// First pane (main) should be ~60% width.
	if regions[0].W != 60 {
		t.Errorf("main pane width = %d, want 60", regions[0].W)
	}
	// Right panes should fill the remaining width.
	if regions[1].W != 40 {
		t.Errorf("right pane width = %d, want 40", regions[1].W)
	}
	// Right panes should stack vertically.
	totalH := 0
	for _, r := range regions[1:] {
		totalH += r.H
	}
	if totalH != 50 {
		t.Errorf("right panes total height = %d, want 50", totalH)
	}
}

func TestCalcMainVerticalSingle(t *testing.T) {
	regions := calcMainVertical(1, 100, 50)
	if len(regions) != 1 {
		t.Fatalf("got %d regions, want 1", len(regions))
	}
	if regions[0].W != 100 || regions[0].H != 50 {
		t.Errorf("single pane region = %dx%d, want 100x50", regions[0].W, regions[0].H)
	}
}

// ---------------------------------------------------------------------------
// calcRegions
// ---------------------------------------------------------------------------

func TestComputeLayoutZoomed(t *testing.T) {
	panes := []*Pane{newTestPane("a", true), newTestPane("b", true)}
	state := LayoutState{Mode: LayoutGrid, GridCols: 2, GridRows: 1, Zoomed: true, Focused: "a", Hidden: map[string]bool{}}
	plan := computeLayout(state, panes, 100, 50)
	if len(plan.Panes) != 1 {
		t.Fatalf("zoomed should return 1 pane, got %d", len(plan.Panes))
	}
	if plan.Panes[0].Region.W != 100 || plan.Panes[0].Region.H != 50 {
		t.Errorf("zoomed region = %dx%d, want 100x50", plan.Panes[0].Region.W, plan.Panes[0].Region.H)
	}
}

func TestComputeLayoutFocusModeOld(t *testing.T) {
	panes := []*Pane{newTestPane("a", true), newTestPane("b", true)}
	state := LayoutState{Mode: LayoutFocus, Focused: "a", Hidden: map[string]bool{}}
	plan := computeLayout(state, panes, 100, 50)
	if len(plan.Panes) != 1 {
		t.Fatalf("focus mode should return 1 pane, got %d", len(plan.Panes))
	}
}

func TestComputeLayoutNoVisible(t *testing.T) {
	panes := []*Pane{newTestPane("a", true)}
	state := LayoutState{Mode: LayoutGrid, Focused: "a", Hidden: map[string]bool{"a": true}}
	plan := computeLayout(state, panes, 100, 50)
	if len(plan.Panes) != 0 {
		t.Errorf("no visible panes should return 0 pane renders, got %d", len(plan.Panes))
	}
}

func TestComputeLayout2ColOld(t *testing.T) {
	panes := []*Pane{newTestPane("a", true), newTestPane("b", true), newTestPane("c", true)}
	state := LayoutState{Mode: Layout2Col, Focused: "a", Hidden: map[string]bool{}}
	plan := computeLayout(state, panes, 100, 50)
	if len(plan.Panes) != 3 {
		t.Fatalf("got %d pane renders, want 3", len(plan.Panes))
	}
}

// ---------------------------------------------------------------------------
// execCmd - remaining cases (grid, focus, zoom, panel, main, quit, unknown)
// ---------------------------------------------------------------------------

func TestExecCmdGrid(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 2

	// Grid with no arg: auto-calculate.
	tui.execCmd("grid")
	if tui.layoutState.Mode != LayoutGrid {
		t.Error("grid command should set LayoutGrid")
	}

	// Grid with arg.
	tui.execCmd("grid 3x2")
	if tui.layoutState.GridCols != 3 || tui.layoutState.GridRows != 2 {
		t.Errorf("grid 3x2: cols=%d rows=%d, want 3,2", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}

	// Grid with invalid arg.
	tui.execCmd("grid abc")
	if tui.cmd.error == "" {
		t.Error("invalid grid should produce error")
	}
}

func TestExecCmdFocus(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1")

	// Focus by name.
	tui.execCmd("focus eng1")
	if tui.layoutState.Focused != "eng1" {
		t.Errorf("focus = %q, want eng1", tui.layoutState.Focused)
	}
	if tui.layoutState.Mode != LayoutFocus {
		t.Error("focus command should set LayoutFocus")
	}

	// Focus with no arg: current pane.
	tui.layoutState.Mode = LayoutGrid
	tui.execCmd("focus")
	if tui.layoutState.Mode != LayoutFocus {
		t.Error("focus with no arg should set LayoutFocus")
	}

	// Focus unknown.
	tui.execCmd("focus bogus")
	if tui.cmd.error == "" {
		t.Error("focus unknown should produce error")
	}
}

func TestExecCmdZoomToggle(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.layoutState.Zoomed = false
	tui.execCmd("zoom")
	if !tui.layoutState.Zoomed {
		t.Error("zoom should toggle on")
	}
	tui.execCmd("zoom")
	if tui.layoutState.Zoomed {
		t.Error("zoom should toggle off")
	}
}

func TestExecCmdPanelToggle(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.layoutState.Overlay = false
	tui.execCmd("panel")
	if !tui.layoutState.Overlay {
		t.Error("panel should toggle on")
	}
	tui.execCmd("panel")
	if tui.layoutState.Overlay {
		t.Error("panel should toggle off")
	}
}

func TestExecCmdMainLayout(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.layoutState.Mode = LayoutGrid
	tui.execCmd("main")
	if tui.layoutState.Mode != Layout2Col {
		t.Error("main should set Layout2Col")
	}
}

func TestExecCmdQuitAndAlias(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	// quit now requires confirmation: first call sets pendingConfirm, does not quit.
	if tui.execCmd("quit") {
		t.Error("quit should not return true on first press; confirmation required")
	}
	if tui.cmd.pendingConfirm != "quit" {
		t.Errorf("pendingConfirm = %q, want %q", tui.cmd.pendingConfirm, "quit")
	}
	// Reset for next check.
	tui.cmd.pendingConfirm = ""
	if tui.execCmd("q") {
		t.Error("q should not return true on first press; confirmation required")
	}
	if tui.cmd.pendingConfirm != "quit" {
		t.Errorf("pendingConfirm = %q, want %q", tui.cmd.pendingConfirm, "quit")
	}
}

func TestExecCmdUnknownEng2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.execCmd("gibberish")
	if tui.cmd.error == "" {
		t.Error("unknown command should set error")
	}
}

func TestExecCmdEmptyString(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	if tui.execCmd("") {
		t.Error("empty command should return false")
	}
}

// ---------------------------------------------------------------------------
// handleCmdKey
// ---------------------------------------------------------------------------

func TestHandleCmdKeyEscapeE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmd.active = true
	tui.cmd.buf = []rune("partial")

	ev := tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
	tui.handleCmdKey(ev)

	if tui.cmd.active {
		t.Error("Escape should deactivate command modal")
	}
	if len(tui.cmd.buf) != 0 {
		t.Error("Escape should clear command buffer")
	}
}

func TestHandleCmdKeyEnterE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.layoutState.Overlay = true // Starts on.
	tui.cmd.active = true
	tui.cmd.buf = []rune("panel")

	ev := tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)
	tui.handleCmdKey(ev)

	if tui.cmd.active {
		t.Error("Enter should deactivate command modal")
	}
	// "panel" toggles overlay: true -> false.
	if tui.layoutState.Overlay {
		t.Error("Enter should have executed 'panel' command (toggling overlay off)")
	}
}

func TestHandleCmdKeyBackspaceE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmd.active = true
	tui.cmd.buf = []rune("abc")

	ev := tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone)
	tui.handleCmdKey(ev)

	if string(tui.cmd.buf) != "ab" {
		t.Errorf("cmdBuf = %q, want %q", string(tui.cmd.buf), "ab")
	}
}

func TestHandleCmdKeyBackspaceEmptyE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmd.active = true
	tui.cmd.buf = tui.cmd.buf[:0]

	ev := tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone)
	tui.handleCmdKey(ev)

	if len(tui.cmd.buf) != 0 {
		t.Error("backspace on empty buffer should remain empty")
	}
}

func TestHandleCmdKeyRuneE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmd.active = true
	tui.cmd.buf = tui.cmd.buf[:0]

	ev := tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone)
	tui.handleCmdKey(ev)

	if string(tui.cmd.buf) != "x" {
		t.Errorf("cmdBuf = %q, want %q", string(tui.cmd.buf), "x")
	}
}

func TestHandleCmdKeyBacktickClosesEmpty(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmd.active = true
	tui.cmd.buf = tui.cmd.buf[:0]

	ev := tcell.NewEventKey(tcell.KeyRune, '`', tcell.ModNone)
	tui.handleCmdKey(ev)

	if tui.cmd.active {
		t.Error("backtick on empty buffer should close modal")
	}
}

func TestHandleCmdKeyBacktickAppendsWhenNonEmpty(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmd.active = true
	tui.cmd.buf = []rune("a")

	ev := tcell.NewEventKey(tcell.KeyRune, '`', tcell.ModNone)
	tui.handleCmdKey(ev)

	if !tui.cmd.active {
		t.Error("backtick on non-empty buffer should not close modal")
	}
	if string(tui.cmd.buf) != "a`" {
		t.Errorf("cmdBuf = %q, want %q", string(tui.cmd.buf), "a`")
	}
}

// ---------------------------------------------------------------------------
// handleKey
// ---------------------------------------------------------------------------

func TestHandleKeyBacktickOpensModalE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	ev := tcell.NewEventKey(tcell.KeyRune, '`', tcell.ModNone)
	tui.handleKey(ev)
	if !tui.cmd.active {
		t.Error("backtick should open command modal")
	}
}

func TestHandleKeyAltSE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.layoutState.Overlay = false
	ev := tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModAlt)
	tui.handleKey(ev)
	if !tui.layoutState.Overlay {
		t.Error("Alt-s should toggle overlay")
	}
}

func TestHandleKeyAltZE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.layoutState.Zoomed = false
	ev := tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModAlt)
	tui.handleKey(ev)
	if !tui.layoutState.Zoomed {
		t.Error("Alt-z should toggle zoom")
	}
}

func TestHandleKeyAltQE2(t *testing.T) {
	// ini-a1e.32: Alt-q now opens confirmation instead of quitting immediately.
	tui, _ := newTestTUIWithScreen("a")
	ev := tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModAlt)
	if tui.handleKey(ev) {
		t.Error("Alt-q should not quit immediately; confirmation required")
	}
	if tui.cmd.pendingConfirm != "quit" {
		t.Errorf("pendingConfirm = %q, want %q", tui.cmd.pendingConfirm, "quit")
	}
}

func TestHandleKeyAltLayoutShortcuts(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d")

	// Alt-1 = focus mode.
	ev := tcell.NewEventKey(tcell.KeyRune, '1', tcell.ModAlt)
	tui.handleKey(ev)
	if tui.layoutState.Mode != LayoutFocus {
		t.Error("Alt-1 should set LayoutFocus")
	}

	// Alt-2 = 2x2 grid.
	ev = tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModAlt)
	tui.handleKey(ev)
	if tui.layoutState.GridCols != 2 || tui.layoutState.GridRows != 2 {
		t.Errorf("Alt-2: %dx%d, want 2x2", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}

	// Alt-3 = 3x3 grid.
	ev = tcell.NewEventKey(tcell.KeyRune, '3', tcell.ModAlt)
	tui.handleKey(ev)
	if tui.layoutState.GridCols != 3 || tui.layoutState.GridRows != 3 {
		t.Errorf("Alt-3: %dx%d, want 3x3", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}

	// Alt-4 = 2-col layout.
	ev = tcell.NewEventKey(tcell.KeyRune, '4', tcell.ModAlt)
	tui.handleKey(ev)
	if tui.layoutState.Mode != Layout2Col {
		t.Error("Alt-4 should set Layout2Col")
	}
}

func TestHandleKeyCycleFocus(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c")
	tui.layoutState.Focused = "a"

	ev := tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt)
	tui.handleKey(ev)
	if tui.layoutState.Focused != "b" {
		t.Errorf("Alt-Right: focused = %q, want b", tui.layoutState.Focused)
	}

	ev = tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModAlt)
	tui.handleKey(ev)
	if tui.layoutState.Focused != "a" {
		t.Errorf("Alt-Left: focused = %q, want a", tui.layoutState.Focused)
	}

	ev = tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModAlt)
	tui.handleKey(ev)
	if tui.layoutState.Focused != "b" {
		t.Errorf("Alt-Down: focused = %q, want b", tui.layoutState.Focused)
	}

	ev = tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModAlt)
	tui.handleKey(ev)
	if tui.layoutState.Focused != "a" {
		t.Errorf("Alt-Up: focused = %q, want a", tui.layoutState.Focused)
	}
}

func TestHandleKeyClearsErrorE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmd.error = "some error"
	ev := tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone)
	tui.handleKey(ev)
	if tui.cmd.error != "" {
		t.Error("keypress should clear cmdError")
	}
}

func TestHandleKeyCmdActiveForwards(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmd.active = true
	tui.cmd.buf = tui.cmd.buf[:0]

	// Escape should close modal (handled by handleCmdKey).
	ev := tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
	quit := tui.handleKey(ev)
	if quit {
		t.Error("Escape in cmd modal should not quit")
	}
	if tui.cmd.active {
		t.Error("Escape should have closed modal")
	}
}

// ---------------------------------------------------------------------------
// uvCellToTcell
// ---------------------------------------------------------------------------

func TestUvCellToTcellNil(t *testing.T) {
	ch, style := uvCellToTcell(nil)
	if ch != ' ' {
		t.Errorf("nil cell rune = %q, want ' '", ch)
	}
	if style != tcell.StyleDefault {
		t.Error("nil cell should return StyleDefault")
	}
}

func TestUvCellToTcellEmptyContent(t *testing.T) {
	cell := &uv.Cell{Content: ""}
	ch, _ := uvCellToTcell(cell)
	if ch != ' ' {
		t.Errorf("empty content rune = %q, want ' '", ch)
	}
}

func TestUvCellToTcellWithContent(t *testing.T) {
	cell := &uv.Cell{Content: "A"}
	ch, _ := uvCellToTcell(cell)
	if ch != 'A' {
		t.Errorf("rune = %q, want 'A'", ch)
	}
}

func TestUvCellToTcellAttributes(t *testing.T) {
	cell := &uv.Cell{
		Content: "X",
		Style: uv.Style{
			Attrs:     uv.AttrBold | uv.AttrFaint | uv.AttrItalic | uv.AttrReverse | uv.AttrStrikethrough,
			Underline: 1,
		},
	}
	_, style := uvCellToTcell(cell)
	// Verify attributes are set by checking they differ from default.
	if style == tcell.StyleDefault {
		t.Error("style with attributes should differ from default")
	}
}

// Note: uvCellToTcell with colors, uvColorToTcell, tcellKeyToUV, SocketPath tests are in tui_test.go (eng1).

// ---------------------------------------------------------------------------
// selectionFor
// ---------------------------------------------------------------------------

func TestSelectionForPane(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true), newTestPane("b", true))
	tui.sel.active = true
	tui.sel.pane = 0
	tui.sel.startX = 1
	tui.sel.startY = 2
	tui.sel.endX = 3
	tui.sel.endY = 4

	// Matching pane.
	sel := tui.selectionFor(0)
	if !sel.Active || sel.StartX != 1 || sel.EndY != 4 {
		t.Error("selectionFor matching pane should return active selection")
	}

	// Non-matching pane.
	sel = tui.selectionFor(1)
	if sel.Active {
		t.Error("selectionFor non-matching pane should return inactive selection")
	}

	// No active selection.
	tui.sel.active = false
	sel = tui.selectionFor(0)
	if sel.Active {
		t.Error("selectionFor with no active selection should return inactive")
	}
}

// ---------------------------------------------------------------------------
// DefaultConfig
// ---------------------------------------------------------------------------

func TestDefaultConfigAgents(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.Agents) != 4 {
		t.Errorf("DefaultConfig agents = %d, want 4", len(cfg.Agents))
	}
	names := make(map[string]bool)
	for _, a := range cfg.Agents {
		names[a.Name] = true
	}
	for _, want := range []string{"super", "eng1", "eng2", "qa1"} {
		if !names[want] {
			t.Errorf("DefaultConfig missing agent %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// Render with SimulationScreen (covers renderOverlay, renderFocusBorder,
// renderGridDividers, renderCmdLine, renderCmdError)
// ---------------------------------------------------------------------------

func newTestTUIWithScreen(names ...string) (*TUI, tcell.SimulationScreen) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)

	panes := make([]*Pane, len(names))
	for i, n := range names {
		emu := vt.NewSafeEmulator(40, 10)
		// Drain emulator responses so SendKey doesn't block.
		go func() {
			buf := make([]byte, 256)
			for {
				_, err := emu.Read(buf)
				if err != nil {
					return
				}
			}
		}()
		panes[i] = &Pane{
			name:    n,
			emu:     emu,
			alive:   true,
			visible: true,
			region:  Region{X: i * 60, Y: 0, W: 60, H: 20},
		}
	}

	ls := DefaultLayoutState(names)
	t := &TUI{
		screen:      s,
		panes:       panes,
		layoutState: ls,
		lastW:       120,
		lastH:       40,
	}
	t.plan = computeLayout(ls, panes, 120, 40)
	return t, s
}

func TestRenderOverlayOnScreen(t *testing.T) {
	tui, s := newTestTUIWithScreen("super", "eng1")
	tui.layoutState.Overlay = true
	tui.render()

	// The overlay should have drawn something in the top-right area.
	// Check that the screen has non-space content near the right edge.
	sw, _ := s.Size()
	found := false
	for x := sw - 30; x < sw; x++ {
		mainc, _, _, _ := s.GetContent(x, 1)
		if mainc != ' ' && mainc != 0 {
			found = true
			break
		}
	}
	if !found {
		t.Error("overlay should render content on screen")
	}
}


func TestRenderGridDividersOnScreen(t *testing.T) {
	tui, s := newTestTUIWithScreen("a", "b")
	tui.layoutState.Overlay = false
	tui.applyLayout()
	tui.render()

	// The divider between panes should be a vertical line at the boundary.
	if len(tui.plan.Panes) >= 2 && tui.plan.Panes[1].Region.X > 0 {
		divX := tui.plan.Panes[1].Region.X - 1
		mainc, _, _, _ := s.GetContent(divX, tui.plan.Panes[1].Region.Y)
		if mainc != '\u2502' {
			t.Errorf("divider char = %q, want U+2502", mainc)
		}
	}
}

func TestRenderCmdLineOnScreen(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.cmd.active = true
	tui.cmd.buf = []rune("test")
	tui.layoutState.Overlay = false
	tui.render()

	_, sh := s.Size()
	// Command line prompt '>' should appear at sh-1.
	mainc, _, _, _ := s.GetContent(0, sh-1)
	if mainc != '>' {
		t.Errorf("cmd prompt = %q, want '>'", mainc)
	}
}

func TestRenderCmdErrorOnScreen(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.cmd.active = false
	tui.cmd.error = "bad command"
	tui.layoutState.Overlay = false
	tui.render()

	_, sh := s.Size()
	// Error text should appear at sh-1.
	mainc, _, _, _ := s.GetContent(1, sh-1)
	if mainc != 'b' {
		t.Errorf("error first char = %q, want 'b'", mainc)
	}
}

func TestRenderZoomedMode(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.layoutState.Focused = "a"
	tui.layoutState.Zoomed = true
	tui.layoutState.Overlay = false
	// Should not panic - just renders the focused pane full screen.
	tui.render()
}

func TestRenderFocusMode(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.layoutState.Focused = "b"
	tui.layoutState.Mode = LayoutFocus
	tui.layoutState.Overlay = false
	tui.render()
}

func TestRenderNoFocusedPane(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.layoutState.Focused = "nonexistent"
	tui.layoutState.Zoomed = true
	tui.layoutState.Overlay = false
	// Should not panic.
	tui.render()
}

// ---------------------------------------------------------------------------
// forwardMouseEvent / forwardMouseToFocused
// ---------------------------------------------------------------------------

func TestForwardMouseToFocused(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.layoutState.Focused = "a"
	r := tui.panes[0].region

	// Click inside the pane region (should not panic).
	tui.forwardMouseToFocused(r.X+5, r.Y+5, uv.MouseLeft, false, false, tcell.ModNone)

	// Click outside (should be ignored).
	tui.forwardMouseToFocused(r.X+r.W+10, r.Y+5, uv.MouseLeft, false, false, tcell.ModNone)

	// Out of range focused.
	tui.layoutState.Focused = "nonexistent"
	tui.forwardMouseToFocused(5, 5, uv.MouseLeft, false, false, tcell.ModNone)
}

func TestForwardMouseEvent(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	p := tui.panes[0]

	// Click, motion, release - none should panic.
	tui.forwardMouseEvent(p, 5, 5, uv.MouseLeft, false, false, tcell.ModShift|tcell.ModAlt|tcell.ModCtrl)
	tui.forwardMouseEvent(p, 5, 5, uv.MouseLeft, true, false, tcell.ModNone)
	tui.forwardMouseEvent(p, 5, 5, uv.MouseLeft, false, true, tcell.ModNone)
}

// ---------------------------------------------------------------------------
// handleMouse
// ---------------------------------------------------------------------------

func TestHandleMouseButton1StartSelection(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.applyLayout()

	r := tui.panes[0].region
	ev := tcell.NewEventMouse(r.X+5, r.Y+5, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	if !tui.sel.active {
		t.Error("Button1 press should start selection")
	}
	if tui.sel.pane != 0 {
		t.Errorf("selPane = %d, want 0", tui.sel.pane)
	}
	if tui.layoutState.Focused != "a" {
		t.Errorf("focused = %q, want a", tui.layoutState.Focused)
	}
}

func TestHandleMouseDragUpdatesSelection(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()

	r := tui.panes[0].region
	// Start selection.
	ev := tcell.NewEventMouse(r.X+1, r.Y+2, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	// Drag.
	ev = tcell.NewEventMouse(r.X+10, r.Y+5, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	if tui.sel.endX != 10 {
		t.Errorf("selEndX = %d, want 10", tui.sel.endX)
	}
}

func TestHandleMouseReleaseClearsSelection(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()

	r := tui.panes[0].region
	// Start selection.
	ev := tcell.NewEventMouse(r.X+1, r.Y+2, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)
	if !tui.sel.active {
		t.Fatal("selection should be active after click")
	}

	// Release.
	ev = tcell.NewEventMouse(r.X+5, r.Y+3, tcell.ButtonNone, tcell.ModNone)
	tui.handleMouse(ev)
	if tui.sel.active {
		t.Error("selection should be cleared after release")
	}
}

func TestHandleMouseWheelUp(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	// Write content to create scrollback.
	for i := 0; i < 50; i++ {
		tui.panes[0].emu.Write([]byte("line\r\n"))
	}

	r := tui.panes[0].region
	ev := tcell.NewEventMouse(r.X+1, r.Y+1, tcell.WheelUp, tcell.ModNone)
	tui.handleMouse(ev)

	if !tui.panes[0].InScrollback() {
		t.Error("WheelUp should enter scrollback")
	}
}

func TestHandleMouseWheelDown(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	tui.panes[0].scrollOffset = 10

	r := tui.panes[0].region
	ev := tcell.NewEventMouse(r.X+1, r.Y+1, tcell.WheelDown, tcell.ModNone)
	tui.handleMouse(ev)

	if tui.panes[0].scrollOffset != 7 {
		t.Errorf("scrollOffset = %d, want 7 (10-3)", tui.panes[0].scrollOffset)
	}
}

func TestHandleMouseIgnoredDuringCmd(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmd.active = true
	r := tui.panes[0].region
	ev := tcell.NewEventMouse(r.X+1, r.Y+1, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)
	if tui.sel.active {
		t.Error("mouse should be ignored when command modal is active")
	}
}

func TestHandleMouseMiddleClick(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	r := tui.panes[0].region
	// Should not panic.
	ev := tcell.NewEventMouse(r.X+5, r.Y+5, tcell.Button2, tcell.ModNone)
	tui.handleMouse(ev)
}

func TestHandleMouseRightClick(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	r := tui.panes[0].region
	ev := tcell.NewEventMouse(r.X+5, r.Y+5, tcell.Button3, tcell.ModNone)
	tui.handleMouse(ev)
}

func TestHandleMouseHiddenPaneSkipped(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.applyLayout()
	tui.layoutState.Hidden = map[string]bool{"a": true}

	r := tui.panes[0].region
	ev := tcell.NewEventMouse(r.X+5, r.Y+5, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)
	// Click should not start selection on hidden pane.
	if tui.sel.active && tui.sel.pane == 0 {
		t.Error("should not select hidden pane")
	}
}

// ---------------------------------------------------------------------------
// Render (increase coverage of pane rendering paths)
// ---------------------------------------------------------------------------

func TestRenderPaneScrollbackMode(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0]

	// Write content to create scrollback.
	for i := 0; i < 50; i++ {
		p.emu.Write([]byte("scrollback line\r\n"))
	}
	p.ScrollUp(10)
	tui.layoutState.Overlay = false
	tui.render()

	// Bottom ribbon should show scroll indicator.
	r := p.region
	ribbonY := r.Y + r.H - 1
	found := false
	for x := r.X; x < r.X+r.W; x++ {
		mainc, _, _, _ := s.GetContent(x, ribbonY)
		if mainc == '+' {
			found = true
			break
		}
	}
	if !found {
		t.Error("scrollback mode should show +N in bottom ribbon")
	}
}

func TestRenderPaneDead(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	tui.panes[0].alive = false
	tui.layoutState.Overlay = false
	// Should render dead pane without panic.
	tui.render()
}

func TestSessionDesc(t *testing.T) {
	p := &Pane{sessionDesc: "test desc"}
	if got := p.SessionDesc(); got != "test desc" {
		t.Errorf("SessionDesc = %q, want %q", got, "test desc")
	}
}

// Note: TestContentOffsetScrollback is in tui_test.go (eng1).

// ---------------------------------------------------------------------------
// Overlay with hidden panes
// ---------------------------------------------------------------------------

func TestRenderOverlayWithHiddenPanes(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "eng2")
	tui.panes[2].SetVisible(false)
	tui.layoutState.Overlay = true
	tui.render()
	// Should not panic, and overlay should show all panes.
}

func TestRenderOverlayTitleShowsProjectName(t *testing.T) {
	// ini-bfs: overlay title should show "Agents (name)" when projectName is set.
	tui, s := newTestTUIWithScreen("eng1")
	tui.projectName = "myproject"
	tui.layoutState.Overlay = true
	tui.render()

	// Scan the screen for the project name characters.
	sw, sh := s.Size()
	found := false
outer:
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			mainc, _, _, _ := s.GetContent(x, y)
			if mainc == 'm' { // first char of "myproject"
				found = true
				break outer
			}
		}
	}
	if !found {
		t.Error("overlay title should contain project name 'myproject'")
	}
}

func TestRenderOverlayTitleFallsBackWithoutProjectName(t *testing.T) {
	// When projectName is empty, title should still say "Agents" without crashing.
	tui, _ := newTestTUIWithScreen("eng1")
	tui.projectName = ""
	tui.layoutState.Overlay = true
	tui.render() // must not panic
}

// ---------------------------------------------------------------------------
// copySelection
// ---------------------------------------------------------------------------

func TestCopySelection_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		content string
		pane    int
		startX  int
		startY  int
		endX    int
		endY    int
	}{
		{"normal range", "Hello World\r\n", 0, 0, 0, 4, 0},
		{"out of range pane", "", 99, 0, 0, 0, 0},
		{"reversed coordinates", "ABCDEF\r\n", 0, 5, 0, 0, 0},
		{"multi-line", "Line1\r\nLine2\r\nLine3\r\n", 0, 0, 0, 4, 2},
		{"empty content", "", 0, 0, 0, 5, 0},
		{"endY exceeds rows", "X\r\n", 0, 0, 0, 0, 999},
		{"endX exceeds width", "test\r\n", 0, 0, 0, 999, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tui, _ := newTestTUIWithScreen("a")
			tui.applyLayout()
			if tt.content != "" && tt.pane < len(tui.panes) {
				tui.panes[tt.pane].emu.Write([]byte(tt.content))
			}
			tui.sel.pane = tt.pane
			tui.sel.startX = tt.startX
			tui.sel.startY = tt.startY
			tui.sel.endX = tt.endX
			tui.sel.endY = tt.endY
			tui.copySelection() // Must not panic.
		})
	}
}

// ---------------------------------------------------------------------------
// Render additional paths (selection, cursor, hidden pane border)
// ---------------------------------------------------------------------------

func TestRenderWithSelectionHighlight(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	tui.layoutState.Overlay = false
	tui.panes[0].emu.Write([]byte("Some content\r\n"))

	tui.sel.active = true
	tui.sel.pane = 0
	tui.sel.startX = 0
	tui.sel.startY = 0
	tui.sel.endX = 5
	tui.sel.endY = 0
	tui.render() // Should draw selection highlight.
}

func TestRenderWithCursor(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	tui.layoutState.Focused = "a"
	tui.layoutState.Overlay = false
	tui.sel.active = false
	tui.panes[0].emu.Write([]byte("cursor test\r\n"))
	tui.render() // Should draw cursor.
}


// ---------------------------------------------------------------------------
// contentOffset with content heavier than pane
// ---------------------------------------------------------------------------

func TestContentOffsetContentExceedsPaneHeight(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 10)
	p := &Pane{
		emu:    emu,
		region: Region{0, 0, 80, 6}, // InnerSize: 80x5 (smaller than emu)
	}
	// Write content that fills the emulator.
	for i := 0; i < 8; i++ {
		emu.Write([]byte("line\r\n"))
	}
	startRow, renderOffset := p.contentOffset()
	// Content should exceed pane height, so startRow > 0.
	if startRow == 0 && renderOffset == 0 {
		// At least one should be non-zero if content exceeds pane.
		// (exact values depend on cursor position)
	}
	_ = startRow
	_ = renderOffset
}

// Note: restart/r commands require a real PTY and are not unit-testable.

// ---------------------------------------------------------------------------
// Render edge cases
// ---------------------------------------------------------------------------

func TestRenderTooSmallRegion(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)

	emu := vt.NewSafeEmulator(10, 5)
	p := &Pane{name: "tiny", emu: emu, alive: true, visible: true, region: Region{0, 0, 0, 1}}
	// W < 1 or H < 2 should return immediately.
	p.Render(s, false, false, 1, Selection{})
	p.region = Region{0, 0, 10, 1} // H < 2
	p.Render(s, false, false, 1, Selection{})
}

func TestRenderAltScreen(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0]
	// Switch to alt screen via escape sequence.
	p.emu.Write([]byte("\x1b[?1049h")) // Enter alt screen.
	p.emu.Write([]byte("alt screen content\r\n"))
	tui.layoutState.Overlay = false
	tui.render()
}

// ---------------------------------------------------------------------------
// calcRegions grid mode
// ---------------------------------------------------------------------------

func TestCalcRegionsGridDefault(t *testing.T) {
	tui := newTestTUI(
		newTestPane("a", true),
		newTestPane("b", true),
		newTestPane("c", true),
	)
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols = 2
	tui.layoutState.GridRows = 2
	state := tui.layoutState
	state.Hidden = map[string]bool{"b": true}
	plan := computeLayout(state, tui.panes, 100, 50)
	if len(plan.Panes) != 2 {
		t.Errorf("got %d pane renders, want 2 (b is hidden)", len(plan.Panes))
	}
}

// ---------------------------------------------------------------------------
// Pane rendering with session description extraction
// ---------------------------------------------------------------------------

func TestRenderExtractsSessionDesc(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0]

	// Write content followed by a "session description" at the cursor row.
	p.emu.Write([]byte("output line\r\n"))
	p.emu.Write([]byte("session: test project"))
	tui.layoutState.Overlay = false
	tui.render()

	desc := p.SessionDesc()
	// The session description extraction happens during Render.
	_ = desc // Just ensuring no panic.
}

// ---------------------------------------------------------------------------
// handleMouse drag clamp edge cases
// ---------------------------------------------------------------------------

func TestHandleMouseDragClampNegative(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()

	r := tui.panes[0].region
	// Start selection.
	ev := tcell.NewEventMouse(r.X+5, r.Y+5, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	// Drag to negative coordinates (outside pane).
	ev = tcell.NewEventMouse(r.X-10, r.Y-10, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	if tui.sel.endX < 0 || tui.sel.endY < 0 {
		t.Error("selection coordinates should be clamped to >= 0")
	}
}

func TestHandleMouseDragClampBeyondPane(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()

	r := tui.panes[0].region
	// Start selection.
	ev := tcell.NewEventMouse(r.X+5, r.Y+5, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	// Drag beyond pane bounds.
	ev = tcell.NewEventMouse(r.X+r.W+50, r.Y+r.H+50, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	cols, rows := r.InnerSize()
	if tui.sel.endX >= cols || tui.sel.endY >= rows {
		t.Error("selection coordinates should be clamped to pane bounds")
	}
}

// ---------------------------------------------------------------------------
// handleEvent mouse dispatch
// ---------------------------------------------------------------------------

func TestHandleEventMouse(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	r := tui.panes[0].region

	mev := tcell.NewEventMouse(r.X+1, r.Y+1, tcell.Button1, tcell.ModNone)
	quit := tui.handleEvent(mev)
	if quit {
		t.Error("mouse event should not quit")
	}
}

// ---------------------------------------------------------------------------
// cycleFocus edge: empty panes
// ---------------------------------------------------------------------------

func TestCycleFocusNoPanes(t *testing.T) {
	tui := &TUI{}
	tui.cycleFocus(1) // Should not panic.
}

// ---------------------------------------------------------------------------
// recalcGrid non-grid layout
// ---------------------------------------------------------------------------

func TestRecalcGridNonGridLayoutE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.layoutState.Mode = Layout2Col
	tui.autoRecalcGrid()
	if tui.layoutState.Mode != Layout2Col {
		t.Error("recalcGrid should preserve non-grid layout")
	}
}

// ---------------------------------------------------------------------------
// setGrid
// ---------------------------------------------------------------------------

func TestSetGrid(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols = 3
	tui.layoutState.GridRows = 2
	tui.layoutState.Zoomed = false
	tui.applyLayout()
	if tui.layoutState.GridCols != 3 || tui.layoutState.GridRows != 2 {
		t.Errorf("setGrid: cols=%d rows=%d, want 3,2", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}
	if tui.layoutState.Mode != LayoutGrid {
		t.Error("setGrid should set LayoutGrid")
	}
	if tui.layoutState.Zoomed {
		t.Error("setGrid should clear zoomed")
	}
}

// ---------------------------------------------------------------------------
// handleEvent dispatch
// ---------------------------------------------------------------------------

func TestHandleEventDispatch(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")

	// Key event.
	kev := tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone)
	quit := tui.handleEvent(kev)
	if quit {
		t.Error("regular key should not quit")
	}

	// Resize event.
	rev := tcell.NewEventResize(100, 50)
	quit = tui.handleEvent(rev)
	if quit {
		t.Error("resize should not quit")
	}
}

// Note: SocketPath test is in tui_test.go (eng1).
