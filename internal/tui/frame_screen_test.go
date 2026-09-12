package tui

import (
	"fmt"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// countingScreen records what reaches the wrapped screen.
type countingScreen struct {
	tcell.Screen
	sets, shows int
}

func (c *countingScreen) SetContent(x, y int, mainc rune, combc []rune, style tcell.Style) {
	c.sets++
	c.Screen.SetContent(x, y, mainc, combc, style)
}

func (c *countingScreen) Show() {
	c.shows++
	c.Screen.Show()
}

func newCountedFrameScreen(t *testing.T, w, h int) (*frameScreen, *countingScreen, tcell.SimulationScreen) {
	t.Helper()
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Fini)
	sim.SetSize(w, h)
	cs := &countingScreen{Screen: sim}
	return newFrameScreen(cs), cs, sim
}

func drawThree(f *frameScreen, style tcell.Style) {
	f.Clear()
	f.SetContent(0, 0, 'A', nil, style)
	f.SetContent(1, 0, 'B', nil, style)
	f.SetContent(2, 1, 'C', nil, style)
}

// ini-p39's defect in one cell: an identical frame after Clear must send
// tcell nothing -- no cells, no Show.
func TestFrameScreen_IdenticalFrameReachesTcellNowhere(t *testing.T) {
	f, cs, _ := newCountedFrameScreen(t, 10, 4)
	drawThree(f, tcell.StyleDefault.Foreground(tcell.ColorRed))
	f.Show()
	if cs.shows != 1 || f.lastFlushed != 10*4 {
		t.Fatalf("first frame: shows=%d flushed=%d, want 1 and every cell", cs.shows, f.lastFlushed)
	}
	sets := cs.sets

	drawThree(f, tcell.StyleDefault.Foreground(tcell.ColorRed))
	f.Show()
	if cs.sets != sets || cs.shows != 1 || f.lastFlushed != 0 || f.lastShown {
		t.Errorf("identical frame reached tcell: sets +%d, shows %d, flushed %d", cs.sets-sets, cs.shows, f.lastFlushed)
	}
}

func TestFrameScreen_ChangedCellIsTheOnlyOneFlushed(t *testing.T) {
	f, cs, sim := newCountedFrameScreen(t, 10, 4)
	drawThree(f, tcell.StyleDefault)
	f.Show()
	sets := cs.sets

	f.Clear()
	f.SetContent(0, 0, 'A', nil, tcell.StyleDefault)
	f.SetContent(1, 0, 'X', nil, tcell.StyleDefault) // the one change
	f.SetContent(2, 1, 'C', nil, tcell.StyleDefault)
	f.Show()
	if cs.sets-sets != 1 || f.lastFlushed != 1 || cs.shows != 2 {
		t.Errorf("one changed cell flushed %d cells (shows=%d)", cs.sets-sets, cs.shows)
	}
	if ch, _, _, _ := sim.GetContent(1, 0); ch != 'X' {
		t.Errorf("tcell holds %q at the changed cell, want X", ch)
	}
}

// A style-only change is a change.
func TestFrameScreen_StyleChangeIsFlushed(t *testing.T) {
	f, _, sim := newCountedFrameScreen(t, 10, 4)
	drawThree(f, tcell.StyleDefault)
	f.Show()
	drawThree(f, tcell.StyleDefault.Bold(true))
	f.Show()
	if f.lastFlushed != 3 {
		t.Errorf("style change flushed %d cells, want the 3 styled ones", f.lastFlushed)
	}
	_, _, st, _ := sim.GetContent(0, 0)
	if _, _, attrs := st.Decompose(); attrs&tcell.AttrBold == 0 {
		t.Error("tcell did not receive the new style")
	}
}

// Painting over: tcell receives the final value once, never the intermediate.
func TestFrameScreen_DoublePaintReachesTcellOnceWithTheFinalValue(t *testing.T) {
	f, cs, sim := newCountedFrameScreen(t, 10, 4)
	f.Clear()
	f.Show()
	sets := cs.sets
	f.Clear()
	f.SetContent(3, 2, 'A', nil, tcell.StyleDefault)
	f.SetContent(3, 2, 'B', nil, tcell.StyleDefault)
	f.Show()
	if cs.sets-sets != 1 {
		t.Errorf("double paint flushed %d cells, want 1", cs.sets-sets)
	}
	if ch, _, _, _ := sim.GetContent(3, 2); ch != 'B' {
		t.Errorf("tcell holds %q, want the final B", ch)
	}
	f.Clear()
	f.SetContent(3, 2, 'A', nil, tcell.StyleDefault)
	f.SetContent(3, 2, 'B', nil, tcell.StyleDefault)
	f.Show()
	if f.lastFlushed != 0 {
		t.Errorf("repeated double paint flushed %d cells, want 0", f.lastFlushed)
	}
}

