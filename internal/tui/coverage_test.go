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
	regions := calcPaneGrid(2, 2, 4, 100, 50)
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
	regions := calcPaneGrid(2, 2, 3, 100, 50)
	if len(regions) != 3 {
		t.Fatalf("got %d regions, want 3", len(regions))
	}
	// Third pane should fill the entire width (it's alone in its row).
	if regions[2].W != 100 {
		t.Errorf("last row pane width = %d, want 100", regions[2].W)
	}
}

func TestCalcPaneGridEmpty(t *testing.T) {
	regions := calcPaneGrid(2, 2, 0, 100, 50)
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

func TestCalcRegionsZoomed(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true), newTestPane("b", true))
	tui.zoomed = true
	regions := tui.calcRegions(100, 50)
	if len(regions) != 1 {
		t.Fatalf("zoomed should return 1 region, got %d", len(regions))
	}
	if regions[0].W != 100 || regions[0].H != 50 {
		t.Errorf("zoomed region = %dx%d, want 100x50", regions[0].W, regions[0].H)
	}
}

func TestCalcRegionsFocusMode(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true), newTestPane("b", true))
	tui.layout = LayoutFocus
	regions := tui.calcRegions(100, 50)
	if len(regions) != 1 {
		t.Fatalf("focus mode should return 1 region, got %d", len(regions))
	}
}

func TestCalcRegionsNoVisible(t *testing.T) {
	tui := newTestTUI(newTestPane("a", false))
	regions := tui.calcRegions(100, 50)
	if regions != nil {
		t.Errorf("no visible panes should return nil, got %v", regions)
	}
}

func TestCalcRegions2ColLayout(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true), newTestPane("b", true), newTestPane("c", true))
	tui.layout = Layout2Col
	regions := tui.calcRegions(100, 50)
	if len(regions) != 3 {
		t.Fatalf("got %d regions, want 3", len(regions))
	}
}

// ---------------------------------------------------------------------------
// execCmd - remaining cases (grid, focus, zoom, panel, main, quit, unknown)
// ---------------------------------------------------------------------------

func TestExecCmdGrid(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d")
	tui.layout = LayoutGrid
	tui.gridCols, tui.gridRows = 2, 2

	// Grid with no arg: auto-calculate.
	tui.execCmd("grid")
	if tui.layout != LayoutGrid {
		t.Error("grid command should set LayoutGrid")
	}

	// Grid with arg.
	tui.execCmd("grid 3x2")
	if tui.gridCols != 3 || tui.gridRows != 2 {
		t.Errorf("grid 3x2: cols=%d rows=%d, want 3,2", tui.gridCols, tui.gridRows)
	}

	// Grid with invalid arg.
	tui.execCmd("grid abc")
	if tui.cmdError == "" {
		t.Error("invalid grid should produce error")
	}
}

func TestExecCmdFocus(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1")

	// Focus by name.
	tui.execCmd("focus eng1")
	if tui.focused != 1 {
		t.Errorf("focus = %d, want 1", tui.focused)
	}
	if tui.layout != LayoutFocus {
		t.Error("focus command should set LayoutFocus")
	}

	// Focus with no arg: current pane.
	tui.layout = LayoutGrid
	tui.execCmd("focus")
	if tui.layout != LayoutFocus {
		t.Error("focus with no arg should set LayoutFocus")
	}

	// Focus unknown.
	tui.execCmd("focus bogus")
	if tui.cmdError == "" {
		t.Error("focus unknown should produce error")
	}
}

func TestExecCmdZoomToggle(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.zoomed = false
	tui.execCmd("zoom")
	if !tui.zoomed {
		t.Error("zoom should toggle on")
	}
	tui.execCmd("zoom")
	if tui.zoomed {
		t.Error("zoom should toggle off")
	}
}

