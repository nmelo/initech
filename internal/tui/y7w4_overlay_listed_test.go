package tui

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-7w4: the Alt+s overlay lists only the agents doing something or needing
// attention: running, background, waiting, pending input, a dialog or banner
// on screen, dead, and idle with a stuck bead. Idle and suspended agents have
// no row; when nothing is listed, the line "all idle".

// listedTen are the agents that do something or need attention, in the
// fixture's panel order; quietForty are idle or suspended with nothing stuck.
var listedTen = []string{"aa-run1", "aa-run2", "ab-bg", "ac-wait1", "ac-wait2", "ad-pend", "ae-dialog", "af-banner", "ag-dead", "ah-stuck"}

func quietForty() []string {
	var out []string
	for i := 1; i <= 30; i++ {
		out = append(out, fmt.Sprintf("zi-idle%02d", i))
	}
	for i := 1; i <= 10; i++ {
		out = append(out, fmt.Sprintf("zs-susp%02d", i))
	}
	return out
}

// setPaneState stores a pane's state as the detectors would.
func setPaneState(p *Pane, st ActivityState) {
	p.mu.Lock()
	p.activity = st
	if st == StateSuspended {
		p.suspended = true
	}
	p.mu.Unlock()
}

// screenThenIdle paints a capture on the pane, lets one maintenance tick read
// it, and leaves the pane's state idle: the dialog or banner is on screen
// while the state reads idle, so only the screen can list it.
func screenThenIdle(t *testing.T, p *Pane, file string) {
	t.Helper()
	rows := lvxwFixture(t, file)
	p.emu.Resize(100, len(rows)+2)
	lvxwPaint(p, rows)
	var last time.Time
	maintainModal([]PaneView{p}, time.Now(), &last)
	setPaneState(p, StateIdle)
}

// fiftyTUI is the bead's fixture window: ten doing something or needing
// attention, forty idle or suspended.
func fiftyTUI(t *testing.T, now time.Time) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	names := append(append([]string{}, listedTen...), quietForty()...)
	tui, s := newTestTUIWithScreen(names...)
	s.SetSize(160, 60)
	for _, n := range []string{"aa-run1", "aa-run2"} {
		setPaneState(ysr2Pane(tui, n), StateRunning)
	}
	bg := ysr2Pane(tui, "ab-bg")
	setPaneState(bg, StateIdle)
	bg.bgWork = backgroundWork{shells: 1, since: now.Add(-time.Minute)}
	for _, n := range []string{"ac-wait1", "ac-wait2"} {
		setPaneState(ysr2Pane(tui, n), StateWaitingInput)
	}
	pend := ysr2Pane(tui, "ad-pend")
	setPaneState(pend, StateIdle)
	pend.pendingInput = "half a line"
	screenThenIdle(t, ysr2Pane(tui, "ae-dialog"), "haiku312_rm_guard_prompt_w80.txt")
	screenThenIdle(t, ysr2Pane(tui, "af-banner"), "haiku316_trust_w100.txt")
	setPaneState(ysr2Pane(tui, "ag-dead"), StateDead)
	setPaneState(ysr2Pane(tui, "ah-stuck"), StateIdle)
	for _, n := range quietForty() {
		if strings.HasPrefix(n, "zs-") {
			setPaneState(ysr2Pane(tui, n), StateSuspended)
		} else {
			setPaneState(ysr2Pane(tui, n), StateIdle)
		}
	}
	// ah-stuck's bead is quiet two hours: stuck by the pace rule. zi-idle01's
	// is quiet ten minutes: not stuck, so zi-idle01 is idle and unlisted.
	tui.notePaceAnswer([]bdBead{
		{ID: "ini-st.1", Status: "in_progress", Assignee: "ah-stuck", UpdatedAt: now.Add(-2 * time.Hour)},
		{ID: "ini-st.2", Status: "in_progress", Assignee: "zi-idle01", UpdatedAt: now.Add(-10 * time.Minute)},
	}, true)
	tui.refreshPaceLines(now)
	return tui, s
}

// overlayListedNames is every agent with a dot in the frame, left column then
// right, each top to bottom.
func overlayListedNames(t *testing.T, tui *TUI, rows [][]rune, all []string) (left, right []string) {
	t.Helper()
	var drawn []string
	for _, n := range all {
		if _, _, ok := overlayDotOf(rows, n); ok {
			drawn = append(drawn, n)
		}
	}
	if len(drawn) == 0 {
		return nil, nil
	}
	return leftRight(t, tui, overlayColumns(t, rows, drawn), rows)
}

