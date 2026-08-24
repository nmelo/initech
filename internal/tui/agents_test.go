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

// TestAgentsModal_CloseRemovesEmptyGroup is ini-2rc's group-lifecycle AC:
// a band left empty by a grab disappears when the modal closes.
func TestAgentsModal_CloseRemovesEmptyGroup(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1")
	tui.openAgentsModal()
	tui.layoutState.Groups = append(tui.layoutState.Groups, "empty-band")
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))

	for _, g := range tui.layoutState.Groups {
		if g == "empty-band" {
			t.Error("empty band should be removed when the modal closes")
		}
	}
}

// TestAgentsModal_NavigateVertical_AcrossBands uses a 3-family fleet (core,
// eng, qa each seed to their own band) so Down/Up actually crosses bands --
// a same-family fleet lands in one band and Down has nothing to move to.
func TestAgentsModal_NavigateVertical_AcrossBands(t *testing.T) {
	tui, s := newTestTUIWithScreen("super", "eng1", "qa1")
	tui.openAgentsModal()
	if got := tui.layoutState.GroupOf["super"]; got != "core" {
		t.Fatalf("precondition: super should seed to core, got %q", got)
	}

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if got := tui.panes[tui.agents.selected].Name(); got != "eng1" {
		t.Errorf("after Down from core: selected = %q, want eng1", got)
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if got := tui.panes[tui.agents.selected].Name(); got != "qa1" {
		t.Errorf("after Down x2: selected = %q, want qa1", got)
	}
	// Down past the last band is a no-op.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if got := tui.panes[tui.agents.selected].Name(); got != "qa1" {
		t.Errorf("Down past last band: selected = %q, want qa1 (unchanged)", got)
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if got := tui.panes[tui.agents.selected].Name(); got != "eng1" {
		t.Errorf("after Up: selected = %q, want eng1", got)
	}
	_ = s
}

// TestAgentsModal_NavigateHorizontal_WithinBand: same-family agents share
// one band and lay out left-to-right; Right/Left moves among them.
func TestAgentsModal_NavigateHorizontal_WithinBand(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	if got := tui.panes[tui.agents.selected].Name(); got != "eng2" {
		t.Errorf("after Right: selected = %q, want eng2", got)
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	if got := tui.panes[tui.agents.selected].Name(); got != "eng3" {
		t.Errorf("after Right x2: selected = %q, want eng3", got)
	}
	// Right past the band's end is a no-op.
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	if got := tui.panes[tui.agents.selected].Name(); got != "eng3" {
		t.Errorf("Right past band end: selected = %q, want eng3 (unchanged)", got)
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	if got := tui.panes[tui.agents.selected].Name(); got != "eng2" {
		t.Errorf("after Left: selected = %q, want eng2", got)
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
	// Band labels should also render.
	for _, band := range []string{"core", "eng", "qa"} {
		if !strings.Contains(allText, band) {
			t.Errorf("rendered output missing band label %q", band)
		}
	}
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

// TestAgentsModal_RenderHiddenAgentNameItalic locates eng2's cell via the
// same layout function renderAgentsGrid uses, rather than a hand-derived
// offset -- the box's origin depends on content size, which depends on the
// fleet, so a hardcoded coordinate would silently stop testing anything if
// either changed.
func TestAgentsModal_RenderHiddenAgentNameItalic(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Hidden["eng2"] = true
	tui.openAgentsModal()
	tui.render()

	sw, sh := s.Size()
	members := tui.agentsGroupMembers()
	box := agentsGridBoxDims(members, tui.layoutState.Groups, untieredTiers(tui.layoutState.Groups), false, sw, sh, false)

	cells := agentsGridLayoutCells(members, tui.layoutState.Groups, box.innerX, box.startY, box.perRow)
	var eng1Cell, eng2Cell *gridCell
	for i := range cells {
		switch tui.panes[cells[i].paneIdx].Name() {
		case "eng1":
			eng1Cell = &cells[i]
		case "eng2":
			eng2Cell = &cells[i]
		}
	}
	if eng1Cell == nil || eng2Cell == nil {
		t.Fatal("could not locate both cells")
	}

	nameColOffset := 4 + 4 // "%3d " + "[x] "
	_, _, visibleStyle, _ := s.GetContent(eng1Cell.x+nameColOffset, eng1Cell.y)
	_, _, visibleAttrs := visibleStyle.Decompose()
	if visibleAttrs&tcell.AttrItalic != 0 {
		t.Fatal("visible agent's name should not be italic")
	}

	_, _, hiddenStyle, _ := s.GetContent(eng2Cell.x+nameColOffset, eng2Cell.y)
	_, _, hiddenAttrs := hiddenStyle.Decompose()
	if hiddenAttrs&tcell.AttrItalic == 0 {
		t.Fatal("hidden agent's name should be italic")
	}
}

func TestAgentsModal_RenderShowsProtectMarker(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Protected = map[string]bool{"eng1": true}
	tui.openAgentsModal()
	tui.render()

	sw, sh := s.Size()
	allText := readScreenRect(s, 0, 0, sw, sh)

	if !strings.ContainsRune(allText, '•') {
		t.Error("rendered output missing the protect marker '•' for a protected agent")
	}
}

func TestAgentsModal_RenderShowsLivePinMarker(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.LivePinned = map[string]int{"eng2": 0}
	tui.openAgentsModal()
	tui.render()

	sw, sh := s.Size()
	allText := readScreenRect(s, 0, 0, sw, sh)

	if !strings.ContainsRune(allText, '*') {
		t.Error("rendered output missing the pin marker '*' for a live-pinned agent")
	}
}

// TestAgentsModal_RenderLiveModeDisplayedMarker: an agent occupying a live
// slot without an explicit pin shows the auto-displayed marker '◦', distinct
// from the explicit-pin marker '*' (spec: live pin/slot state "must remain
// representable in the cell").
func TestAgentsModal_RenderLiveModeDisplayedMarker(t *testing.T) {
	tui, s := newTestTUIWithScreen("super", "eng1", "pm")
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.LivePinned = map[string]int{"pm": 1}
	tui.layoutState.LiveSlots = []string{"super", "pm"}
	tui.openAgentsModal()
	tui.render()

	sw, sh := s.Size()
	allText := readScreenRect(s, 0, 0, sw, sh)

	if !strings.ContainsRune(allText, '◦') {
		t.Errorf("rendered output missing the live-displayed marker '◦' for super (in LiveSlots, not pinned). Got:\n%s", allText)
	}
	if !strings.ContainsRune(allText, '*') {
		t.Error("rendered output missing the pin marker '*' for pm (explicitly live-pinned)")
	}
}

func TestAgentsModal_RenderIsFloating(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()
	tui.render()

	sw, sh := s.Size()
	members := tui.agentsGroupMembers()
	box := agentsGridBoxDims(members, tui.layoutState.Groups, untieredTiers(tui.layoutState.Groups), false, sw, sh, false)
	startX, startY := box.startX, box.startY

	c, _, _ := s.Get(startX, startY)
	if c != "┌" {
		t.Errorf("top-left corner = %q, want box drawing char", c)
	}
	if startX > 0 {
		c0, _, _ := s.Get(0, 0)
		if c0 == "┌" {
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
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
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
		tcell.NewEventKey(tcell.KeyLeft, 0, 0),
		tcell.NewEventKey(tcell.KeyRight, 0, 0),
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
	tui, _ := newTestTUIWithScreen("super", "eng1")
	tui.openAgentsModal()

	tui.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if got := tui.panes[tui.agents.selected].Name(); got != "eng1" {
		t.Errorf("handleKey should route Down to agents modal: selected = %q, want eng1", got)
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

func TestAgentsModal_ReorderViaGrab_WithinBand(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	if tui.panes[0].Name() != "eng2" || tui.panes[1].Name() != "eng1" {
		t.Errorf("after move right: order = [%s, %s, %s], want [eng2, eng1, eng3]",
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

// TestAgentsModal_ReorderViaGrab_AcrossBands is ini-2rc's core new
// mechanism: grabbing an agent and moving it into a different band's line
// reassigns its group, not just its position.
func TestAgentsModal_ReorderViaGrab_AcrossBands(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1")
	tui.openAgentsModal()
	if got := tui.layoutState.GroupOf["super"]; got != "core" {
		t.Fatalf("precondition: super should be in core, got %q", got)
	}

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0)) // grab super
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))  // carry into eng's line

	if got := tui.layoutState.GroupOf["super"]; got != "eng" {
		t.Errorf("super's group after cross-band grab = %q, want eng", got)
	}
	if got := tui.panes[tui.agents.selected].Name(); got != "super" {
		t.Errorf("selection should follow the grabbed agent, got %q", got)
	}
}

func TestAgentsModal_ProtectToggle(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()

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
	tui.layoutState.Mode = LayoutGrid
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'p', 0))
	if tui.agents.error == "" {
		t.Error("p in non-live mode should show error")
	}
}

func TestAgentsModal_MultiPinDoesNotEvictSlot0(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.GridCols = 2
	tui.layoutState.GridRows = 1
	tui.layoutState.LivePinned = make(map[string]int)
	tui.layoutState.LiveSlots = []string{"eng1", "eng2"}
	tui.openAgentsModal()

	tui.agents.selected = 0
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'p', 0))
	if tui.layoutState.LivePinned["eng1"] != 0 {
		t.Fatalf("eng1 should be pinned to slot 0, got %d", tui.layoutState.LivePinned["eng1"])
	}

	tui.agents.selected = 2
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'p', 0))
	if _, ok := tui.layoutState.LivePinned["eng1"]; !ok {
		t.Error("eng1 pin should NOT be evicted when pinning eng3")
	}
	if slot, ok := tui.layoutState.LivePinned["eng3"]; !ok || slot != 1 {
		t.Errorf("eng3 should be pinned to slot 1, got slot=%d ok=%v", slot, ok)
	}
}

func TestAgentsModal_AllSlotsPinnedShowsError(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.GridCols = 2
	tui.layoutState.GridRows = 1
	tui.layoutState.LivePinned = map[string]int{"eng1": 0, "eng2": 1}
	tui.layoutState.LiveSlots = []string{"eng1", "eng2"}
	tui.openAgentsModal()

	tui.agents.selected = 2
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'p', 0))
	if tui.agents.error == "" {
		t.Error("should show error when all slots are pinned")
	}
	if _, ok := tui.layoutState.LivePinned["eng3"]; ok {
		t.Error("eng3 should NOT be pinned when all slots are full")
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
	if tui.agents.selected != 0 {
		t.Errorf("selected=%d, want 0 after reset", tui.agents.selected)
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
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
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
	if !tui.agents.active {
		t.Error("Esc canceling a grab should NOT also close the modal (a second Esc closes)")
	}
}

func TestAgentsModal_EscTwiceClosesAfterCancelingMoving(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0)) // cancels grab
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0)) // closes modal
	if tui.agents.active {
		t.Error("second Esc should close the modal")
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

// ── Group creation ('g') ────────────────────────────────────────────

func TestAgentsModal_CreateGroupViaG(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()
	before := len(tui.layoutState.Groups)

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'g', 0))
	if !tui.agents.creatingGroup {
		t.Fatal("g should open the group-name prompt")
	}
	for _, r := range "mkt" {
		tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	if tui.agents.creatingGroup {
		t.Error("Enter should close the group-name prompt")
	}
	found := false
	for _, g := range tui.layoutState.Groups {
		if g == "mkt" {
			found = true
		}
	}
	if !found {
		t.Errorf("groups after creation = %v, want to contain %q", tui.layoutState.Groups, "mkt")
	}
	if len(tui.layoutState.Groups) != before+1 {
		t.Errorf("group count = %d, want %d", len(tui.layoutState.Groups), before+1)
	}
}

