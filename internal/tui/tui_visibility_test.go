package tui

import "testing"

// newTestPane creates a minimal Pane for testing visibility logic.
// No PTY, emulator, or process is started.
func newTestPane(name string, visible bool) *Pane {
	p := &Pane{name: name, visible: visible}
	return p
}

// newTestTUI creates a TUI with the given panes and no screen.
func newTestTUI(panes ...*Pane) *TUI {
	return &TUI{panes: panes}
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

func TestVisiblePanes(t *testing.T) {
	a := newTestPane("super", true)
	b := newTestPane("eng1", true)
	c := newTestPane("eng2", false)
	d := newTestPane("qa1", true)

	tui := newTestTUI(a, b, c, d)
	vis := tui.visiblePanes()

	if len(vis) != 3 {
		t.Fatalf("visiblePanes() = %d, want 3", len(vis))
	}
	if vis[0] != a || vis[1] != b || vis[2] != d {
		t.Error("visiblePanes() returned wrong panes")
	}
}

func TestVisiblePanesAllVisible(t *testing.T) {
	a := newTestPane("super", true)
	b := newTestPane("eng1", true)

	tui := newTestTUI(a, b)
	vis := tui.visiblePanes()

	if len(vis) != 2 {
		t.Fatalf("visiblePanes() = %d, want 2", len(vis))
	}
}

func TestVisiblePanesAllHidden(t *testing.T) {
	a := newTestPane("super", false)
	b := newTestPane("eng1", false)

	tui := newTestTUI(a, b)
	vis := tui.visiblePanes()

	if len(vis) != 0 {
		t.Fatalf("visiblePanes() = %d, want 0", len(vis))
	}
}

func TestAllPanes(t *testing.T) {
	a := newTestPane("super", true)
	b := newTestPane("eng1", false)

	tui := newTestTUI(a, b)
	all := tui.allPanes()

	if len(all) != 2 {
		t.Fatalf("allPanes() = %d, want 2", len(all))
	}
}

func TestVisibleCount(t *testing.T) {
	a := newTestPane("super", true)
	b := newTestPane("eng1", false)
	c := newTestPane("eng2", true)
	d := newTestPane("qa1", false)

	tui := newTestTUI(a, b, c, d)
	if tui.visibleCount() != 2 {
		t.Errorf("visibleCount() = %d, want 2", tui.visibleCount())
	}

	b.SetVisible(true)
	if tui.visibleCount() != 3 {
		t.Errorf("visibleCount() after showing b = %d, want 3", tui.visibleCount())
	}
}

func TestPaneIndex(t *testing.T) {
	a := newTestPane("super", true)
	b := newTestPane("eng1", true)
	c := newTestPane("eng2", true)

	tui := newTestTUI(a, b, c)

	if tui.paneIndex(a) != 0 {
		t.Errorf("paneIndex(a) = %d, want 0", tui.paneIndex(a))
	}
	if tui.paneIndex(b) != 1 {
		t.Errorf("paneIndex(b) = %d, want 1", tui.paneIndex(b))
	}
	if tui.paneIndex(c) != 2 {
		t.Errorf("paneIndex(c) = %d, want 2", tui.paneIndex(c))
	}

	orphan := newTestPane("orphan", true)
	if tui.paneIndex(orphan) != -1 {
		t.Errorf("paneIndex(orphan) = %d, want -1", tui.paneIndex(orphan))
	}
}

func TestCycleFocusSkipsHidden(t *testing.T) {
	a := newTestPane("super", true)  // 0
	b := newTestPane("eng1", false)  // 1 (hidden)
	c := newTestPane("eng2", true)   // 2
	d := newTestPane("qa1", false)   // 3 (hidden)
	e := newTestPane("qa2", true)    // 4

	tui := newTestTUI(a, b, c, d, e)
	tui.focused = 0

	// Forward: 0 -> 2 (skips hidden 1)
	tui.cycleFocus(1)
	if tui.focused != 2 {
		t.Errorf("after cycleFocus(1) from 0: focused = %d, want 2", tui.focused)
	}

	// Forward: 2 -> 4 (skips hidden 3)
	tui.cycleFocus(1)
	if tui.focused != 4 {
		t.Errorf("after cycleFocus(1) from 2: focused = %d, want 4", tui.focused)
	}

	// Forward: 4 -> 0 (wraps, skips nothing)
	tui.cycleFocus(1)
	if tui.focused != 0 {
		t.Errorf("after cycleFocus(1) from 4: focused = %d, want 0", tui.focused)
	}

	// Backward: 0 -> 4 (wraps, skips hidden 3)
	tui.cycleFocus(-1)
	if tui.focused != 4 {
		t.Errorf("after cycleFocus(-1) from 0: focused = %d, want 4", tui.focused)
	}
}

func TestCycleFocusAllHidden(t *testing.T) {
	a := newTestPane("super", false)
	b := newTestPane("eng1", false)

	tui := newTestTUI(a, b)
	tui.focused = 0

	// Should not change focus when all panes are hidden.
	tui.cycleFocus(1)
	if tui.focused != 0 {
		t.Errorf("cycleFocus with all hidden: focused = %d, want 0", tui.focused)
	}
}

func TestCalcRegionsUsesVisibleCount(t *testing.T) {
	a := newTestPane("super", true)
	b := newTestPane("eng1", false) // hidden
	c := newTestPane("eng2", true)

	tui := newTestTUI(a, b, c)
	tui.layout = LayoutGrid
	tui.gridCols = 2
	tui.gridRows = 2

	regions := tui.calcRegions(200, 100)

	// Should produce 2 regions (for 2 visible panes), not 3.
	if len(regions) != 2 {
		t.Errorf("calcRegions produced %d regions, want 2", len(regions))
	}
}

func TestHiddenPaneKeepsRegion(t *testing.T) {
	a := newTestPane("super", true)
	b := newTestPane("eng1", true)

	// Give b a region as if it were laid out.
	b.region = Region{X: 100, Y: 0, W: 100, H: 50}

	tui := newTestTUI(a, b)
	tui.layout = LayoutGrid
	tui.gridCols = 2
	tui.gridRows = 1

	// Hide b. Its region should not be overwritten by relayout.
	// (relayout needs a screen, so we test the principle: visiblePanes
	// won't include b, so the relayout loop won't touch it.)
	b.SetVisible(false)
	vis := tui.visiblePanes()

	for _, p := range vis {
		if p == b {
			t.Error("hidden pane b should not appear in visiblePanes")
		}
	}

	// b's region is untouched.
	if b.region.X != 100 || b.region.W != 100 {
		t.Errorf("hidden pane b's region was modified: %+v", b.region)
	}
}
