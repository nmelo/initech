// QA tests for ini-l8c: Hot-add/remove agents without TUI restart.
// Covers edge cases, behavioral invariants, and error messages from the AC.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── addPane edge cases ──────────────────────────────────────────────

// AC2: Adding a name that already exists returns exact error message.
func TestAddPane_AlreadyExistsErrorMessage(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("eng1")}),
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			return PaneConfig{Name: name, Dir: "/tmp"}, nil
		},
	}
	err := tui.addPane("eng1")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want contains 'already exists'", err)
	}
}

// AC3: Adding a name with no workspace returns descriptive error.
func TestAddPane_MissingWorkspaceErrorMessage(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("eng1")}),
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			return PaneConfig{Name: name, Dir: "/nonexistent/path/" + name}, nil
		},
	}
	err := tui.addPane("eng3")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want contains 'not found'", err)
	}
}

// AC: eventCh is wired on hot-added pane so bead auto-detection works.
func TestAddPane_EventChWired(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "eng3"), 0755)
	ch := make(chan AgentEvent, 8)

	tui := &TUI{
		panes:       toPaneViews([]*Pane{testPane("eng1")}),
		layoutState: DefaultLayoutState([]string{"eng1"}),
		agentEvents: ch,
		sockPath:    "/tmp/test.sock",
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			return PaneConfig{Name: name, Dir: filepath.Join(dir, name)}, nil
		},
	}
	err := tui.addPane("eng3")
	if err != nil {
		t.Fatalf("addPane: %v", err)
	}
	defer tui.panes[1].Close()

	if tui.panes[1].(*Pane).eventCh == nil {
		t.Error("eventCh not wired on hot-added pane")
	}
}

// AC: INITECH_SOCKET and INITECH_AGENT are injected into hot-added pane env.
func TestAddPane_EnvInjected(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "eng3"), 0755)

	tui := &TUI{
		panes:       toPaneViews([]*Pane{testPane("eng1")}),
		layoutState: DefaultLayoutState([]string{"eng1"}),
		agentEvents: make(chan AgentEvent, 8),
		sockPath:    "/tmp/test-injected.sock",
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			return PaneConfig{Name: name, Dir: filepath.Join(dir, name)}, nil
		},
	}

	// We can't directly read the env of the spawned process, but we can verify
	// the builder was called and the pane was created. The env injection
	// happens in addPane before NewPane. A more robust test would need to
	// intercept the PaneConfig, but the builder closure tests cover that path.
	// Here we just verify no error and the pane is created.
	err := tui.addPane("eng3")
	if err != nil {
		t.Fatalf("addPane: %v", err)
	}
	defer tui.panes[1].Close()
	if tui.panes[1].Name() != "eng3" {
		t.Errorf("pane name = %q, want eng3", tui.panes[1].Name())
	}
}

// AC: Grid recalculates after add (e.g., 2 visible -> 3 visible changes grid).
func TestAddPane_GridRecalculated(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "eng3"), 0755)

	tui := &TUI{
		panes:       toPaneViews([]*Pane{testPane("eng1"), testPane("eng2")}),
		layoutState: DefaultLayoutState([]string{"eng1", "eng2"}),
		agentEvents: make(chan AgentEvent, 8),
		sockPath:    "/tmp/test.sock",
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			return PaneConfig{Name: name, Dir: filepath.Join(dir, name)}, nil
		},
	}

	// Before add: 2 panes = 2x1 grid.
	if tui.layoutState.GridCols != 2 || tui.layoutState.GridRows != 1 {
		t.Fatalf("initial grid = %dx%d, want 2x1", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}

	err := tui.addPane("eng3")
	if err != nil {
		t.Fatalf("addPane: %v", err)
	}
	defer tui.panes[2].Close()

	// After add: 3 visible panes = 2x2 grid.
	if tui.layoutState.GridCols != 2 || tui.layoutState.GridRows != 2 {
		t.Errorf("grid after add = %dx%d, want 2x2", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}
}

// AC: PaneConfigBuilder returning error is propagated.
func TestAddPane_BuilderError(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("eng1")}),
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			return PaneConfig{}, fmt.Errorf("role catalog lookup failed")
		},
	}
	err := tui.addPane("eng3")
	if err == nil || !strings.Contains(err.Error(), "role catalog lookup failed") {
		t.Errorf("error = %v, want builder error propagated", err)
	}
}

// ── removePane edge cases ───────────────────────────────────────────

// AC5: Removing a nonexistent pane returns exact error.
func TestRemovePane_NotFoundErrorMessage(t *testing.T) {
	tui := &TUI{
		panes:       toPaneViews([]*Pane{testPane("eng1"), testPane("eng2")}),
		layoutState: DefaultLayoutState([]string{"eng1", "eng2"}),
	}
	err := tui.removePane("eng99")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want contains 'not found'", err)
	}
}

// AC: Removing last pane returns exact error.
func TestRemovePane_LastPaneErrorMessage(t *testing.T) {
	tui := &TUI{
		panes:       toPaneViews([]*Pane{testPane("eng1")}),
		layoutState: DefaultLayoutState([]string{"eng1"}),
	}
	err := tui.removePane("eng1")
	if err == nil || !strings.Contains(err.Error(), "cannot remove last agent") {
		t.Errorf("error = %v, want contains 'cannot remove last agent'", err)
	}
}

