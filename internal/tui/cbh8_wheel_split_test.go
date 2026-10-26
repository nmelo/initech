package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// ini-cbh8: in the Focus, Live Focus and Live List splits the wheel over a
// right-side pane is dropped. Before, it focused that pane, which the splits
// draw in the left slot, so each tick swapped the sides and put the displaced
// pane under the pointer for the next one.

// cbh8Splits enters each split with a held on the left.
var cbh8Splits = map[string]func(*TUI){
	"Focus split":      func(tui *TUI) { tui.toggleFocusSplit() },
	"Live Focus split": func(tui *TUI) { tui.toggleLiveFocusSplit() },
	"Live List split":  func(tui *TUI) { tui.toggleLiveListSplit() },
}

// cbh8Split is a, b and c with a held on the left of the named split, and
// history in every pane so a wheel tick has something to scroll.
func cbh8Split(t *testing.T, enter func(*TUI)) *TUI {
	t.Helper()
	tui, _ := newTestTUIWithScreen("a", "b", "c")
	for _, pv := range tui.panes {
		p := pv.(*Pane)
		var b strings.Builder
		for i := 0; i < 200; i++ {
			b.WriteString("history line " + strconv.Itoa(i) + "\r\n")
		}
		p.emu.Write([]byte(b.String()))
	}
	tui.layoutState.Focused = "a"
	tui.applyLayout()
	enter(tui)
	tui.applyLayout()
	if left := cbh8Left(tui); left != "a" {
		t.Fatalf("precondition: left slot %q, want a", left)
	}
	return tui
}

// cbh8Left is the pane the plan draws in the left slot.
func cbh8Left(tui *TUI) string {
	if len(tui.plan.Panes) == 0 {
		return ""
	}
	return tui.plan.Panes[0].Pane.Name()
}

func cbh8Region(t *testing.T, tui *TUI, name string) Region {
	t.Helper()
	for _, pr := range tui.plan.Panes {
		if pr.Pane.Name() == name {
			return pr.Region
		}
	}
	t.Fatalf("%s is not drawn", name)
	return Region{}
}

func cbh8Pane(tui *TUI, name string) *Pane {
	for _, pv := range tui.panes {
		if pv.Name() == name {
			return pv.(*Pane)
		}
	}
	return nil
}

func cbh8Offset(p *Pane) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scrollOffset
}

// Criterion 1: wheel ticks over a right-side pane move nothing and scroll
// nothing, in every split, up and down.
func TestWheelSplit_ARightSidePaneIgnoresTheWheel(t *testing.T) {
	for split, enter := range cbh8Splits {
		for _, dir := range []tcell.ButtonMask{tcell.WheelUp, tcell.WheelDown} {
			tui := cbh8Split(t, enter)
			b := cbh8Region(t, tui, "b")
			before := cbh8Offset(cbh8Pane(tui, "b"))
			// Checked after every tick: the flip alternates, so an even
			// number of ticks would land back on a and hide it.
			for i := 1; i <= 4; i++ {
				tui.handleMouse(tcell.NewEventMouse(b.X+2, b.Y+2, dir, tcell.ModNone))
				if left, f := cbh8Left(tui), tui.layoutState.Focused; left != "a" || f != "a" {
					t.Errorf("%s, wheel %v tick %d over b: left slot %q, focused %q, want a", split, dir, i, left, f)
					break
				}
			}
			if after := cbh8Offset(cbh8Pane(tui, "b")); after != before {
				t.Errorf("%s, wheel %v over b: b scrolled %d -> %d, want untouched", split, dir, before, after)
			}
		}
	}
}

// Criterion 2: over the left pane the wheel scrolls it, as before.
func TestWheelSplit_TheLeftPaneStillScrolls(t *testing.T) {
	for split, enter := range cbh8Splits {
		tui := cbh8Split(t, enter)
		a := cbh8Region(t, tui, "a")
		tui.handleMouse(tcell.NewEventMouse(a.X+2, a.Y+2, tcell.WheelUp, tcell.ModNone))
		if off := cbh8Offset(cbh8Pane(tui, "a")); off == 0 {
			t.Errorf("%s: a wheel up over the left pane did not scroll it", split)
		}
		if left := cbh8Left(tui); left != "a" {
			t.Errorf("%s: the left slot moved to %q", split, left)
		}
	}
}

// Feed it nothing: a wheel over empty right-side space changes nothing.
func TestWheelSplit_EmptyRightSpaceIsIgnored(t *testing.T) {
	// Live Focus split with nobody else working: the right side is empty.
	tui, s := newTestTUIWithScreen("a", "b", "c")
	setActivity(tui, "b", StateIdle)
	setActivity(tui, "c", StateIdle)
	tui.layoutState.Focused = "a"
	tui.applyLayout()
	tui.toggleLiveFocusSplit()
	tui.applyLayout()
	w, h := s.Size()
	tui.handleMouse(tcell.NewEventMouse(w-5, h/2, tcell.WheelUp, tcell.ModNone))
	if left := cbh8Left(tui); left != "a" || tui.layoutState.Focused != "a" {
		t.Errorf("live focus, empty right: left %q focused %q, want a", left, tui.layoutState.Focused)
	}
	// Live List split: an empty cell.
	tui2 := cbh8Split(t, cbh8Splits["Live List split"])
	if len(tui2.plan.Waiting) == 0 {
		t.Fatal("precondition: the list has an empty cell")
	}
	cell := tui2.plan.Waiting[len(tui2.plan.Waiting)-1]
	tui2.handleMouse(tcell.NewEventMouse(cell.X+1, cell.Y+1, tcell.WheelDown, tcell.ModNone))
	if left := cbh8Left(tui2); left != "a" || tui2.layoutState.Focused != "a" {
		t.Errorf("live list, empty cell: left %q focused %q, want a", left, tui2.layoutState.Focused)
	}
}
