package tui

import "testing"

func TestComputeLayoutMoveFocusFromHidden(t *testing.T) {
	panes := []*Pane{
		hiddenTestPane("a"),
		testPane("b"),
		hiddenTestPane("c"),
	}
	state := LayoutState{
		Mode:     LayoutGrid,
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
		testPane("super"),
		testPane("eng1"),
	)

	if p := tui.findPaneByName("eng1"); p == nil || p.Name() != "eng1" {
		t.Error("findPaneByName should find eng1")
	}
	if p := tui.findPaneByName("nonexistent"); p != nil {
		t.Error("findPaneByName should return nil for unknown name")
	}
}
