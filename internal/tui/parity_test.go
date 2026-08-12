//go:build !windows

// parity_test.go is ini-9ka.8's verification half: which cross-window fleet
// operations actually fall out of existing primitives, and which do not.
//
// The bead marked "much of this should fall out" as MUST-VERIFY rather than
// assumed. The answer, established here rather than asserted: DISPATCH falls
// out; HIDE, PIN and PROTECT do not, and cannot as currently structured. The
// negative controls below are the "prove it broken first" artifacts the bead's
// spine requires -- they exist so the gap is a recorded fact with a failing
// test attached, not a claim in a comment.
package tui

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// windowTUI builds a TUI standing in for one window of a multi-window session,
// with its own layout state -- which is the point: ini-9ka.3 made LayoutState
// per-window, so two windows genuinely have separate copies.
func windowTUI(t *testing.T, windowID string, paneNames ...string) *TUI {
	t.Helper()
	panes := make([]PaneView, len(paneNames))
	for i, n := range paneNames {
		panes[i] = testPane(n)
	}
	tui := &TUI{
		panes:       panes,
		windowID:    windowID,
		projectRoot: t.TempDir(),
		project:     &config.Project{WindowListen: "127.0.0.1:7500"},
		agentEvents: make(chan AgentEvent, 8),
	}
	tui.layoutState = DefaultLayoutState(paneNames)
	return tui
}

// TestParity_HideDoesNotPropagateAcrossWindows is a NEGATIVE CONTROL: it
// records that hiding an agent in one window is invisible to another, which
// violates the spec's "visibility/pin/protect stay GLOBAL" decision.
//
// The cause is a contradiction between two individually-correct decisions,
// not an oversight: the spec requires these three fields to be global, while
// ini-9ka.3 correctly made LayoutState per-window -- and Hidden, Protected and
// LivePinned all live inside LayoutState. toggleHidden mutates local state and
// saves to THIS window's own layout file; no network call exists on that path.
//
// When global-state sync lands, this test inverts: same setup, opposite
// assertion. Written so that inversion is a one-line change and the gap cannot
// be quietly closed without someone confronting this comment.
func TestParity_HideDoesNotPropagateAcrossWindows(t *testing.T) {
	w1 := windowTUI(t, WindowOne, "eng1", "eng2")
	w2 := windowTUI(t, "window-2", "eng1", "eng2")

	if !w2.toggleHidden("eng1") {
		t.Fatal("precondition: hiding eng1 in window 2 should succeed")
	}
	if !w2.layoutState.Hidden["eng1"] {
		t.Fatal("precondition: window 2 should see its own hide")
	}

	if w1.layoutState.Hidden["eng1"] {
		t.Fatal("UNEXPECTED: hide propagated to window 1 -- if this now passes, global visibility sync has landed and this negative control must be inverted, not deleted")
	}
	t.Log("CONFIRMED GAP (ini-9ka.8): hide in window 2 is invisible to window 1. " +
		"Hidden lives in LayoutState, which ini-9ka.3 correctly made per-window, " +
		"while the spec requires visibility to be global. Escalated on the bead.")
}

// TestParity_ProtectDoesNotPropagateAcrossWindows is the same negative control
// for protect, which fails for a second, independent reason worth recording
// separately: SetProtected sets a field on a local *Pane object, and in a
// secondary window that agent is a RemotePane, so the call does not reach the
// same object even in principle.
func TestParity_ProtectDoesNotPropagateAcrossWindows(t *testing.T) {
	w1 := windowTUI(t, WindowOne, "eng1")
	w2 := windowTUI(t, "window-2", "eng1")

	p2 := w2.panes[0].(*Pane)
	p2.SetProtected(true)
	if !p2.IsProtected() {
		t.Fatal("precondition: window 2's own pane object should report protected")
	}

	p1 := w1.panes[0].(*Pane)
	if p1.IsProtected() {
		t.Fatal("UNEXPECTED: protect propagated -- global state sync has landed; invert this control rather than deleting it")
	}
	t.Log("CONFIRMED GAP (ini-9ka.8): protect is per-process pane state with no cross-window channel. " +
		"The control stream has no protect command (send/peek/resize/forward_send/peers_query/ping/" +
		"configure_agent/stop_agent/restart_agent).")
}