// TestAgentsModal_CreateGroupInsertsAfterCurrentBand is the spec's precise
// placement rule: "the new (empty) band appears after the current one" --
// not always at the end of the list.
func TestAgentsModal_CreateGroupInsertsAfterCurrentBand(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "qa1")
	tui.openAgentsModal()
	if diff := tui.layoutState.Groups; len(diff) != 3 || diff[0] != "core" || diff[1] != "eng" || diff[2] != "qa" {
		t.Fatalf("precondition: groups = %v, want [core eng qa]", diff)
	}

	// Selection is on super (core, the first band) -- new group should land
	// right after core, not after qa (the last band).
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'g', 0))
	for _, r := range "mkt" {
		tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	want := []string{"core", "mkt", "eng", "qa"}
	got := tui.layoutState.Groups
	if len(got) != len(want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("groups[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestAgentsModal_CreateGroupEmptyNameRejected(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1")
	tui.openAgentsModal()
	before := append([]string(nil), tui.layoutState.Groups...)

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'g', 0))
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	if !tui.agents.creatingGroup {
		t.Error("an empty name should not close the prompt")
	}
	if len(tui.layoutState.Groups) != len(before) {
		t.Errorf("groups changed on empty-name Enter: %v -> %v", before, tui.layoutState.Groups)
	}
}

