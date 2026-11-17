package tui

import (
	"strings"
	"testing"
)

// ini-yfws: while a pane is zoomed the "no agent is working" line is never
// drawn; the zoomed plan holds one pane, which the empty-right check read as
// the held pane alone.

// TestLiveFocus_ZoomedOnTheHeldPaneDrawsNoLine is AC1: live focus, nobody
// working, zoom on the left (held) pane.
func TestLiveFocus_ZoomedOnTheHeldPaneDrawsNoLine(t *testing.T) {
	tui, s := newTestTUIWithScreen("a", "b", "c")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.Focused = "a"
	setActivity(tui, "b", StateIdle)
	setActivity(tui, "c", StateIdle)
	altShiftF(tui)
	tui.render()
	if !strings.Contains(simScreenText(s), liveFocusNoneWorking) {
		t.Fatalf("precondition (AC2's twin): un-zoomed, the line is drawn")
	}
	tui.cmdZoom()
	if !tui.layoutState.Zoomed {
		t.Fatal("precondition: the zoom command did not zoom")
	}
	tui.render()
	if got := simScreenText(s); strings.Contains(got, liveFocusNoneWorking) {
		t.Errorf("zoomed on a, the screen says %q", liveFocusNoneWorking)
	}
}

// TestLiveFocus_ZoomedOnARightPaneDrawsNoLine is AC3: zoom on a working
// right-side pane; and AC4: zooming out shows it, with no line, on the first
// draw.
func TestLiveFocus_ZoomedOnARightPaneDrawsNoLine(t *testing.T) {
	tui, s := newTestTUIWithScreen("a", "b", "c")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.Focused = "a"
	setActivity(tui, "b", StateRunning)
	setActivity(tui, "c", StateIdle)
	altShiftF(tui)
	tui.layoutState.Focused = "b"
	tui.cmdZoom()
	tui.render()
	if got := simScreenText(s); strings.Contains(got, liveFocusNoneWorking) {
		t.Errorf("zoomed on b, the screen says %q", liveFocusNoneWorking)
	}
	tui.cmdZoom() // out
	if tui.layoutState.Zoomed {
		t.Fatal("precondition: the zoom command did not zoom out")
	}
	tui.render()
	got := simScreenText(s)
	if strings.Contains(got, liveFocusNoneWorking) || len(tui.plan.Panes) < 2 {
		t.Errorf("zoomed out with b working: line drawn %v, plan %d panes", strings.Contains(got, liveFocusNoneWorking), len(tui.plan.Panes))
	}
}
