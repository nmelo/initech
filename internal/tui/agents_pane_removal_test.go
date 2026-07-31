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

// TestAgentsSearch_RenderSurvivesPaneRemoval is the ini-w7ym regression test,
// re-targeted at the grid modal (ini-2rc): the agents modal is open with
// search active, then t.panes shrinks (concurrent initech remove / peer
// disconnect) leaving t.agents.selected pointing past the end. The next
// render must not crash. The grid computes its cell list fresh from the
// CURRENT t.panes every render (no cached filtered snapshot to go stale --
// see agents_grid.go), so this exercises the remaining stale-reference
// surface: the persisted selected index itself.
func TestAgentsSearch_RenderSurvivesPaneRemoval(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "qa1")
	tui.openAgentsModal()
	tui.agents.searching = true
	tui.agents.searchBuf = nil
	tui.agents.selected = 2 // last valid index before the shrink

	// Concurrent removal shrinks t.panes; selected now points past the end.
	tui.panes = tui.panes[:1]

	if !didNotPanic(func() { tui.render() }) {
		t.Fatal("render panicked on a stale selected index after pane removal (ini-w7ym)")
	}
}

// TestAgentsReconcile_ClampsAfterShrink asserts the root fix: reconciling
// after a pane-set change leaves selected valid against the current pane set
// (ini-w7ym / ini-t3ov shared root).
func TestAgentsReconcile_ClampsAfterShrink(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"), testPane("qa1"), testPane("super"))
	tui.agents.active = true
	tui.agents.selected = 3

	tui.panes = tui.panes[:2] // shrink to eng1, eng2
	tui.agentsReconcile()

	if tui.agents.selected < 0 || tui.agents.selected >= len(tui.panes) {
		t.Errorf("selected=%d out of range after reconcile (len panes=%d)", tui.agents.selected, len(tui.panes))
	}
}

// TestAgentsMoveH_GuardsStaleSelectionWhileGrabbed is the ini-t3ov regression
// test, re-targeted at agentsMoveH (the grid's within-band move/grab,
// replacing the flat modal's agentsMoveUp/Down): a grabbed cell with a stale
// selected index >= len(t.panes) (after a concurrent removal) must not crash.
func TestAgentsMoveH_GuardsStaleSelectionWhileGrabbed(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"))
	tui.agents.active = true
	tui.agents.moving = true
	tui.agents.selected = 5 // stale: >= len(t.panes)==2

	if !didNotPanic(func() { tui.agentsMoveH(1) }) {
		t.Fatal("agentsMoveH panicked on a stale selected index while grabbed (ini-t3ov)")
	}
	// Guard matches the sibling actions (early return on out-of-range
	// selected); it must not have swapped anything. Clamping of a stale
	// index is the reconcile's job, not agentsMoveH's.
	if tui.panes[0].Name() != "eng1" || tui.panes[1].Name() != "eng2" {
		t.Errorf("agentsMoveH reordered panes despite an out-of-range selection: %s,%s", tui.panes[0].Name(), tui.panes[1].Name())
	}
}

// TestAgentsMoveV_GuardsStaleSelectionWhileGrabbed is the same regression
// for the grid's cross-band vertical move.
func TestAgentsMoveV_GuardsStaleSelectionWhileGrabbed(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("qa1"))
	tui.agents.active = true
	tui.agents.moving = true
	tui.agents.selected = 5 // stale: >= len(t.panes)==2

	if !didNotPanic(func() { tui.agentsMoveV(nil, 1) }) {
		t.Fatal("agentsMoveV panicked on a stale selected index while grabbed (ini-t3ov)")
	}
	if tui.panes[0].Name() != "eng1" || tui.panes[1].Name() != "qa1" {
		t.Errorf("agentsMoveV reordered panes despite an out-of-range selection: %s,%s", tui.panes[0].Name(), tui.panes[1].Name())
	}
}
