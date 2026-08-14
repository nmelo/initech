// Regression tests for ini-6gjg: the top modal renders from a ~2s cache
// (t.top.data) while its destructive actions ('r' restart, 'k' kill) indexed
// the live t.panes. Inside that window a pane removal makes the highlighted
// row and the action target diverge, so the operator kills an agent they never
// selected. These tests pin the contract: an action resolves to the agent on
// the highlighted row, or it is refused — never redirected to a neighbour.
package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// topStaleTUI builds a TUI with the top modal open and a cache snapshot that
// matches the pane set at build time, so a test can shrink t.panes afterwards
// to reproduce the stale-cache window. Panes carry a trivial launch command:
// if a regression ever lets the restart path run when it should not, the spawn
// is a process that exits immediately rather than a login shell that lingers.
func topStaleTUI(names ...string) (*TUI, map[string]*Pane) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 30)

	panes := make([]*Pane, len(names))
	byName := make(map[string]*Pane, len(names))
	for i, n := range names {
		p := testPane(n)
		p.cfg.Command = []string{"true"}
		panes[i] = p
		byName[n] = p
	}

	tui := newTestTUI(panes...)
	tui.screen = s
	tui.lastW, tui.lastH = 120, 30
	tui.top.active = true
	tui.refreshTopData() // snapshot matching the pane set the operator sees
	return tui, byName
}

// TestTopEnsureVisible_KeepsScrollOffsetNonNegative pins a bound that only
// started mattering once renderTop began keeping the highlight on screen.
// selected == -1 is the modal's "nothing highlighted" state, and scrolling to
// meet it drove scrollOffset negative, which indexes the row slice at -1.
func TestTopEnsureVisible_KeepsScrollOffsetNonNegative(t *testing.T) {
	tui, _ := topStaleTUI("eng1", "eng2")
	tui.top.selected = -1 // nothing highlighted

	tui.topEnsureVisible()

	if tui.top.scrollOffset < 0 {
		t.Errorf("scrollOffset = %d, want >= 0 (a negative offset indexes t.top.data out of range)", tui.top.scrollOffset)
	}
}

// TestTopKill_TargetsHighlightedAgentAfterRemoval is the ini-6gjg regression
// test. Four agents are shown, the highlight is on eng3, then eng1 is removed
// (another agent runs `initech remove`, or a peer disconnects). The rendered
// rows still show all four for the rest of the cache window, so 'k' must kill
// eng3 — the agent under the highlight. Indexing the live slice at the same
// position kills eng4 instead, destroying the wrong agent's in-flight work.
func TestTopKill_TargetsHighlightedAgentAfterRemoval(t *testing.T) {
	tui, panes := topStaleTUI("eng1", "eng2", "eng3", "eng4")
	tui.top.selected = 2 // the drawn highlight is on eng3

	// Concurrent removal of eng1: the live slice shrinks, the rendered
	// snapshot does not (its cache has up to 2s left to live).
	tui.panes = tui.panes[1:]

	tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone))

	if !panes["eng4"].IsAlive() {
		t.Error("k killed eng4 while the operator's highlight was on eng3: wrong-target kill (ini-6gjg)")
	}
	if panes["eng3"].IsAlive() {
		t.Error("k did not kill eng3, the agent on the highlighted row")
	}
	if !panes["eng2"].IsAlive() {
		t.Error("k killed eng2, which was neither highlighted nor removed")
	}
}

// TestTopKill_RefusesWhenHighlightedAgentRemoved covers the case where the
// highlighted agent itself is the one that disappeared. There is no correct
// target left, so the kill must be refused with feedback — silently killing
// whoever inherited the index is the exact failure ini-6gjg describes.
func TestTopKill_RefusesWhenHighlightedAgentRemoved(t *testing.T) {
	tui, panes := topStaleTUI("eng1", "eng2", "eng3")
	tui.top.selected = 0 // the drawn highlight is on eng1

	tui.panes = tui.panes[1:] // eng1 removed; the snapshot still shows it

	tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone))

	for _, name := range []string{"eng2", "eng3"} {
		if !panes[name].IsAlive() {
			t.Errorf("k killed %s after the highlighted agent (eng1) was removed: a destructive action must be refused, not redirected", name)
		}
	}
	if tui.cmd.error == "" {
		t.Error("refused kill left the operator no feedback (cmd.error is empty)")
	}
}

// TestTopRestart_RefusesWhenHighlightedAgentRemoved is the restart half of the
// same contract: restarting an agent destroys its session, so 'r' must not
// fall through to whichever pane inherited the highlighted index.
func TestTopRestart_RefusesWhenHighlightedAgentRemoved(t *testing.T) {
	tui, panes := topStaleTUI("eng1", "eng2", "eng3")
	tui.top.selected = 0 // the drawn highlight is on eng1

	tui.panes = tui.panes[1:] // eng1 removed; the snapshot still shows it

	tui.handleTopKey(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone))

	for _, name := range []string{"eng2", "eng3"} {
		if !panes[name].IsAlive() {
			t.Errorf("r restarted %s after the highlighted agent (eng1) was removed: a destructive action must be refused, not redirected", name)
		}
	}
	if p, ok := tui.panes[0].(*Pane); !ok || p != panes["eng2"] {
		t.Error("r replaced a live pane after the highlighted agent was removed")
	}
	if tui.cmd.error == "" {
		t.Error("refused restart left the operator no feedback (cmd.error is empty)")
	}
}

