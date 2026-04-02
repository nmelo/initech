package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
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

	tui.panes[0].(*Pane).activity = StateRunning
	tui.panes[0].(*Pane).lastOutputTime = time.Now()
	tui.panes[0].(*Pane).beadID = "ini-abc"
	tui.panes[1].(*Pane).activity = StateIdle
	tui.panes[2].(*Pane).activity = StateRunning
	tui.panes[2].(*Pane).lastOutputTime = time.Now()

	tui.openAgentsModal()
	tui.renderAgents()

	sw, _ := s.Size()
	rows := make([]string, 10)
	for row := 0; row < 10; row++ {
		var b strings.Builder
		for x := 0; x < sw; x++ {
			c, _, _ := s.Get(x, row)
			b.WriteString(c)
		}
		rows[row] = b.String()
	}

	if !strings.Contains(rows[0], "initech agents") {
		t.Errorf("title row = %q, want 'initech agents'", rows[0])
	}

	allText := strings.Join(rows, "\n")
	for _, name := range []string{"eng1", "qa1", "super"} {
		if !strings.Contains(allText, name) {
			t.Errorf("rendered output missing agent %q", name)
		}
	}

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
	readRow := func(row int) string {
		var b strings.Builder
		for x := 0; x < sw; x++ {
			c, _, _ := s.Get(x, row)
			b.WriteString(c)
		}
		return b.String()
	}

	row1 := readRow(4)
	row2 := readRow(5)

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

	row1 := readRow(4)
	row2 := readRow(5)

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
		tcell.NewEventKey(tcell.KeyRune, ' ', 0),
		tcell.NewEventKey(tcell.KeyRune, 'p', 0),
		tcell.NewEventKey(tcell.KeyEnter, 0, 0),
		tcell.NewEventKey(tcell.KeyRune, 'A', 0),
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

	tui.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.agents.selected != 1 {
		t.Errorf("handleKey should route Down to agents modal: selected = %d, want 1", tui.agents.selected)
	}
}

// ── Action tests ────────────────────────────────────────────────────

func TestAgentsModal_SpaceToggleVisibility(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()

	// Hide eng1.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	if !tui.layoutState.Hidden["eng1"] {
		t.Error("Space should hide the selected agent")
	}

	// Unhide eng1.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	if tui.layoutState.Hidden["eng1"] {
		t.Error("Space again should unhide the agent")
	}
}

func TestAgentsModal_SpaceLastVisibleGuard(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1")
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	if tui.layoutState.Hidden["eng1"] {
		t.Error("should not allow hiding the last visible pane")
	}
	if tui.agents.error == "" {
		t.Error("should set error message when trying to hide last visible pane")
	}
}

func TestAgentsModal_SpaceLastVisibleGuardMultiple(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	// Hide eng2 first so eng1 is the only visible.
	tui.layoutState.Hidden = map[string]bool{"eng2": true}
	tui.openAgentsModal()

	// Try to hide eng1 (last visible).
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	if tui.layoutState.Hidden["eng1"] {
		t.Error("should not hide eng1 when it's the last visible pane")
	}
	if tui.agents.error == "" {
		t.Error("expected error for last-visible guard")
	}
}

func TestAgentsModal_EnterGrabDrop(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()

	// Grab.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !tui.agents.moving {
		t.Error("Enter should start moving mode")
	}

	// Drop.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if tui.agents.moving {
		t.Error("Enter again should stop moving mode")
	}
}

func TestAgentsModal_ReorderViaGrab(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.openAgentsModal()

	// Grab eng1 (index 0).
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	// Move down.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.panes[0].Name() != "eng2" || tui.panes[1].Name() != "eng1" {
		t.Errorf("after move down: order = [%s, %s, %s], want [eng2, eng1, eng3]",
			tui.panes[0].Name(), tui.panes[1].Name(), tui.panes[2].Name())
	}
	if tui.agents.selected != 1 {
		t.Errorf("selected = %d, want 1 (follows grabbed item)", tui.agents.selected)
	}

	// Drop.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if tui.agents.moving {
		t.Error("should have dropped after Enter")
	}

	// Order persisted.
	if len(tui.layoutState.Order) != 3 {
		t.Fatalf("Order length = %d, want 3", len(tui.layoutState.Order))
	}
	if tui.layoutState.Order[0] != "eng2" || tui.layoutState.Order[1] != "eng1" {
		t.Errorf("persisted order = %v, want [eng2, eng1, eng3]", tui.layoutState.Order)
	}
}

