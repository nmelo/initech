//go:build !windows

package tui

import (
	"encoding/json"
	"testing"
	"time"
)

// ── ini-1q3x.2: the no-bead marker in secondary windows ──
// Unix-only for the same reason as parity_test.go: these drive the window
// server, which hangs the tui package on windows CI. The portable cells are
// in no_bead_test.go.

// Every window shows the same ribbon: the marker rides agent_status, both
// edges, and a viewer applies it.
func TestNoBead_MarkerRidesAgentStatusToOtherWindows(t *testing.T) {
	p := windowServerTestPane("eng1")
	ws, cleanup, err := startWindowServer(
		testWindowProject("127.0.0.1:0"), "test",
		[]*Pane{p}, func(f func()) { go f() }, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("startWindowServer: %v", err)
	}
	t.Cleanup(cleanup)
	session, ctrl, _ := dialWindow(t, ws.Addr(), "window-2")
	defer session.Close()
	frames := make(chan ControlResp, 16)
	go func() {
		sc := NewIPCScanner(ctrl)
		for sc.Scan() {
			var ev ControlResp
			if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Action == agentStatusAction {
				frames <- ev
			}
		}
	}()
	tu := newTestTUI()
	tu.windowSrv = ws
	tu.panes = []PaneView{p}
	tu.broadcastAgentStatusChanges()
	drainAgentStatus(frames, 200*time.Millisecond)

	p.SetNoBeadWork("bump main")
	tu.broadcastAgentStatusChanges()
	ev := awaitAgentStatus(t, frames, "marker up")
	if ev.NoBead != "bump main" || len(ev.Beads) != 0 {
		t.Errorf("frame no_bead=%q beads=%v, want the marker and no beads", ev.NoBead, ev.Beads)
	}

	viewer := newTestTUI()
	rp := &RemotePane{name: "eng1", alive: true}
	viewer.panes = []PaneView{rp}
	viewer.applyAgentStatus(ev.Name, ev.Beads, ev.NoBead, ev.Text, ev.WaitingState, ev.Suspended)
	if rp.NoBeadWork() != "bump main" {
		t.Errorf("viewer marker = %q, want applied", rp.NoBeadWork())
	}

	p.SetBead("", "")
	tu.broadcastAgentStatusChanges()
	ev = awaitAgentStatus(t, frames, "marker cleared")
	if ev.NoBead != "" {
		t.Errorf("clear frame still carries no_bead=%q", ev.NoBead)
	}
	viewer.applyAgentStatus(ev.Name, ev.Beads, ev.NoBead, ev.Text, ev.WaitingState, ev.Suspended)
	if rp.NoBeadWork() != "" {
		t.Errorf("viewer kept the marker after the clear: %q", rp.NoBeadWork())
	}
}

// A window attaching while the marker is up sees it on its first frame.
func TestNoBead_HandshakeSeedsTheMarker(t *testing.T) {
	p := windowServerTestPane("eng1")
	p.SetNoBeadWork("bump main")
	ws, cleanup, err := startWindowServer(
		testWindowProject("127.0.0.1:0"), "test",
		[]*Pane{p}, func(f func()) { go f() }, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("startWindowServer: %v", err)
	}
	t.Cleanup(cleanup)
	session, _, hello := dialWindow(t, ws.Addr(), "window-2")
	defer session.Close()
	if len(hello.Agents) != 1 || hello.Agents[0].NoBead != "bump main" {
		t.Errorf("hello agents = %+v, want eng1 with no_bead", hello.Agents)
	}
}
