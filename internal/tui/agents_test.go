package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestAgentsModal_OpenViaCommand(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.execCmd("agents")
	if !tui.agents.active {
		t.Error("agents modal should be active after 'agents' command")
	}
	if tui.agents.selected != 0 {
		t.Errorf("selected = %d, want 0", tui.agents.selected)
	}
}

func TestAgentsModal_OpenViaAltA(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModAlt))
	if !tui.agents.active {
		t.Error("agents modal should be active after Alt+a")
	}
}

func TestAgentsModal_CloseEsc(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1")
	tui.openAgentsModal()
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if tui.agents.active {
		t.Error("agents modal should close on Esc")
	}
}

func TestAgentsModal_CloseQ(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1")
	tui.openAgentsModal()
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'q', 0))
	if tui.agents.active {
		t.Error("agents modal should close on q")
	}
}

func TestAgentsModal_CloseBacktick(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1")
	tui.openAgentsModal()
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, '`', 0))
	if tui.agents.active {
		t.Error("agents modal should close on backtick")
	}
}

func TestAgentsModal_NavigateDownUp(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.agents.selected != 1 {
		t.Errorf("after Down: selected = %d, want 1", tui.agents.selected)
	}

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.agents.selected != 2 {
		t.Errorf("after Down x2: selected = %d, want 2", tui.agents.selected)
	}

	// Should not go past the last row.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.agents.selected != 2 {
		t.Errorf("Down past end: selected = %d, want 2", tui.agents.selected)
	}

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if tui.agents.selected != 1 {
		t.Errorf("after Up: selected = %d, want 1", tui.agents.selected)
	}

	// Should not go above first row.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if tui.agents.selected != 0 {
		t.Errorf("Up past start: selected = %d, want 0", tui.agents.selected)
	}
}

func TestAgentsModal_NavigateJK(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'j', 0))
	if tui.agents.selected != 1 {
		t.Errorf("after j: selected = %d, want 1", tui.agents.selected)
	}

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'k', 0))
	if tui.agents.selected != 0 {
		t.Errorf("after k: selected = %d, want 0", tui.agents.selected)
	}
}

func TestAgentsModal_RenderShowsAllAgents(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "qa1", "super")

	// Set some state to verify rendering.
	tui.panes[0].(*Pane).activity = StateRunning
	tui.panes[0].(*Pane).lastOutputTime = time.Now()
	tui.panes[0].(*Pane).beadID = "ini-abc"
	tui.panes[1].(*Pane).activity = StateIdle
	tui.panes[2].(*Pane).activity = StateRunning
	tui.panes[2].(*Pane).lastOutputTime = time.Now()

	tui.openAgentsModal()
	tui.renderAgents()

	sw, _ := s.Size()
	// Read all rendered rows.
	rows := make([]string, 10)
	for row := 0; row < 10; row++ {
		var b strings.Builder
		for x := 0; x < sw; x++ {
			c, _, _ := s.Get(x, row)
			b.WriteString(c)
		}
		rows[row] = b.String()
	}

	// Title.
	if !strings.Contains(rows[0], "initech agents") {
		t.Errorf("title row = %q, want 'initech agents'", rows[0])
	}

	// Agent names should appear in rendered rows.
	allText := strings.Join(rows, "\n")
	for _, name := range []string{"eng1", "qa1", "super"} {
		if !strings.Contains(allText, name) {
			t.Errorf("rendered output missing agent %q", name)
		}
	}

	// Bead ID should appear.
	if !strings.Contains(allText, "ini-abc") {
		t.Error("rendered output missing bead ID 'ini-abc'")
	}
}

func TestAgentsModal_RenderShowsVisibilityCheckbox(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Hidden["eng2"] = true
	tui.openAgentsModal()
	tui.renderAgents()

	sw, _ := s.Size()
	// Read rows 4 and 5 (agent rows start at row 4: title=0, header=2, sep=3).
	readRow := func(row int) string {
		var b strings.Builder
		for x := 0; x < sw; x++ {
			c, _, _ := s.Get(x, row)
			b.WriteString(c)
		}
		return b.String()
	}

	row1 := readRow(4) // eng1 - visible
	row2 := readRow(5) // eng2 - hidden

	if !strings.Contains(row1, "[x]") {
		t.Errorf("visible agent row = %q, want '[x]'", row1)
	}
	if !strings.Contains(row2, "[ ]") {
		t.Errorf("hidden agent row = %q, want '[ ]'", row2)
	}
}

func TestAgentsModal_RenderShowsPinBadge(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Pinned = map[string]bool{"eng1": true}
	tui.openAgentsModal()
	tui.renderAgents()

	sw, _ := s.Size()
	readRow := func(row int) string {
		var b strings.Builder
		for x := 0; x < sw; x++ {
			c, _, _ := s.Get(x, row)
			b.WriteString(c)
		}
		return b.String()
	}

	row1 := readRow(4) // eng1 - pinned
	row2 := readRow(5) // eng2 - not pinned

	if !strings.Contains(row1, "[P]") {
		t.Errorf("pinned agent row = %q, want '[P]'", row1)
	}
	if strings.Contains(row2, "[P]") {
		t.Errorf("unpinned agent row = %q, should not contain '[P]'", row2)
	}
}

func TestAgentsModal_CloseDoesNotCorruptLayout(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	origMode := tui.layoutState.Mode
	origFocused := tui.layoutState.Focused

	tui.openAgentsModal()
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))

	if tui.layoutState.Mode != origMode {
		t.Errorf("layout mode changed: got %v, want %v", tui.layoutState.Mode, origMode)
	}
	if tui.layoutState.Focused != origFocused {
		t.Errorf("focused pane changed: got %q, want %q", tui.layoutState.Focused, origFocused)
	}
}

func TestAgentsModal_EmptyPaneList(t *testing.T) {
	tui := &TUI{panes: nil}
	tui.agents.active = true
	// Should close without panic.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.agents.active {
		t.Error("agents modal should auto-close with no panes")
	}
}

func TestAgentsModal_HandleKeyReturnsFalse(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1")
	tui.openAgentsModal()

	keys := []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyUp, 0, 0),
		tcell.NewEventKey(tcell.KeyDown, 0, 0),
		tcell.NewEventKey(tcell.KeyRune, 'j', 0),
		tcell.NewEventKey(tcell.KeyRune, 'k', 0),
		tcell.NewEventKey(tcell.KeyEscape, 0, 0),
	}
	for _, ev := range keys {
		tui.agents.active = true
		if tui.handleAgentsKey(ev) {
			t.Errorf("handleAgentsKey should return false for key %v", ev.Key())
		}
	}
}

func TestAgentsModal_InterceptsKeysWhenActive(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()

	// Pressing Down via handleKey (not handleAgentsKey) should be intercepted.
	tui.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.agents.selected != 1 {
		t.Errorf("handleKey should route Down to agents modal: selected = %d, want 1", tui.agents.selected)
	}
}