// AC1: exactly the ten, in band order, in their columns; no idle or suspended
// row. The stuck agent's row reads stuck after its state.
func TestOverlayListed_OnlyTheTenAreListed(t *testing.T) {
	now := time.Now()
	tui, s := fiftyTUI(t, now)
	rows := overlayFrameRows(tui, s)
	all := append(append([]string{}, listedTen...), quietForty()...)
	left, right := overlayListedNames(t, tui, rows, all)
	if got, want := strings.Join(left, ","), "aa-run1,aa-run2,ab-bg"; got != want {
		t.Errorf("left column %s, want %s\n%s", got, want, framesText(rows))
	}
	if got, want := strings.Join(right, ","), "ac-wait1,ac-wait2,ad-pend,ae-dialog,af-banner,ag-dead,ah-stuck"; got != want {
		t.Errorf("right column %s, want %s\n%s", got, want, framesText(rows))
	}
	x, y, _ := overlayDotOf(rows, "ah-stuck")
	if !strings.Contains(string(rows[y][x:]), "idle · stuck") {
		t.Errorf("the stuck row reads %q, want 'idle · stuck'", strings.TrimSpace(string(rows[y][x:])))
	}
	if frame := framesText(rows); strings.Contains(frame, "zi-") || strings.Contains(frame, "zs-") || strings.Contains(frame, "all idle") {
		t.Errorf("an idle or suspended agent, or the all idle line, is drawn:\n%s", frame)
	}
}

// AC2: all idle or suspended: "all idle" where the columns are; the footer and
// the effort rows as before; one starting to run replaces the line.
func TestOverlayListed_AllIdleIsOneLine(t *testing.T) {
	now := time.Now()
	tui, s := newTestTUIWithScreen("eng1", "eng2", "eng3")
	setPaneState(ysr2Pane(tui, "eng1"), StateIdle)
	setPaneState(ysr2Pane(tui, "eng2"), StateIdle)
	setPaneState(ysr2Pane(tui, "eng3"), StateSuspended)
	tui.layoutState.Hidden["eng2"] = true
	tui.notePaceAnswer(effortFixture(now), true)
	tui.refreshPaceLines(now)
	rows := overlayFrameRows(tui, s)
	y := tui.overlayBounds.y + 1
	if line := strings.TrimSpace(strings.Trim(string(rows[y]), " │")); !strings.Contains(string(rows[y]), "all idle") {
		t.Errorf("first row %q, want all idle\n%s", line, framesText(rows))
	}
	if tui.overlayBounds.agentCount != 1 || len(tui.overlayBounds.dots) != 0 {
		t.Errorf("list rows %d, dots %d: want the one line and nothing to click", tui.overlayBounds.agentCount, len(tui.overlayBounds.dots))
	}
	rule, lines := effortBlock(tui, rows)
	if !strings.Contains(rule, "──") || len(lines) < 3 || lines[0] != "ini-abc 0/1 · 0/h" {
		t.Errorf("effort rows under the line: rule %q rows %q\n%s", rule, lines, framesText(rows))
	}
	if frame := framesText(rows); !strings.Contains(frame, "2 visible, 1 hidden") {
		t.Errorf("the footer no longer counts every agent:\n%s", frame)
	}
	setPaneState(ysr2Pane(tui, "eng1"), StateRunning)
	rows = overlayFrameRows(tui, s)
	if frame := framesText(rows); strings.Contains(frame, "all idle") {
		t.Errorf("eng1 runs and the all idle line stays:\n%s", frame)
	}
	if _, _, ok := overlayDotOf(rows, "eng1"); !ok {
		t.Errorf("eng1 runs and has no row:\n%s", framesText(rows))
	}
}

