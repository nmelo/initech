package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// The activity sweep is capped at about five steps a second (ini-ysr, step
// approved 2026-10-28). Measured before the cap: one running pane with no new
// output wrote about 5 KB a second to the terminal, 30 writes a second, all on
// its top edge, because the sweep moved every frame. Between steps the bar's
// cells are now identical, so the terminal is sent nothing.

// sweepBarAt renders the top edge of a working pane at elapsed time d after
// its sweep epoch and returns the bar as text plus colours.
func sweepBarAt(t *testing.T, p *Pane, epoch time.Time, d time.Duration) string {
	t.Helper()
	old := kittClock
	kittClock = func() time.Time { return epoch.Add(d) }
	defer func() { kittClock = old }()
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(80, 11)
	p.Render(s, false, false, 1, Selection{})
	var b strings.Builder
	for x := 0; x < 80; x++ {
		_, style, _ := s.Get(x, 0)
		fg, _, _ := style.Decompose()
		b.WriteString(fg.String())
		b.WriteByte(' ')
	}
	return b.String()
}

func sweepPane(epoch time.Time) *Pane {
	return &Pane{
		name: "eng1", emu: vt.NewSafeEmulator(80, 10), alive: true, visible: true,
		activity: StateRunning, kittEpoch: epoch, region: Region{X: 0, Y: 0, W: 80, H: 11},
	}
}

// Over one second at 30 frames a second the bar takes at most five distinct
// looks (six where a step boundary falls inside the window).
func TestSweepCap_AtMostFiveStepsASecond(t *testing.T) {
	epoch := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	p := sweepPane(epoch)
	seen := map[string]bool{}
	for f := 0; f < 30; f++ {
		seen[sweepBarAt(t, p, epoch, time.Second+time.Duration(f)*time.Second/30)] = true
	}
	if n := len(seen); n > 6 {
		t.Errorf("the sweep took %d distinct looks in one second of frames, want at most 6", n)
	}
}

// The twin: it still moves. Across a full second the bar changes at least
// four times, and two frames in the same step are identical.
func TestSweepCap_StillMovesAndHoldsWithinAStep(t *testing.T) {
	epoch := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	p := sweepPane(epoch)
	seen := map[string]bool{}
	for f := 0; f < 30; f++ {
		seen[sweepBarAt(t, p, epoch, time.Second+time.Duration(f)*time.Second/30)] = true
	}
	if n := len(seen); n < 4 {
		t.Errorf("the sweep took only %d looks in a second; it must still animate", n)
	}
	a := sweepBarAt(t, p, epoch, 2*time.Second+10*time.Millisecond)
	b := sweepBarAt(t, p, epoch, 2*time.Second+150*time.Millisecond)
	if a != b {
		t.Error("two frames inside one step drew different bars")
	}
}

// An idle pane's bar is flat and does not move, as before.
func TestSweepCap_AnIdleBarIsFlat(t *testing.T) {
	epoch := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	p := sweepPane(epoch)
	p.activity = StateIdle
	if a, b := sweepBarAt(t, p, epoch, time.Second), sweepBarAt(t, p, epoch, 3*time.Second); a != b {
		t.Error("an idle pane's bar changed over time")
	}
}

// AC2: the glide across and back still takes eight seconds.
func TestSweepCap_TheGlideIsStillEightSeconds(t *testing.T) {
	epoch := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	p := sweepPane(epoch)
	if a, b := sweepBarAt(t, p, epoch, 1*time.Second), sweepBarAt(t, p, epoch, 9*time.Second); a != b {
		t.Error("the bar at 1 s and 9 s differ: the glide is no longer 8 s")
	}
	if a, b := sweepBarAt(t, p, epoch, 1*time.Second), sweepBarAt(t, p, epoch, 5*time.Second); a == b {
		t.Error("the bar at 1 s and 5 s are the same: the glide is not 8 s")
	}
}

// AC3: the pane's own output still draws every frame, inside one sweep step.
func TestSweepCap_ContentStillDrawsEveryFrame(t *testing.T) {
	epoch := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	p := sweepPane(epoch)
	row := func(d time.Duration) string {
		old := kittClock
		kittClock = func() time.Time { return epoch.Add(d) }
		defer func() { kittClock = old }()
		s := tcell.NewSimulationScreen("")
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		s.SetSize(80, 11)
		p.Render(s, false, false, 1, Selection{})
		var b strings.Builder
		for x := 0; x < 10; x++ {
			ch, _, _, _ := s.GetContent(x, 1)
			b.WriteRune(ch)
		}
		return b.String()
	}
	p.emu.Write([]byte("first"))
	a := row(2*time.Second + 10*time.Millisecond)
	p.emu.Write([]byte("\r\x1b[2Ksecond"))
	b := row(2*time.Second + 40*time.Millisecond)
	if !strings.HasPrefix(a, "first") || !strings.HasPrefix(b, "second") {
		t.Errorf("content inside one sweep step: %q then %q, want first then second", a, b)
	}
}

// AC4 feed it nothing: a zero-width pane draws nothing and does not panic; a
// pane whose sweep started this instant draws its bar; a pane that stops
// mid-glide draws the plain edge.
func TestSweepCap_NothingToSweep(t *testing.T) {
	epoch := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	s := &clampedScreen{Screen: tcell.NewSimulationScreen(""), r: Region{W: 0, H: 5}}
	sweepPane(epoch).renderActivityBar(s, Region{X: 0, Y: 0, W: 0, H: 5})

	p := sweepPane(epoch)
	colours := map[string]bool{}
	for _, c := range strings.Fields(sweepBarAt(t, p, epoch, 0)) {
		colours[c] = true
	}
	if len(colours) < 2 {
		t.Errorf("a sweep at its first instant drew one colour only: no bright cell")
	}
	mid := sweepBarAt(t, p, epoch, 3*time.Second)
	p.activity = StateIdle
	flat := sweepBarAt(t, p, epoch, 3*time.Second)
	if flat == mid {
		t.Error("a pane that stopped mid-glide still draws the sweep")
	}
	if strings.Count(flat, flat[:strings.Index(flat, " ")+1]) != 80 {
		t.Errorf("the stopped pane's edge is not plain: %q", flat)
	}
}