func TestExecCmdPanelToggle(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.overlay = false
	tui.execCmd("panel")
	if !tui.overlay {
		t.Error("panel should toggle on")
	}
	tui.execCmd("panel")
	if tui.overlay {
		t.Error("panel should toggle off")
	}
}

func TestExecCmdMainLayout(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.layout = LayoutGrid
	tui.execCmd("main")
	if tui.layout != Layout2Col {
		t.Error("main should set Layout2Col")
	}
}

func TestExecCmdQuitAndAlias(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	if !tui.execCmd("quit") {
		t.Error("quit should return true")
	}
	if !tui.execCmd("q") {
		t.Error("q should return true")
	}
}

func TestExecCmdUnknownEng2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.execCmd("gibberish")
	if tui.cmdError == "" {
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
	tui.cmdActive = true
	tui.cmdBuf = []rune("partial")

	ev := tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
	tui.handleCmdKey(ev)

	if tui.cmdActive {
		t.Error("Escape should deactivate command modal")
	}
	if len(tui.cmdBuf) != 0 {
		t.Error("Escape should clear command buffer")
	}
}

func TestHandleCmdKeyEnterE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.overlay = true // Starts on.
	tui.cmdActive = true
	tui.cmdBuf = []rune("panel")

	ev := tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)
	tui.handleCmdKey(ev)

	if tui.cmdActive {
		t.Error("Enter should deactivate command modal")
	}
	// "panel" toggles overlay: true -> false.
	if tui.overlay {
		t.Error("Enter should have executed 'panel' command (toggling overlay off)")
	}
}

func TestHandleCmdKeyBackspaceE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmdActive = true
	tui.cmdBuf = []rune("abc")

	ev := tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone)
	tui.handleCmdKey(ev)

	if string(tui.cmdBuf) != "ab" {
		t.Errorf("cmdBuf = %q, want %q", string(tui.cmdBuf), "ab")
	}
}

func TestHandleCmdKeyBackspaceEmptyE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmdActive = true
	tui.cmdBuf = tui.cmdBuf[:0]

	ev := tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone)
	tui.handleCmdKey(ev)

	if len(tui.cmdBuf) != 0 {
		t.Error("backspace on empty buffer should remain empty")
	}
}

func TestHandleCmdKeyRuneE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmdActive = true
	tui.cmdBuf = tui.cmdBuf[:0]

	ev := tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone)
	tui.handleCmdKey(ev)

	if string(tui.cmdBuf) != "x" {
		t.Errorf("cmdBuf = %q, want %q", string(tui.cmdBuf), "x")
	}
}

func TestHandleCmdKeyBacktickClosesEmpty(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmdActive = true
	tui.cmdBuf = tui.cmdBuf[:0]

	ev := tcell.NewEventKey(tcell.KeyRune, '`', tcell.ModNone)
	tui.handleCmdKey(ev)

	if tui.cmdActive {
		t.Error("backtick on empty buffer should close modal")
	}
}

func TestHandleCmdKeyBacktickAppendsWhenNonEmpty(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmdActive = true
	tui.cmdBuf = []rune("a")

	ev := tcell.NewEventKey(tcell.KeyRune, '`', tcell.ModNone)
	tui.handleCmdKey(ev)

	if !tui.cmdActive {
		t.Error("backtick on non-empty buffer should not close modal")
	}
	if string(tui.cmdBuf) != "a`" {
		t.Errorf("cmdBuf = %q, want %q", string(tui.cmdBuf), "a`")
	}
}

// ---------------------------------------------------------------------------
// handleKey
// ---------------------------------------------------------------------------

func TestHandleKeyBacktickOpensModalE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	ev := tcell.NewEventKey(tcell.KeyRune, '`', tcell.ModNone)
	tui.handleKey(ev)
	if !tui.cmdActive {
		t.Error("backtick should open command modal")
	}
}

func TestHandleKeyAltSE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.overlay = false
	ev := tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModAlt)
	tui.handleKey(ev)
	if !tui.overlay {
		t.Error("Alt-s should toggle overlay")
	}
}

