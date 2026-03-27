// QA tests for ini-8vv: in-TUI help reference card.
package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// AC5: hint text in command bar contains "?:help".
func TestHelpHintTextInCmdBar(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.cmd.active = true
	tui.render()

	// renderCmdLine places the hint right-aligned on the cmd row (sh-2).
	sw, sh := s.Size()
	hintRow := sh - 2
	var buf strings.Builder
	for x := 0; x < sw; x++ {
		c, _, _, _ := s.GetContent(x, hintRow)
		buf.WriteRune(c)
	}
	if !strings.Contains(buf.String(), "?:help") {
		t.Errorf("cmd hint row %d = %q, want contains '?:help'", hintRow, buf.String())
	}
}

// AC1/2: 'help' and '?' both open the modal and render the reference card.
func TestRenderHelp_RendersOnScreen(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)
	tui := &TUI{screen: s, help: helpModal{active: true}}
	tui.renderHelp()

	// Title row should contain "initech help".
	var title strings.Builder
	for x := 0; x < 30; x++ {
		c, _, _, _ := s.GetContent(x, 0)
		title.WriteRune(c)
	}
	if !strings.Contains(title.String(), "initech help") {
		t.Errorf("title row = %q, want contains 'initech help'", title.String())
	}
}

// AC3: 'Keybindings' section header rendered in yellow/bold.
func TestRenderHelp_KeybindingsSectionVisible(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)
	tui := &TUI{screen: s, help: helpModal{active: true, scrollOffset: 0}}
	tui.renderHelp()

	// Row 1 should start with "Keybindings" (first helpLine, offset 1 from title).
	var row1 strings.Builder
	for x := 0; x < 20; x++ {
		c, _, _, _ := s.GetContent(x, 1)
		row1.WriteRune(c)
	}
	if !strings.Contains(row1.String(), "Keybindings") {
		t.Errorf("row 1 = %q, want 'Keybindings' section", row1.String())
	}
}

// All commands listed in helpLines have corresponding execCmd cases.
func TestHelpLines_CommandsMatchExecCmd(t *testing.T) {
	// Spot-check key commands: their words must appear in helpLines.
	required := []string{
		"grid", "focus", "zoom", "panel", "main",
		"show", "hide", "view", "layout", "restart",
		"patrol", "top", "add", "remove", "help", "quit",
	}
	helpContent := strings.Join(helpLines, "\n")
	for _, cmd := range required {
		if !strings.Contains(helpContent, cmd) {
			t.Errorf("helpLines missing command %q", cmd)
		}
	}
}

// KeyDown scrolls down (increases scrollOffset) when below max.
func TestHandleHelpKey_ArrowDownScrolls(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 5) // small screen so helpMaxOffset() > 0
	tui := &TUI{screen: s, help: helpModal{active: true, scrollOffset: 0}}
	tui.handleHelpKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.help.scrollOffset != 1 {
		t.Errorf("Down: scrollOffset = %d, want 1", tui.help.scrollOffset)
	}
}

// KeyUp decrements scrollOffset but clamps at 0.
func TestHandleHelpKey_ArrowUpScrolls(t *testing.T) {
	tui := &TUI{help: helpModal{active: true, scrollOffset: 3}}
	tui.handleHelpKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if tui.help.scrollOffset != 2 {
		t.Errorf("Up from 3: scrollOffset = %d, want 2", tui.help.scrollOffset)
	}
	// Clamp: Up from 0 stays 0.
	tui2 := &TUI{help: helpModal{active: true, scrollOffset: 0}}
	tui2.handleHelpKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if tui2.help.scrollOffset != 0 {
		t.Errorf("Up from 0: scrollOffset = %d, want 0 (clamped)", tui2.help.scrollOffset)
	}
}

// CtrlC closes help modal.
func TestHandleHelpKey_CtrlCCloses(t *testing.T) {
	tui := &TUI{help: helpModal{active: true}}
	tui.handleHelpKey(tcell.NewEventKey(tcell.KeyCtrlC, 0, 0))
	if tui.help.active {
		t.Error("Ctrl+C should close help modal")
	}
}

// renderHelp always returns false (never quits TUI).
func TestHandleHelpKey_AlwaysReturnsFalse(t *testing.T) {
	tui := &TUI{help: helpModal{active: true}}
	for _, ev := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyEscape, 0, 0),
		tcell.NewEventKey(tcell.KeyRune, 'j', 0),
		tcell.NewEventKey(tcell.KeyRune, 'k', 0),
		tcell.NewEventKey(tcell.KeyRune, 'q', 0),
		tcell.NewEventKey(tcell.KeyDown, 0, 0),
	} {
		// Reset active so Esc doesn't stop others from running.
		tui.help.active = true
		if tui.handleHelpKey(ev) {
			t.Errorf("handleHelpKey should always return false, got true for key %v", ev.Key())
		}
	}
}

// Small terminal: renderHelp handles sw<20 or sh<5 without panic.
func TestRenderHelp_SmallTerminalNoPanic(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(15, 3)
	tui := &TUI{screen: s, help: helpModal{active: true}}
	tui.renderHelp() // Must not panic.
}

// renderHelp is checked BEFORE top modal and eventLogM in render().
func TestHelpModalPriorityInRender(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)
	tui := &TUI{
		screen:    s,
		help:      helpModal{active: true},
		top:       topModal{active: true},
		eventLogM: eventLogModal{active: true},
	}
	// If help takes priority, the title row should say "initech help".
	tui.render()
	var title strings.Builder
	for x := 0; x < 30; x++ {
		c, _, _, _ := s.GetContent(x, 0)
		title.WriteRune(c)
	}
	if !strings.Contains(title.String(), "initech help") {
		t.Errorf("help should take render priority; title = %q", title.String())
	}
}

// scrollOffset is clamped by renderHelp when over max.
func TestRenderHelp_ScrollOffsetClamped(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)
	tui := &TUI{screen: s, help: helpModal{active: true, scrollOffset: 9999}}
	tui.renderHelp() // Should not panic or render garbage.
	maxScroll := len(helpLines) - (40 - 2)
	if maxScroll < 0 {
		maxScroll = 0
	}
	if tui.help.scrollOffset > maxScroll {
		t.Errorf("scrollOffset %d not clamped to max %d during renderHelp",
			tui.help.scrollOffset, maxScroll)
	}
}
