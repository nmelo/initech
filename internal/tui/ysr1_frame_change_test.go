package tui

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// ini-ysr.1: each pane knows whether it changed since the last frame was
// drawn: bytes reached its emulator, a window 2 stream delivered a frame, it
// was scrolled, or the frame loop draws it differently (focus, dimming, its
// number, its region, the selection). The frame loop takes the fact for every
// pane it draws; nothing acts on it yet.

// ysrChanged draws one frame and returns the panes the loop found changed.
func ysrChanged(tui *TUI) string {
	tui.render()
	var names []string
	for n := range tui.frameChanged {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func ysrTUI(t *testing.T) *TUI {
	t.Helper()
	tui, _ := newTestTUIWithScreen("a", "b", "c")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 3, 1
	tui.layoutState.Focused = "a"
	tui.applyLayout()
	return tui
}

func ysrPane(tui *TUI, name string) *Pane {
	for _, p := range tui.panes {
		if p.Name() == name {
			return p.(*Pane)
		}
	}
	return nil
}

// AC4 feed it nothing: every pane is changed on its first frame and none on
// the frames after, with no bytes and no input.
func TestFrameChange_AQuietPaneIsUnchangedAfterItsFirstFrame(t *testing.T) {
	tui := ysrTUI(t)
	if got := ysrChanged(tui); got != "a,b,c" {
		t.Fatalf("first frame changed %q, want every pane", got)
	}
	for i := 0; i < 3; i++ {
		if got := ysrChanged(tui); got != "" {
			t.Fatalf("frame %d changed %q with nothing happening", i+2, got)
		}
	}
}

// AC2 and AC5: bytes from the child, through the real read loop, mark the
// pane; the frame loop takes the fact, so the next frame has it unchanged.
func TestFrameChange_BytesFromTheChildMarkThePaneUntilAFrameIsDrawn(t *testing.T) {
	p, err := NewPane(PaneConfig{Name: "a", Command: []string{"sh", "-c", "sleep 0.2; printf 'tick'; sleep 30"}}, 10, 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	p.Start()
	tui, _ := newTestTUIWithScreen()
	tui.panes = []PaneView{p}
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.Focused = "a"
	tui.applyLayout()
	ysrChanged(tui) // the first frame
	p.takeFrameChanged()

	deadline := time.Now().Add(5 * time.Second)
	for !p.dirty.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the child's bytes never marked the pane changed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := ysrChanged(tui); got != "a" {
		t.Fatalf("the frame after the bytes found %q changed, want a", got)
	}
	if p.dirty.Load() {
		t.Error("the frame loop did not take the fact: the pane still reads changed")
	}
	if got := ysrChanged(tui); got != "" {
		t.Errorf("the frame after that found %q changed, want none", got)
	}
}

// AC3: scrolling marks the pane, and only that pane.
func TestFrameChange_ScrollMarksThePane(t *testing.T) {
	tui := ysrTUI(t)
	ysrChanged(tui)
	ysrPane(tui, "b").ScrollUp(1)
	if got := ysrChanged(tui); got != "b" {
		t.Errorf("after scrolling b: %q changed, want b", got)
	}
}

// AC3: a focus change marks the pane that lost focus and the one that took
// it, whichever path moved it.
func TestFrameChange_FocusChangeMarksBothPanes(t *testing.T) {
	tui := ysrTUI(t)
	ysrChanged(tui)
	tui.layoutState.Focused = "c"
	tui.applyLayout()
	if got := ysrChanged(tui); got != "a,c" {
		t.Errorf("after focus a -> c: %q changed, want a,c", got)
	}
}

// AC3: a selection in a pane marks it.
func TestFrameChange_SelectionMarksThePane(t *testing.T) {
	tui := ysrTUI(t)
	ysrChanged(tui)
	tui.sel = mouseSelection{active: true, pane: 1, startX: 0, startY: 0, endX: 3, endY: 0}
	if got := ysrChanged(tui); got != "b" {
		t.Errorf("after selecting in b: %q changed, want b", got)
	}
}

// AC3: a layout change, which resizes every pane, marks them all.
func TestFrameChange_ALayoutChangeMarksEveryPaneItMoves(t *testing.T) {
	tui := ysrTUI(t)
	ysrChanged(tui)
	tui.layoutState.GridCols, tui.layoutState.GridRows = 1, 3
	tui.applyLayout()
	if got := ysrChanged(tui); got != "a,b,c" {
		t.Errorf("after 3x1 -> 1x3: %q changed, want a,b,c", got)
	}
}

// AC3: a window 2 pane is marked by its stream writing a frame.
func TestFrameChange_ARemoteStreamFrameMarksThePane(t *testing.T) {
	rp := &RemotePane{name: "a", host: "w2", alive: true, emu: vt.NewSafeEmulator(40, 10)}
	if rp.takeFrameChanged() {
		t.Fatal("precondition: a new remote pane reads changed")
	}
	rp.writeEmu([]byte("hello"))
	if !rp.takeFrameChanged() {
		t.Error("a stream frame did not mark the remote pane")
	}
	if rp.takeFrameChanged() {
		t.Error("taking the fact did not clear it")
	}
}

// A pane that comes back after the full-screen event log is changed: it was
// not on screen.
func TestFrameChange_PanesAreChangedAfterTheEventLog(t *testing.T) {
	tui := ysrTUI(t)
	ysrChanged(tui)
	tui.eventLogM.active = true
	tui.render()
	tui.eventLogM.active = false
	if got := ysrChanged(tui); got != "a,b,c" {
		t.Errorf("after the event log closed: %q changed, want every pane", got)
	}
}

// Each property the frame loop draws a pane with is compared on its own: a
// change in any one of them, and nothing else, marks the pane.
func TestFrameChange_EachDrawnPropertyIsCompared(t *testing.T) {
	p := testPane("a")
	base := PaneRender{Pane: p, Region: Region{X: 0, Y: 0, W: 40, H: 10}, Index: 1}
	for name, change := range map[string]func(*PaneRender, *Selection){
		"focus":     func(pr *PaneRender, _ *Selection) { pr.Focused = true },
		"dimming":   func(pr *PaneRender, _ *Selection) { pr.Dimmed = true },
		"number":    func(pr *PaneRender, _ *Selection) { pr.Index = 2 },
		"region":    func(pr *PaneRender, _ *Selection) { pr.Region.W = 39 },
		"selection": func(_ *PaneRender, s *Selection) { s.Active = true },
	} {
		last := map[PaneView]paneFrameState{}
		paneChangedForFrame(nil, last, base, Selection{}, false, false)
		if paneChangedForFrame(last, map[PaneView]paneFrameState{}, base, Selection{}, false, false) {
			t.Fatalf("%s: precondition: the same frame twice reads changed", name)
		}
		pr, sel := base, Selection{}
		change(&pr, &sel)
		if !paneChangedForFrame(last, map[PaneView]paneFrameState{}, pr, sel, false, false) {
			t.Errorf("a change of %s alone did not mark the pane", name)
		}
	}
	// The live [P] badge is drawn with the pane (ini-ysr.3).
	last := map[PaneView]paneFrameState{}
	paneChangedForFrame(nil, last, base, Selection{}, false, false)
	if !paneChangedForFrame(last, map[PaneView]paneFrameState{}, base, Selection{}, true, false) {
		t.Error("a pin alone did not mark the pane")
	}
}