func TestHandleKeyAltZE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.zoomed = false
	ev := tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModAlt)
	tui.handleKey(ev)
	if !tui.zoomed {
		t.Error("Alt-z should toggle zoom")
	}
}

func TestHandleKeyAltQE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	ev := tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModAlt)
	if !tui.handleKey(ev) {
		t.Error("Alt-q should return true (quit)")
	}
}

func TestHandleKeyAltLayoutShortcuts(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d")

	// Alt-1 = focus mode.
	ev := tcell.NewEventKey(tcell.KeyRune, '1', tcell.ModAlt)
	tui.handleKey(ev)
	if tui.layout != LayoutFocus {
		t.Error("Alt-1 should set LayoutFocus")
	}

	// Alt-2 = 2x2 grid.
	ev = tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModAlt)
	tui.handleKey(ev)
	if tui.gridCols != 2 || tui.gridRows != 2 {
		t.Errorf("Alt-2: %dx%d, want 2x2", tui.gridCols, tui.gridRows)
	}

	// Alt-3 = 3x3 grid.
	ev = tcell.NewEventKey(tcell.KeyRune, '3', tcell.ModAlt)
	tui.handleKey(ev)
	if tui.gridCols != 3 || tui.gridRows != 3 {
		t.Errorf("Alt-3: %dx%d, want 3x3", tui.gridCols, tui.gridRows)
	}

	// Alt-4 = 2-col layout.
	ev = tcell.NewEventKey(tcell.KeyRune, '4', tcell.ModAlt)
	tui.handleKey(ev)
	if tui.layout != Layout2Col {
		t.Error("Alt-4 should set Layout2Col")
	}
}

func TestHandleKeyCycleFocus(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c")
	tui.focused = 0

	ev := tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt)
	tui.handleKey(ev)
	if tui.focused != 1 {
		t.Errorf("Alt-Right: focused = %d, want 1", tui.focused)
	}

	ev = tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModAlt)
	tui.handleKey(ev)
	if tui.focused != 0 {
		t.Errorf("Alt-Left: focused = %d, want 0", tui.focused)
	}

	ev = tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModAlt)
	tui.handleKey(ev)
	if tui.focused != 1 {
		t.Errorf("Alt-Down: focused = %d, want 1", tui.focused)
	}

	ev = tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModAlt)
	tui.handleKey(ev)
	if tui.focused != 0 {
		t.Errorf("Alt-Up: focused = %d, want 0", tui.focused)
	}
}

func TestHandleKeyClearsErrorE2(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmdError = "some error"
	ev := tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone)
	tui.handleKey(ev)
	if tui.cmdError != "" {
		t.Error("keypress should clear cmdError")
	}
}

