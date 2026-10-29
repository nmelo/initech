package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-fg1q: one meaning of "working" wherever the TUI decides running versus
// idle. Working = running, or background (a quiet pane whose footer reports a
// background shell, monitor or task). The splits keep a background agent on
// the right, live mode ranks and counts it as running, the top title turns
// green for it and its ribbon sweeps. The background screens are the real
// Claude Code 2.1.310 Haiku footers of the pi8p corpus.

// (a) The predicate.
func TestWorking_RunningAndBackgroundAreWorkingNothingElseIs(t *testing.T) {
	for st, want := range map[ActivityState]bool{
		StateRunning:      true,
		StateBackground:   true,
		StateIdle:         false,
		StateWaitingInput: false,
		StateSuspended:    false,
		StateDead:         false,
	} {
		if got := paneIsWorking(&mockPaneView{name: "eng1", alive: true, activity: st}); got != want {
			t.Errorf("%v: working %v, want %v", st, got, want)
		}
	}
}

// The predicate reads the pane's real state: a quiet pane whose footer shows
// a background shell is working, the same pane once the shell ends is not,
// and a dialog over it is waiting, not working.
func TestWorking_ARealBackgroundFooterIsWorking(t *testing.T) {
	tu, p := pi8pPane()
	t0 := time.Now()
	pi8pTick(t, tu, p, "haiku310_landing_shell.txt", StateIdle, t0)
	if !paneIsWorking(p) {
		t.Fatalf("a quiet pane with a background shell reads %v, not working", p.Activity())
	}
	pi8pTick(t, tu, p, "haiku310_landing_shell.txt", StateWaitingInput, t0.Add(time.Second))
	if paneIsWorking(p) {
		t.Errorf("a waiting pane with a background shell reads working; waiting is the stronger state")
	}
	pi8pTick(t, tu, p, "haiku310_landed.txt", StateIdle, t0.Add(2*time.Second))
	if paneIsWorking(p) {
		t.Errorf("the shell ended and the pane is idle, yet it reads working (%v)", p.Activity())
	}
}

// swapPane replaces the named pane in the TUI with p.
func swapPane(t *testing.T, tui *TUI, p *Pane) {
	t.Helper()
	for i, pv := range tui.panes {
		if pv.Name() == p.name {
			tui.panes[i] = p
			return
		}
	}
	t.Fatalf("no pane %q to swap", p.name)
}

// (b) Live list split: a pane that goes from running to background keeps its
// box across the hold; when the shell ends and the pane is idle it leaves.
func TestLiveList_ABackgroundAgentKeepsItsBoxUntilTheJobEnds(t *testing.T) {
	tui := listFleet(t, "super", "eng1", "eng2")
	setActivity(tui, "eng2", StateIdle)
	eng1 := newEmuPane("eng1", 160, 45)
	swapPane(t, tui, eng1)
	altShiftL(tui)
	if !strings.Contains(listJoined(tui), "eng1") {
		t.Fatalf("precondition: running eng1 has no box: %s", listJoined(tui))
	}
	t0 := time.Now()
	for _, at := range []time.Duration{time.Second, liveHoldDuration + time.Second, 3 * liveHoldDuration} {
		pi8pTick(t, tui, eng1, "haiku310_landing_shell.txt", StateIdle, t0.Add(at))
		if got := eng1.Activity(); got != StateBackground {
			t.Fatalf("precondition at +%v: eng1 reads %v, want background", at, got)
		}
		tui.tickLiveList(t0.Add(at))
		if !strings.Contains(listJoined(tui), "eng1") {
			t.Fatalf("at +%v eng1 (background, shell running) lost its box: %s", at, listJoined(tui))
		}
	}
	end := t0.Add(3*liveHoldDuration + time.Second)
	pi8pTick(t, tui, eng1, "haiku310_landed.txt", StateIdle, end)
	tui.tickLiveList(end)
	if strings.Contains(listJoined(tui), "eng1") {
		t.Errorf("the shell ended and eng1 is idle past its hold, yet it keeps a box: %s", listJoined(tui))
	}
}

// (c) Live focus split: the background pane stays on the right, and with it
// the only other agent the right side never says nobody is working.
func TestLiveFocus_ABackgroundAgentStaysOnTheRight(t *testing.T) {
	tui, s := newTestTUIWithScreen("a", "b")
	// Wide enough that b's right-side region holds the 160-column capture
	// without wrapping its footer.
	s.SetSize(300, 60)
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.Focused = "a"
	b := newEmuPane("b", 160, 45)
	swapPane(t, tui, b)
	altShiftF(tui)
	t0 := time.Now()
	at := t0.Add(liveHoldDuration + time.Second)
	pi8pTick(t, tui, b, "haiku310_landing_shell.txt", StateIdle, at)
	if got := b.Activity(); got != StateBackground {
		t.Fatalf("precondition: b reads %v, want background", got)
	}
	tui.tickLiveFocus(at)
	if !strings.Contains(liveFocusJoined(tui.layoutState.RightSet), "b") {
		t.Fatalf("b (background, shell running) left the right side after its hold: %v", tui.layoutState.RightSet)
	}
	tui.applyLayout()
	tui.render()
	if got := simScreenText(s); strings.Contains(got, liveFocusNoneWorking) {
		t.Errorf("the right side says %q while b's background shell runs", liveFocusNoneWorking)
	}
}

