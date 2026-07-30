package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
)

// TestRenderNotBlockedByRemoteConnection verifies that the TUI renders its
// first frame within 2 seconds of startup, even when a remote peer is
// configured but unreachable. This is a regression test for the class of bug
// where remote connection logic blocks the main render loop.
func TestRenderNotBlockedByRemoteConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow render deadlock test in short mode")
	}
	if raceDetectorEnabled {
		// ini-ls0c/ini-adb9: same shape as TestRemotePane_MultiPane_RenderDoesNotBlock
		// and TestRemotePane_DAQueryDoesNotDeadlock's remote_pane_does_not_block
		// subtest -- this asserts tui.render() completes within a fixed 2s
		// wall-clock budget, and -race's own instrumentation overhead is a
		// large enough fraction of that budget to false-fail under a loaded
		// parallel run with nothing actually wrong. See
		// remoteRenderDeadlockBound's doc comment in remote_render_test.go
		// for why the bound stays tight instead of growing to compensate.
		//
		// CAVEAT: after this skip, this test's coverage survives under
		// `go test ./... -count=1` (no -short, no -race) -- not under
		// `go test -race ./internal/tui/` (what QA runs, and what surfaced
		// ini-adb9) and not under `make check`/`make test`. It ALSO
		// incidentally survives under `make integration` (ini-dtmh): this
		// test's name happens to match the `RenderNotBlocked` alternative
		// in that target's -run regex, which passes neither -race nor
		// -short, so it runs for real there too -- confirmed empirically,
		// a genuine 5.00s PASS, not a skip. Unlike the other three tests
		// skipped under raceDetectorEnabled (see remoteRenderDeadlockBound's
		// doc comment in remote_render_test.go), which do not match that
		// regex and survive ONLY under `go test ./... -count=1`, this one
		// has a second surviving location. Treat that as incidental, not
		// load-bearing: that -run regex has already swept in coverage it
		// wasn't meant to report once before (ini-1g0n) and a future edit to
		// it could just as easily stop matching this test's name. Either
		// way, this is a deliberate skip-under-race trade, not an oversight
		// -- don't read it as dead code and delete it.
		t.Skip("ini-ls0c/ini-adb9: -race overhead confounds the deadline; see remoteRenderDeadlockBound's doc comment")
	}
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)

	// Local pane (always works).
	localPane := newEmuPane("eng1", 120, 39)

	// Configure a remote peer pointing to an unreachable address.
	// Connection will time out, but the render loop must not wait for it.
	proj := &config.Project{
		Name:     "test",
		Root:     t.TempDir(),
		PeerName: "testhost",
		Remotes: map[string]config.Remote{
			"unreachable": {Addr: "192.0.2.1:9999"}, // RFC 5737 TEST-NET, guaranteed unroutable.
		},
	}

	quitCh := make(chan struct{})
	tui := &TUI{
		screen:      s,
		panes:       []PaneView{localPane},
		layoutState: DefaultLayoutState([]string{"eng1"}),
		lastW:       120,
		lastH:       40,
		project:     proj,
		quitCh:      quitCh,
		ipcCh:       make(chan ipcAction, 32),
		agentEvents: make(chan AgentEvent, 64),
	}
	tui.plan = computeLayout(tui.layoutState, tui.panes, 120, 39)

	// Start the peer manager in the background (same as Run() does).
	// This will attempt to connect to the unreachable remote.
	pm := newPeerManager(proj, func(peerName string, panes []PaneView) {
		tui.runOnMain(func() {
			tui.handlePeerUpdate(peerName, panes)
		})
	}, nil, quitCh)

	// Verify the TUI can render within 2 seconds (must not block on remote).
	rendered := make(chan struct{})
	go func() {
		tui.render()
		close(rendered)
	}()

	select {
	case <-rendered:
		// Success: render completed without blocking.
	case <-time.After(2 * time.Second):
		t.Fatal("render blocked for >2s, likely deadlocked on remote connection")
	}

	// Verify local pane is visible in the render output.
	// Scan all rows for the pane name "eng1" (ribbon, overlay, or status).
	sw, sh := s.Size()
	found := false
	for y := 0; y < sh; y++ {
		var line string
		for x := 0; x < sw; x++ {
			ch, _, _ := s.Get(x, y)
			line += ch
		}
		if containsStr(line, "eng1") {
			found = true
			break
		}
	}
	if !found {
		t.Error("local pane 'eng1' should be visible regardless of remote connection state")
	}

	// Cleanup.
	close(quitCh)
	pm.wait()
}

// TestRenderWithFailedRemoteShowsLocalPanes verifies that when a remote
// connection fails immediately (refused), local panes still render normally.
func TestRenderWithFailedRemoteShowsLocalPanes(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)

	p1 := newEmuPane("super", 60, 39)
	p2 := newEmuPane("eng1", 60, 39)

	tui := &TUI{
		screen:      s,
		panes:       []PaneView{p1, p2},
		layoutState: DefaultLayoutState([]string{"super", "eng1"}),
		lastW:       120,
		lastH:       40,
		quitCh:      make(chan struct{}),
		agentEvents: make(chan AgentEvent, 64),
	}
	tui.plan = computeLayout(tui.layoutState, tui.panes, 120, 39)

	// Simulate a peer update with nil panes (remote failed).
	tui.handlePeerUpdate("deadhost", nil)

	// Render must succeed and show both local panes.
	tui.render()

	if len(tui.panes) != 2 {
		t.Errorf("pane count = %d, want 2 (local panes preserved after remote failure)", len(tui.panes))
	}
}

// TestRenderWithConnectedRemoteAddsPane verifies that when a remote
// connects, its panes appear in the grid alongside local panes.
func TestRenderWithConnectedRemoteAddsPane(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)

	local := newEmuPane("eng1", 120, 39)

	tui := &TUI{
		screen:      s,
		panes:       []PaneView{local},
		layoutState: DefaultLayoutState([]string{"eng1"}),
		lastW:       120,
		lastH:       40,
		quitCh:      make(chan struct{}),
		agentEvents: make(chan AgentEvent, 64),
	}
	tui.plan = computeLayout(tui.layoutState, tui.panes, 120, 39)

	// Simulate a remote peer connecting with one agent.
	remotePane := newEmuPane("eng2", 60, 39)
	// Wrap in a minimal RemotePane-like PaneView (newEmuPane returns *Pane,
	// which satisfies PaneView, good enough for this test).
	tui.handlePeerUpdate("workbench", []PaneView{remotePane})

	if len(tui.panes) != 2 {
		t.Errorf("pane count = %d, want 2 (local + remote)", len(tui.panes))
	}

	// Render should not panic with mixed local + "remote" panes.
	tui.render()
}