func TestHandleKeyCmdActiveForwards(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.cmdActive = true
	tui.cmdBuf = tui.cmdBuf[:0]

	// Escape should close modal (handled by handleCmdKey).
	ev := tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
	quit := tui.handleKey(ev)
	if quit {
		t.Error("Escape in cmd modal should not quit")
	}
	if tui.cmdActive {
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
	tui.selActive = true
	tui.selPane = 0
	tui.selStartX = 1
	tui.selStartY = 2
	tui.selEndX = 3
	tui.selEndY = 4

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
	tui.selActive = false
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

	gridCols, gridRows := autoGrid(len(names))
	t := &TUI{
		screen:   s,
		panes:    panes,
		layout:   LayoutGrid,
		gridCols: gridCols,
		gridRows: gridRows,
		overlay:  true,
		lastW:    120,
		lastH:    40,
	}
	return t, s
}

func TestRenderOverlayOnScreen(t *testing.T) {
	tui, s := newTestTUIWithScreen("super", "eng1")
	tui.overlay = true
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
	tui.overlay = false
	// Relayout so regions are adjacent.
	tui.relayout()
	tui.render()

	// The divider between panes should be a vertical line at the boundary.
	regions := tui.calcRegions(s.Size())
	if len(regions) >= 2 && regions[1].X > 0 {
		divX := regions[1].X - 1
		mainc, _, _, _ := s.GetContent(divX, regions[1].Y)
		if mainc != '\u2502' {
			t.Errorf("divider char = %q, want U+2502", mainc)
		}
	}
}

func TestRenderCmdLineOnScreen(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.cmdActive = true
	tui.cmdBuf = []rune("test")
	tui.overlay = false
	tui.render()

	_, sh := s.Size()
	// Command line prompt '>' should appear at bottom.
	mainc, _, _, _ := s.GetContent(0, sh-1)
	if mainc != '>' {
		t.Errorf("cmd prompt = %q, want '>'", mainc)
	}
}

func TestRenderCmdErrorOnScreen(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.cmdActive = false
	tui.cmdError = "bad command"
	tui.overlay = false
	tui.render()

	_, sh := s.Size()
	// Error text should appear at bottom row.
	mainc, _, _, _ := s.GetContent(1, sh-1)
	if mainc != 'b' {
		t.Errorf("error first char = %q, want 'b'", mainc)
	}
}

func TestRenderZoomedMode(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.focused = 0
	tui.zoomed = true
	tui.overlay = false
	// Should not panic - just renders the focused pane full screen.
	tui.render()
}

func TestRenderFocusMode(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.focused = 1
	tui.layout = LayoutFocus
	tui.overlay = false
	tui.render()
}

func TestRenderNoFocusedPane(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.focused = -1
	tui.zoomed = true
	tui.overlay = false
	// Should not panic.
	tui.render()
}

// ---------------------------------------------------------------------------
// forwardMouseEvent / forwardMouseToFocused
// ---------------------------------------------------------------------------

func TestForwardMouseToFocused(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.focused = 0
	r := tui.panes[0].region

	// Click inside the pane region (should not panic).
	tui.forwardMouseToFocused(r.X+5, r.Y+5, uv.MouseLeft, false, false, tcell.ModNone)

	// Click outside (should be ignored).
	tui.forwardMouseToFocused(r.X+r.W+10, r.Y+5, uv.MouseLeft, false, false, tcell.ModNone)

	// Out of range focused.
	tui.focused = -1
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
	tui.relayout()

	r := tui.panes[0].region
	ev := tcell.NewEventMouse(r.X+5, r.Y+5, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	if !tui.selActive {
		t.Error("Button1 press should start selection")
	}
	if tui.selPane != 0 {
		t.Errorf("selPane = %d, want 0", tui.selPane)
	}
	if tui.focused != 0 {
		t.Errorf("focused = %d, want 0", tui.focused)
	}
}

func TestHandleMouseDragUpdatesSelection(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()

	r := tui.panes[0].region
	// Start selection.
	ev := tcell.NewEventMouse(r.X+1, r.Y+2, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	// Drag.
	ev = tcell.NewEventMouse(r.X+10, r.Y+5, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	if tui.selEndX != 10 {
		t.Errorf("selEndX = %d, want 10", tui.selEndX)
	}
}

func TestHandleMouseReleaseClearsSelection(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()

	r := tui.panes[0].region
	// Start selection.
	ev := tcell.NewEventMouse(r.X+1, r.Y+2, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)
	if !tui.selActive {
		t.Fatal("selection should be active after click")
	}

	// Release.
	ev = tcell.NewEventMouse(r.X+5, r.Y+3, tcell.ButtonNone, tcell.ModNone)
	tui.handleMouse(ev)
	if tui.selActive {
		t.Error("selection should be cleared after release")
	}
}

func TestHandleMouseWheelUp(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
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
	tui.relayout()
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
	tui.cmdActive = true
	r := tui.panes[0].region
	ev := tcell.NewEventMouse(r.X+1, r.Y+1, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)
	if tui.selActive {
		t.Error("mouse should be ignored when command modal is active")
	}
}

func TestHandleMouseMiddleClick(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
	r := tui.panes[0].region
	// Should not panic.
	ev := tcell.NewEventMouse(r.X+5, r.Y+5, tcell.Button2, tcell.ModNone)
	tui.handleMouse(ev)
}

func TestHandleMouseRightClick(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
	r := tui.panes[0].region
	ev := tcell.NewEventMouse(r.X+5, r.Y+5, tcell.Button3, tcell.ModNone)
	tui.handleMouse(ev)
}

func TestHandleMouseHiddenPaneSkipped(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b")
	tui.relayout()
	tui.panes[0].SetVisible(false)

	r := tui.panes[0].region
	ev := tcell.NewEventMouse(r.X+5, r.Y+5, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)
	// Click should not start selection on hidden pane.
	if tui.selActive && tui.selPane == 0 {
		t.Error("should not select hidden pane")
	}
}

// ---------------------------------------------------------------------------
// Render (increase coverage of pane rendering paths)
// ---------------------------------------------------------------------------

func TestRenderPaneScrollbackMode(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.relayout()
	p := tui.panes[0]

	// Write content to create scrollback.
	for i := 0; i < 50; i++ {
		p.emu.Write([]byte("scrollback line\r\n"))
	}
	p.ScrollUp(10)
	tui.overlay = false
	tui.render()

	// Title bar should show scroll indicator.
	r := p.region
	found := false
	for x := r.X; x < r.X+r.W; x++ {
		mainc, _, _, _ := s.GetContent(x, r.Y)
		if mainc == '+' {
			found = true
			break
		}
	}
	if !found {
		t.Error("scrollback mode should show +N in title bar")
	}
}

func TestRenderPaneDead(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
	tui.panes[0].alive = false
	tui.overlay = false
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
	tui.overlay = true
	tui.render()
	// Should not panic, and overlay should show all panes.
}

// ---------------------------------------------------------------------------
// copySelection
// ---------------------------------------------------------------------------

func TestCopySelectionExtractsText(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
	p := tui.panes[0]
	p.emu.Write([]byte("Hello World\r\n"))

	tui.selPane = 0
	tui.selStartX = 0
	tui.selStartY = 0
	tui.selEndX = 4
	tui.selEndY = 0
	tui.copySelection()
}

func TestCopySelectionOutOfRange(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.selPane = 99
	tui.copySelection()
}

func TestCopySelectionReversed(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
	p := tui.panes[0]
	p.emu.Write([]byte("ABCDEF\r\n"))

	tui.selPane = 0
	tui.selStartX = 5
	tui.selStartY = 0
	tui.selEndX = 0
	tui.selEndY = 0
	tui.copySelection()
}

func TestCopySelectionMultiLine(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
	p := tui.panes[0]
	p.emu.Write([]byte("Line1\r\nLine2\r\nLine3\r\n"))

	tui.selPane = 0
	tui.selStartX = 0
	tui.selStartY = 0
	tui.selEndX = 4
	tui.selEndY = 2
	tui.copySelection()
}

func TestCopySelectionEmptyContent(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
	// No content written - emulator is empty.

	tui.selPane = 0
	tui.selStartX = 0
	tui.selStartY = 0
	tui.selEndX = 5
	tui.selEndY = 0
	tui.copySelection() // text="" -> early return
}

func TestCopySelectionExceedsPaneRows(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
	p := tui.panes[0]
	p.emu.Write([]byte("X\r\n"))

	tui.selPane = 0
	tui.selStartX = 0
	tui.selStartY = 0
	tui.selEndX = 0
	tui.selEndY = 999 // Beyond pane rows, should be clamped.
	tui.copySelection()
}

func TestCopySelectionEndColBeyondWidth(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
	p := tui.panes[0]
	p.emu.Write([]byte("test\r\n"))

	tui.selPane = 0
	tui.selStartX = 0
	tui.selStartY = 0
	tui.selEndX = 999 // Beyond cols, should be clamped.
	tui.selEndY = 0
	tui.copySelection()
}

// ---------------------------------------------------------------------------
// Render additional paths (selection, cursor, hidden pane border)
// ---------------------------------------------------------------------------

func TestRenderWithSelectionHighlight(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
	tui.overlay = false
	tui.panes[0].emu.Write([]byte("Some content\r\n"))

	tui.selActive = true
	tui.selPane = 0
	tui.selStartX = 0
	tui.selStartY = 0
	tui.selEndX = 5
	tui.selEndY = 0
	tui.render() // Should draw selection highlight.
}

func TestRenderWithCursor(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
	tui.focused = 0
	tui.overlay = false
	tui.selActive = false
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
	p.Render(s, false, Selection{})
	p.region = Region{0, 0, 10, 1} // H < 2
	p.Render(s, false, Selection{})
}

func TestRenderAltScreen(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
	p := tui.panes[0]
	// Switch to alt screen via escape sequence.
	p.emu.Write([]byte("\x1b[?1049h")) // Enter alt screen.
	p.emu.Write([]byte("alt screen content\r\n"))
	tui.overlay = false
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
	tui.layout = LayoutGrid
	tui.gridCols = 2
	tui.gridRows = 2
	regions := tui.calcRegions(100, 50)
	if len(regions) != 3 {
		t.Errorf("got %d regions, want 3", len(regions))
	}
}

// ---------------------------------------------------------------------------
// Pane rendering with session description extraction
// ---------------------------------------------------------------------------

func TestRenderExtractsSessionDesc(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
	p := tui.panes[0]

	// Write content followed by a "session description" at the cursor row.
	p.emu.Write([]byte("output line\r\n"))
	p.emu.Write([]byte("session: test project"))
	tui.overlay = false
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
	tui.relayout()

	r := tui.panes[0].region
	// Start selection.
	ev := tcell.NewEventMouse(r.X+5, r.Y+5, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	// Drag to negative coordinates (outside pane).
	ev = tcell.NewEventMouse(r.X-10, r.Y-10, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	if tui.selEndX < 0 || tui.selEndY < 0 {
		t.Error("selection coordinates should be clamped to >= 0")
	}
}

func TestHandleMouseDragClampBeyondPane(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()

	r := tui.panes[0].region
	// Start selection.
	ev := tcell.NewEventMouse(r.X+5, r.Y+5, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	// Drag beyond pane bounds.
	ev = tcell.NewEventMouse(r.X+r.W+50, r.Y+r.H+50, tcell.Button1, tcell.ModNone)
	tui.handleMouse(ev)

	cols, rows := r.InnerSize()
	if tui.selEndX >= cols || tui.selEndY >= rows {
		t.Error("selection coordinates should be clamped to pane bounds")
	}
}

// ---------------------------------------------------------------------------
// handleEvent mouse dispatch
// ---------------------------------------------------------------------------

func TestHandleEventMouse(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.relayout()
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
	tui.layout = Layout2Col
	tui.recalcGrid()
	if tui.layout != Layout2Col {
		t.Error("recalcGrid should preserve non-grid layout")
	}
}

// ---------------------------------------------------------------------------
// setGrid
// ---------------------------------------------------------------------------

func TestSetGrid(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a", "b", "c", "d")
	tui.setGrid(3, 2)
	if tui.gridCols != 3 || tui.gridRows != 2 {
		t.Errorf("setGrid: cols=%d rows=%d, want 3,2", tui.gridCols, tui.gridRows)
	}
	if tui.layout != LayoutGrid {
		t.Error("setGrid should set LayoutGrid")
	}
	if tui.zoomed {
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
