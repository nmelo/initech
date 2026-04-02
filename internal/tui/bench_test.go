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
