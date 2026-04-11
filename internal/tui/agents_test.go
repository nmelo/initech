package tui

import (
	"fmt"
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
	if tui.agents.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d, want 0", tui.agents.scrollOffset)
	}
}

func TestAgentsModal_OpenViaAltA(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModAlt))
	if !tui.agents.active {
		t.Error("agents modal should be active after Alt+a")
	}
}

func TestAgentsModal_AltAToggles(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModAlt))
	if !tui.agents.active {
		t.Fatal("Alt+a should open agents modal")
	}
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModAlt))
	if tui.agents.active {
		t.Error("Alt+a again should close agents modal")
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
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.agents.selected != 2 {
		t.Errorf("Down past end: selected = %d, want 2", tui.agents.selected)
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if tui.agents.selected != 1 {
		t.Errorf("after Up: selected = %d, want 1", tui.agents.selected)
	}
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

// readScreenRect reads a rectangle of text from the simulation screen.
func readScreenRect(s tcell.SimulationScreen, x, y, w, h int) string {
	var b strings.Builder
	for row := y; row < y+h; row++ {
		for col := x; col < x+w; col++ {
			c, _, _ := s.Get(col, row)
			b.WriteString(c)
		}
		if row < y+h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func TestAgentsModal_RenderShowsAllAgents(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "qa1", "super")
	tui.panes[0].(*Pane).activity = StateRunning
	tui.panes[0].(*Pane).lastOutputTime = time.Now()
	tui.panes[0].(*Pane).beadIDs = []string{"ini-abc"}
	tui.panes[1].(*Pane).activity = StateIdle
	tui.panes[2].(*Pane).activity = StateRunning
	tui.panes[2].(*Pane).lastOutputTime = time.Now()

	tui.openAgentsModal()
	tui.render()

	sw, sh := s.Size()
	allText := readScreenRect(s, 0, 0, sw, sh)

	if !strings.Contains(allText, "initech agents") {
		t.Error("rendered output missing 'initech agents' title")
	}
	for _, name := range []string{"eng1", "qa1", "super"} {
		if !strings.Contains(allText, name) {
			t.Errorf("rendered output missing agent %q", name)
		}
	}
	// Bead IDs no longer shown in the agents modal (ini-2j2).
	// They appear in the pane ribbon instead.
}

func TestAgentsModal_RenderShowsVisibilityCheckbox(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Hidden["eng2"] = true
	tui.openAgentsModal()
	tui.render()

	sw, sh := s.Size()
	allText := readScreenRect(s, 0, 0, sw, sh)

	if !strings.Contains(allText, "[x]") {
		t.Error("rendered output missing [x] for visible agent")
	}
	if !strings.Contains(allText, "[ ]") {
		t.Error("rendered output missing [ ] for hidden agent")
	}
}

func TestAgentsModal_RenderHiddenAgentNameItalic(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Hidden["eng2"] = true
	tui.openAgentsModal()
	tui.render()

	sw, sh := s.Size()
	boxW := agentsBoxW
	if sw-4 < boxW {
		boxW = sw - 4
	}
	boxH := agentsBoxH
	if sh-4 < boxH {
		boxH = sh - 4
	}
	startX := (sw - boxW) / 2
	startY := (sh - boxH) / 2
	innerX := startX + 1
	row := startY + 4 // first data row is startY+3, second is startY+4

	visiblePrefix := fmt.Sprintf("%s%2d  %s ", " ", 1, "[x]")
	_, _, visibleStyle, _ := s.GetContent(innerX+len([]rune(visiblePrefix)), startY+3)
	_, _, visibleAttrs := visibleStyle.Decompose()
	if visibleAttrs&tcell.AttrItalic != 0 {
		t.Fatal("visible agents modal name should not be italic")
	}

	hiddenPrefix := fmt.Sprintf("%s%2d  %s ", " ", 2, "[ ]")
	_, _, hiddenStyle, _ := s.GetContent(innerX+len([]rune(hiddenPrefix)), row)
	_, _, hiddenAttrs := hiddenStyle.Decompose()
	if hiddenAttrs&tcell.AttrItalic == 0 {
		t.Fatal("hidden agents modal name should be italic")
	}
}

func TestAgentsModal_RenderShowsPinBadge(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Protected = map[string]bool{"eng1": true}
	tui.openAgentsModal()
	tui.render()

	sw, sh := s.Size()
	allText := readScreenRect(s, 0, 0, sw, sh)

	if !strings.Contains(allText, "[*]") {
		t.Error("rendered output missing [*] for protected agent")
	}
}

func TestAgentsModal_RenderShowsLivePinBadge(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.LivePinned = map[string]int{"eng2": 0}
	tui.openAgentsModal()
	tui.render()

	sw, sh := s.Size()
	allText := readScreenRect(s, 0, 0, sw, sh)

	// LivePinned agent should show pin indicator (either [P] or P:N).
	if !strings.Contains(allText, "[P]") && !strings.Contains(allText, "P:") {
		t.Error("rendered output missing pin badge for live-pinned agent eng2")
	}
}

func TestAgentsModal_RenderShowsLivePinBadgeInLiveMode(t *testing.T) {
	tui, s := newTestTUIWithScreen("super", "eng1", "pm")
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.Protected = map[string]bool{"super": true}
	tui.layoutState.LivePinned = map[string]int{"pm": 1}
	tui.layoutState.LiveSlots = []string{"super", "pm"}
	tui.openAgentsModal()
	tui.render()

	sw, sh := s.Size()
	allText := readScreenRect(s, 0, 0, sw, sh)

	// In live mode: super (general pinned) should show P:0, pm (live-pinned) should show P:1.
	if !strings.Contains(allText, "P:") {
		t.Errorf("rendered output missing P:N for pinned agents in live mode. Got:\n%s", allText)
	}
}

func TestAgentsModal_RenderIsFloating(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()
	tui.render()

	sw, sh := s.Size()
	// The box should be centered, so corners should not be at (0,0).
	boxW := agentsBoxW
	if sw-4 < boxW {
		boxW = sw - 4
	}
	boxH := agentsBoxH
	if sh-4 < boxH {
		boxH = sh - 4
	}
	startX := (sw - boxW) / 2
	startY := (sh - boxH) / 2

	// Top-left corner should be a box drawing character.
	c, _, _ := s.Get(startX, startY)
	if c != "\u250c" {
		t.Errorf("top-left corner = %q, want box drawing char", c)
	}

	// Cell at (0,0) should NOT be the box corner (floating, not full-screen).
	if startX > 0 {
		c0, _, _ := s.Get(0, 0)
		if c0 == "\u250c" {
			t.Error("box corner at (0,0) means full-screen, not floating")
		}
	}
}

func TestAgentsModal_RenderHelpLine(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.openAgentsModal()
	tui.render()

	sw, sh := s.Size()
	allText := readScreenRect(s, 0, 0, sw, sh)
	if !strings.Contains(allText, "Esc close") {
		t.Error("help line should contain 'Esc close'")
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

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	if !tui.layoutState.Hidden["eng1"] {
		t.Error("Space should hide the selected agent")
	}
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
	tui.layoutState.Hidden = map[string]bool{"eng2": true}
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	if tui.layoutState.Hidden["eng1"] {
		t.Error("should not hide eng1 when it's the last visible pane")
	}
}

func TestAgentsModal_EnterGrabDrop(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !tui.agents.moving {
		t.Error("Enter should start moving mode")
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if tui.agents.moving {
		t.Error("Enter again should stop moving mode")
	}
}

func TestAgentsModal_ReorderViaGrab(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.panes[0].Name() != "eng2" || tui.panes[1].Name() != "eng1" {
		t.Errorf("after move down: order = [%s, %s, %s], want [eng2, eng1, eng3]",
			tui.panes[0].Name(), tui.panes[1].Name(), tui.panes[2].Name())
	}
	if tui.agents.selected != 1 {
		t.Errorf("selected = %d, want 1", tui.agents.selected)
	}

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if tui.agents.moving {
		t.Error("should have dropped after Enter")
	}
	if len(tui.layoutState.Order) != 3 || tui.layoutState.Order[0] != "eng2" {
		t.Errorf("persisted order = %v, want [eng2, eng1, eng3]", tui.layoutState.Order)
	}
}

func TestAgentsModal_ProtectToggle(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()

	// Shift+P toggles auto-suspend protection.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'P', 0))
	if !tui.layoutState.Protected["eng1"] {
		t.Error("P should protect the selected agent")
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'P', 0))
	if tui.layoutState.Protected["eng1"] {
		t.Error("P again should unprotect the agent")
	}
}

func TestAgentsModal_LivePinToggle(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.LivePinned = make(map[string]int)
	tui.layoutState.LiveSlots = []string{"eng1", "eng2"}
	tui.openAgentsModal()

	// p toggles live pin.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'p', 0))
	if _, pinned := tui.layoutState.LivePinned["eng1"]; !pinned {
		t.Error("p should live-pin the selected agent")
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'p', 0))
	if _, pinned := tui.layoutState.LivePinned["eng1"]; pinned {
		t.Error("p again should unpin the agent")
	}
}

func TestAgentsModal_LivePinRequiresLiveMode(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Mode = LayoutGrid // not live mode
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'p', 0))
	if tui.agents.error == "" {
		t.Error("p in non-live mode should show error")
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
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'R', 0))

	if tui.panes[0].Name() != "eng3" || tui.panes[1].Name() != "eng1" || tui.panes[2].Name() != "eng2" {
		t.Errorf("after R: order = [%s, %s, %s], want [eng3, eng1, eng2]",
			tui.panes[0].Name(), tui.panes[1].Name(), tui.panes[2].Name())
	}
	if tui.agents.selected != 0 || tui.agents.scrollOffset != 0 {
		t.Errorf("selected=%d scroll=%d, want 0,0 after reset",
			tui.agents.selected, tui.agents.scrollOffset)
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

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	if tui.agents.error == "" {
		t.Fatal("expected error from last-visible guard")
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.agents.error != "" {
		t.Error("error should be cleared on next keypress")
	}
}

func TestAgentsModal_EscCancelsMoving(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
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
	tui.render()

	sw, sh := s.Size()
	allText := readScreenRect(s, 0, 0, sw, sh)
	if !strings.Contains(allText, "moving eng1") {
		t.Errorf("title should contain 'moving eng1' when grabbed, got:\n%s", allText)
	}
}

func TestAgentsModal_RenderErrorLine(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.openAgentsModal()
	tui.agents.error = "test error message"
	tui.render()

	sw, sh := s.Size()
	allText := readScreenRect(s, 0, 0, sw, sh)
	if !strings.Contains(allText, "test error message") {
		t.Error("rendered output should contain error message")
	}
}

// ── Scroll tests ────────────────────────────────────────────────────

func TestAgentsModal_ScrollDownWhenOverflow(t *testing.T) {
	// Create more agents than the viewport can show (viewport ~30 rows).
	count := 40
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("agent%02d", i)
	}
	tui, _ := newTestTUIWithScreen(names...)
	tui.openAgentsModal()

	// Navigate to the bottom.
	for i := 0; i < count-1; i++ {
		tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	}
	if tui.agents.selected != count-1 {
		t.Errorf("selected = %d, want %d", tui.agents.selected, count-1)
	}
	if tui.agents.scrollOffset <= 0 {
		t.Error("scrollOffset should be > 0 after navigating past viewport")
	}
	// Selected must be within viewport.
	vp := tui.agentsViewportHeight()
	if tui.agents.selected < tui.agents.scrollOffset || tui.agents.selected >= tui.agents.scrollOffset+vp {
		t.Errorf("selected %d not in viewport [%d, %d)", tui.agents.selected, tui.agents.scrollOffset, tui.agents.scrollOffset+vp)
	}
}

func TestAgentsModal_ScrollUpFromBottom(t *testing.T) {
	count := 40
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("agent%02d", i)
	}
	tui, _ := newTestTUIWithScreen(names...)
	tui.openAgentsModal()

	// Go to bottom.
	for i := 0; i < count-1; i++ {
		tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	}
	savedOffset := tui.agents.scrollOffset

	// Go back to top.
	for i := 0; i < count-1; i++ {
		tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	}
	if tui.agents.selected != 0 {
		t.Errorf("selected = %d, want 0", tui.agents.selected)
	}
	if tui.agents.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d, want 0 at top", tui.agents.scrollOffset)
	}
	_ = savedOffset
}