// TestParity_SessionNoticeReachesEveryAttachedWindow is the INVERTED form of
// what was a negative control: fan-out now exists, so this asserts the
// capability rather than recording its absence. Two windows attach; window 1
// broadcasts; BOTH receive it off the real control stream.
//
// The transport was never the gap -- gracefulShutdown had always pushed
// unsolicited messages to every ctrlConns entry, and the client's ControlMux
// had always routed ID-less messages to its events channel. Only a message
// type and a raise site were missing, which is why this closed without new
// plumbing.
func TestParity_SessionNoticeReachesEveryAttachedWindow(t *testing.T) {
	panes := []*Pane{windowServerTestPane("eng1")}
	ws, addr := startTestWindowServer(t, panes)

	s2, c2, _ := dialWindow(t, addr, "window-2")
	defer s2.Close()
	defer c2.Close()
	s3, c3, _ := dialWindow(t, addr, "window-3")
	defer s3.Close()
	defer c3.Close()
	waitForClients(t, ws, 2)

	const notice = "window-9 disconnected; its agents folded back into window 1"
	ws.broadcastSessionNotice(notice)

	// One scanner per control stream -- the IPCScanner double-reader trap
	// (qa1's find on ini-9ka.2): a second scanner on the same conn steals
	// buffered bytes from the first.
	// Dispatch BY ACTION rather than assuming the notice is the first message
	// on the stream -- it is not. The server also pushes replay_start and
	// other unsolicited traffic, and the real client (peerManager.consumeEvents)
	// switches on ev.Action for exactly this reason. A test that asserted
	// ordering would be testing the harness, not the fan-out.
	for name, ctrl := range map[string]net.Conn{"window-2": c2, "window-3": c3} {
		ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
		scanner := NewIPCScanner(ctrl)
		found := false
		for scanner.Scan() {
			var got ControlResp
			if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
				continue
			}
			if got.Action != sessionNoticeAction {
				continue // replay_start and friends.
			}
			found = true
			if got.Text != notice {
				t.Errorf("%s got text %q, want %q", name, got.Text, notice)
			}
			if got.ID != "" {
				t.Errorf("%s: notice carried an ID (%q); it must be ID-less so ControlMux routes it as an unsolicited event rather than a response nobody is waiting for", name, got.ID)
			}
			break
		}
		if !found {
			t.Errorf("%s never received the session notice; a fold-back in window 1 would be invisible there", name)
		}
	}
}

// TestParity_SecondaryWindowSurfacesAReceivedNotice covers the receiving half:
// a broadcast notice must become an ordinary local event in the attached
// window, or it would arrive on the wire and render nowhere.
func TestParity_SecondaryWindowSurfacesAReceivedNotice(t *testing.T) {
	w2 := windowTUI(t, "window-2", "eng1")

	w2.surfaceSessionNotice("window-3 reattached; its agents moved back")

	select {
	case ev := <-w2.agentEvents:
		if ev.Detail == "" {
			t.Error("surfaced notice has no detail text")
		}
		if ev.Pane != "" {
			t.Errorf("surfaced notice attached to pane %q; session notices are session-level", ev.Pane)
		}
	default:
		t.Fatal("a received session notice did not become a local event; it would arrive on the wire and render nowhere")
	}
}

