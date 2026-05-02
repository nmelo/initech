package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestAgentInfoVisibility(t *testing.T) {
	// Verify AgentInfo carries visibility state correctly.
	visible := AgentInfo{Name: "eng1", Status: "running", Visible: true}
	hidden := AgentInfo{Name: "eng2", Status: "idle", Visible: false}

	if !visible.Visible {
		t.Error("visible agent should have Visible=true")
	}
	if hidden.Visible {
		t.Error("hidden agent should have Visible=false")
	}
}

func TestOverlayPanelWidthWithHiddenMarker(t *testing.T) {
	// The overlay panel width must account for the " [h]" suffix on hidden panes.
	// Simulate the width calculation from renderOverlay.
	names := []struct {
		name    string
		visible bool
	}{
		{"super", true},
		{"eng1", true},
		{"qa-long-name", false}, // hidden: effective length = 12 + 4 = 16
	}

	maxNameLen := 0
	for _, n := range names {
		nameLen := len(n.name)
		if !n.visible {
			nameLen += 4 // " [h]"
		}
		if nameLen > maxNameLen {
			maxNameLen = nameLen
		}
	}

	// Panel width = 4 (left border + dot + space) + maxNameLen + 1 (gap) + 8 (status) + 2 (right border + padding)
	panelW := 4 + maxNameLen + 1 + 8 + 2

	// The hidden pane's name + marker ("qa-long-name [h]" = 16 chars) must fit.
	// 4 + 16 + 1 + 8 + 2 = 31
	if panelW != 31 {
		t.Errorf("panelW = %d, want 31", panelW)
	}
}

func TestSummaryLineOnlyWhenHidden(t *testing.T) {
	// When all panes are visible, no summary line should be added.
	agents := []AgentInfo{
		{Name: "a", Visible: true},
		{Name: "b", Visible: true},
	}
	hiddenCount := 0
	for _, a := range agents {
		if !a.Visible {
			hiddenCount++
		}
	}
	summaryRow := hiddenCount > 0
	panelH := len(agents) + 2
	if summaryRow {
		panelH++
	}
	// 2 agents + 2 border rows = 4
	if panelH != 4 {
		t.Errorf("panelH = %d, want 4 (no summary row when all visible)", panelH)
	}

	// When one pane is hidden, summary line is added.
	agents = append(agents, AgentInfo{Name: "c", Visible: false})
	hiddenCount = 0
	for _, a := range agents {
		if !a.Visible {
			hiddenCount++
		}
	}
	summaryRow = hiddenCount > 0
	panelH = len(agents) + 2
	if summaryRow {
		panelH++
	}
	// 3 agents + 2 borders + 1 summary = 6
	if panelH != 6 {
		t.Errorf("panelH = %d, want 6 (summary row when hidden panes exist)", panelH)
	}
}

func TestRenderOverlay_HiddenAgentNameItalic(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Hidden["eng2"] = true

	tui.renderOverlay()

	sw, _ := s.Size()
	maxNameLen := len("eng2") + 4 // hidden suffix " [h]"
	panelW := 4 + maxNameLen + 1 + 7 + 2
	px := sw - panelW - 1
	py := 1

	_, _, visibleStyle, _ := s.GetContent(px+4, py+1)
	_, _, visibleAttrs := visibleStyle.Decompose()
	if visibleAttrs&tcell.AttrItalic != 0 {
		t.Fatal("visible overlay agent name should not be italic")
	}

	_, _, hiddenStyle, _ := s.GetContent(px+4, py+2)
	_, _, hiddenAttrs := hiddenStyle.Decompose()
	if hiddenAttrs&tcell.AttrItalic == 0 {
		t.Fatal("hidden overlay agent name should be italic")
	}
}

// ── ini-khy: overlay dot click-to-toggle ────────────────────────────

func TestOverlayDotClick_TogglesHidden(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.layoutState.Overlay = true
	tui.renderOverlay()

	dotCol := tui.overlayBounds.x + 2
	eng2Row := tui.overlayBounds.y + 1 + 1 // agent index 1 = eng2

	if tui.layoutState.Hidden["eng2"] {
		t.Fatal("eng2 should start visible")
	}

	tui.handleMouse(tcell.NewEventMouse(dotCol, eng2Row, tcell.Button1, 0))

	if !tui.layoutState.Hidden["eng2"] {
		t.Error("clicking dot should hide eng2")
	}

	// Click again to unhide.
	tui.handleMouse(tcell.NewEventMouse(dotCol, eng2Row, tcell.Button1, 0))

	if tui.layoutState.Hidden["eng2"] {
		t.Error("clicking dot again should unhide eng2")
	}
}

func TestOverlayDotClick_OnlyDotColumnIsTarget(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Overlay = true
	tui.renderOverlay()

	nameCol := tui.overlayBounds.x + 4 // name starts here, not the dot
	eng1Row := tui.overlayBounds.y + 1  // agent index 0

	tui.handleMouse(tcell.NewEventMouse(nameCol, eng1Row, tcell.Button1, 0))

	if tui.layoutState.Hidden["eng1"] {
		t.Error("clicking the name column (not dot) should not toggle visibility")
	}
}

func TestOverlayDotClick_BlocksLastVisible(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Overlay = true
	tui.layoutState.Hidden["eng2"] = true // only eng1 visible
	tui.renderOverlay()

	dotCol := tui.overlayBounds.x + 2
	eng1Row := tui.overlayBounds.y + 1

	tui.handleMouse(tcell.NewEventMouse(dotCol, eng1Row, tcell.Button1, 0))

	if tui.layoutState.Hidden["eng1"] {
		t.Error("should not be able to hide last visible pane")
	}
}

func TestOverlayDotClick_OverlayNotVisible(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Overlay = false
	// Set fake bounds as if overlay had rendered previously.
	tui.overlayBounds.x = 100
	tui.overlayBounds.y = 1
	tui.overlayBounds.agentCount = 2

	dotCol := tui.overlayBounds.x + 2
	eng1Row := tui.overlayBounds.y + 1

	tui.handleMouse(tcell.NewEventMouse(dotCol, eng1Row, tcell.Button1, 0))

	if tui.layoutState.Hidden["eng1"] {
		t.Error("should not toggle when overlay is not visible")
	}
}

func TestOverlayDotClick_ClickOutsideAgentRows(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Overlay = true
	tui.renderOverlay()

	dotCol := tui.overlayBounds.x + 2
	pastLastRow := tui.overlayBounds.y + 1 + 2 // only 2 agents (indices 0,1)

	tui.handleMouse(tcell.NewEventMouse(dotCol, pastLastRow, tcell.Button1, 0))

	if tui.layoutState.Hidden["eng1"] || tui.layoutState.Hidden["eng2"] {
		t.Error("clicking outside agent rows should not toggle anything")
	}
}

func TestOverlayDotClick_StoresBoundsCorrectly(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.renderOverlay()

	if tui.overlayBounds.agentCount != 3 {
		t.Errorf("agentCount = %d, want 3", tui.overlayBounds.agentCount)
	}
	if tui.overlayBounds.y != 1 {
		t.Errorf("overlay y = %d, want 1 (standard py)", tui.overlayBounds.y)
	}
}
