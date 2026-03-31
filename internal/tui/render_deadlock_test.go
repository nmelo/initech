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
			ch, _, _, _ := s.GetContent(x, y)
			line += string(ch)
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