// (d) Live mode: equal in every signal but state, running and background
// score the same...
func TestLiveMode_BackgroundScoresAsRunning(t *testing.T) {
	now := time.Now()
	sig := func(st ActivityState) *mockPaneView {
		return &mockPaneView{name: "eng1", alive: true, activity: st, runStart: now.Add(-time.Minute), beadID: "ini-x"}
	}
	run, bg := convictionScore(sig(StateRunning), now), convictionScore(sig(StateBackground), now)
	if run != bg {
		t.Errorf("running scores %d, background %d; equal in every other signal they must score the same", run, bg)
	}
	if idle := convictionScore(sig(StateIdle), now); idle >= bg {
		t.Errorf("idle scores %d, background %d; the working signal must still separate them", idle, bg)
	}
}

// ...and a background pane counts toward the agents live mode shows: with
// only its working signal it is visible, as a running pane is.
func TestLiveMode_ABackgroundPaneCountsAsActive(t *testing.T) {
	now := time.Now()
	panes := []PaneView{
		&mockPaneView{name: "super", alive: true, activity: StateIdle},
		&mockPaneView{name: "eng1", alive: true, activity: StateBackground, runStart: now.Add(-time.Minute)},
	}
	le := NewLiveEngine(0, map[string]int{"super": 0}, nil)
	got := le.TickAuto(panes, now)
	if !strings.Contains(strings.Join(got, ","), "eng1") {
		t.Errorf("a background pane with a run under way is not shown in live mode: %v", got)
	}
}

// bgPane is a real Pane reading background: idle, with a background shell
// recorded from its footer.
func bgPane(p *Pane) *Pane {
	p.mu.Lock()
	p.activity = StateIdle
	p.bgWork = backgroundWork{shells: 1, since: time.Now()}
	p.mu.Unlock()
	return p
}

// (e) The top title is green with one background pane and no running pane.
func TestRenderTop_TitleGreenWhenOnlyABackgroundPane(t *testing.T) {
	tui, s := topTestTUI([]topEntry{{Name: "eng1", PID: 100, Status: "idle"}})
	bgPane(tui.panes[0].(*Pane))
	if got := tui.panes[0].Activity(); got != StateBackground {
		t.Fatalf("precondition: pane reads %v, want background", got)
	}
	tui.renderTop()
	row := findRow(readAllScreen(s), "initech top")
	if row < 0 {
		t.Fatal("title not found")
	}
	w, _ := s.Size()
	for x := 0; x < w; x++ {
		if c, style, _ := s.Get(x, row); c == "i" {
			if _, bg, _ := style.Decompose(); bg == tcell.ColorDarkGreen {
				return
			}
		}
	}
	t.Error("title is not green with a background pane working")
}

// (f) Feed it nothing: an empty fleet scores nothing and shows nobody, and a
// dead pane is never working whatever its stored state.
func TestWorking_NothingToShow(t *testing.T) {
	le := NewLiveEngine(0, nil, nil)
	if got := le.TickAuto(nil, time.Now()); len(got) != 0 {
		t.Errorf("an empty fleet shows %v", got)
	}
	if paneIsWorking(&mockPaneView{name: "x", activity: StateDead}) {
		t.Error("a dead pane reads working")
	}
}

// Entering either split with agents already in the background places them at
// once, as entry places running agents: the seed fills the right side, where
// the engine alone would add one per tick. Three agents, because entry runs
// two engine ticks: with two or fewer they would be placed either way.
func TestSplits_ABackgroundAgentIsPlacedOnEntry(t *testing.T) {
	for _, split := range []struct {
		name  string
		enter func(*TUI)
		right func(*TUI) string
	}{
		{"live focus", altShiftF, func(tu *TUI) string { return liveFocusJoined(tu.layoutState.RightSet) }},
		{"live list", altShiftL, listJoined},
	} {
		tui := listFleet(t, "super", "eng1", "eng2", "eng3")
		// Paint them all, then one maintenance tick: the tick runs once a second,
		// so a second tick at the same instant reads nothing.
		var bg []*Pane
		for _, name := range []string{"eng1", "eng2", "eng3"} {
			p := newEmuPane(name, 160, 45)
			swapPane(t, tui, p)
			lvxwPaint(p, lvxwFixture(t, "haiku310_landing_shell.txt"))
			p.mu.Lock()
			p.activity = StateIdle
			p.mu.Unlock()
			bg = append(bg, p)
		}
		tui.modalMaintenance(time.Now())
		for _, p := range bg {
			if got := p.Activity(); got != StateBackground {
				t.Fatalf("%s precondition: %s reads %v, want background", split.name, p.name, got)
			}
		}
		split.enter(tui)
		if got := split.right(tui); !strings.Contains(got, "eng1") || !strings.Contains(got, "eng2") || !strings.Contains(got, "eng3") {
			t.Errorf("%s: background eng1, eng2 and eng3 are not all on the right at entry: %s", split.name, got)
		}
	}
}
