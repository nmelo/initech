package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// ini-amx: the activity sweep is gone; every pane's top edge is a flat line
// in every state. A running pane keeps its running square, title colour and
// tint; an idle pane its plain edge and silver title; a background pane, with
// no tint and now no sweep, is marked by a dim [bg <age>] tag on its ribbon.

var amxEdgeStyle = tcell.StyleDefault.Foreground(tcell.NewRGBColor(35, 35, 35))

// amxFleet is the eleven-pane grid with p01 running (tinted), p02 in the
// background (a shell started 12 s ago) and the rest idle.
func amxFleet(t *testing.T) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	// Two frames first: the frame loop reads each footer once a second, and
	// a first read of these bare panes would clear the background record.
	tui, s := ysr3Parked(t)
	run := ysr2Pane(tui, "p01")
	run.mu.Lock()
	run.activity, run.lastOutputTime, run.tintUntil = StateRunning, time.Now(), time.Now().Add(time.Hour)
	run.mu.Unlock()
	bg := ysr2Pane(tui, "p02")
	bg.mu.Lock()
	bg.activity = StateIdle
	bg.bgWork = backgroundWork{shells: 1, since: time.Now().Add(-12 * time.Second)}
	bg.mu.Unlock()
	tui.lastDrawn = nil
	tui.render()
	return tui, s
}

// amxTopEdgeFlat reports the first cell of the pane's top edge that is not a
// plain line in the edge's base style, or "" when the edge is flat.
func amxTopEdgeFlat(s tcell.SimulationScreen, r Region) string {
	for x := r.X; x < r.X+r.W; x++ {
		ch, _, st, _ := s.GetContent(x, r.Y)
		fg, _, _ := st.Decompose()
		want, _, _ := amxEdgeStyle.Decompose()
		if ch != '─' || fg.Hex() != want.Hex() {
			return string(ch) + " " + fg.String()
		}
	}
	return ""
}

func amxRibbon(s tcell.SimulationScreen, r Region) string { return ysr3Row(s, r, r.Y+r.H-1) }

// AC1: running, background and idle panes all have a flat top edge, frame
// after frame as the clock moves.
func TestFlatEdge_EveryStateHasAFlatTopEdge(t *testing.T) {
	tui, s := amxFleet(t)
	for f := 0; f < 3; f++ {
		for _, name := range []string{"p01", "p02", "p03"} {
			p := ysr2Pane(tui, name)
			if bad := amxTopEdgeFlat(s, p.region); bad != "" {
				t.Errorf("frame %d: %s (%v) top edge has a non-flat cell: %s", f, name, p.Activity(), bad)
			}
		}
		time.Sleep(210 * time.Millisecond) // past a sweep step, had there been one
		tui.lastDrawn = nil
		tui.render()
	}
}

// AC2: what marks the states is unchanged: the running pane's square, title
// colour and tint; the idle pane's silver title.
func TestFlatEdge_TheOtherMarksStay(t *testing.T) {
	tui, s := amxFleet(t)
	run := ysr2Pane(tui, "p01")
	r := run.region
	ch, _, st, _ := s.GetContent(r.X, r.Y+r.H-1)
	if ch != '█' || st != runningSquareStyle {
		t.Errorf("the running pane's ribbon starts %q, want its running square", ch)
	}
	_, bg, _ := func() (tcell.Color, tcell.Color, tcell.AttrMask) {
		_, _, st, _ := s.GetContent(r.X+r.W-2, r.Y+r.H-3)
		return st.Decompose()
	}()
	if bg.Hex() != runningTintColor.Hex() {
		t.Errorf("the running pane's body background is %v, want the running tint", bg)
	}
	idle := ysr2Pane(tui, "p03")
	ir := idle.region
	_, _, st, _ = s.GetContent(ir.X+2, ir.Y+ir.H-1)
	if fg, _, _ := st.Decompose(); fg != tcell.ColorGray {
		t.Errorf("the idle pane's title colour is %v, want silver (gray)", fg)
	}
	if strings.Contains(amxRibbon(s, ir), "[bg") || strings.Contains(amxRibbon(s, r), "[bg") {
		t.Error("a running or idle pane's ribbon carries a [bg] tag")
	}
	bgp := ysr2Pane(tui, "p02")
	_, bgc, _ := func() (tcell.Color, tcell.Color, tcell.AttrMask) {
		_, _, st, _ := s.GetContent(bgp.region.X+bgp.region.W-2, bgp.region.Y+bgp.region.H-3)
		return st.Decompose()
	}()
	if bgc.Hex() == runningTintColor.Hex() {
		t.Error("the background pane is tinted; the tint marks output only")
	}
}