// Sync means the terminal is being repainted in full: the next Show must
// hand tcell every cell again, even though the frame is unchanged.
func TestFrameScreen_SyncReflushesEveryCell(t *testing.T) {
	f, cs, _ := newCountedFrameScreen(t, 10, 4)
	drawThree(f, tcell.StyleDefault)
	f.Show()
	f.Sync()
	drawThree(f, tcell.StyleDefault)
	f.Show()
	if f.lastFlushed != 10*4 || cs.shows != 2 {
		t.Errorf("after Sync flushed %d cells (shows=%d), want every cell", f.lastFlushed, cs.shows)
	}
}

// A resize reallocates both buffers and reflushes; nothing panics at the old
// bounds.
func TestFrameScreen_ResizeReallocatesAndReflushes(t *testing.T) {
	f, _, sim := newCountedFrameScreen(t, 10, 4)
	drawThree(f, tcell.StyleDefault)
	f.Show()
	sim.SetSize(30, 12)
	f.Clear()
	f.SetContent(29, 11, 'Z', nil, tcell.StyleDefault)
	f.Show()
	if w, h := f.Size(); w != 30 || h != 12 {
		t.Errorf("size = %dx%d after resize", w, h)
	}
	if f.lastFlushed != 30*12 {
		t.Errorf("after resize flushed %d cells, want every cell", f.lastFlushed)
	}
	if ch, _, _, _ := sim.GetContent(29, 11); ch != 'Z' {
		t.Errorf("cell at the new corner = %q", ch)
	}
}

// Reading back goes to the back buffer: what this frame drew, before Show.
func TestFrameScreen_ReadBackSeesThisFrame(t *testing.T) {
	f, _, _ := newCountedFrameScreen(t, 10, 4)
	f.Clear()
	f.SetContent(4, 1, 'Q', []rune{0x301}, tcell.StyleDefault.Italic(true))
	ch, comb, st, _ := f.GetContent(4, 1)
	if ch != 'Q' || len(comb) != 1 || comb[0] != 0x301 {
		t.Errorf("GetContent = %q %v", ch, comb)
	}
	if _, _, attrs := st.Decompose(); attrs&tcell.AttrItalic == 0 {
		t.Error("GetContent lost the style")
	}
	if s, _, _ := f.Get(4, 1); s != "Q́" {
		t.Errorf("Get = %q", s)
	}
}

// The whole product frame, through the buffer and bare, cell for cell: what
// a frame LOOKS like must not change (the goldens' claim, asserted directly
// on a fleet with the Agents overlay painted over pane content).
func TestFrameScreen_RenderedFrameIsIdenticalThroughTheBuffer(t *testing.T) {
	names := []string{"super", "eng1", "eng2", "eng3", "qa1", "qa2"}
	paint := func(tui *TUI) {
		for _, pv := range tui.panes {
			p := pv.(*Pane)
			for l := 1; l <= 6; l++ {
				p.emu.Write([]byte(fmt.Sprintf("LINE-%d-P39-STATIC\r\n", l)))
			}
		}
		tui.layoutState.Hidden["qa2"] = true
		tui.openAgentsModal()
	}
	bare, bareSim := newTestTUIWithScreen(names...)
	paint(bare)
	bare.render()
	bare.render()

	buffered, bufSim := newTestTUIWithScreen(names...)
	buffered.screen = newFrameScreen(bufSim)
	paint(buffered)
	buffered.render()
	buffered.render() // the second frame flushes nothing; tcell must still hold the first

	a, w, h := bareSim.GetContents()
	b, w2, h2 := bufSim.GetContents()
	if w != w2 || h != h2 {
		t.Fatalf("sizes differ: %dx%d vs %dx%d", w, h, w2, h2)
	}
	diff := 0
	for i := range a {
		if string(a[i].Runes) != string(b[i].Runes) || a[i].Style != b[i].Style {
			if diff < 5 {
				t.Errorf("cell (%d,%d): bare %q vs buffered %q", i%w, i/w, string(a[i].Runes), string(b[i].Runes))
			}
			diff++
		}
	}
	if diff > 0 {
		t.Errorf("%d cells differ between the bare and buffered frame", diff)
	}
	fs := buffered.screen.(*frameScreen)
	if fs.lastFlushed != 0 || fs.lastShown {
		t.Errorf("second identical product frame flushed %d cells", fs.lastFlushed)
	}
}