func TestAgentsModal_ScrollWithGrab(t *testing.T) {
	names := make([]string, 40)
	for i := range names {
		names[i] = fmt.Sprintf("agent%02d", i)
	}
	tui, _ := newTestTUIWithScreen(names...)
	tui.openAgentsModal()

	// Grab first agent and move it down past viewport.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	for i := 0; i < 35; i++ {
		tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	}

	// The grabbed item should still be visible.
	vp := tui.agentsViewportHeight()
	if tui.agents.selected < tui.agents.scrollOffset || tui.agents.selected >= tui.agents.scrollOffset+vp {
		t.Errorf("grabbed item at %d not in viewport [%d, %d)",
			tui.agents.selected, tui.agents.scrollOffset, tui.agents.scrollOffset+vp)
	}
}

func TestAgentsModal_ScrollRenderIndicators(t *testing.T) {
	names := make([]string, 50)
	for i := range names {
		names[i] = fmt.Sprintf("agent%02d", i)
	}
	tui, s := newTestTUIWithScreen(names...)
	tui.openAgentsModal()

	// Navigate partway down so there are items above and below.
	// Viewport is ~30 rows, so go past that to trigger scrolling.
	for i := 0; i < 35; i++ {
		tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	}
	tui.render()

	sw, sh := s.Size()
	allText := readScreenRect(s, 0, 0, sw, sh)
	// Should have up and down arrows.
	if !strings.ContainsRune(allText, '\u2191') {
		t.Error("should show up arrow when items above viewport")
	}
	if !strings.ContainsRune(allText, '\u2193') {
		t.Error("should show down arrow when items below viewport")
	}
}
