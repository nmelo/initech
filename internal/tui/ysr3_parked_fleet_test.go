package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// ini-ysr.3: an idle, suspended or hidden pane is not drawn between changes,
// its ribbon and top edge included; the running pane's sweep still moves; the
// layers over the panes (overlay, status bar, lists) still draw every frame.
// What a frame draws is counted at the screen: every cell written inside a
// pane's region, whoever wrote it.

// ysr3Counter records the cells a frame writes.
type ysr3Counter struct {
	tcell.SimulationScreen
	w       int
	written map[int]bool
}

func (c *ysr3Counter) SetContent(x, y int, ch rune, comb []rune, st tcell.Style) {
	c.written[y*c.w+x] = true
	c.SimulationScreen.SetContent(x, y, ch, comb, st)
}

// ysr3Count draws n frames and returns, summed over the frames, the cells
// written inside a pane that no layer wrote that frame (the panes' own
// drawing), and the cells written elsewhere (the status bar, the overlay...).
func ysr3Count(tui *TUI, s tcell.SimulationScreen, n int) (inPanes, outside int) {
	w, _ := s.Size()
	c := &ysr3Counter{SimulationScreen: s, w: w}
	saved := tui.screen
	tui.screen = c
	defer func() { tui.screen = saved }()
	for f := 0; f < n; f++ {
		c.written = map[int]bool{}
		tui.render()
		for i := range c.written {
			x, y := i%w, i/w
			in := false
			for _, pr := range tui.plan.Panes {
				r := pr.Region
				if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
					in = true
					break
				}
			}
			if in && !(i < len(tui.lastLayer) && tui.lastLayer[i]) {
				inPanes++
			} else {
				outside++
			}
		}
	}
	return inPanes, outside
}

// ysr3Parked is the eleven-pane grid at rest: every pane idle except the
// named ones, which are suspended, two frames in so nothing is pending.
func ysr3Parked(t *testing.T, suspended ...string) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	tui, s := ysr2TUI(t)
	for _, n := range suspended {
		p := ysr2Pane(tui, n)
		p.mu.Lock()
		p.suspended, p.activity = true, StateSuspended
		p.mu.Unlock()
	}
	tui.render()
	tui.render()
	return tui, s
}

func ysr3Row(s tcell.SimulationScreen, r Region, y int) string {
	var b strings.Builder
	for x := r.X; x < r.X+r.W; x++ {
		ch, _, _, _ := s.GetContent(x, y)
		b.WriteRune(ch)
	}
	return b.String()
}

// AC1: a parked fleet, idle and suspended, draws nothing inside any pane,
// frame after frame; the status bar still draws every frame.
func TestParkedFleet_AFrameDrawsNoPaneCells(t *testing.T) {
	tui, s := ysr3Parked(t, "p02", "p07")
	in, out := ysr3Count(tui, s, 30)
	if in != 0 {
		t.Errorf("30 frames of a parked fleet wrote %d cells inside panes, want 0", in)
	}
	if out == 0 {
		t.Error("30 frames wrote nothing outside the panes: the status bar stopped drawing")
	}
	if len(tui.chromesDrawn) != 0 || len(tui.bodiesDrawn) != 0 {
		t.Errorf("the last frame drew chrome %v and bodies %v, want none", tui.chromesDrawn, tui.bodiesDrawn)
	}
	ysr2SameAsFull(t, tui, s, "a parked fleet")
}

// AC1 twin: the overlay over a parked fleet still draws every frame, and
// when it closes the panes under it show again exactly.
func TestParkedFleet_TheOverlayStillDraws(t *testing.T) {
	tui, s := ysr3Parked(t)
	tui.layoutState.Overlay = true
	for f := 0; f < 3; f++ {
		w, _ := s.Size()
		c := &ysr3Counter{SimulationScreen: s, w: w, written: map[int]bool{}}
		tui.screen = c
		tui.render()
		tui.screen = s
		over := 0
		for i, on := range tui.lastLayer {
			if on && c.written[i] {
				over++
			}
		}
		if over < 100 {
			t.Fatalf("frame %d: the overlay wrote %d cells, want it drawn every frame", f, over)
		}
	}
	if in, _ := ysr3Count(tui, s, 3); in != 0 {
		t.Errorf("the panes under the overlay wrote %d cells of their own, want 0", in)
	}
	tui.layoutState.Overlay = false
	ysr2SameAsFull(t, tui, s, "the overlay closed over a parked fleet")
}

