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
	tui.layout = LayoutGrid
	tui.gridCols, tui.gridRows = 2, 2

	tui.execCmd("hide eng2")
	if tui.panes[2].Visible() {
		t.Error("eng2 should be hidden after hide command")
	}
	if !tui.panes[0].Visible() || !tui.panes[1].Visible() || !tui.panes[3].Visible() {
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
	if !tui.panes[1].Visible() {
		t.Error("should not be able to hide the last visible pane")
	}
	if tui.cmdError != "cannot hide last visible pane" {
		t.Errorf("expected error message, got %q", tui.cmdError)
	}
}

func TestHideAllFails(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
	)

	tui.execCmd("hide all")
	if tui.cmdError != "cannot hide all panes" {
		t.Errorf("expected error, got %q", tui.cmdError)
	}
	if !tui.panes[0].Visible() || !tui.panes[1].Visible() {
		t.Error("hide all should not change visibility")
	}
}

func TestHideUnknownAgent(t *testing.T) {
	tui := newTestTUI(newTestPane("super", true))
	tui.execCmd("hide nonexistent")
	if tui.cmdError == "" {
		t.Error("expected error for unknown agent")
	}
}

func TestShowCommand(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
		newTestPane("eng2", false),
	)

	tui.execCmd("show eng2")
	if !tui.panes[2].Visible() {
		t.Error("eng2 should be visible after show command")
	}
}

func TestShowAllCommand(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", false),
		newTestPane("eng2", true),
		newTestPane("qa1", false),
	)

	tui.execCmd("show all")
	for _, p := range tui.panes {
		if !p.Visible() {
			t.Errorf("pane %q should be visible after show all", p.name)
		}
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

	if !tui.panes[0].Visible() {
		t.Error("super should be visible")
	}
	if tui.panes[1].Visible() {
		t.Error("eng1 should be hidden")
	}
	if tui.panes[2].Visible() {
		t.Error("eng2 should be hidden")
	}
	if !tui.panes[3].Visible() {
		t.Error("qa1 should be visible")
	}
}

func TestViewUnknownAgentFails(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
	)

	tui.execCmd("view super bogus")
	if tui.cmdError == "" {
		t.Error("expected error for unknown agent in view")
	}
	// Nothing should have changed since validation failed.
	if !tui.panes[0].Visible() || !tui.panes[1].Visible() {
		t.Error("visibility should not change on validation failure")
	}
}

func TestHideFocusedPaneMoveFocus(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
		newTestPane("eng2", true),
	)
	tui.focused = 1 // Focus eng1.

	tui.execCmd("hide eng1")

	if tui.focused == 1 {
		t.Error("focus should have moved away from hidden pane")
	}
	if !tui.panes[tui.focused].Visible() {
		t.Error("focus should be on a visible pane")
	}
}

func TestEnsureFocusVisibleMovesToFirstVisible(t *testing.T) {
	tui := newTestTUI(
		newTestPane("a", false),
		newTestPane("b", true),
		newTestPane("c", false),
	)
	tui.focused = 0 // Focused on hidden pane.

	tui.ensureFocusVisible()

	if tui.focused != 1 {
		t.Errorf("focus = %d, want 1 (first visible pane)", tui.focused)
	}
}

func TestFindPaneByName(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
	)

	if p := tui.findPaneByName("eng1"); p == nil || p.name != "eng1" {
		t.Error("findPaneByName should find eng1")
	}
	if p := tui.findPaneByName("nonexistent"); p != nil {
		t.Error("findPaneByName should return nil for unknown name")
	}
}

func TestShowNoArgError(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	tui.execCmd("show")
	if tui.cmdError == "" {
		t.Error("show with no arg should produce error")
	}
}

func TestHideNoArgError(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	tui.execCmd("hide")
	if tui.cmdError == "" {
		t.Error("hide with no arg should produce error")
	}
}

func TestViewNoArgError(t *testing.T) {
	tui := newTestTUI(newTestPane("a", true))
	tui.execCmd("view")
	if tui.cmdError == "" {
		t.Error("view with no arg should produce error")
	}
}
