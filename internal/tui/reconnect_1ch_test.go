package tui

// Regression tests for ini-1ch: a viewer that attaches to a LIVE window 1 must
// hold the connection, and must not narrate its retries.
//
// The operator saw four stacked "[] window1 disconnected" notices against a
// window 1 that was alive and responding the whole time. Three separate defects
// produced that one screenshot, so they are pinned separately here -- a single
// test covering "it works now" would go green again the moment any one of them
// came back.

import (
	"testing"
	"time"
)

// TestWaitForDisconnect_EmptyPaneSetIsNotDeath is the primary defect, and it is
// a vacuous-truth bug: waitForDisconnect asked "are ALL panes dead?" over a
// slice that can legitimately be EMPTY, and an empty set satisfies "all" for
// free. A window with no agents assigned therefore declared a healthy peer dead
// on the first 2s tick, tore down the session, reconnected, and did it again --
// forever, against a server that never failed.
//
// The window itself was the evidence nobody had: window 1's log showed repeated
// "client disconnected" for a client that was never gone.
func TestWaitForDisconnect_EmptyPaneSetIsNotDeath(t *testing.T) {
	pm := &peerManager{quit: make(chan struct{})}
	pc := &peerConn{} // connected, zero agents assigned to this window

	done := make(chan struct{})
	go func() {
		pm.waitForDisconnect("window1", pc)
		close(done)
	}()

	// Well past the 2s liveness tick: if an empty set still reads as death,
	// this returns almost immediately.
	select {
	case <-done:
		t.Fatal("waitForDisconnect returned for a peer with NO panes. " +
			"\"every pane is dead\" is vacuously true over an empty set, so a window with no " +
			"agents assigned tears down a perfectly healthy connection and reconnects forever " +
			"(ini-1ch)")
	case <-time.After(5 * time.Second):
	}

	close(pm.quit)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("waitForDisconnect ignored quit; the peer goroutine would leak on shutdown")
	}
}

// TestWaitForDisconnect_AllPanesDeadStillEnds guards the other direction, so
// the fix for the empty case cannot be "never return", which would leave a
// genuinely dead peer connected forever and is the failure the vacuous check
// was presumably written to prevent.
func TestWaitForDisconnect_AllPanesDeadStillEnds(t *testing.T) {
	pm := &peerManager{quit: make(chan struct{})}
	dead := testPane("eng1")
	dead.mu.Lock()
	dead.alive = false
	dead.mu.Unlock()
	pc := &peerConn{panes: []PaneView{dead}}

	done := make(chan struct{})
	go func() {
		pm.waitForDisconnect("window1", pc)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("waitForDisconnect never returned though every pane was dead; a real peer " +
			"death must still end the session")
	}
}

// TestHandlePeerUpdate_NotifiesOnStateChangeOnly pins the notice discipline.
// The reconnect loop retries by design (ini-civ), so an event per ATTEMPT is an
// unbounded stack of identical notices describing one fact -- four of them on
// the operator's screen.
func TestHandlePeerUpdate_NotifiesOnStateChangeOnly(t *testing.T) {
	tui := newTestTUI()
	countNotices := func() int {
		n := 0
		for _, ev := range tui.eventLog {
			if ev.Type == EventPeerConnected || ev.Type == EventPeerDisconnected {
				n++
			}
		}
		return n
	}

	// Four failed attempts in a row: one state change, so one notice.
	for i := 0; i < 4; i++ {
		tui.handlePeerUpdate("window1", nil, false)
	}
	if got := countNotices(); got != 1 {
		t.Errorf("four consecutive failed attempts produced %d notices, want 1 -- the operator "+
			"saw a stack of four identical lines for a single underlying fact", got)
	}

	// A genuine transition to connected notifies once more.
	tui.handlePeerUpdate("window1", []PaneView{testPane("eng1")}, true)
	if got := countNotices(); got != 2 {
		t.Errorf("reconnecting produced %d total notices, want 2; a state CHANGE must still "+
			"be announced or the operator cannot tell recovery from a hung viewer", got)
	}
	// ...and holding that state does not keep announcing it.
	tui.handlePeerUpdate("window1", []PaneView{testPane("eng1")}, true)
	if got := countNotices(); got != 2 {
		t.Errorf("a sustained connection produced %d notices, want 2", got)
	}
}

// TestHandlePeerUpdate_ConnectedWithNoAgentsIsNotADisconnect pins the defect
// that made a WORKING attach announce itself as a failure. The old code read
// the peer's state off len(newPanes), so "connected, zero agents assigned to
// this window" -- a healthy and ordinary state -- rendered as "disconnected".
func TestHandlePeerUpdate_ConnectedWithNoAgentsIsNotADisconnect(t *testing.T) {
	tui := newTestTUI()

	tui.handlePeerUpdate("window1", nil, true)

	for _, ev := range tui.eventLog {
		if ev.Type == EventPeerDisconnected {
			t.Fatalf("a connected peer with no assigned agents was reported as disconnected: %q. "+
				"Connection state and agent count are independent facts; inferring one from the "+
				"other is what put a disconnect notice on a working viewer (ini-1ch)", ev.Detail)
		}
	}
}
