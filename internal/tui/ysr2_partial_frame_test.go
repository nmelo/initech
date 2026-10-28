package tui

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-ysr.2: a frame draws a pane's body only when it changed, when a layer
// covered it last frame, or when its last draw was not its real body; every
// pane's chrome is drawn every frame; the whole screen is drawn afresh on a
// new size or layout and after the event log. Each test that changes what is
// on screen compares the partial frame with a forced full frame: any cell
// that differs is a stale cell.

// ysr2TUI is a 4x3 grid of eleven panes on a 160x48 screen, each body filled
// with its name, with the sweep clock pinned so frames compare equal.
func ysr2TUI(t *testing.T, names ...string) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	if len(names) == 0 {
		for i := 1; i <= 11; i++ {
			names = append(names, fmt.Sprintf("p%02d", i))
		}
	}
	old := kittClock
	kittClock = func() time.Time { return time.Date(2026, 10, 28, 19, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { kittClock = old })
	tui, s := newTestTUIWithScreen(names...)
	s.SetSize(160, 48)
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 4, 3
	tui.layoutState.GridExplicit = true
	tui.layoutState.Focused = names[0]
	tui.applyLayout()
	ysr2Settled(tui)
	for _, pv := range tui.panes {
		p := pv.(*Pane)
		p.activity = StateIdle
		p.emu.Write([]byte(strings.Repeat(p.name+" body ", 4)))
	}
	return tui, s
}

// ysr2Settled ends every pane's post-resize settle, as the clock would a
// moment later: the tests below are about what a frame draws once panes are
// at rest, and a settling pane is drawn every frame by design.
func ysr2Settled(tui *TUI) {
	for _, pv := range tui.panes {
		p := pv.(*Pane)
		p.resizeSettleFrames = 0
		p.resizeSettleDeadline = time.Time{}
	}
}

// ysr2Frame draws one frame and returns the panes whose body it drew.
func ysr2Frame(tui *TUI) string {
	tui.render()
	b := append([]string(nil), tui.bodiesDrawn...)
	sort.Strings(b)
	return strings.Join(b, ",")
}

