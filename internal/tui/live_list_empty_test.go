package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-lpnu (operator decision): in the live list split a box no agent fills
// draws nothing, no border and no "waiting" label. The grid keeps its shape,
// so an empty box keeps its place and nothing moves.

// firstMarkIn returns the first drawn (non-blank) cell inside r, or "".
func firstMarkIn(s tcell.SimulationScreen, r Region) string {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			if ch, _, _, _ := s.GetContent(x, y); ch != ' ' && ch != 0 {
				return fmt.Sprintf("%q at (%d,%d)", ch, x, y)
			}
		}
	}
	return ""
}

// assertEmptyBoxesBlank checks every box no agent fills draws nothing.
func assertEmptyBoxesBlank(t *testing.T, tui *TUI, s tcell.SimulationScreen, what string) {
	t.Helper()
	if len(tui.plan.Waiting) == 0 {
		t.Fatalf("%s: no empty boxes to check", what)
	}
	for i, r := range tui.plan.Waiting {
		if mark := firstMarkIn(s, r); mark != "" {
			t.Errorf("%s: empty box %d %v draws %s", what, i, r, mark)
		}
	}
}

// listScreen is a list split over a held pane and the named agents, all idle
// unless listed as working, rendered once.
func listScreen(t *testing.T, rows int, working []string, names ...string) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	tui, s := newTestTUIWithScreen(append([]string{"held"}, names...)...)
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.Focused = "held"
	tui.layoutState.Overlay = false
	busy := map[string]bool{}
	for _, n := range working {
		busy[n] = true
	}
	for _, n := range names {
		if !busy[n] {
			setActivity(tui, n, StateIdle)
		}
	}
	altShiftL(tui)
	if rows != liveListDefaultRows {
		tui.execCmd(fmt.Sprintf("grid 4x%d", rows))
	}
	tui.applyLayout()
	tui.render()
	return tui, s
}

// Criterion 1: the boxes no agent fills are blank; the two agents' boxes are
// drawn.
func TestLiveList_EmptyBoxesDrawNothing(t *testing.T) {
	tui, s := listScreen(t, 3, []string{"a", "b"}, "a", "b", "c", "d")
	if len(tui.plan.Waiting) != 10 {
		t.Fatalf("%d empty boxes, want 10 (12 boxes, 2 agents)", len(tui.plan.Waiting))
	}
	assertEmptyBoxesBlank(t, tui, s, "4x3, two working")
	for _, pr := range tui.plan.Panes {
		if firstMarkIn(s, pr.Region) == "" {
			t.Errorf("%s's box %v drew nothing", pr.Pane.Name(), pr.Region)
		}
	}
}

// Criterion 2: nobody working and no pins. The right side is entirely empty,
// with no "no agent is working" line, and the held pane keeps its 40%.
func TestLiveList_NobodyWorkingLeavesTheRightSideEmpty(t *testing.T) {
	tui, s := listScreen(t, 3, nil, "a", "b", "c")
	w, h := s.Size()
	right := Region{X: tui.plan.Waiting[0].X, Y: 0, W: w - tui.plan.Waiting[0].X, H: h - 2}
	if mark := firstMarkIn(s, right); mark != "" {
		t.Errorf("the right side %v draws %s, want it empty", right, mark)
	}
	if len(tui.plan.Panes) != 1 || tui.plan.Panes[0].Pane.Name() != "held" {
		t.Fatalf("plan %v, want the held pane alone", tui.plan.Panes)
	}
	if got, want := tui.plan.Panes[0].Region.W, liveFocusRightX(w); got < want-1 || got > want+1 {
		t.Errorf("held pane width %d of %d, want its 40%% (%d)", got, w, want)
	}
}

// Criterion 3: the box an agent leaves goes blank on the next tick, with
// nothing of the agent's screen left behind; a box that fills draws the
// agent.
func TestLiveList_ABoxThatEmptiesGoesBlank(t *testing.T) {
	tui, s := listScreen(t, 3, []string{"a", "b"}, "a", "b", "c")
	var bBox Region
	for _, pr := range tui.plan.Panes {
		if pr.Pane.Name() == "b" {
			bBox = pr.Region
		}
	}
	if bBox.W == 0 || firstMarkIn(s, bBox) == "" {
		t.Fatalf("b is working but has no drawn box (%v)", bBox)
	}
	// render's per-frame maintenance recomputes Activity from output time,
	// so the states are set again before every tick.
	now := time.Now()
	step := func(after time.Duration, a, b, c ActivityState) {
		setActivity(tui, "a", a)
		setActivity(tui, "b", b)
		setActivity(tui, "c", c)
		tui.tickLiveList(now.Add(after))
		setActivity(tui, "a", a)
		setActivity(tui, "b", b)
		setActivity(tui, "c", c)
		tui.applyLayout()
		tui.render()
	}
	step(11*time.Second, StateRunning, StateIdle, StateIdle)
	if got := listJoined(tui); got != "a" {
		t.Fatalf("boxes %s after b stopped, want a alone", got)
	}
	if mark := firstMarkIn(s, bBox); mark != "" {
		t.Errorf("the box b left %v still draws %s", bBox, mark)
	}
	assertEmptyBoxesBlank(t, tui, s, "after b stopped")
	step(12*time.Second, StateRunning, StateIdle, StateRunning)
	if got := listJoined(tui); got != "a,c" {
		t.Fatalf("boxes %s after c started, want a,c", got)
	}
	for _, pr := range tui.plan.Panes {
		if pr.Pane.Name() == "c" && firstMarkIn(s, pr.Region) == "" {
			t.Errorf("c started working but its box %v drew nothing", pr.Region)
		}
	}
	assertEmptyBoxesBlank(t, tui, s, "after c started")
}

// Feed it nothing: a 4x1 list with no agent working.
func TestLiveList_AOneRowListWithNobodyWorkingIsBlank(t *testing.T) {
	tui, s := listScreen(t, 1, nil, "a", "b")
	if len(tui.plan.Waiting) != 4 {
		t.Fatalf("%d empty boxes, want the 4 of a 4x1 list", len(tui.plan.Waiting))
	}
	assertEmptyBoxesBlank(t, tui, s, "4x1, nobody working")
	var frame strings.Builder
	w, h := s.Size()
	for y := 0; y < h-2; y++ {
		for x := tui.plan.Waiting[0].X; x < w; x++ {
			ch, _, _, _ := s.GetContent(x, y)
			frame.WriteRune(ch)
		}
	}
	if strings.Contains(frame.String(), "waiting") || strings.Contains(frame.String(), liveFocusNoneWorking) {
		t.Errorf("the empty list draws a label")
	}
}
