package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// BenchmarkUvCellToTcell measures the per-cell conversion from ultraviolet
// Cell to tcell rune+style. This is the innermost loop of the render path,
// called once per visible cell per frame.
func BenchmarkUvCellToTcell(b *testing.B) {
	emu := vt.NewSafeEmulator(80, 24)
	// Write content so cells are populated (not nil/empty fast path).
	emu.Write([]byte("Hello World! This is benchmark content for cell conversion.\r\n"))
	emu.Write([]byte("\033[32mColored text\033[0m and \033[1mbold text\033[0m.\r\n"))

	// Pre-read a cell for the benchmark.
	cell := emu.CellAt(0, 0)
	if cell == nil {
		b.Fatal("cell at (0,0) is nil")
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		uvCellToTcell(cell)
	}
}

// BenchmarkUvCellToTcell_Empty measures the nil/empty fast path.
func BenchmarkUvCellToTcell_Empty(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		uvCellToTcell(nil)
	}
}

// BenchmarkRenderPane measures a full pane render (content + ribbon + activity bar).
// Simulates an 80x24 pane with terminal content.
func BenchmarkRenderPane(b *testing.B) {
	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	// Write some content to make the render path non-trivial.
	for i := 0; i < 20; i++ {
		emu.Write([]byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit.\r\n"))
	}

	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 25) // 24 content + 1 ribbon

	p := &Pane{
		name:      "eng1",
		emu:       emu,
		alive:     true,
		visible:   true,
		activity:  StateRunning,
		kittEpoch: time.Now(),
		region:    Region{X: 0, Y: 0, W: 80, H: 25},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p.Render(s, false, false, 1, Selection{})
	}
}

// BenchmarkRenderPane_Dimmed measures dimmed pane rendering (unfocused path).
func BenchmarkRenderPane_Dimmed(b *testing.B) {
	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	for i := 0; i < 20; i++ {
		emu.Write([]byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit.\r\n"))
	}

	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 25)

	p := &Pane{
		name:      "eng1",
		emu:       emu,
		alive:     true,
		visible:   true,
		activity:  StateIdle,
		kittEpoch: time.Now(),
		region:    Region{X: 0, Y: 0, W: 80, H: 25},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p.Render(s, false, true, 1, Selection{})
	}
}

// BenchmarkProcessTreeRSS measures the cost of the process tree RSS query.
// Uses the current process (no children, fast path).
func BenchmarkProcessTreeRSS(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		processTreeRSS(1) // PID 1 (launchd/init), always exists
	}
}

// benchTUI creates a TUI with n panes (80x24 each), a SimulationScreen, and
// an initial render so renderCount > 1. Each pane has terminal content.
func benchTUI(b *testing.B, n int) *TUI {
	b.Helper()
	s := tcell.NewSimulationScreen("")
	s.Init()
	// Size the screen to fit n panes side-by-side + 2 rows for status bar.
	s.SetSize(80*n, 26)

	panes := make([]PaneView, n)
	names := make([]string, n)
	for i := 0; i < n; i++ {
		name := "pane" + string(rune('0'+i))
		names[i] = name
		emu := vt.NewSafeEmulator(80, 24)
		go func() {
			buf := make([]byte, 256)
			for {
				if _, err := emu.Read(buf); err != nil {
					return
				}
			}
		}()
		for j := 0; j < 20; j++ {
			emu.Write([]byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit.\r\n"))
		}
		panes[i] = &Pane{
			name:      name,
			emu:       emu,
			alive:     true,
			visible:   true,
			activity:  StateIdle,
			kittEpoch: time.Now(),
			region:    Region{X: i * 80, Y: 0, W: 80, H: 25},
		}
	}

	ls := DefaultLayoutState(names)
	t := &TUI{
		screen:      s,
		panes:       panes,
		layoutState: ls,
		lastW:       80 * n,
		lastH:       26,
	}
	t.plan = computeLayout(ls, panes, 80*n, 24)

	// Initial render to get past renderCount <= 1 guard.
	// Mark all panes dirty for the first frame.
	for _, p := range panes {
		p.(*Pane).mu.Lock()
		p.(*Pane).dirty = true
		p.(*Pane).mu.Unlock()
	}
	t.render()

	return t
}

// BenchmarkTUIRender_StableFrame measures the full TUI render when no pane
// has new emulator content. The dirty-frame optimization should skip all
// per-cell emulator reads and only redraw the status bar.
func BenchmarkTUIRender_StableFrame(b *testing.B) {
	t := benchTUI(b, 4)
	// All panes are clean (dirty cleared by initial render).

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		t.render()
	}
}

// BenchmarkTUIRender_OnePaneChanged measures the full TUI render when exactly
// one out of four panes has new content. Compares against StableFrame to show
// the cost of a single dirty pane.
func BenchmarkTUIRender_OnePaneChanged(b *testing.B) {
	t := benchTUI(b, 4)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Mark one pane dirty before each render.
		t.panes[0].(*Pane).mu.Lock()
		t.panes[0].(*Pane).dirty = true
		t.panes[0].(*Pane).mu.Unlock()
		t.render()
	}
}

// BenchmarkTUIRender_AllPanesDirty measures the worst case: all 4 panes
// have new content, requiring a full redraw. Should be comparable to the
// pre-optimization baseline.
func BenchmarkTUIRender_AllPanesDirty(b *testing.B) {
	t := benchTUI(b, 4)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, p := range t.panes {
			p.(*Pane).mu.Lock()
			p.(*Pane).dirty = true
			p.(*Pane).mu.Unlock()
		}
		t.render()
	}
}