// TestAgentsModal_CreateGroupExactDuplicateRejected is ini-9y3s's core AC:
// two groups sharing a label make every by-name lookup resolve to the
// first, so create/rename/move/delete on "the second one" silently act on
// the first -- a routing corruption, not a cosmetic annoyance.
func TestAgentsModal_CreateGroupExactDuplicateRejected(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "qa1")
	tui.openAgentsModal()
	before := append([]string(nil), tui.layoutState.Groups...)
	if !groupNameExists(before, "eng") {
		t.Fatalf("precondition: %v should already contain \"eng\"", before)
	}

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'g', 0))
	for _, r := range "eng" {
		tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	if !tui.agents.creatingGroup {
		t.Error("a duplicate name should not close the prompt, same as an empty one")
	}
	if tui.agents.error == "" {
		t.Error("a duplicate name should set a visible notice, same surface as the empty-name rejection")
	}
	if len(tui.layoutState.Groups) != len(before) {
		t.Errorf("groups changed on duplicate-name Enter: %v -> %v", before, tui.layoutState.Groups)
	}
}

// TestAgentsModal_CreateGroupCaseVariantIsNotADuplicate pins the spec's
// explicit instruction: case variants ("Eng" vs "eng") are DIFFERENT names,
// never folded together.
func TestAgentsModal_CreateGroupCaseVariantIsNotADuplicate(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "qa1")
	tui.openAgentsModal()
	if !groupNameExists(tui.layoutState.Groups, "eng") {
		t.Fatalf("precondition: %v should already contain \"eng\"", tui.layoutState.Groups)
	}

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'g', 0))
	for _, r := range "Eng" {
		tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	if tui.agents.creatingGroup {
		t.Error("\"Eng\" should be accepted as a distinct name from \"eng\", not rejected as a duplicate")
	}
	if !groupNameExists(tui.layoutState.Groups, "Eng") {
		t.Errorf("groups = %v, want it to contain \"Eng\" as its own band", tui.layoutState.Groups)
	}
}