// AC3: going idle leaves at the next draw; a stuck idle agent stays until its
// bead moves (at the minute's refresh) or it works; suspended leaves; waking
// into running appears; dead stays until restarted (running) or removed.
func TestOverlayListed_RowsComeAndGoWithTheirState(t *testing.T) {
	now := time.Now()
	tui, s := fiftyTUI(t, now)
	drawn := func(n string) bool {
		_, _, ok := overlayDotOf(overlayFrameRows(tui, s), n)
		return ok
	}
	setPaneState(ysr2Pane(tui, "aa-run1"), StateIdle)
	if drawn("aa-run1") {
		t.Error("aa-run1 went idle and is still listed")
	}
	setPaneState(ysr2Pane(tui, "aa-run2"), StateSuspended)
	if drawn("aa-run2") {
		t.Error("aa-run2 was suspended and is still listed")
	}
	p := ysr2Pane(tui, "zs-susp01")
	p.mu.Lock()
	p.suspended = false
	p.mu.Unlock()
	setPaneState(p, StateRunning)
	if !drawn("zs-susp01") {
		t.Error("zs-susp01 woke into running and has no row")
	}
	// The stuck agent's bead moves in bd: it stays until the minute's refresh.
	tui.notePaceAnswer([]bdBead{{ID: "ini-st.1", Status: "in_progress", Assignee: "ah-stuck", UpdatedAt: now}}, true)
	tui.refreshPaceLines(now.Add(30 * time.Second))
	if !drawn("ah-stuck") {
		t.Error("ah-stuck left before the pace refresh")
	}
	tui.refreshPaceLines(now.Add(time.Minute))
	if drawn("ah-stuck") {
		t.Error("ah-stuck's bead moved and it is still listed after the refresh")
	}
	// Dead stays; restarted it is running; removed it is gone.
	if !drawn("ag-dead") {
		t.Error("a dead agent is not listed")
	}
	setPaneState(ysr2Pane(tui, "ag-dead"), StateRunning)
	rows := overlayFrameRows(tui, s)
	left, _ := overlayListedNames(t, tui, rows, []string{"ag-dead", "zs-susp01", "ab-bg"})
	if !strings.Contains(strings.Join(left, ","), "ag-dead") {
		t.Errorf("a restarted agent is not in the working column: %v", left)
	}
	var keep []PaneView
	for _, pv := range tui.panes {
		if pv.Name() != "ag-dead" {
			keep = append(keep, pv)
		}
	}
	tui.panes = keep
	if drawn("ag-dead") {
		t.Error("a removed agent is still listed")
	}
}

// AC3: a minute of frames with the overlay open makes no bd call; the stuck
// read is the pace refresh's.
func TestOverlayListed_NoBdCallFromAFrame(t *testing.T) {
	var calls atomic.Int32
	old := bdListBeads
	bdListBeads = func(ctx context.Context, dir string) ([]bdBead, error) {
		calls.Add(1)
		return nil, nil
	}
	t.Cleanup(func() { bdListBeads = old })
	now := time.Now()
	tui, s := fiftyTUI(t, now)
	for f := 0; f < 60; f++ {
		tui.refreshPaceLines(now.Add(time.Duration(f) * time.Second))
		tui.render()
		overlayFrameRows(tui, s)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("a minute of frames called bd %d times, want 0", n)
	}
}

// AC4: the grid and the agents panel are unchanged by the filter: the frame
// with the overlay off, and the panel, are the same as with every agent
// working (nothing for the filter to drop).
func TestOverlayListed_TheGridAndThePickerAreUnchanged(t *testing.T) {
	now := time.Now()
	frame := func(filtered, picker bool) string {
		tui, s := fiftyTUI(t, now)
		if !filtered {
			overlayListedOff = true
			defer func() { overlayListedOff = false }()
		}
		tui.layoutState.Overlay = false
		if picker {
			tui.agents.active = true
		}
		s.Clear()
		tui.render()
		if picker {
			tui.renderAgentsGrid()
		}
		return framesText(overlayScreenRows(s))
	}
	for _, picker := range []bool{false, true} {
		if a, b := frame(true, picker), frame(false, picker); a != b {
			t.Errorf("picker open %v: the frame depends on the overlay filter:\n%s\nwant\n%s", picker, a, b)
		}
	}
	if !strings.Contains(frame(true, true), "zi-idle30") {
		t.Error("the agents panel no longer shows an idle agent")
	}
}

// AC4: a listed row's dot still toggles.
func TestOverlayListed_AListedDotStillToggles(t *testing.T) {
	now := time.Now()
	tui, s := fiftyTUI(t, now)
	stepOverlayClock(t)
	x, y, ok := overlayDotOf(overlayFrameRows(tui, s), "ac-wait2")
	if !ok {
		t.Fatal("ac-wait2 is not drawn")
	}
	tui.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, 0))
	if !tui.layoutState.Hidden["ac-wait2"] {
		t.Error("a click on a listed dot did not hide it")
	}
}

// AC5: an agent whose state is unknown stays listed, on the right.
func TestOverlayListed_AnUnknownStateIsListed(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	setPaneState(ysr2Pane(tui, "eng1"), StateIdle)
	setPaneState(ysr2Pane(tui, "eng2"), ActivityState(99))
	rows := overlayFrameRows(tui, s)
	_, right := overlayListedNames(t, tui, rows, []string{"eng1", "eng2"})
	if strings.Join(right, ",") != "eng2" {
		t.Errorf("right column %v, want the unknown agent alone\n%s", right, framesText(rows))
	}
}
