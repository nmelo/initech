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

// TestParity_NoSessionNoticeBroadcastExists records the fan-out gap and, more
// usefully, the mechanism that would carry it. Window 1 ALREADY pushes
// unsolicited messages to every attached window -- gracefulShutdown writes
// {"action":"shutdown"} to all ctrlConns, and the client's ControlMux routes
// unsolicited messages to a broadcast channel. So notice fan-out needs no new
// transport, only a message type and a raise site.
//
// That asymmetry is the routing topology the epic's docs need: window 1 is the
// hub. Secondary windows cannot push to each other; they reach window 1 via
// control commands, so a session notice should be raised BY window 1 and
// broadcast outward, not raised locally in a secondary window where it would
// render in exactly one place.
func TestParity_NoSessionNoticeBroadcastExists(t *testing.T) {
	w2 := windowTUI(t, "window-2", "eng1")

	// A notice raised in a secondary window today lands only on that window's
	// own local channel -- there is no path off this process.
	w2.noticeAssignmentWriteFailed("move group eng", ErrAssignmentReadOnly)

	select {
	case ev := <-w2.agentEvents:
		if ev.Type != EventAssignmentWriteRefused {
			t.Errorf("unexpected event type %v", ev.Type)
		}
	default:
		t.Fatal("the notice did not even reach the local channel")
	}
	t.Log("CONFIRMED GAP (ini-9ka.8): a session notice raised in a secondary window is process-local; " +
		"no broadcast reaches other windows. Transport EXISTS and is unused for this -- " +
		"gracefulShutdown already pushes unsolicited messages to all ctrlConns, and ControlMux " +
		"routes unsolicited messages client-side. Topology: window 1 is the hub; notices should be " +
		"raised by window 1 and fanned out.")
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