func TestAgentsModal_ReorderViaJK(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()

	// Grab eng1.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	// Move down with j.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'j', 0))
	if tui.panes[0].Name() != "eng2" || tui.panes[1].Name() != "eng1" {
		t.Error("j should swap grabbed item down")
	}

	// Move back up with k.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'k', 0))
	if tui.panes[0].Name() != "eng1" || tui.panes[1].Name() != "eng2" {
		t.Error("k should swap grabbed item up")
	}
}

func TestAgentsModal_ReorderBoundsCheck(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()

	// Grab eng1 at top, try to move up (should be no-op).
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if tui.panes[0].Name() != "eng1" {
		t.Error("moving up at top should be a no-op")
	}

	// Move to bottom, try to move down past end.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.agents.selected != 1 {
		t.Errorf("selected = %d, want 1 (clamped)", tui.agents.selected)
	}
}

func TestAgentsModal_HiddenAgentReorderable(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.layoutState.Hidden = map[string]bool{"eng2": true}
	tui.openAgentsModal()

	// Select eng2 (index 1) and grab it.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	// Move down.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.panes[2].Name() != "eng2" {
		t.Errorf("hidden agent should be reorderable: panes[2] = %s, want eng2", tui.panes[2].Name())
	}
}

func TestAgentsModal_PinToggle(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()

	// Pin eng1.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'p', 0))
	if !tui.layoutState.Pinned["eng1"] {
		t.Error("p should pin the selected agent")
	}

	// Unpin eng1.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'p', 0))
	if tui.layoutState.Pinned["eng1"] {
		t.Error("p again should unpin the agent")
	}
}

func TestAgentsModal_RevealAll(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.layoutState.Hidden = map[string]bool{"eng1": true, "eng3": true}
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'A', 0))
	for _, name := range []string{"eng1", "eng2", "eng3"} {
		if tui.layoutState.Hidden[name] {
			t.Errorf("after A: %s should not be hidden", name)
		}
	}
}

func TestAgentsModal_ResetOrder(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.project = &config.Project{Roles: []string{"eng3", "eng1", "eng2"}}
	// Current order: eng1, eng2, eng3.
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'R', 0))

	// Order should match config.
	if tui.panes[0].Name() != "eng3" || tui.panes[1].Name() != "eng1" || tui.panes[2].Name() != "eng2" {
		t.Errorf("after R: order = [%s, %s, %s], want [eng3, eng1, eng2]",
			tui.panes[0].Name(), tui.panes[1].Name(), tui.panes[2].Name())
	}
	if tui.agents.selected != 0 {
		t.Errorf("selected = %d, want 0 (reset to top)", tui.agents.selected)
	}
	if tui.agents.moving {
		t.Error("moving should be false after reset")
	}
}

func TestAgentsModal_ResetOrderNoConfig(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.project = nil
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'R', 0))
	if tui.agents.error == "" {
		t.Error("R with no config should set error")
	}
}

func TestAgentsModal_ErrorClearsOnNextKey(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1")
	tui.openAgentsModal()

	// Trigger error.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	if tui.agents.error == "" {
		t.Fatal("expected error from last-visible guard")
	}

	// Any keypress should clear it.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.agents.error != "" {
		t.Error("error should be cleared on next keypress")
	}
}

func TestAgentsModal_EscCancelsMoving(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !tui.agents.moving {
		t.Fatal("should be in moving mode")
	}

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if tui.agents.moving {
		t.Error("Esc should cancel moving mode")
	}
	if tui.agents.active {
		t.Error("Esc should also close the modal")
	}
}

func TestAgentsModal_RenderMovingTitle(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()
	tui.agents.moving = true

	tui.renderAgents()

	sw, _ := s.Size()
	var title strings.Builder
	for x := 0; x < sw; x++ {
		c, _, _ := s.Get(x, 0)
		title.WriteString(c)
	}
	if !strings.Contains(title.String(), "moving eng1") {
		t.Errorf("title = %q, want 'moving eng1' when grabbed", title.String())
	}
}

func TestAgentsModal_RenderErrorLine(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.openAgentsModal()
	tui.agents.error = "test error message"

	tui.renderAgents()

	_, sh := s.Size()
	sw, _ := s.Size()
	errY := sh - 2
	var errRow strings.Builder
	for x := 0; x < sw; x++ {
		c, _, _ := s.Get(x, errY)
		errRow.WriteString(c)
	}
	if !strings.Contains(errRow.String(), "test error message") {
		t.Errorf("error row = %q, want 'test error message'", errRow.String())
	}
}