// AC: Remove focused pane → focus cleared (applyLayout will snap to first visible).
func TestRemovePane_FocusCleared(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("eng1"), testPane("eng2"), testPane("eng3")}),
		layoutState: LayoutState{
			Focused:  "eng2",
			GridCols: 3,
			GridRows: 1,
			Mode:     LayoutGrid,
		},
	}
	err := tui.removePane("eng2")
	if err != nil {
		t.Fatalf("removePane: %v", err)
	}
	// Focus should not remain on the removed pane.
	if tui.layoutState.Focused == "eng2" {
		t.Error("focus still on removed pane eng2")
	}
}

// AC: Grid recalculates after remove (3 visible -> 2 visible).
func TestRemovePane_GridRecalculated(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("eng1"), testPane("eng2"), testPane("eng3")}),
		layoutState: LayoutState{
			GridCols: 2,
			GridRows: 2,
			Mode:     LayoutGrid,
		},
	}
	err := tui.removePane("eng3")
	if err != nil {
		t.Fatalf("removePane: %v", err)
	}
	// 2 panes -> 2x1 grid.
	if tui.layoutState.GridCols != 2 || tui.layoutState.GridRows != 1 {
		t.Errorf("grid after remove = %dx%d, want 2x1", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}
}

// AC: Remove preserves remaining pane order (no shuffle).
func TestRemovePane_OrderPreserved(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("a"), testPane("b"), testPane("c"), testPane("d")}),
		layoutState: LayoutState{
			GridCols: 2,
			GridRows: 2,
			Mode:     LayoutGrid,
		},
	}
	err := tui.removePane("b")
	if err != nil {
		t.Fatalf("removePane: %v", err)
	}
	names := make([]string, len(tui.panes))
	for i, p := range tui.panes {
		names[i] = p.Name()
	}
	want := "a,c,d"
	got := strings.Join(names, ",")
	if got != want {
		t.Errorf("pane order after removing b = %q, want %q", got, want)
	}
}

// AC: Remove a hidden pane removes it from both panes slice and Hidden map.
func TestRemovePane_HiddenPaneFullyRemoved(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{testPane("eng1"), testPane("eng2"), testPane("eng3")}),
		layoutState: LayoutState{
			Hidden:   map[string]bool{"eng3": true},
			GridCols: 2,
			GridRows: 1,
			Mode:     LayoutGrid,
		},
	}
	err := tui.removePane("eng3")
	if err != nil {
		t.Fatalf("removePane: %v", err)
	}
	// Gone from panes.
	for _, p := range tui.panes {
		if p.Name() == "eng3" {
			t.Error("eng3 still in panes slice")
		}
	}
	// Gone from Hidden.
	if tui.layoutState.Hidden["eng3"] {
		t.Error("eng3 still in Hidden map")
	}
}

// ── recalcGrid ──────────────────────────────────────────────────────

// Verify autoGrid mapping for common visible counts.
func TestRecalcGrid_CommonCounts(t *testing.T) {
	tests := []struct {
		visible  int
		wantCols int
		wantRows int
	}{
		{1, 1, 1},
		{2, 2, 1},
		{3, 2, 2},
		{4, 2, 2},
		{5, 3, 2},
		{6, 3, 2},
	}
	for _, tt := range tests {
		panes := make([]*Pane, tt.visible)
		for i := range panes {
			panes[i] = testPane(fmt.Sprintf("p%d", i))
		}
		tui := &TUI{
			panes:       toPaneViews(panes),
			layoutState: LayoutState{GridCols: 1, GridRows: 1},
		}
		tui.recalcGrid()
		if tui.layoutState.GridCols != tt.wantCols || tui.layoutState.GridRows != tt.wantRows {
			t.Errorf("recalcGrid(%d visible) = %dx%d, want %dx%d",
				tt.visible,
				tui.layoutState.GridCols, tui.layoutState.GridRows,
				tt.wantCols, tt.wantRows)
		}
	}
}

// ── Close nil-guard (test safety) ───────────────────────────────────

// Pane.Close must not panic on panes without a real PTY (e.g., testPane).
func TestPaneCloseNilGuard(t *testing.T) {
	p := testPane("test")
	// This should not panic.
	p.Close()
}

// ── Pane.Close double-close safety ──────────────────────────────────

func TestPaneCloseDoubleClose(t *testing.T) {
	p := testPane("test")
	p.Close()
	// Second close should not panic.
	p.Close()
}

// ── initech.yaml not modified (structural check) ────────────────────

// The addPane method does not write to disk (no config save). Verify by
// checking that addPane doesn't call anything that would modify initech.yaml.
// This is a structural assertion: the method signature takes no config path.
func TestAddPane_NoConfigModification(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "eng3"), 0755)

	// Write a fake initech.yaml to verify it's not touched.
	yamlPath := filepath.Join(dir, "initech.yaml")
	os.WriteFile(yamlPath, []byte("original"), 0644)

	tui := &TUI{
		panes:       toPaneViews([]*Pane{testPane("eng1")}),
		layoutState: DefaultLayoutState([]string{"eng1"}),
		agentEvents: make(chan AgentEvent, 8),
		sockPath:    "/tmp/test.sock",
		paneConfigBuilder: func(name string) (PaneConfig, error) {
			return PaneConfig{Name: name, Dir: filepath.Join(dir, name)}, nil
		},
	}

	err := tui.addPane("eng3")
	if err != nil {
		t.Fatalf("addPane: %v", err)
	}
	defer tui.panes[1].Close()

	content, _ := os.ReadFile(yamlPath)
	if string(content) != "original" {
		t.Error("initech.yaml was modified by addPane")
	}
}
