package tui

import (
	"testing"
)

func TestHideCommand(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
		newTestPane("eng2", true),
		newTestPane("qa1", true),
	)
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 2

	tui.execCmd("hide eng2")
	if !tui.layoutState.Hidden[tui.panes[2].Name()] {
		t.Error("eng2 should be hidden after hide command")
	}
	if tui.layoutState.Hidden[tui.panes[0].Name()] || tui.layoutState.Hidden[tui.panes[1].Name()] || tui.layoutState.Hidden[tui.panes[3].Name()] {
		t.Error("other panes should remain visible")
	}
}

func TestHideLastVisiblePaneFails(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
	)

	tui.execCmd("hide super")
	tui.execCmd("hide eng1")
	if tui.layoutState.Hidden[tui.panes[1].Name()] {
		t.Error("should not be able to hide the last visible pane")
	}
	if tui.cmd.error != "cannot hide last visible pane" {
		t.Errorf("expected error message, got %q", tui.cmd.error)
	}
}

func TestHideAllFails(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
	)

	tui.execCmd("hide all")
	if tui.cmd.error != "cannot hide all panes" {
		t.Errorf("expected error, got %q", tui.cmd.error)
	}
	if tui.layoutState.Hidden[tui.panes[0].Name()] || tui.layoutState.Hidden[tui.panes[1].Name()] {
		t.Error("hide all should not change visibility")
	}
}

func TestHideUnknownAgent(t *testing.T) {
	tui := newTestTUI(newTestPane("super", true))
	tui.execCmd("hide nonexistent")
	if tui.cmd.error == "" {
		t.Error("expected error for unknown agent")
	}
}

func TestUnhideCommand(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
		newTestPane("eng2", false),
	)

	tui.execCmd("unhide eng2")
	if tui.layoutState.Hidden["eng2"] {
		t.Error("eng2 should be visible after unhide command")
	}
}

func TestUnhideAllCommand(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", false),
		newTestPane("eng2", true),
		newTestPane("qa1", false),
	)

	tui.execCmd("unhide all")
	for _, p := range tui.panes {
		if tui.layoutState.Hidden[p.Name()] {
			t.Errorf("pane %q should be visible after unhide all", p.Name())
		}
	}
}

func TestShowReorder(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
		newTestPane("eng2", true),
	)

	tui.execCmd("show eng2, eng1")
	if tui.panes[0].Name() != "eng2" || tui.panes[1].Name() != "eng1" || tui.panes[2].Name() != "super" {
		names := make([]string, len(tui.panes))
		for i, p := range tui.panes {
			names[i] = p.Name()
		}
		t.Errorf("show reorder: got %v, want [eng2 eng1 super]", names)
	}
}

func TestViewCommand(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
		newTestPane("eng2", true),
		newTestPane("qa1", true),
	)

	tui.execCmd("view super qa1")

	if tui.layoutState.Hidden[tui.panes[0].Name()] {
		t.Error("super should be visible")
	}
	if !tui.layoutState.Hidden[tui.panes[1].Name()] {
		t.Error("eng1 should be hidden")
	}
	if !tui.layoutState.Hidden[tui.panes[2].Name()] {
		t.Error("eng2 should be hidden")
	}
	if tui.layoutState.Hidden[tui.panes[3].Name()] {
		t.Error("qa1 should be visible")
	}
}

func TestViewUnknownAgentFails(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
	)

	tui.execCmd("view super bogus")
	if tui.cmd.error == "" {
		t.Error("expected error for unknown agent in view")
	}
	// Nothing should have changed since validation failed.
	if tui.layoutState.Hidden[tui.panes[0].Name()] || tui.layoutState.Hidden[tui.panes[1].Name()] {
		t.Error("visibility should not change on validation failure")
	}
}

func TestHideFocusedPaneMoveFocus(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
		newTestPane("eng2", true),
	)
	tui.layoutState.Focused = "eng1"

	tui.execCmd("hide eng1")

	if tui.layoutState.Focused == "eng1" {
		t.Error("focus should have moved away from hidden pane")
	}
}

func TestComputeLayoutMoveFocusFromHidden(t *testing.T) {
	panes := []*Pane{
		newTestPane("a", false),
		newTestPane("b", true),
		newTestPane("c", false),
	}
	state := LayoutState{
		Mode:    LayoutGrid,
		GridCols: 1, GridRows: 1,
		Focused: "a", // Hidden pane.
		Hidden:  map[string]bool{"a": true, "c": true},
	}
	plan := computeLayout(state, toPaneViews(panes), 200, 100)

	if plan.ValidatedFocus != "b" {
		t.Errorf("focus = %q, want b (first visible pane)", plan.ValidatedFocus)
	}
}

func TestFindPaneByName(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
	)

	if p := tui.findPaneByName("eng1"); p == nil || p.Name() != "eng1" {
		t.Error("findPaneByName should find eng1")
	}
	if p := tui.findPaneByName("nonexistent"); p != nil {
		t.Error("findPaneByName should return nil for unknown name")
	}
}

func TestShowNoArgError(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	tui.execCmd("show")
	if tui.cmd.error == "" {
		t.Error("show with no arg should produce error")
	}
}

func TestHideNoArgError(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	tui.execCmd("hide")
	if tui.cmd.error == "" {
		t.Error("hide with no arg should produce error")
	}
}

func TestViewNoArgError(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	tui.execCmd("view")
	if tui.cmd.error == "" {
		t.Error("view with no arg should produce error")
	}
}
