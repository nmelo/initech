package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// testPane is defined in layout_test.go (same package).

// ── addPane ──────────────────────────────────────────────────────────

func TestAddPane_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	dir := t.TempDir()
	wsDir := filepath.Join(dir, "eng3")
	os.MkdirAll(wsDir, 0755)

	tui := &TUI{
		panes:       toPaneViews([]*Pane{testPane("eng1"), testPane("eng2")}),
		layoutState: DefaultLayoutState([]string{"eng1", "eng2"}),
		agentEvents: make(chan AgentEvent, 8),
		quitCh:      make(chan struct{}),
		sockPath:    "/tmp/test.sock",
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			return PaneConfig{
				Name:    name,
				Dir:     filepath.Join(dir, name),
				Command: []string{"/bin/sh", "-c", "exit 0"},
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
	if tui.panes[2].Name() != "eng3" {
		t.Errorf("new pane name = %q, want eng3", tui.panes[2].Name())
	}
	// Clean up the PTY process.
	tui.panes[2].Close()
}

// TestAddPane_SetsGoroutines verifies that addPane sets safeGo and calls Start()
// so the new pane's PTY goroutines actually run. Before the ini-a1e.13 fix,
// Start() was never called and the pane was a frozen black screen.
func TestAddPane_SetsGoroutines(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "eng3"), 0755)

	tui := &TUI{
		panes:       toPaneViews([]*Pane{testPane("eng1")}),
		layoutState: DefaultLayoutState([]string{"eng1"}),
		agentEvents: make(chan AgentEvent, 8),
		quitCh:      make(chan struct{}),
		sockPath:    "/tmp/test.sock",
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			return PaneConfig{Name: name, Dir: filepath.Join(dir, name), Command: []string{"/bin/sh", "-c", "exit 0"}}, nil
		},
	}

	if err := tui.addPane("eng3"); err != nil {
		t.Fatalf("addPane: %v", err)
	}
	p := tui.panes[len(tui.panes)-1]
	t.Cleanup(func() { p.Close() })

	if p.(*Pane).safeGo == nil {
		t.Error("addPane did not set safeGo on new pane")
	}
	if !p.IsAlive() {
		t.Error("new pane is not alive — Start() may not have been called")
	}
}

func TestAddPane_AlreadyExists(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("eng1")}),
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			return PaneConfig{Name: name, Dir: "/tmp", Command: []string{"/bin/sh", "-c", "exit 0"}}, nil
		},
	}

	err := tui.addPane("eng1")
	if err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}
}

func TestAddPane_NoBuilder(t *testing.T) {
	tui := &TUI{
		panes:             toPaneViews([]*Pane{testPane("eng1")}),
		paneConfigBuilder: nil,
	}

	err := tui.addPane("eng2")
	if err == nil {
		t.Fatal("expected error when builder is nil, got nil")
	}
}

func TestAddPane_MissingWorkspace(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("eng1")}),
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

func TestAddPane_InvalidNames(t *testing.T) {
	tui := &TUI{}
	invalid := []string{
		"../escape",
		"has/slash",
		"has space",
		"null\x00byte",
		"ctrl\x01char",
		"back\\slash",
		"semi;colon",
		"pipe|char",
		"$dollar",
		"eng1:remote",
	}
	for _, name := range invalid {
		if err := tui.addPane(name); err == nil {
			t.Errorf("expected error for invalid name %q, got nil", name)
		}
	}
}

func TestAddPane_ValidNames(t *testing.T) {
	// These should pass name validation (will fail later on missing workspace, etc.)
	tui := &TUI{}
	valid := []string{"eng1", "qa-1", "super_2", "Agent-X", "a"}
	for _, name := range valid {
		err := tui.addPane(name)
		// Should NOT fail on name validation (will fail for other reasons like no builder).
		if err != nil && err.Error() == fmt.Sprintf("invalid agent name %q: must contain only letters, digits, hyphens, or underscores", name) {
			t.Errorf("valid name %q rejected by validation", name)
		}
	}
}

func TestAddPane_NameTooLong(t *testing.T) {
	tui := &TUI{}
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	err := tui.addPane(string(long))
	if err == nil {
		t.Fatal("expected error for overlength name")
	}
}

// ── removePane ───────────────────────────────────────────────────────

func TestRemovePane_Success(t *testing.T) {
	tui := &TUI{
		panes:       toPaneViews([]*Pane{testPane("eng1"), testPane("eng2"), testPane("eng3")}),
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
		if p.Name() == "eng2" {
			t.Error("eng2 still in panes after removal")
		}
	}
}

func TestRemovePane_NotFound(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("eng1")}),
	}

	err := tui.removePane("eng99")
	if err == nil {
		t.Fatal("expected error for nonexistent pane, got nil")
	}
}

func TestRemovePane_LastPane(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("eng1")}),
	}

	err := tui.removePane("eng1")
	if err == nil {
		t.Fatal("expected error when removing last pane, got nil")
	}
}

func TestRemovePane_CleansHidden(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("eng1"), testPane("eng2")}),
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
		panes: toPaneViews([]*Pane{testPane("eng1"), testPane("eng2")}),
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
	tui := &TUI{panes: toPaneViews([]*Pane{testPane("eng1")})}
	err := tui.removePane("")
	if err == nil {
		t.Fatal("expected error for empty name, got nil")
	}
}

// ── recalcGrid ───────────────────────────────────────────────────────

func TestRecalcGrid(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("a"), testPane("b"), testPane("c")}),
		layoutState: LayoutState{
			GridCols: 1,
			GridRows: 1,
		},
	}

	tui.recalcGrid(true)

	// autoGrid(3) should give 2x2.
	if tui.layoutState.GridCols != 2 || tui.layoutState.GridRows != 2 {
		t.Errorf("grid = %dx%d, want 2x2", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}
	if tui.layoutState.Mode != LayoutGrid {
		t.Error("mode should be LayoutGrid after recalcGrid")
	}

	// recalcGrid(true) preserves LayoutLive (live mode manages its own slots).
	tui.layoutState.Mode = LayoutLive
	tui.recalcGrid(true)
	if tui.layoutState.Mode != LayoutLive {
		t.Error("recalcGrid(true) should preserve LayoutLive")
	}
}

func TestRecalcGrid_HiddenExcluded(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("a"), testPane("b"), testPane("c")}),
		layoutState: LayoutState{
			Hidden:   map[string]bool{"c": true},
			GridCols: 1,
			GridRows: 1,
		},
	}

	tui.recalcGrid(true)

	// Only 2 visible; autoGrid(2) = 2x1.
	if tui.layoutState.GridCols != 2 || tui.layoutState.GridRows != 1 {
		t.Errorf("grid = %dx%d, want 2x1", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}
}
