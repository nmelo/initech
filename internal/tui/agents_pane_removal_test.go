package tui

import "testing"

// didNotPanic runs f and reports whether it completed without panicking.
func didNotPanic(f func()) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	f()
	return true
}

// TestAgentsSearch_RenderSurvivesPaneRemoval is the ini-w7ym
// regression test: the agents modal is open with search active (filtered holds
// a snapshot of pane indices), then t.panes shrinks (concurrent initech remove
// / peer disconnect). The next render must NOT crash on a stale filtered index.
func TestAgentsSearch_RenderSurvivesPaneRemoval(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "qa1")
	tui.openAgentsModal()
	tui.agents.searching = true
	tui.agents.searchBuf = nil
	tui.agentsRefilter() // filtered = [0,1,2]

	// Concurrent removal shrinks t.panes; filtered still references index 1 and 2.
	tui.panes = tui.panes[:1]

	if !didNotPanic(func() { tui.render() }) {
		t.Fatal("render panicked on a stale filtered index after pane removal (ini-w7ym)")
	}
}

// TestAgentsReconcile_ClampsAfterShrink asserts the root fix: reconciling after
// a pane-set change leaves filtered/selected/scrollOffset valid against the
// current pane set (ini-w7ym / ini-t3ov shared root).
func TestAgentsReconcile_ClampsAfterShrink(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"), testPane("qa1"), testPane("super"))
	tui.agents.active = true
	tui.agents.searching = true
	tui.agents.searchBuf = nil
	tui.agentsRefilter() // filtered = [0,1,2,3]
	tui.agents.selected = 3
	tui.agents.scrollOffset = 3

	tui.panes = tui.panes[:2] // shrink to eng1, eng2
	tui.agentsReconcile()

	for _, idx := range tui.agents.filtered {
		if idx < 0 || idx >= len(tui.panes) {
			t.Errorf("filtered index %d out of range after reconcile (len panes=%d): %v", idx, len(tui.panes), tui.agents.filtered)
		}
	}
	if tui.agents.selected < 0 || tui.agents.selected >= len(tui.agents.filtered) {
		t.Errorf("selected=%d out of range after reconcile (filtered len=%d)", tui.agents.selected, len(tui.agents.filtered))
	}
}

// TestAgentsMoveUp_GuardsStaleSelectionWhileGrabbed is the ini-t3ov regression
// test: a grabbed row (moving=true) with a stale selected index >= len(t.panes)
// (after a concurrent removal) must not crash when moved up.
func TestAgentsMoveUp_GuardsStaleSelectionWhileGrabbed(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"))
	tui.agents.active = true
	tui.agents.moving = true
	tui.agents.selected = 5 // stale: >= len(t.panes)==2

	if !didNotPanic(func() { tui.agentsMoveUp() }) {
		t.Fatal("agentsMoveUp panicked on a stale selected index while grabbed (ini-t3ov)")
	}
	// Guard matches the siblings (early return on out-of-range selected); it must
	// not have swapped anything. Clamping of a stale index is the reconcile's job
	// (see TestAgentsReconcile_ClampsAfterShrink), not agentsMoveUp's.
	if tui.panes[0].Name() != "eng1" || tui.panes[1].Name() != "eng2" {
		t.Errorf("agentsMoveUp reordered panes despite an out-of-range selection: %s,%s", tui.panes[0].Name(), tui.panes[1].Name())
	}
}
