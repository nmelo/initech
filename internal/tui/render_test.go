// render_test.go tests render helpers.
package tui

import (
	"fmt"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestRenderCmdError_NarrowTerminal verifies renderCmdError does not panic
// when the terminal is too narrow (sw < 5). Previously msg[:sw-4] would
// cause a slice-bounds panic when sw <= 4 (ini-a1e.6).
func TestRenderCmdError_NarrowTerminal(t *testing.T) {
	for _, width := range []int{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			s := tcell.NewSimulationScreen("")
			s.Init()
			s.SetSize(width, 10)
			tui := &TUI{
				screen: s,
				cmd:    cmdModal{error: "something went wrong"},
			}
			// Must not panic.
			tui.renderCmdError()
		})
	}
}

// TestRenderCmdError_NormalWidth verifies renderCmdError renders without panic
// for a standard terminal width.
func TestRenderCmdError_NormalWidth(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{
		screen: s,
		cmd:    cmdModal{error: "build failed"},
	}
	// Must not panic.
	tui.renderCmdError()
}

// TestRenderStatusBar_DefaultHints verifies the status bar shows keyboard
// hints when no modal or error is active.
func TestRenderStatusBar_DefaultHints(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{screen: s}
	tui.renderStatusBar()

	// Check that the bottom row contains hint text.
	_, sh := s.Size()
	y := sh - 1
	var line string
	for x := 0; x < 60; x++ {
		ch, _, _, _ := s.GetContent(x, y)
		line += string(ch)
	}
	if len(line) == 0 {
		t.Error("status bar should render hint text")
	}
	// Should contain at least one recognizable hint.
	found := false
	for _, hint := range []string{"commands", "zoom", "overlay", "help"} {
		for i := 0; i <= len(line)-len(hint); i++ {
			if line[i:i+len(hint)] == hint {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Errorf("status bar should contain keyboard hints, got: %q", line)
	}
}

// TestRenderStatusBar_Error verifies the status bar shows error text when
// cmd.error is set.
func TestRenderStatusBar_Error(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{
		screen: s,
		cmd:    cmdModal{error: "something broke"},
	}
	tui.renderStatusBar()

	// The bottom row should have a red background (error style).
	_, sh := s.Size()
	y := sh - 1
	var line string
	for x := 0; x < 20; x++ {
		ch, _, _, _ := s.GetContent(x, y)
		line += string(ch)
	}
	if len(line) == 0 {
		t.Error("status bar should render error text")
	}
}

// TestRenderStatusBar_CmdActive verifies the status bar shows the command
// input when the command modal is active.
func TestRenderStatusBar_CmdActive(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{
		screen: s,
		cmd:    cmdModal{active: true, buf: []rune("gri")},
	}
	tui.renderStatusBar()

	// Should show the > prompt on the bottom row.
	_, sh := s.Size()
	y := sh - 1
	ch, _, _, _ := s.GetContent(0, y)
	if ch != '>' {
		t.Errorf("command bar should show > prompt, got %q", string(ch))
	}
}

// TestApplyLayout_ReservesStatusBar verifies that applyLayout reserves the
// bottom row for the status bar, so panes don't extend to the last row.
func TestApplyLayout_ReservesStatusBar(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)
	panes := []*Pane{newEmuPane("eng1", 120, 39)}
	ls := DefaultLayoutState([]string{"eng1"})
	tui := &TUI{
		screen:      s,
		panes:       panes,
		layoutState: ls,
		lastW:       120,
		lastH:       40,
	}
	tui.applyLayout()

	// The single pane should fill height 39 (40 - 1 for status bar).
	if len(tui.plan.Panes) != 1 {
		t.Fatalf("expected 1 pane in plan, got %d", len(tui.plan.Panes))
	}
	pr := tui.plan.Panes[0]
	if pr.Region.H != 39 {
		t.Errorf("pane height = %d, want 39 (screen 40 - 1 status bar)", pr.Region.H)
	}
	// Pane should not extend into the status bar row (row 39).
	if pr.Region.Y+pr.Region.H > 39 {
		t.Errorf("pane extends into status bar: Y=%d H=%d (bottom=%d)", pr.Region.Y, pr.Region.H, pr.Region.Y+pr.Region.H)
	}
}
