package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// ini-ysr.4: a background pane's body is drawn at most four times a second
// while its bytes keep coming, and the frame after its last byte shows the
// final content; running panes, ribbons and the sweep are not capped.

// ysr4Clock steps the frame clock by one frame (1/30 s) per call.
func ysr4Clock(t *testing.T) func() {
	t.Helper()
	now := time.Date(2026, 10, 28, 21, 0, 0, 0, time.UTC)
	old := frameClock
	frameClock = func() time.Time { return now }
	t.Cleanup(func() { frameClock = old })
	return func() { now = now.Add(time.Second / 30) }
}

// ysr4Background puts the named pane in the background state: idle, with a
// background shell running.
func ysr4Background(p *Pane) {
	p.mu.Lock()
	p.activity = StateIdle
	p.bgWork = backgroundWork{shells: 1, since: time.Now()}
	p.mu.Unlock()
}

// ysr4Feed writes one line to the pane as readLoop does: the bytes, then the
// changed fact.
func ysr4Feed(p *Pane, n int) {
	p.emu.Write([]byte(fmt.Sprintf("\x1b[H\x1b[2Kline %d", n)))
	p.markFrameChanged()
}

func ysr4Drew(tui *TUI, name string) bool { return slices.Contains(tui.bodiesDrawn, name) }

// AC1: a background pane fed a byte every frame for a second is drawn at
// most the cap's count, and the frame after its last byte shows the final
// content.
func TestBackgroundCap_ABackgroundPaneIsDrawnAtTheCap(t *testing.T) {
	tick := ysr4Clock(t)
	tui, s := ysr3Parked(t)
	p := ysr2Pane(tui, "p03")
	ysr4Background(p)
	if a := p.Activity(); a != StateBackground {
		t.Fatalf("fixture: p03 reads %v, want background", a)
	}
	drawn := 0
	for f := 0; f < 30; f++ {
		tick()
		ysr4Feed(p, f)
		tui.render()
		if ysr4Drew(tui, "p03") {
			drawn++
		}
	}
	if drawn > backgroundRedrawsPerSec+1 {
		t.Errorf("a background pane fed every frame for a second was drawn %d times, want at most %d", drawn, backgroundRedrawsPerSec+1)
	}
	if drawn < backgroundRedrawsPerSec-1 {
		t.Errorf("a background pane fed every frame was drawn only %d times in a second; it must still update", drawn)
	}
	tick()
	tui.render()
	if got := ysr3Row(s, p.region, p.region.Y+1); !strings.HasPrefix(got, "line 29") {
		t.Errorf("the frame after the last byte shows %q, want the final content line 29", got)
	}
	ysr2SameAsFull(t, tui, s, "a background pane after its last byte")
}

// AC2: a running pane fed a byte every frame is drawn every frame.
func TestBackgroundCap_ARunningPaneIsDrawnEveryFrame(t *testing.T) {
	tick := ysr4Clock(t)
	tui, _ := ysr3Parked(t)
	p := ysr2Pane(tui, "p03")
	drawn := 0
	for f := 0; f < 30; f++ {
		tick()
		p.mu.Lock()
		p.activity, p.lastOutputTime = StateRunning, time.Now()
		p.mu.Unlock()
		ysr4Feed(p, f)
		tui.render()
		if ysr4Drew(tui, "p03") {
			drawn++
		}
	}
	if drawn != 30 {
		t.Errorf("a running pane fed every frame was drawn %d of 30 frames", drawn)
	}
}

// AC3: a background pane going running draws on the next frame; going idle
// shows what was held and then keeps its cells.
func TestBackgroundCap_LeavingBackgroundDrawsAtOnce(t *testing.T) {
	for _, to := range []string{"running", "idle"} {
		t.Run(to, func(t *testing.T) {
			tick := ysr4Clock(t)
			tui, s := ysr3Parked(t)
			p := ysr2Pane(tui, "p03")
			ysr4Background(p)
			tick()
			ysr4Feed(p, 1)
			tui.render() // drawn: the first byte after a quiet spell
			tick()
			ysr4Feed(p, 2)
			tui.render()
			if ysr4Drew(tui, "p03") {
				t.Fatal("fixture: the second byte inside a quarter second was not held")
			}
			tick()
			p.mu.Lock()
			p.bgWork = backgroundWork{}
			if to == "running" {
				p.activity, p.lastOutputTime = StateRunning, time.Now()
			}
			p.mu.Unlock()
			ysr4Feed(p, 3)
			tui.render()
			if !ysr4Drew(tui, "p03") {
				t.Errorf("a pane gone %s with new bytes was not drawn on the next frame", to)
			}
			if got := ysr3Row(s, p.region, p.region.Y+1); !strings.HasPrefix(got, "line 3") {
				t.Errorf("gone %s, the pane shows %q, want line 3", to, got)
			}
			if to == "idle" {
				body := ysr3Row(s, p.region, p.region.Y+1)
				if in, _ := ysr3Count(tui, s, 5); in != 0 {
					t.Errorf("the idle pane wrote %d cells over five frames, want 0", in)
				}
				if got := ysr3Row(s, p.region, p.region.Y+1); got != body {
					t.Errorf("the idle pane's body changed: %q, was %q", got, body)
				}
			}
			ysr2SameAsFull(t, tui, s, "a pane gone "+to)
		})
	}
}

