package tui

// Regression tests for the ini-6m4 crash chain: a viewer that plans zero of
// its own agents, and an emulator panic that killed the window.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHandlePeerUpdate_NewRemotePanesAreGroupedBeforeLayout pins the ordering
// defect behind the operator's crash loop. visiblePanesForWindow resolves each
// pane's window through GroupOf; the panes a peer update just added have no
// entry until ensureGroups runs, and an unknown pane defaults to window 1. So
// a secondary window filtered out its OWN AGENTS: the operator's log showed
// 'total_panes=8' followed by 'plan_panes=0', his window planned nothing, no
// emulator was ever resized, and 53-row Claude replay into 24-row buffers
// crashed the process (ini-w6z).
func TestHandlePeerUpdate_NewRemotePanesAreGroupedBeforeLayout(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	os.WriteFile(filepath.Join(root, ".initech", "assignments.yaml"),
		[]byte("group_window:\n    eng: window-2\n"), 0o644)
	a, err := LoadAssignment(root)
	if err != nil {
		t.Fatal(err)
	}

	tui := newTestTUI()
	tui.projectRoot = root
	tui.windowID = "window-2"
	tui.assignment = a

	names := []string{"super", "pmm", "shipper", "growth", "pm", "qa1", "eng1", "eng2"}
	panes := make([]PaneView, len(names))
	for i, n := range names {
		panes[i] = &RemotePane{name: n, host: "window1", alive: true}
	}

	tui.handlePeerUpdate("window1", panes, true)

	vis := tui.visiblePanesForWindow()
	if len(vis) != 2 {
		got := make([]string, len(vis))
		for i, p := range vis {
			got[i] = p.Name()
		}
		t.Fatalf("viewer plans %d pane(s) %v immediately after the peer update, want 2 (eng1, eng2). "+
			"Ungrouped panes default to window 1, so the viewer filters out its own agents until "+
			"something else happens to run ensureGroups -- and with zero planned panes no emulator "+
			"is resized, which is what armed the replay crash", len(vis), got)
	}
	for i, want := range []string{"eng1", "eng2"} {
		if vis[i].Name() != want {
			t.Errorf("planned pane %d = %q, want %q", i, vis[i].Name(), want)
		}
	}
}

// TestDrainData_EmulatorPanicCostsRenderingNotTheWindow pins the containment:
// a parser bug in the emulator must be absorbed per-pane. Before this, an
// out-of-range scroll operation panicked the viewer process, which relaunched
// and crashed again every few seconds (the operator's 'window 2 disconnects').
//
// The pane must stay ALIVE: waitForDisconnect reads all-panes-dead as a lost
// peer, so containment that marked panes dead would tear down a healthy
// session and rebuild the crash loop as a reconnect loop (the ini-1ch shape).
func TestDrainData_EmulatorPanicCostsRenderingNotTheWindow(t *testing.T) {
	rp := &RemotePane{name: "eng1", host: "window1", alive: true,
		dataCh: make(chan []byte, 4)}
	// A nil emulator makes every write panic -- standing in for the fork's
	// out-of-range indexing without needing its exact byte sequence.
	rp.dataCh <- []byte("\x1b[4S after a 53-row scroll region")

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("the emulator panic escaped DrainData and would have killed the window: %v", r)
			}
		}()
		rp.DrainData()
	}()

	if !rp.IsAlive() {
		t.Fatal("the pane was marked dead by containment; an all-dead pane set tears down the " +
			"session, so this trades the crash loop for a reconnect loop")
	}
	if !rp.emuPanicked {
		t.Fatal("emuPanicked not set; the panic barrier did not actually engage, so this test " +
			"proved nothing (the write must have panicked for the containment to be exercised)")
	}
	// Later chunks keep flowing (and keep being contained).
	rp.dataCh <- []byte("more bytes")
	rp.DrainData()
}

// TestModalNumbering_FollowsTheFleetNotTheArrangement pins the numbering
// contract (ini-6m4): the displayed number is the agent's position in the
// FLEET's canonical order (window 1's creation order, served to viewers via
// hello_ok), not its position in this window's t.panes -- which each window
// reorders from its own saved layout, giving two windows two numberings for
// the same fleet and making grab-by-number act on different agents per window.
func TestModalNumbering_FollowsTheFleetNotTheArrangement(t *testing.T) {
	a := testPane("alpha")
	b := testPane("beta")
	c := testPane("gamma")
	for i, p := range []*Pane{a, b, c} {
		p.SetFleetIdx(i) // canonical: alpha=0 beta=1 gamma=2
	}
	tui := newTestTUI(a, b, c)
	// This window's own arrangement reverses them.
	reorderPanes(tui.panes, []string{"gamma", "beta", "alpha"})

	for localIdx, want := range map[int]int{0: 2, 1: 1, 2: 0} {
		if got := fleetIdxOf(tui.panes[localIdx], localIdx); got != want {
			t.Errorf("pane at local position %d numbers as %d, want the fleet index %d -- "+
				"numbering by local position is per-window state leaking into a global identifier",
				localIdx, got+1, want+1)
		}
	}
}

// TestFleetIdxOf_UnstampedPaneFallsBackToLocalIndex pins the zero-value
// contract: panes built as struct literals (tests, fakes, old peers) carry no
// stamp, and must number by local position rather than all reading as "fleet
// pane 1" -- the first draft stored the index directly and did exactly that.
func TestFleetIdxOf_UnstampedPaneFallsBackToLocalIndex(t *testing.T) {
	p := testPane("unstamped") // struct literal: fleetNum zero value
	if got := fleetIdxOf(p, 4); got != 4 {
		t.Errorf("unstamped pane numbers as %d, want the local fallback 4", got+1)
	}
	rp := &RemotePane{name: "old-peer"}
	if got := fleetIdxOf(rp, 6); got != 6 {
		t.Errorf("unstamped remote pane numbers as %d, want the local fallback 6", got+1)
	}
}

// TestStampFleetThenApplyOrder_StampsCreationOrderNotArrangement pins the
// sequence itself: the canonical number comes from creation position, and the
// saved arrangement is applied only afterwards. Stamping after the reorder
// passes every other test in this file (they stamp manually) while recreating
// the two-windows-two-numberings bug for any window with a saved layout --
// which is exactly how the first mutation run caught this property untested.
func TestStampFleetThenApplyOrder_StampsCreationOrderNotArrangement(t *testing.T) {
	a, b, c := testPane("alpha"), testPane("beta"), testPane("gamma")
	panes := []PaneView{a, b, c}

	stampFleetThenApplyOrder(panes, []string{"gamma", "beta", "alpha"})

	// The arrangement applied...
	if panes[0].Name() != "gamma" {
		t.Fatalf("saved order not applied: panes[0] = %q", panes[0].Name())
	}
	// ...and the numbers ignore it.
	for want, p := range map[int]*Pane{0: a, 1: b, 2: c} {
		if got := p.FleetIdx(); got != want {
			t.Errorf("%s fleet number = %d, want %d (creation position); the stamp ran after "+
				"the reorder and baked this window's arrangement into the canonical numbers",
				p.Name(), got+1, want+1)
		}
	}
}
