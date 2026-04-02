// QA tests for ini-eqk.5: suspended state in overlay, ribbon, and top modal.
package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// TestStateSuspended_String verifies the suspended state string label.
func TestStateSuspended_String(t *testing.T) {
	if got := StateSuspended.String(); got != "suspended" {
		t.Errorf("StateSuspended.String() = %q, want %q", got, "suspended")
	}
}

// TestIsSuspended_DefaultFalse verifies a new pane is not suspended.
func TestIsSuspended_DefaultFalse(t *testing.T) {
	p := &Pane{emu: vt.NewSafeEmulator(80, 24)}
	if p.IsSuspended() {
		t.Error("new pane should not be suspended")
	}
}

// TestSetSuspended_RoundTrip verifies SetSuspended/IsSuspended.
func TestSetSuspended_RoundTrip(t *testing.T) {
	p := &Pane{emu: vt.NewSafeEmulator(80, 24)}
	p.SetSuspended(true)
	if !p.IsSuspended() {
		t.Error("IsSuspended should be true after SetSuspended(true)")
	}
	p.SetSuspended(false)
	if p.IsSuspended() {
		t.Error("IsSuspended should be false after SetSuspended(false)")
	}
}

// TestUpdateActivity_Suspended verifies that updateActivity returns
// StateSuspended when alive=false and suspended=true.
func TestUpdateActivity_Suspended(t *testing.T) {
	p := &Pane{
		emu:       vt.NewSafeEmulator(80, 24),
		alive:     false,
		suspended: true,
	}
	p.updateActivity()
	if p.activity != StateSuspended {
		t.Errorf("activity = %v, want StateSuspended", p.activity)
	}
}

// TestUpdateActivity_DeadNotSuspended verifies that a dead non-suspended pane
// gets StateDead (not StateSuspended).
func TestUpdateActivity_DeadNotSuspended(t *testing.T) {
	p := &Pane{
		emu:       vt.NewSafeEmulator(80, 24),
		alive:     false,
		suspended: false,
	}
	p.updateActivity()
	if p.activity != StateDead {
		t.Errorf("activity = %v, want StateDead", p.activity)
	}
}

// TestOverlayDot_SuspendedIsBluHollow verifies the overlay uses a blue hollow
// dot for suspended agents.
func TestOverlayDot_SuspendedIsBlueHollow(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.panes[0].(*Pane).alive = false
	tui.panes[0].(*Pane).suspended = true
	tui.layoutState.Overlay = true
	tui.render()

	// The overlay dot is at column px+2, row py+1. Find it by scanning
	// the right edge of the screen for the hollow dot character.
	sw, _ := s.Size()
	found := false
	for x := sw - 30; x < sw; x++ {
		c, style, _ := s.Get(x, 2) // row 2 = first agent row (py=1, +1)
		if c == "\u25cb" {                   // hollow dot
			fg, _, _ := style.Decompose()
			if fg == tcell.ColorDodgerBlue {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("overlay should show blue hollow dot for suspended agent")
	}
}

// TestRibbon_SuspendedShowsSusp verifies the ribbon badge shows [susp] for
// suspended panes.
func TestRibbon_SuspendedShowsSusp(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(40, 11)

	p := &Pane{
		name:      "eng1",
		emu:       emu,
		alive:     false,
		suspended: true,
		visible:   true,
		region:    Region{X: 0, Y: 0, W: 40, H: 11},
	}
	p.Render(s, false, false, 1, Selection{})

	// Ribbon is at row r.Y + r.H - 1 = 10. Scan for "[susp]".
	var buf strings.Builder
	for x := 0; x < 40; x++ {
		c, _, _ := s.Get(x, 10)
		buf.WriteString(c)
	}
	ribbon := buf.String()
	if !strings.Contains(ribbon, "[susp]") {
		t.Errorf("ribbon = %q, want to contain '[susp]'", ribbon)
	}
}

// TestRibbon_DeadNotSuspendedShowsDead verifies a dead non-suspended pane
// still shows [dead], not [susp].
func TestRibbon_DeadNotSuspendedShowsDead(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(40, 11)

	p := &Pane{
		name:      "eng1",
		emu:       emu,
		alive:     false,
		suspended: false,
		visible:   true,
		region:    Region{X: 0, Y: 0, W: 40, H: 11},
	}
	p.Render(s, false, false, 1, Selection{})

	var buf strings.Builder
	for x := 0; x < 40; x++ {
		c, _, _ := s.Get(x, 10)
		buf.WriteString(c)
	}
	ribbon := buf.String()
	if !strings.Contains(ribbon, "[dead]") {
		t.Errorf("ribbon = %q, want to contain '[dead]'", ribbon)
	}
	if strings.Contains(ribbon, "[susp]") {
		t.Errorf("ribbon = %q, should NOT contain '[susp]' for non-suspended dead pane", ribbon)
	}
}