// TestTopResolveSelected_ResolvesHighlightedRowAfterRemoval covers the
// targeting that both destructive actions share. Restart is asserted here
// rather than by pressing 'r' end-to-end because a *successful* restart calls
// NewPane, which allocates a real PTY and spawns a process; the kill test
// above carries the end-to-end proof for the shared mechanism.
func TestTopResolveSelected_ResolvesHighlightedRowAfterRemoval(t *testing.T) {
	tui, _ := topStaleTUI("eng1", "eng2", "eng3", "eng4")
	tui.top.selected = 2 // eng3 is highlighted

	tui.panes = tui.panes[1:] // eng1 removed: live index 2 now holds eng4

	idx, ok := tui.topResolveSelected()
	if !ok {
		t.Fatal("resolve refused a highlighted agent that is still running")
	}
	if got := tui.panes[idx].Name(); got != "eng3" {
		t.Errorf("resolved to %s, want eng3 (the highlighted row); live index 2 holds %s", got, tui.panes[2].Name())
	}
}

// TestTopResolveSelected_DistinguishesSameNameAcrossHosts pins that identity
// is the host-qualified paneKey and not the display name: two peers can each
// run an agent called "eng1", and acting on one must never hit the other.
func TestTopResolveSelected_DistinguishesSameNameAcrossHosts(t *testing.T) {
	local := testPane("eng1")
	remote := newFakeRemotePaneView("eng1", "spark1")
	tui := &TUI{
		panes:       []PaneView{local, remote},
		layoutState: DefaultLayoutState([]string{"eng1", "spark1:eng1"}),
	}
	tui.top.active = true
	tui.refreshTopData()

	tui.top.selected = 1 // the remote eng1 row
	if idx, ok := tui.topResolveSelected(); !ok {
		t.Fatal("resolve refused the remote eng1 row")
	} else if agentKey(tui.panes[idx]) != "spark1:eng1" {
		t.Errorf("resolved to %s, want spark1:eng1", agentKey(tui.panes[idx]))
	}

	// Drop the local eng1. The rendered rows still list both, and the remote
	// eng1 has slid to live index 0 — matching on name alone would now be
	// ambiguous, matching on paneKey is not.
	tui.panes = tui.panes[1:]
	if idx, ok := tui.topResolveSelected(); !ok {
		t.Fatal("resolve refused the remote eng1 after the local eng1 was removed")
	} else if agentKey(tui.panes[idx]) != "spark1:eng1" {
		t.Errorf("resolved to %s, want spark1:eng1", agentKey(tui.panes[idx]))
	}
}

// TestRefreshTopData_ReanchorsHighlightToSameAgent asserts the highlight
// follows the agent rather than the position: once the rows are rebuilt after
// eng1 is removed, the operator is still pointed at eng3 and not at whoever
// slid into row 2.
func TestRefreshTopData_ReanchorsHighlightToSameAgent(t *testing.T) {
	tui, _ := topStaleTUI("eng1", "eng2", "eng3", "eng4")
	tui.top.selected = 2 // eng3

	tui.panes = tui.panes[1:]
	tui.topReconcile()   // the pane-set change invalidates the rows
	tui.refreshTopData() // next render rebuilds from live state

	if got := tui.top.data[tui.top.selected].Name; got != "eng3" {
		t.Errorf("highlight landed on %s after the rebuild, want eng3", got)
	}
}

// TestTopReconcile_InvalidatesCachedRowsOnPaneSetChange asserts what the
// removal and peer-update paths call it for: the cached rows stop being
// trusted the moment the pane set changes, so the modal cannot keep drawing a
// removed agent for the rest of the cache window.
func TestTopReconcile_InvalidatesCachedRowsOnPaneSetChange(t *testing.T) {
	tui, _ := topStaleTUI("eng1", "eng2")
	if tui.top.cacheTime.IsZero() {
		t.Fatal("fixture did not populate the row cache")
	}

	tui.panes = tui.panes[1:]
	tui.topReconcile()

	if !tui.top.cacheTime.IsZero() {
		t.Error("topReconcile left the row cache valid; the modal would keep drawing the removed agent")
	}
	tui.refreshTopData()
	if len(tui.top.data) != 1 || tui.top.data[0].Name != "eng2" {
		t.Errorf("rows after reconcile+refresh = %+v, want just eng2", tui.top.data)
	}
}

// TestHandleTopKey_DownStopsAtLastRenderedRow pins navigation to the rendered
// rows rather than the live pane count. While a snapshot is stale the operator
// must still be able to reach the last row they can see — and no further.
func TestHandleTopKey_DownStopsAtLastRenderedRow(t *testing.T) {
	tui, _ := topStaleTUI("eng1", "eng2", "eng3")
	tui.panes = tui.panes[:1] // live slice shrank; three rows are still drawn
	tui.top.selected = 1

	tui.handleTopKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if tui.top.selected != 2 {
		t.Errorf("selected = %d, want 2: Down must reach the last drawn row", tui.top.selected)
	}
	tui.handleTopKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if tui.top.selected != 2 {
		t.Errorf("selected = %d, want 2: Down must stop at the last drawn row", tui.top.selected)
	}
}
