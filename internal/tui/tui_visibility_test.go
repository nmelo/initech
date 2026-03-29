package tui

import "testing"

// newTestPane creates a minimal Pane for testing layout logic.
func newTestPane(name string, visible bool) *Pane {
	p := &Pane{name: name, visible: visible}
	return p
}

// newTestTUI creates a TUI with the given panes and no screen.
func newTestTUI(panes ...*Pane) *TUI {
	names := make([]string, len(panes))
	for i, p := range panes {
		names[i] = p.name
	}
	ls := DefaultLayoutState(names)
	// Mark hidden panes in layoutState.
	for _, p := range panes {
		if !p.visible {
			if ls.Hidden == nil {
				ls.Hidden = make(map[string]bool)
			}
			ls.Hidden[p.name] = true
		}
	}
	// Convert []*Pane to []PaneView.
	views := make([]PaneView, len(panes))
	for i, p := range panes {
		views[i] = p
	}
	return &TUI{panes: views, layoutState: ls}
}

// toPaneViews converts a []*Pane to []PaneView for test helpers.
func toPaneViews(panes []*Pane) []PaneView {
	views := make([]PaneView, len(panes))
	for i, p := range panes {
		views[i] = p
	}
	return views
}

func TestPaneVisibleDefault(t *testing.T) {
	p := &Pane{visible: true}
	if !p.Visible() {
		t.Error("new pane should be visible by default")
	}
}

func TestPaneSetVisible(t *testing.T) {
	p := newTestPane("eng1", true)
	p.SetVisible(false)
	if p.Visible() {
		t.Error("pane should be hidden after SetVisible(false)")
	}
	p.SetVisible(true)
	if !p.Visible() {
		t.Error("pane should be visible after SetVisible(true)")
	}
}

func TestComputeLayoutVisibility(t *testing.T) {
	panes := []*Pane{
		newTestPane("super", true),
		newTestPane("eng1", true),
		newTestPane("eng2", false),
		newTestPane("qa1", true),
	}
	state := LayoutState{
		Mode: LayoutGrid, GridCols: 2, GridRows: 2,
		Focused: "super",
		Hidden:  map[string]bool{"eng2": true},
	}
	plan := computeLayout(state, toPaneViews(panes), 200, 100)
	if len(plan.Panes) != 3 {
		t.Fatalf("visible panes = %d, want 3", len(plan.Panes))
	}
	for _, pr := range plan.Panes {
		if pr.Pane.Name() == "eng2" {
			t.Error("hidden pane eng2 should not be in the plan")
		}
	}
}

func TestComputeLayoutAllVisible(t *testing.T) {
	panes := []*Pane{newTestPane("a", true), newTestPane("b", true)}
	state := LayoutState{Mode: LayoutGrid, GridCols: 2, GridRows: 1, Focused: "a", Hidden: map[string]bool{}}
	plan := computeLayout(state, toPaneViews(panes), 200, 100)
	if len(plan.Panes) != 2 {
		t.Fatalf("visible panes = %d, want 2", len(plan.Panes))
	}
}

func TestComputeLayoutAllHiddenOld(t *testing.T) {
	panes := []*Pane{newTestPane("a", false), newTestPane("b", false)}
	state := LayoutState{Mode: LayoutGrid, Focused: "a", Hidden: map[string]bool{"a": true, "b": true}}
	plan := computeLayout(state, toPaneViews(panes), 200, 100)
	if len(plan.Panes) != 0 {
		t.Fatalf("visible panes = %d, want 0", len(plan.Panes))
	}
}

func TestVisibleCountFromState(t *testing.T) {
	panes := []*Pane{
		newTestPane("super", true),
		newTestPane("eng1", false),
		newTestPane("eng2", true),
		newTestPane("qa1", false),
	}
	tui := &TUI{
		panes:       toPaneViews(panes),
		layoutState: LayoutState{Hidden: map[string]bool{"eng1": true, "qa1": true}},
	}
	if tui.visibleCountFromState() != 2 {
		t.Errorf("visibleCountFromState() = %d, want 2", tui.visibleCountFromState())
	}
}

func TestCycleFocusSkipsHidden(t *testing.T) {
	panes := []*Pane{
		newTestPane("super", true),
		newTestPane("eng1", false),
		newTestPane("eng2", true),
		newTestPane("qa1", false),
		newTestPane("qa2", true),
	}
	tui := &TUI{
		panes: toPaneViews(panes),
		layoutState: LayoutState{
			Focused: "super",
			Hidden:  map[string]bool{"eng1": true, "qa1": true},
		},
	}

	// Forward: super -> eng2 (skips hidden eng1)
	tui.cycleFocus(1)
	if tui.layoutState.Focused != "eng2" {
		t.Errorf("after +1 from super: focused = %q, want eng2", tui.layoutState.Focused)
	}

	// Forward: eng2 -> qa2 (skips hidden qa1)
	tui.cycleFocus(1)
	if tui.layoutState.Focused != "qa2" {
		t.Errorf("after +1 from eng2: focused = %q, want qa2", tui.layoutState.Focused)
	}

	// Forward: qa2 -> super (wraps)
	tui.cycleFocus(1)
	if tui.layoutState.Focused != "super" {
		t.Errorf("after +1 from qa2: focused = %q, want super", tui.layoutState.Focused)
	}

	// Backward: super -> qa2 (wraps, skips hidden qa1)
	tui.cycleFocus(-1)
	if tui.layoutState.Focused != "qa2" {
		t.Errorf("after -1 from super: focused = %q, want qa2", tui.layoutState.Focused)
	}
}

func TestCycleFocusAllHidden(t *testing.T) {
	panes := []*Pane{newTestPane("a", false), newTestPane("b", false)}
	tui := &TUI{
		panes: toPaneViews(panes),
		layoutState: LayoutState{
			Focused: "a",
			Hidden:  map[string]bool{"a": true, "b": true},
		},
	}
	tui.cycleFocus(1)
	// Focus should not change.
	if tui.layoutState.Focused != "a" {
		t.Errorf("cycleFocus with all hidden: focused = %q, want a", tui.layoutState.Focused)
	}
}

func TestComputeLayoutHiddenPaneFocusBug(t *testing.T) {
	// This is the structural test for the hidden-pane-focus bug.
	// Focusing a hidden pane should snap focus to the first visible pane.
	panes := []*Pane{
		newTestPane("super", true),
		newTestPane("eng1", false),
		newTestPane("qa1", true),
	}
	state := LayoutState{
		Mode:    LayoutFocus,
		Focused: "eng1", // hidden!
		Hidden:  map[string]bool{"eng1": true},
	}
	plan := computeLayout(state, toPaneViews(panes), 200, 100)

	if len(plan.Panes) != 1 {
		t.Fatalf("focus mode: got %d pane renders, want 1", len(plan.Panes))
	}
	if plan.ValidatedFocus == "eng1" {
		t.Error("focus should NOT be on hidden pane eng1")
	}
	if plan.ValidatedFocus != "super" {
		t.Errorf("focus should snap to first visible (super), got %q", plan.ValidatedFocus)
	}
	if plan.Panes[0].Pane.Name() != "super" {
		t.Errorf("rendered pane should be super, got %q", plan.Panes[0].Pane.Name())
	}
}