// The cap holds the body only: a held pane's ribbon follows its bead at once.
func TestBackgroundCap_TheRibbonIsNotCapped(t *testing.T) {
	tick := ysr4Clock(t)
	tui, s := ysr3Parked(t)
	p := ysr2Pane(tui, "p03")
	ysr4Background(p)
	tick()
	ysr4Feed(p, 1)
	tui.render()
	tick()
	ysr4Feed(p, 2)
	p.mu.Lock()
	p.beadIDs = []string{"ini-held"}
	p.mu.Unlock()
	tui.render()
	if ysr4Drew(tui, "p03") {
		t.Fatal("fixture: the body was not held")
	}
	if got := ysr3Row(s, p.region, p.region.Y+p.region.H-1); !strings.Contains(got, "ini-held") {
		t.Errorf("a held pane's ribbon reads %q, want the new bead", got)
	}
}

// AC4 feed it nothing: a background pane with no bytes is not drawn; the cap
// with zero panes draws the status bar and nothing else.
func TestBackgroundCap_NothingToDraw(t *testing.T) {
	tick := ysr4Clock(t)
	tui, s := ysr3Parked(t)
	ysr4Background(ysr2Pane(tui, "p03"))
	tick()
	tui.render()
	tui.render()
	for f := 0; f < 10; f++ {
		tick()
		if in, _ := ysr3Count(tui, s, 1); in != 0 {
			t.Fatalf("frame %d: a quiet background pane wrote %d cells, want 0", f, in)
		}
	}

	tui2, s2 := newTestTUIWithScreen()
	tui2.panes = nil
	tui2.applyLayout()
	tick()
	if in, out := ysr3Count(tui2, s2, 3); in != 0 || out == 0 {
		t.Errorf("zero panes: %d pane cells, %d other cells; want 0 and some", in, out)
	}
}

// A clock that stepped back draws the pane: the cap never holds a pane until
// the clock catches up.
func TestBackgroundCap_AClockSteppedBackDoesNotHold(t *testing.T) {
	var f frameChange
	f.renderFinal = true
	at := time.Date(2026, 10, 28, 21, 0, 0, 0, time.UTC)
	f.noteBody(at)
	f.markFrameChanged()
	if !f.holdBody(true, at.Add(100*time.Millisecond)) {
		t.Fatal("control: a byte 100 ms after a draw is not held")
	}
	f.markFrameChanged()
	if f.holdBody(true, at.Add(-time.Hour)) {
		t.Error("a byte on a clock an hour behind the last draw is held")
	}
}

// A held pane drawn anyway for a view change (focus) draws its held bytes
// with it, and is not drawn again on the next frame for the same bytes.
func TestBackgroundCap_AViewChangeDrawsTheHeldBytesOnce(t *testing.T) {
	tick := ysr4Clock(t)
	tui, s := ysr3Parked(t)
	p := ysr2Pane(tui, "p03")
	ysr4Background(p)
	tick()
	ysr4Feed(p, 1)
	tui.render()
	tick()
	ysr4Feed(p, 2)
	tui.layoutState.Focused = "p03"
	tui.applyLayout()
	ysr2Settled(tui)
	tui.render()
	if !ysr4Drew(tui, "p03") {
		t.Fatal("a focus change on a held pane did not draw it")
	}
	if got := ysr3Row(s, p.region, p.region.Y+1); !strings.HasPrefix(got, "line 2") {
		t.Errorf("the pane drawn for focus shows %q, want the held line 2", got)
	}
	tick()
	tui.render()
	if ysr4Drew(tui, "p03") {
		t.Error("the held bytes drew the pane a second time on the next frame")
	}
}