// TestAgentsModal_CreateGroupWhitespaceVariantIsNotADuplicate: internal
// whitespace is part of the name, never collapsed for comparison.
func TestAgentsModal_CreateGroupWhitespaceVariantIsNotADuplicate(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "qa1")
	tui.openAgentsModal()
	tui.layoutState.Groups = append(tui.layoutState.Groups, "eng team")

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'g', 0))
	for _, r := range "eng  team" { // two spaces, not one
		tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	if tui.agents.creatingGroup {
		t.Error("\"eng  team\" (two spaces) should be accepted as distinct from \"eng team\" (one), not rejected")
	}
	if !groupNameExists(tui.layoutState.Groups, "eng  team") {
		t.Errorf("groups = %v, want it to contain \"eng  team\" as its own band", tui.layoutState.Groups)
	}
}

func TestAgentsModal_CreateGroupEscCancels(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1")
	tui.openAgentsModal()
	before := append([]string(nil), tui.layoutState.Groups...)

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'g', 0))
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, 'x', 0))
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))

	if tui.agents.creatingGroup {
		t.Error("Esc should close the group-name prompt")
	}
	if len(tui.layoutState.Groups) != len(before) {
		t.Errorf("groups changed after Esc-canceled creation: %v -> %v", before, tui.layoutState.Groups)
	}
}

// TestAgentsModal_NavigatesFullFleetWithoutScrolling confirms the
// content-sized grid handles a full 23-agent fleet by growing the box, not
// by scrolling (spec: "as big as it needs to be, no bigger" -- there is no
// viewport concept in this design at all, unlike the flat modal it
// replaces).
func TestAgentsModal_NavigatesFullFleetWithoutScrolling(t *testing.T) {
	names := make([]string, 23)
	for i := range names {
		names[i] = fmt.Sprintf("qa%d", i+1)
	}
	tui, _ := newTestTUIWithScreen(names...)
	tui.openAgentsModal()
	for i := 0; i < 22; i++ {
		tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	}
	if got := tui.panes[tui.agents.selected].Name(); got != "qa23" {
		t.Errorf("after 22 Rights across a 23-member single band: selected = %q, want qa23", got)
	}
}