// AC3: the background pane's ribbon shows a dim [bg <age>] tag, the age
// grows, and the tag clears when the pane goes idle.
func TestFlatEdge_ABackgroundPaneIsTaggedWithItsAge(t *testing.T) {
	tui, s := amxFleet(t)
	p := ysr2Pane(tui, "p02")
	rib := amxRibbon(s, p.region)
	if !strings.Contains(rib, "p02 [bg 12s]") {
		t.Fatalf("the background ribbon reads %q, want the tag [bg 12s] after the name", rib)
	}
	x := p.region.X + strings.Index(rib, "[bg")
	_, _, st, _ := s.GetContent(x, p.region.Y+p.region.H-1)
	if fg, _, _ := st.Decompose(); fg != tcell.ColorDimGray {
		t.Errorf("the [bg] tag's colour is %v, want dim (dimgray)", fg)
	}
	p.mu.Lock()
	p.bgWork.since = time.Now().Add(-4 * time.Minute)
	p.mu.Unlock()
	tui.render()
	if rib := amxRibbon(s, p.region); !strings.Contains(rib, "[bg 4m]") {
		t.Errorf("after four minutes the ribbon reads %q, want [bg 4m]", rib)
	}
	p.mu.Lock()
	p.bgWork = backgroundWork{}
	p.mu.Unlock()
	tui.render()
	if rib := amxRibbon(s, p.region); strings.Contains(rib, "[bg") {
		t.Errorf("gone idle, the ribbon still reads %q", rib)
	}
	ysr2SameAsFull(t, tui, s, "a background pane gone idle")

	// Running with a shell is running (background never overrides it): no tag.
	run := ysr2Pane(tui, "p01")
	run.mu.Lock()
	run.bgWork = backgroundWork{shells: 1, since: time.Now().Add(-time.Minute)}
	run.mu.Unlock()
	tui.render()
	if rib := amxRibbon(s, run.region); strings.Contains(rib, "[bg") {
		t.Errorf("a running pane with a shell shows a background tag: %q", rib)
	}
}

// AC4 feed it nothing: a zero-width pane draws nothing; a ribbon with no
// room for the tag keeps the name; an empty fleet renders.
func TestFlatEdge_NothingToDraw(t *testing.T) {
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(40, 10)
	p := testPane("bgpane")
	p.alive, p.visible = true, true
	p.bgWork = backgroundWork{shells: 1, since: time.Now()}
	p.region = Region{X: 0, Y: 0, W: 0, H: 5}
	p.Render(sim, false, false, 1, Selection{})
	for x := 0; x < 40; x++ {
		if ch, _, _, _ := sim.GetContent(x, 0); ch != ' ' {
			t.Fatalf("a zero-width pane drew %q at column %d", ch, x)
		}
	}
	p.region = Region{X: 0, Y: 0, W: 12, H: 5}
	p.Render(sim, false, false, 1, Selection{})
	if rib := ysr3Row(sim, p.region, 4); !strings.Contains(rib, "1 bgpane") {
		t.Errorf("a narrow background ribbon reads %q, want the name kept", rib)
	}

	tui, s := newTestTUIWithScreen()
	tui.panes = nil
	tui.applyLayout()
	tui.render()
	if _, out := ysr3Count(tui, s, 2); out == 0 {
		t.Error("an empty fleet drew nothing")
	}
}

// A window 2 pane in the background carries the same tag from window 1's
// record.
func TestFlatEdge_ARemoteBackgroundPaneIsTagged(t *testing.T) {
	tui, s := newTestTUIWithScreen()
	rp := &RemotePane{name: "eng1", host: "w2", alive: true, emu: vt.NewSafeEmulator(118, 37)}
	rp.bgWork = backgroundWork{shells: 1, since: time.Now().Add(-3 * time.Minute)}
	tui.panes = []PaneView{rp}
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.Focused = agentKey(rp)
	tui.notifications = nil
	tui.applyLayout()
	tui.render()
	if a := rp.Activity(); a != StateBackground {
		t.Fatalf("fixture: the remote pane reads %v", a)
	}
	if rib := amxRibbon(s, rp.region); !strings.Contains(rib, "[bg 3m]") {
		t.Errorf("a window 2 background ribbon reads %q, want [bg 3m]", rib)
	}
}
