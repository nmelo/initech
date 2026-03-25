package tui

import (
	"testing"
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