// AC2: a hidden pane fed bytes draws nothing; revealing it shows its current
// content, drawn once.
func TestParkedFleet_AHiddenPaneIsFedAndShownWhenRevealed(t *testing.T) {
	tui, s := ysr3Parked(t)
	if !tui.toggleHidden("p05") {
		t.Fatal("could not hide p05")
	}
	ysr2Settled(tui)
	tui.render()
	tui.render()
	p := ysr2Pane(tui, "p05")
	for _, pr := range tui.plan.Panes {
		if pr.Pane == PaneView(p) {
			t.Fatal("fixture: the hidden pane is still in the plan")
		}
	}
	p.emu.Write([]byte("\x1b[2J\x1b[Hwhile hidden"))
	p.markFrameChanged() // as readLoop does
	if in, _ := ysr3Count(tui, s, 10); in != 0 {
		t.Errorf("10 frames with a hidden pane receiving bytes wrote %d pane cells, want 0", in)
	}
	if !tui.toggleHidden("p05") {
		t.Fatal("could not reveal p05")
	}
	ysr2Settled(tui)
	tui.render()
	if got := ysr3Row(s, p.region, p.region.Y+1); !strings.HasPrefix(got, "while hidden") {
		t.Errorf("the revealed pane's first row reads %q, want its current content", got)
	}
	if got := ysr2Frame(tui); got != "" {
		t.Errorf("the frame after the reveal drew bodies %q, want none: drawn once", got)
	}
	ysr2SameAsFull(t, tui, s, "a revealed pane")
}

// AC3: a suspended pane waking draws its ribbon that frame, and its content
// when the respawn's bytes arrive.
func TestParkedFleet_ASuspendedPaneWakingDrawsItsRibbonAndContent(t *testing.T) {
	tui, s := ysr3Parked(t, "p02")
	p := ysr2Pane(tui, "p02")
	ribbonY := p.region.Y + p.region.H - 1
	if got := ysr3Row(s, p.region, ribbonY); !strings.Contains(got, "susp") {
		t.Fatalf("fixture: the suspended ribbon reads %q", got)
	}
	p.mu.Lock()
	p.waking = true
	p.mu.Unlock()
	tui.render()
	if got := ysr3Row(s, p.region, ribbonY); !strings.Contains(got, "waking") {
		t.Errorf("the frame after the wake began shows ribbon %q, want waking", got)
	}
	p.mu.Lock()
	p.waking, p.suspended, p.activity = false, false, StateRunning
	p.lastOutputTime = time.Now()
	p.mu.Unlock()
	p.emu.Write([]byte("\x1b[2J\x1b[Hawake again"))
	p.markFrameChanged()
	tui.render()
	if got := ysr3Row(s, p.region, p.region.Y+1); !strings.HasPrefix(got, "awake again") {
		t.Errorf("the woken pane's first row reads %q", got)
	}
	ysr2SameAsFull(t, tui, s, "a woken pane")
}

// AC3: a pane going idle draws its top edge flat and keeps its body cells.
func TestParkedFleet_APaneGoingIdleKeepsItsCells(t *testing.T) {
	tui, s := ysr3Parked(t)
	p := ysr2Pane(tui, "p03")
	p.mu.Lock()
	p.activity, p.lastOutputTime = StateRunning, time.Now()
	p.mu.Unlock()
	tui.render()
	tui.render()
	body := ysr3Row(s, p.region, p.region.Y+1)
	p.mu.Lock()
	p.activity, p.lastOutputTime = StateIdle, time.Time{}
	p.mu.Unlock()
	tui.render()
	if got := ysr3Row(s, p.region, p.region.Y+1); got != body {
		t.Errorf("going idle changed the body row: %q, was %q", got, body)
	}
	ysr2SameAsFull(t, tui, s, "a pane gone idle")
	if in, _ := ysr3Count(tui, s, 5); in != 0 {
		t.Errorf("the idle pane is drawn %d cells over five frames, want 0", in)
	}
}

// A window 2 pane at rest is not drawn either.
func TestParkedFleet_ARemotePaneAtRestIsNotDrawn(t *testing.T) {
	tui, s := newTestTUIWithScreen()
	rp := &RemotePane{name: "eng1", host: "w2", alive: true, emu: vt.NewSafeEmulator(118, 37)}
	rp.emu.Write([]byte(strings.Repeat("remote body ", 30)))
	tui.panes = []PaneView{rp}
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.Focused = agentKey(rp)
	tui.notifications = nil
	tui.applyLayout()
	tui.render()
	tui.render()
	if in, _ := ysr3Count(tui, s, 10); in != 0 {
		t.Errorf("a remote pane at rest wrote %d cells over ten frames, want 0", in)
	}
	rp.scroll.offset = 0
	rp.mu.Lock()
	rp.beadIDs = []string{"ini-new"}
	rp.mu.Unlock()
	tui.render()
	if got := ysr3Row(s, rp.region, rp.region.Y+rp.region.H-1); !strings.Contains(got, "ini-new") {
		t.Errorf("a new bead on a remote pane at rest: ribbon reads %q", got)
	}
	ysr2SameAsFull(t, tui, s, "a remote pane's new bead")
}