// ysr2Cells is the whole screen, rune and style, as one string.
func ysr2Cells(s tcell.SimulationScreen) string {
	w, h := s.Size()
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ch, _, st, _ := s.GetContent(x, y)
			fg, bg, at := st.Decompose()
			// By value: tcell names one colour two ways (dimgray, dimgrey).
			fmt.Fprintf(&b, "%c%d %d %v|", ch, fg.Hex(), bg.Hex(), at)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// ysr2SameAsFull draws a partial frame, then a forced full frame, and fails
// on any cell where they differ.
func ysr2SameAsFull(t *testing.T, tui *TUI, s tcell.SimulationScreen, what string) {
	t.Helper()
	tui.render()
	partial := ysr2Cells(s)
	tui.lastDrawn = nil // the next frame starts from a cleared screen
	tui.render()
	if full := ysr2Cells(s); partial != full {
		pl, fl := strings.Split(partial, "\n"), strings.Split(full, "\n")
		for y := range pl {
			if y < len(fl) && pl[y] != fl[y] {
				t.Fatalf("%s: stale cells, first on row %d", what, y)
			}
		}
		t.Fatalf("%s: the partial frame differs from a full frame", what)
	}
}

func ysr2Pane(tui *TUI, name string) *Pane {
	for _, pv := range tui.panes {
		if pv.Name() == name {
			return pv.(*Pane)
		}
	}
	return nil
}

// AC1 and AC5: after the first frame a pane that never changes is not drawn
// again; with one pane receiving bytes every frame, a frame draws that one;
// a byte on an idle pane draws it on the next frame.
func TestPartialFrame_OnlyPanesThatChangedAreDrawn(t *testing.T) {
	tui, _ := ysr2TUI(t)
	if got := ysr2Frame(tui); strings.Count(got, ",") != 10 {
		t.Fatalf("first frame drew %q, want all eleven", got)
	}
	run := ysr2Pane(tui, "p01")
	for i := 0; i < 3; i++ {
		run.emu.Write([]byte("tick "))
		run.markFrameChanged()
		if got := ysr2Frame(tui); got != "p01" {
			t.Fatalf("frame %d drew %q, want only the pane with new bytes", i+2, got)
		}
	}
	idle := ysr2Pane(tui, "p07")
	idle.emu.Write([]byte("late"))
	idle.markFrameChanged()
	if got := ysr2Frame(tui); got != "p07" {
		t.Errorf("a byte on an idle pane drew %q, want p07", got)
	}
	if got := ysr2Frame(tui); got != "" {
		t.Errorf("a frame with nothing new drew %q, want none", got)
	}
}

// AC5: after a layout change every pane is drawn once, then not again.
func TestPartialFrame_ALayoutDrawsEveryPaneOnce(t *testing.T) {
	tui, s := ysr2TUI(t)
	ysr2Frame(tui)
	ysr2Frame(tui)
	tui.layoutState.GridCols, tui.layoutState.GridRows = 3, 4
	tui.applyLayout()
	ysr2Settled(tui)
	if got := ysr2Frame(tui); strings.Count(got, ",") != 10 {
		t.Errorf("the frame after a layout change drew %q, want all eleven", got)
	}
	if got := ysr2Frame(tui); got != "" {
		t.Errorf("the frame after that drew %q, want none", got)
	}
	ysr2SameAsFull(t, tui, s, "after a layout change")
}

// AC3: every layer that opens over the panes and closes leaves the screen as
// a full frame would draw it.
func TestPartialFrame_LayersLeaveNoStaleCells(t *testing.T) {
	for name, toggle := range map[string]func(*TUI, bool){
		"agents overlay": func(tu *TUI, on bool) { tu.layoutState.Overlay = on },
		"help":           func(tu *TUI, on bool) { tu.help.active = on },
		"inbox":          func(tu *TUI, on bool) { tu.inbox.active = on },
		"agents picker":  func(tu *TUI, on bool) { tu.agents.active = on },
		"toast": func(tu *TUI, on bool) {
			tu.notifications = nil
			if on {
				tu.notifications = []notification{{event: AgentEvent{Type: EventAgentStalled, Pane: "p03", Detail: "a toast over the panes"}, expires: time.Now().Add(time.Minute)}}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			tui, s := ysr2TUI(t)
			ysr2Frame(tui)
			toggle(tui, true)
			ysr2SameAsFull(t, tui, s, name+" open")
			toggle(tui, false)
			ysr2SameAsFull(t, tui, s, name+" closed")
		})
	}
}

// AC3: the event log replaces the screen; closing it draws every pane.
func TestPartialFrame_TheEventLogClosesToAFullFrame(t *testing.T) {
	tui, s := ysr2TUI(t)
	ysr2Frame(tui)
	tui.eventLogM.active = true
	tui.render()
	tui.eventLogM.active = false
	if got := ysr2Frame(tui); strings.Count(got, ",") != 10 {
		t.Errorf("after the event log the frame drew %q, want all eleven", got)
	}
	ysr2SameAsFull(t, tui, s, "after the event log")
}

// AC3: a new screen size draws the whole screen afresh.
func TestPartialFrame_ANewScreenSizeIsAFullFrame(t *testing.T) {
	tui, s := ysr2TUI(t)
	ysr2Frame(tui)
	s.SetSize(150, 46)
	if got := ysr2Frame(tui); strings.Count(got, ",") != 10 {
		t.Errorf("after a resize the frame drew %q, want all eleven", got)
	}
	ysr2Settled(tui)
	ysr2SameAsFull(t, tui, s, "after a resize")
}

// AC4: scroll, selection and focus on an idle pane draw it that frame, and
// the frame matches a full one.
func TestPartialFrame_ScrollSelectionAndFocusDrawThePane(t *testing.T) {
	tui, s := ysr2TUI(t)
	ysr2Frame(tui)
	ysr2Pane(tui, "p05").ScrollUp(1)
	if got := ysr2Frame(tui); got != "p05" {
		t.Errorf("scroll drew %q, want p05", got)
	}
	tui.sel = mouseSelection{active: true, pane: 5, startX: 0, startY: 0, endX: 4, endY: 0}
	if got := ysr2Frame(tui); got != "p06" {
		t.Errorf("a selection drew %q, want p06", got)
	}
	tui.sel = mouseSelection{}
	ysr2Frame(tui)
	tui.layoutState.Focused = "p09"
	tui.applyLayout()
	if got := ysr2Frame(tui); !strings.Contains(got, "p01") || !strings.Contains(got, "p09") {
		t.Errorf("a focus change drew %q, want p01 and p09", got)
	}
	ysr2SameAsFull(t, tui, s, "after a focus change")
}

// Stale-cell trigger: a pane whose last draw was not its real body (a resize
// still settling, a screen read that failed) is drawn again each frame until
// it is, with no bytes arriving.
func TestPartialFrame_APaneNotYetRealIsDrawnAgain(t *testing.T) {
	tui, s := ysr2TUI(t)
	ysr2Frame(tui)
	p := ysr2Pane(tui, "p04")
	// A resize starts the settle and marks the pane changed (its region
	// moved); the frames after that are the ones under test.
	p.resizeSettleFrames = 2
	p.markFrameChanged()
	for i := 0; i < 2; i++ {
		if got := ysr2Frame(tui); got != "p04" {
			t.Fatalf("settling frame %d drew %q, want p04", i+1, got)
		}
	}
	if got := ysr2Frame(tui); got != "p04" {
		t.Fatalf("the frame the settle ended drew %q, want p04 with its real body", got)
	}
	if got := ysr2Frame(tui); got != "" {
		t.Errorf("after the settle, a quiet frame drew %q, want none", got)
	}
	p.renderFinal = false // as a failed screen read leaves it
	if got := ysr2Frame(tui); got != "p04" {
		t.Errorf("after a failed read the frame drew %q, want p04", got)
	}
	ysr2SameAsFull(t, tui, s, "after the settle")
}

// Stale-cell trigger: the running tint fades on a timer with no bytes; the
// pane is drawn when it does.
func TestPartialFrame_ATintThatFadesDrawsThePane(t *testing.T) {
	tui, s := ysr2TUI(t)
	p := ysr2Pane(tui, "p02")
	p.mu.Lock()
	p.tintUntil = time.Now().Add(time.Hour)
	p.mu.Unlock()
	ysr2Frame(tui)
	ysr2Frame(tui)
	p.mu.Lock()
	p.tintUntil = time.Time{}
	p.mu.Unlock()
	if got := ysr2Frame(tui); got != "p02" {
		t.Errorf("the tint faded and the frame drew %q, want p02", got)
	}
	ysr2SameAsFull(t, tui, s, "after the tint faded")
}

// AC2: chrome keeps its cadence with every body idle: a working pane's sweep
// moves although its body is not drawn, and the frame matches a full one.
func TestPartialFrame_TheSweepMovesWithoutTheBody(t *testing.T) {
	tui, s := ysr2TUI(t)
	p := ysr2Pane(tui, "p03")
	// Running with no new body bytes: output a moment ago keeps the real
	// activity path calling it running for the frames below.
	p.mu.Lock()
	p.activity, p.lastOutputTime = StateRunning, time.Now()
	p.mu.Unlock()
	// A real pane's sweep starts when the pane does; from the zero time the
	// elapsed duration saturates and the sweep cannot move.
	p.kittEpoch = time.Date(2026, 10, 28, 18, 0, 0, 0, time.UTC)
	ysr2Frame(tui)
	ysr2Frame(tui)
	r := p.region
	edge := func() string {
		var b strings.Builder
		for x := r.X; x < r.X+r.W; x++ {
			_, _, st, _ := s.GetContent(x, r.Y)
			fg, _, _ := st.Decompose()
			fmt.Fprintf(&b, "%d ", fg.Hex())
		}
		return b.String()
	}
	before := edge()
	kittClock = func() time.Time { return time.Date(2026, 10, 28, 19, 0, 3, 0, time.UTC) }
	if got := ysr2Frame(tui); got != "" {
		t.Fatalf("a sweep step drew bodies %q, want none", got)
	}
	if edge() == before {
		t.Error("the sweep did not move on a frame that drew no body")
	}
	ysr2SameAsFull(t, tui, s, "after a sweep step")
}

// AC5 feed it nothing: an empty plan draws the same frame partial or full.
func TestPartialFrame_AnEmptyPlanMatchesAFullFrame(t *testing.T) {
	tui, s := newTestTUIWithScreen()
	tui.render()
	ysr2SameAsFull(t, tui, s, "an empty plan")
}

// A new screen size is a full frame even when no pane moves: with no panes
// the layout is the same empty one, and the status line moves with the
// screen's height.
func TestPartialFrame_AnEmptyPlanResizedMatchesAFullFrame(t *testing.T) {
	tui, s := newTestTUIWithScreen()
	tui.render()
	s.SetSize(120, 50) // taller: the old status line row stays on screen
	ysr2SameAsFull(t, tui, s, "an empty plan after a resize")
}
