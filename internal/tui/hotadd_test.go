package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// testPane is defined in layout_test.go (same package).

// ── addPane ──────────────────────────────────────────────────────────

func TestAddPane_Success(t *testing.T) {
	dir := t.TempDir()
	wsDir := filepath.Join(dir, "eng3")
	os.MkdirAll(wsDir, 0755)

	tui := &TUI{
		panes:       []*Pane{testPane("eng1"), testPane("eng2")},
		layoutState: DefaultLayoutState([]string{"eng1", "eng2"}),
		agentEvents: make(chan AgentEvent, 8),
		sockPath:    "/tmp/test.sock",
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			return PaneConfig{
				Name: name,
				Dir:  filepath.Join(dir, name),
			}, nil
		},
	}

	err := tui.addPane("eng3")
	if err != nil {
		t.Fatalf("addPane returned error: %v", err)
	}
	if len(tui.panes) != 3 {
		t.Errorf("panes = %d, want 3", len(tui.panes))
	}
	if tui.panes[2].name != "eng3" {
		t.Errorf("new pane name = %q, want eng3", tui.panes[2].name)
	}
	// Clean up the PTY process.
	tui.panes[2].Close()
}

func TestAddPane_AlreadyExists(t *testing.T) {
	tui := &TUI{
		panes: []*Pane{testPane("eng1")},
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			return PaneConfig{Name: name, Dir: "/tmp"}, nil
		},
	}

	err := tui.addPane("eng1")
	if err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}
}

func TestAddPane_NoBuilder(t *testing.T) {
	tui := &TUI{
		panes:             []*Pane{testPane("eng1")},
		paneConfigBuilder: nil,
	}

	err := tui.addPane("eng2")
	if err == nil {
		t.Fatal("expected error when builder is nil, got nil")
	}
}

func TestAddPane_MissingWorkspace(t *testing.T) {
	tui := &TUI{
		panes: []*Pane{testPane("eng1")},
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			return PaneConfig{Name: name, Dir: "/nonexistent/path/" + name}, nil
		},
	}

	err := tui.addPane("eng3")
	if err == nil {
		t.Fatal("expected error for missing workspace, got nil")
	}
}

func TestAddPane_EmptyName(t *testing.T) {
	tui := &TUI{}
	err := tui.addPane("")
	if err == nil {
		t.Fatal("expected error for empty name, got nil")
	}
}

// ── removePane ───────────────────────────────────────────────────────

func TestRemovePane_Success(t *testing.T) {
	tui := &TUI{
		panes:       []*Pane{testPane("eng1"), testPane("eng2"), testPane("eng3")},
		layoutState: DefaultLayoutState([]string{"eng1", "eng2", "eng3"}),
	}

	err := tui.removePane("eng2")
	if err != nil {
		t.Fatalf("removePane returned error: %v", err)
	}
	if len(tui.panes) != 2 {
		t.Errorf("panes = %d, want 2", len(tui.panes))
	}
	for _, p := range tui.panes {
		if p.name == "eng2" {
			t.Error("eng2 still in panes after removal")
		}
	}
}

func TestRemovePane_NotFound(t *testing.T) {
	tui := &TUI{
		panes: []*Pane{testPane("eng1")},
	}

	err := tui.removePane("eng99")
	if err == nil {
		t.Fatal("expected error for nonexistent pane, got nil")
	}
}

func TestRemovePane_LastPane(t *testing.T) {
	tui := &TUI{
		panes: []*Pane{testPane("eng1")},
	}

	err := tui.removePane("eng1")
	if err == nil {
		t.Fatal("expected error when removing last pane, got nil")
	}
}

func TestRemovePane_CleansHidden(t *testing.T) {
	tui := &TUI{
		panes: []*Pane{testPane("eng1"), testPane("eng2")},
		layoutState: LayoutState{
			Hidden:   map[string]bool{"eng2": true},
			GridCols: 2,
			GridRows: 1,
			Mode:     LayoutGrid,
		},
	}

	err := tui.removePane("eng2")
	if err != nil {
		t.Fatalf("removePane returned error: %v", err)
	}
	if tui.layoutState.Hidden["eng2"] {
		t.Error("eng2 still in Hidden after removal")
	}
}

func TestRemovePane_FocusSnaps(t *testing.T) {
	tui := &TUI{
		panes: []*Pane{testPane("eng1"), testPane("eng2")},
		layoutState: LayoutState{
			Focused:  "eng2",
			GridCols: 2,
			GridRows: 1,
			Mode:     LayoutGrid,
		},
	}

	err := tui.removePane("eng2")
	if err != nil {
		t.Fatalf("removePane returned error: %v", err)
	}
	if tui.layoutState.Focused == "eng2" {
		t.Error("focus still on removed pane")
	}
}

func TestRemovePane_EmptyName(t *testing.T) {
	tui := &TUI{panes: []*Pane{testPane("eng1")}}
	err := tui.removePane("")
	if err == nil {
		t.Fatal("expected error for empty name, got nil")
	}
}

// ── recalcGrid ───────────────────────────────────────────────────────

func TestRecalcGrid(t *testing.T) {
	tui := &TUI{
		panes: []*Pane{testPane("a"), testPane("b"), testPane("c")},
		layoutState: LayoutState{
			GridCols: 1,
			GridRows: 1,
		},
	}

	tui.recalcGrid()

	// autoGrid(3) should give 2x2.
	if tui.layoutState.GridCols != 2 || tui.layoutState.GridRows != 2 {
		t.Errorf("grid = %dx%d, want 2x2", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}
	if tui.layoutState.Mode != LayoutGrid {
		t.Error("mode should be LayoutGrid after recalcGrid")
	}
}

func TestRecalcGrid_HiddenExcluded(t *testing.T) {
	tui := &TUI{
		panes: []*Pane{testPane("a"), testPane("b"), testPane("c")},
		layoutState: LayoutState{
			Hidden:   map[string]bool{"c": true},
			GridCols: 1,
			GridRows: 1,
		},
	}

	tui.recalcGrid()

	// Only 2 visible; autoGrid(2) = 2x1.
	if tui.layoutState.GridCols != 2 || tui.layoutState.GridRows != 1 {
		t.Errorf("grid = %dx%d, want 2x1", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}
}
