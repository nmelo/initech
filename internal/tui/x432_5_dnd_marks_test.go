package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// ini-x432.5: an agent in do-not-disturb shows it on its own pane, a "dnd"
// mark on the ribbon beside the held-mail mark, and in the Agents overlay, a
// "· dnd <age>" suffix after the state word and the held count. Both go at
// the next draw once do-not-disturb ends. Every test reads a rendered frame.

// dndRibbon renders p and returns its ribbon row as text.
func dndRibbon(t *testing.T, p *Pane) string {
	t.Helper()
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(60, 11)
	p.Render(s, false, false, 3, Selection{})
	var b strings.Builder
	for x := 0; x < 60; x++ {
		ch, _, _, _ := s.GetContent(x, 10)
		b.WriteRune(ch)
	}
	return b.String()
}

func dndRibbonPane() *Pane {
	return &Pane{
		name: "eng1", emu: vt.NewSafeEmulator(60, 9), alive: true, visible: true,
		activity: StateIdle, region: Region{X: 0, Y: 0, W: 60, H: 11},
	}
}

// AC1 and AC2, the ribbon: the mark while on, gone at the next draw after it
// ends.
func TestDNDMarks_TheRibbonCarriesTheMarkWhileOn(t *testing.T) {
	p := dndRibbonPane()
	if got := dndRibbon(t, p); strings.Contains(got, "dnd") {
		t.Fatalf("precondition: a pane never in do-not-disturb shows %q", got)
	}
	p.startDNDFor(time.Now(), 0)
	if got := dndRibbon(t, p); !strings.Contains(got, "3 eng1 dnd") {
		t.Errorf("ribbon while on: %q, want the badge to read \"3 eng1 dnd\"", got)
	}
	p.endDND(time.Now())
	if got := dndRibbon(t, p); strings.Contains(got, "dnd") {
		t.Errorf("ribbon after it ended: %q still carries the mark", got)
	}
}

// The mark sits beside the held-mail mark, after it.
func TestDNDMarks_TheRibbonMarkFollowsTheHeldMailMark(t *testing.T) {
	p := dndRibbonPane()
	p.heldCount.Store(1) // one message waiting out the typing hold
	if p.HeldMessages() == 0 {
		t.Fatal("precondition: the pane holds no typing-hold mail")
	}
	p.startDNDFor(time.Now(), 0)
	if got := dndRibbon(t, p); !strings.Contains(got, "3 eng1 ✉ dnd") {
		t.Errorf("ribbon: %q, want \"3 eng1 ✉ dnd\"", got)
	}
}

// dndOverlayRow renders the overlay and returns the row that names the agent.
func dndOverlayRow(t *testing.T, tui *TUI, s tcell.SimulationScreen, name string) string {
	t.Helper()
	rows := overlayFrameRows(tui, s)
	_, y, ok := overlayDotOf(rows, name)
	if !ok {
		t.Fatalf("%s is not drawn:\n%s", name, framesText(rows))
	}
	return strings.TrimRight(string(rows[y]), " ")
}

// AC1 and AC2, the overlay: "· dnd <age>" after the state word, the age
// growing, gone after it ends.
func TestDNDMarks_TheOverlayRowCarriesTheSuffixWhileOn(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	setOverlayActivity(t, tui, map[string]ActivityState{"eng1": StateIdle, "eng2": StateIdle})
	eng1 := tui.panes[0].(*Pane)
	if row := dndOverlayRow(t, tui, s, "eng1"); strings.Contains(row, "dnd") {
		t.Fatalf("precondition: never on, the row reads %q", row)
	}
	eng1.startDNDFor(time.Now(), 0)
	if row := dndOverlayRow(t, tui, s, "eng1"); !strings.Contains(row, "idle · dnd 0s") {
		t.Errorf("just on: %q, want \"idle · dnd 0s\"", row)
	}
	eng1.mu.Lock()
	eng1.dndSince = time.Now().Add(-5 * time.Minute)
	eng1.mu.Unlock()
	if row := dndOverlayRow(t, tui, s, "eng1"); !strings.Contains(row, "idle · dnd 5m") {
		t.Errorf("five minutes on: %q, want \"idle · dnd 5m\"", row)
	}
	eng1.endDND(time.Now())
	if row := dndOverlayRow(t, tui, s, "eng1"); strings.Contains(row, "dnd") {
		t.Errorf("after it ended: %q still carries the suffix", row)
	}
}

// AC3: held count and dnd read together, held first.
func TestDNDMarks_HeldAndDNDReadTogether(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	setOverlayActivity(t, tui, map[string]ActivityState{"eng1": StateIdle})
	eng1 := tui.panes[0].(*Pane)
	eng1.startDNDFor(time.Now().Add(-5*time.Minute), 0)
	eng1.EnqueueMessage("one", true)
	eng1.EnqueueMessage("two", true)
	if n := eng1.QueuedMessageCount(); n != 2 {
		t.Fatalf("precondition: %d queued, want 2", n)
	}
	if row := dndOverlayRow(t, tui, s, "eng1"); !strings.Contains(row, "idle · 2 held · dnd 5m") {
		t.Errorf("row %q, want \"idle · 2 held · dnd 5m\"", row)
	}
}

// AC1: the column is by state. A running agent in do-not-disturb stays on the
// working side, an idle one on the other.
func TestDNDMarks_TheColumnIsStillTheState(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	setOverlayActivity(t, tui, map[string]ActivityState{"eng1": StateRunning, "eng2": StateIdle})
	tui.panes[0].(*Pane).startDNDFor(time.Now(), 0)
	tui.panes[1].(*Pane).startDNDFor(time.Now(), 0)
	rows := overlayFrameRows(tui, s)
	left, right := leftRight(t, overlayColumns(t, rows, []string{"eng1", "eng2"}), rows)
	if strings.Join(left, ",") != "eng2" || strings.Join(right, ",") != "eng1" {
		t.Errorf("left %v right %v, want eng2 left (idle) and eng1 right (running)\n%s", left, right, framesText(rows))
	}
}

// AC4 feed it nothing: a fleet never in do-not-disturb draws no dnd word
// anywhere in the overlay.
func TestDNDMarks_NeverOnDrawsTodaysFrame(t *testing.T) {
	tui, s := mixedFleet(t)
	if frame := framesText(overlayFrameRows(tui, s)); strings.Contains(frame, "dnd") {
		t.Errorf("a fleet never in do-not-disturb drew \"dnd\":\n%s", frame)
	}
}