// The ribbon follows a change no byte brings: mail held, a bead claimed.
func TestParkedFleet_ARibbonChangeIsDrawn(t *testing.T) {
	tui, s := ysr3Parked(t)
	p := ysr2Pane(tui, "p04")
	p.mu.Lock()
	p.beadIDs = []string{"ini-abc"}
	p.mu.Unlock()
	tui.render()
	if got := ysr3Row(s, p.region, p.region.Y+p.region.H-1); !strings.Contains(got, "ini-abc") {
		t.Errorf("a bead claimed on an idle pane: ribbon reads %q", got)
	}
	if got := strings.Join(tui.chromesDrawn, ","); got != "p04" {
		t.Errorf("a bead change drew the chrome of %q, want p04 only", got)
	}
	ysr2SameAsFull(t, tui, s, "a new bead on an idle pane")
}

// AC4 feed it nothing: zero panes draw no pane cells and still draw the
// status bar; a pane hidden and revealed with no bytes between is unchanged.
func TestParkedFleet_NothingToDraw(t *testing.T) {
	tui, s := newTestTUIWithScreen()
	tui.panes = nil
	tui.applyLayout()
	tui.render()
	in, out := ysr3Count(tui, s, 3)
	if in != 0 || out == 0 {
		t.Errorf("zero panes: %d pane cells, %d other cells; want 0 and some", in, out)
	}

	tui2, s2 := ysr3Parked(t)
	before := ysr2Cells(s2)
	if !tui2.toggleHidden("p05") || !tui2.toggleHidden("p05") {
		t.Fatal("could not hide and reveal p05")
	}
	ysr2Settled(tui2)
	tui2.render()
	tui2.render()
	if after := ysr2Cells(s2); after != before {
		t.Error("a pane hidden and revealed with no bytes between changed the screen")
	}
}

// The live mode [P] badge is drawn with the pane: pinning or unpinning an
// idle pane shows on the next frame, and a pinned pane at rest is not drawn.
func TestParkedFleet_APinShowsAndClears(t *testing.T) {
	tui, s := ysr3Parked(t)
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.LivePinned = map[string]int{}
	tui.applyLayout()
	ysr2Settled(tui)
	tui.render()
	tui.render()
	if len(tui.plan.Panes) == 0 {
		t.Fatal("fixture: live mode planned no panes")
	}
	pr := tui.plan.Panes[0]
	ribbon := func() string { return ysr3Row(s, pr.Region, pr.Region.Y+pr.Region.H-1) }
	if strings.Contains(ribbon(), "[P]") {
		t.Fatalf("fixture: an unpinned ribbon reads %q", ribbon())
	}
	tui.layoutState.LivePinned[agentKey(pr.Pane)] = 0
	tui.render()
	if !strings.Contains(ribbon(), "[P]") {
		t.Errorf("a pinned idle pane's ribbon reads %q, want [P]", ribbon())
	}
	if in, _ := ysr3Count(tui, s, 5); in != 0 {
		t.Errorf("a pinned pane at rest wrote %d cells over five frames, want 0", in)
	}
	delete(tui.layoutState.LivePinned, agentKey(pr.Pane))
	tui.render()
	if strings.Contains(ribbon(), "[P]") {
		t.Errorf("an unpinned idle pane's ribbon still reads %q", ribbon())
	}
	ysr2SameAsFull(t, tui, s, "a pin cleared")
}

// A layer closing over panes that moved (a pane hidden shifts the others)
// puts back what each pane drew at its new place.
func TestParkedFleet_ALayerOverMovedPanesLeavesNoStaleCells(t *testing.T) {
	tui, s := ysr3Parked(t)
	before := map[string]Region{}
	for _, pr := range tui.plan.Panes {
		before[pr.Pane.Name()] = pr.Pane.GetRegion()
	}
	if !tui.toggleHidden("p02") {
		t.Fatal("could not hide p02")
	}
	ysr2Settled(tui)
	tui.render()
	tui.render()
	moved := 0
	for _, pr := range tui.plan.Panes {
		if r := pr.Pane.GetRegion(); r != before[pr.Pane.Name()] && r.W*r.H == before[pr.Pane.Name()].W*before[pr.Pane.Name()].H {
			moved++
		}
	}
	if moved == 0 {
		t.Fatal("fixture: no pane moved to a same-size region")
	}
	tui.help.active = true
	ysr2SameAsFull(t, tui, s, "help open over moved panes")
	tui.help.active = false
	ysr2SameAsFull(t, tui, s, "help closed over moved panes")
}