// TestParity_DispatchRoutesThroughTheControlStream is the POSITIVE result: the
// one operation that does fall out. A secondary window's RemotePane.SendText
// issues a real `send` control command, which ini-9ka.2 already proved reaches
// the target agent's PTY end to end.
//
// Asserted structurally rather than by re-running the byte hop, per the
// standing instruction not to re-prove what ini-9ka.2 pinned: what is new here
// is that the SECONDARY window's dispatch path is the same command, so parity
// follows from a mechanism already verified rather than from a fresh claim.
func TestParity_DispatchRoutesThroughTheControlStream(t *testing.T) {
	// Real ControlMux over a pipe: the command is read off the wire exactly
	// as window 1 would receive it, rather than through a stub that could
	// agree with a wrong expectation.
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	rp := &RemotePane{name: "eng1", mux: NewControlMux(client)}
	rp.SendText("PING", false)

	sent := make(chan ControlCmd, 1)
	go func() {
		scanner := NewIPCScanner(server)
		if scanner.Scan() {
			var cmd ControlCmd
			if err := json.Unmarshal(scanner.Bytes(), &cmd); err == nil {
				sent <- cmd
			}
		}
	}()

	select {
	case cmd := <-sent:
		if cmd.Action != "send" {
			t.Errorf("dispatch used action %q, want send", cmd.Action)
		}
		if cmd.Target != "eng1" {
			t.Errorf("dispatch targeted %q, want eng1", cmd.Target)
		}
		if cmd.Text != "PING" {
			t.Errorf("dispatch text = %q, want PING", cmd.Text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("dispatch from a secondary window issued no control command; parity does NOT fall out and needs its own wiring")
	}
}

// --- Modal liveness in a secondary window ----------------------------------
//
// The bead asks whether window 2's agents modal shows LIVE state or an
// attach-time snapshot. The answer is neither, exactly, and the distinction
// matters for whoever closes it: ACTIVITY is live because it is derived from
// the byte stream, while BEAD state is not synced AT ALL -- not stale, simply
// never populated from window 1. A fix aimed at "refresh the snapshot" would
// miss that there is no snapshot to refresh.

// TestParity_RemotePaneActivityIsDerivedLiveFromTheStream is the POSITIVE
// half: activity is computed from output recency on every call, so it tracks
// the agent in real time rather than being frozen at attach. This is why my
// ini-9ka.2 RingBuf replay does NOT introduce staleness here -- activity is
// never stored as a snapshot to go stale.
func TestParity_RemotePaneActivityIsDerivedLiveFromTheStream(t *testing.T) {
	rp := &RemotePane{name: "eng1", alive: true, activity: StateRunning}

	// No recent output: derived as idle regardless of the stored activity.
	rp.lastOut = time.Now().Add(-2 * ptyIdleTimeout)
	if got := rp.Activity(); got != StateIdle {
		t.Errorf("activity with stale output = %v, want idle (it must be derived, not remembered)", got)
	}

	// Fresh bytes arrive: the SAME object now reports running, with no
	// refresh call and no re-attach.
	rp.lastOut = time.Now()
	if got := rp.Activity(); got != StateRunning {
		t.Errorf("activity after fresh output = %v, want running -- window 2 would show a stale state", got)
	}
}

// TestParity_RemotePaneBeadStateIsNeverSyncedFromWindowOne is the NEGATIVE
// control, and it records a sharper fact than "the snapshot is stale":
// RemotePane.beadIDs is only ever written by the LOCAL process's own IPC
// (ipc.go's SetBeads path). The attach handshake's helloOK.Agents is consumed
// solely by pushRolesToPeer for zero-config role pushing and never applied to
// pane state, and no control message carries a bead update outward.
//
// So an agent that claims a bead in window 1 shows NO bead in window 2's
// modal -- not an out-of-date one. When this gap closes, invert rather than
// delete: the assertion becomes "window 2 sees the bead window 1 set".
func TestParity_RemotePaneBeadStateIsNeverSyncedFromWindowOne(t *testing.T) {
	// Window 1's view: a local pane holding a bead.
	w1pane := testPane("eng1")
	w1pane.SetBead("ini-123", "some work")
	if w1pane.BeadID() != "ini-123" {
		t.Fatalf("precondition: window 1's pane should hold the bead, got %q", w1pane.BeadID())
	}

	// Window 2's view of the same agent, freshly attached.
	w2pane := &RemotePane{name: "eng1", alive: true}

	if got := w2pane.BeadID(); got != "" {
		t.Fatalf("UNEXPECTED: window 2 sees bead %q -- bead sync has landed; invert this negative control rather than deleting it", got)
	}
	t.Log("CONFIRMED GAP (ini-9ka.8): bead state is never synced to a secondary window. " +
		"RemotePane.beadIDs is written only by the local process's IPC; helloOK.Agents is consumed " +
		"by pushRolesToPeer and never applied to pane state; no control message carries a bead update. " +
		"This is an ABSENT value, not a stale one -- a 'refresh the snapshot' fix would miss it.")
}
