// QA tests for ini-lw0: KITT scanner activity indicator on pane top edge.
package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// TestActivityBar_IdleIsUniform verifies that idle panes render a static
// dim bar with uniform brightness across the top row.
func TestActivityBar_IdleIsUniform(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(40, 11)

	p := &Pane{
		name:      "eng1",
		emu:       emu,
		alive:     true,
		visible:   true,
		activity:  StateIdle,
		kittEpoch: time.Now(),
		region:    Region{X: 0, Y: 0, W: 40, H: 11},
	}
	p.Render(s, false, false, 1, Selection{})

	// Top row (y=0) should have uniform foreground color across all cells.
	_, refStyle, _ := s.Get(0, 0)
	refFg, _, _ := refStyle.Decompose()
	for x := 1; x < 40; x++ {
		_, style, _ := s.Get(x, 0)
		fg, _, _ := style.Decompose()
		if fg != refFg {
			t.Errorf("idle bar col %d fg differs from col 0: uniform expected", x)
			break
		}
	}
}

// TestActivityBar_RunningHasVariation verifies that running panes render the
// KITT scanner with varying brightness (not all cells the same).
func TestActivityBar_RunningHasVariation(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(40, 11)

	p := &Pane{
		name:      "eng1",
		emu:       emu,
		alive:     true,
		visible:   true,
		activity:  StateRunning,
		kittEpoch: time.Now().Add(-500 * time.Millisecond), // offset so peak isn't at edge
		region:    Region{X: 0, Y: 0, W: 40, H: 11},
	}
	p.Render(s, false, false, 1, Selection{})

	// Top row should have at least 2 different foreground colors (bright peak + dim base).
	colors := make(map[tcell.Color]bool)
	for x := 0; x < 40; x++ {
		_, style, _ := s.Get(x, 0)
		fg, _, _ := style.Decompose()
		colors[fg] = true
	}
	if len(colors) < 2 {
		t.Errorf("running bar should have varying brightness, got %d distinct colors", len(colors))
	}
}

// TestActivityBar_DeadIsStatic verifies dead panes get the static dim bar.
func TestActivityBar_DeadIsStatic(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(40, 11)

	p := &Pane{
		name:      "eng1",
		emu:       emu,
		alive:     false,
		visible:   true,
		activity:  StateDead,
		kittEpoch: time.Now(),
		region:    Region{X: 0, Y: 0, W: 40, H: 11},
	}
	p.Render(s, false, false, 1, Selection{})

	// Should be uniform (static bar, not animated).
	_, refStyle, _ := s.Get(0, 0)
	refFg, _, _ := refStyle.Decompose()
	for x := 1; x < 40; x++ {
		_, style, _ := s.Get(x, 0)
		fg, _, _ := style.Decompose()
		if fg != refFg {
			t.Errorf("dead bar col %d fg differs: should be static", x)
			break
		}
	}
}

// TestActivityBar_SuspendedIsStatic verifies suspended panes get the static bar.
func TestActivityBar_SuspendedIsStatic(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(40, 11)

	p := &Pane{
		name:      "eng1",
		emu:       emu,
		alive:     false,
		suspended: true,
		visible:   true,
		activity:  StateSuspended,
		kittEpoch: time.Now(),
		region:    Region{X: 0, Y: 0, W: 40, H: 11},
	}
	p.Render(s, false, false, 1, Selection{})

	_, refStyle, _ := s.Get(0, 0)
	refFg, _, _ := refStyle.Decompose()
	for x := 1; x < 40; x++ {
		_, style, _ := s.Get(x, 0)
		fg, _, _ := style.Decompose()
		if fg != refFg {
			t.Errorf("suspended bar col %d fg differs: should be static", x)
			break
		}
	}
}

// TestActivityBar_UsesHorizontalLine verifies the bar uses the horizontal
// line character (U+2500) for all cells.
func TestActivityBar_UsesHorizontalLine(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(40, 11)

	p := &Pane{
		name:      "eng1",
		emu:       emu,
		alive:     true,
		visible:   true,
		activity:  StateRunning,
		kittEpoch: time.Now(),
		region:    Region{X: 0, Y: 0, W: 40, H: 11},
	}
	p.Render(s, false, false, 1, Selection{})

	for x := 0; x < 40; x++ {
		c, _, _ := s.Get(x, 0)
		if c != "\u2500" {
			t.Errorf("bar col %d char = %q (%q), want U+2500", x, c, c)
			break
		}
	}
}

// TestActivityBar_NarrowPaneNoPanic verifies a very narrow pane doesn't panic.
func TestActivityBar_NarrowPaneNoPanic(t *testing.T) {
	emu := vt.NewSafeEmulator(2, 4)
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(2, 5)

	p := &Pane{
		name:      "a",
		emu:       emu,
		alive:     true,
		visible:   true,
		activity:  StateRunning,
		kittEpoch: time.Now(),
		region:    Region{X: 0, Y: 0, W: 2, H: 5},
	}
	p.Render(s, false, false, 1, Selection{}) // must not panic
}
