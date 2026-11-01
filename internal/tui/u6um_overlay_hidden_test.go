package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// hiddenFixture is three agents: eng1 idle and eng2 suspended, both hidden,
// and eng3 idle and never hidden, in the window named by windowID.
func hiddenFixture(windowID string) (*TUI, tcell.SimulationScreen) {
	tui, s := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.windowID = windowID
	if windowID != WindowOne {
		// A second window lists what window 1 served it (visiblePanesForWindow).
		tui.ownershipServed = true
		tui.paneOwnership = map[string]string{"eng1": windowID, "eng2": windowID, "eng3": windowID}
	}
	setPaneState(ysr2Pane(tui, "eng1"), StateIdle)
	setPaneState(ysr2Pane(tui, "eng2"), StateSuspended)
	setPaneState(ysr2Pane(tui, "eng3"), StateIdle)
	tui.layoutState.Hidden["eng1"] = true
	tui.layoutState.Hidden["eng2"] = true
	return tui, s
}

// In a second window, which has no Agents panel, an agent hidden there keeps
// its row and dot whatever its state, marked [h]: the dot is its only unhide
// control. A never-hidden idle agent stays unlisted, and "all idle" is not
// shown while anything is listed.
func TestOverlayHidden_ASecondWindowListsItsHiddenAgents(t *testing.T) {
	tui, s := hiddenFixture("2")
	rows := overlayFrameRows(tui, s)
	frame := framesText(rows)
	for _, n := range []string{"eng1", "eng2"} {
		x, y, ok := overlayDotOf(rows, n)
		if !ok {
			t.Fatalf("hidden %s has no row in window 2\n%s", n, frame)
		}
		if !strings.Contains(string(rows[y][x:]), n+" [h]") {
			t.Errorf("hidden %s's row %q has no [h]", n, strings.TrimSpace(string(rows[y][x:])))
		}
		// The click target: the dot cell names this agent, so a click
		// toggles it (routed to window 1, proven by the uz42 rig).
		d, ok := tui.overlayBounds.dots[[2]int{x, y}]
		if !ok || d.key != n {
			t.Errorf("the dot of %s maps to %+v (ok %v), want %s", n, d, ok, n)
		}
	}
	if _, _, ok := overlayDotOf(rows, "eng3"); ok {
		t.Errorf("never-hidden idle eng3 is listed\n%s", frame)
	}
	if strings.Contains(frame, overlayAllIdle) {
		t.Errorf("%q shown while hidden agents are listed\n%s", overlayAllIdle, frame)
	}
	if !strings.Contains(frame, "1 visible, 2 hidden") {
		t.Errorf("the footer does not count the two hidden agents:\n%s", frame)
	}
}

// The main window has the Agents panel, which unhides there, so its overlay
// stays active-only: hidden idle and suspended agents have no row, the
// window reads "all idle", and the footer still counts them hidden.
func TestOverlayHidden_TheMainWindowStaysActiveOnly(t *testing.T) {
	tui, s := hiddenFixture(WindowOne)
	rows := overlayFrameRows(tui, s)
	frame := framesText(rows)
	for _, n := range []string{"eng1", "eng2", "eng3"} {
		if _, _, ok := overlayDotOf(rows, n); ok {
			t.Errorf("%s is listed in the main window\n%s", n, frame)
		}
	}
	if !strings.Contains(frame, overlayAllIdle) || !strings.Contains(frame, "1 visible, 2 hidden") {
		t.Errorf("want %q and the hidden count\n%s", overlayAllIdle, frame)
	}
	// Positive twin: the same agents running are listed here.
	setPaneState(ysr2Pane(tui, "eng1"), StateRunning)
	if _, _, ok := overlayDotOf(overlayFrameRows(tui, s), "eng1"); !ok {
		t.Errorf("running hidden eng1 has no row in the main window")
	}
}

// A click on a hidden agent's dot unhides it, and an idle agent once
// unhidden leaves the list. Window 1 is the fleet authority, so the toggle
// applies in-process here; the same click from window 2 is the uz42 rig.
func TestOverlayHidden_TheDotUnhidesAndTheRowGoes(t *testing.T) {
	tui, s := hiddenFixture("2")
	rows := overlayFrameRows(tui, s)
	x, y, ok := overlayDotOf(rows, "eng1")
	if !ok {
		t.Fatalf("hidden eng1 has no row\n%s", framesText(rows))
	}
	tui.windowID = WindowOne // the authority applies the toggle in-process
	stepOverlayClock(t)
	tui.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, 0))
	if tui.layoutState.Hidden["eng1"] {
		t.Fatal("a click on hidden eng1's dot did not unhide it")
	}
	tui.windowID = "2"
	if _, _, ok := overlayDotOf(overlayFrameRows(tui, s), "eng1"); ok {
		t.Error("eng1, unhidden and idle, is still listed")
	}
}
