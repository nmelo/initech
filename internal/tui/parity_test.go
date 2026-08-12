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

// TestParity_RemotePaneBeadStateSyncsFromWindowOne is the INVERTED form of a
// negative control ini-9ka.8 committed (and wrote to be inverted, not
// deleted): bead state now reaches a secondary window. ini-9ka.11 closed it.
//
// The gap was ABSENT, not stale -- nothing ever wrote the field, even though
// the handshake already carried the value and discarded it. That is why the
// fix is a write path rather than a refresh.
func TestParity_RemotePaneBeadStateSyncsFromWindowOne(t *testing.T) {
	rp := &RemotePane{name: "eng1", alive: true}

	// THREE beads, deliberately: not 1 (which a truncating implementation
	// would satisfy by accident) and not any count a default produces. A
	// singular API silently keeping only the first would display a
	// wrong-but-POPULATED ribbon, which is harder to spot than the empty
	// field this bead fixed.
	want := []string{"ini-aaa", "ini-bbb", "ini-ccc"}
	rp.ApplyStatus(want, "reviewing the parity matrix")

	got := rp.BeadIDs()
	if len(got) != len(want) {
		t.Fatalf("BeadIDs() = %v (%d), want %v (%d) -- a truncating propagation shows a wrong-but-populated ribbon", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("bead %d = %q, want %q", i, got[i], want[i])
		}
	}
	if rp.BeadID() != "ini-aaa" {
		t.Errorf("primary bead = %q, want ini-aaa", rp.BeadID())
	}
	if rp.SessionDesc() != "reviewing the parity matrix" {
		t.Errorf("SessionDesc() = %q; sessDesc previously had NO writer at all", rp.SessionDesc())
	}
}

// TestParity_AgentStatusClearPropagates covers the change half of the AC:
// claim, change, and CLEAR must each be reflected. Clearing is the one a
// naive "only apply non-empty updates" implementation gets wrong -- the
// operator would see a bead the agent no longer holds.
func TestParity_AgentStatusClearPropagates(t *testing.T) {
	rp := &RemotePane{name: "eng1", alive: true}

	rp.ApplyStatus([]string{"ini-aaa", "ini-bbb"}, "working")
	if len(rp.BeadIDs()) != 2 {
		t.Fatalf("precondition: expected 2 beads, got %v", rp.BeadIDs())
	}

	rp.ApplyStatus([]string{"ini-ccc"}, "different work") // change
	if got := rp.BeadIDs(); len(got) != 1 || got[0] != "ini-ccc" {
		t.Errorf("after change, BeadIDs() = %v, want [ini-ccc]", got)
	}

	rp.ApplyStatus(nil, "") // clear
	if got := rp.BeadIDs(); len(got) != 0 {
		t.Errorf("after clear, BeadIDs() = %v, want empty -- empty must be applied, not treated as 'no update'", got)
	}
	if rp.SessionDesc() != "" {
		t.Errorf("after clear, SessionDesc() = %q, want empty", rp.SessionDesc())
	}
}

// TestParity_ApplyStatusCopiesTheSlice guards a caller reusing its buffer:
// the pane must not alias the caller's slice, or a later reuse would silently
// rewrite this agent's beads.
func TestParity_ApplyStatusCopiesTheSlice(t *testing.T) {
	rp := &RemotePane{name: "eng1", alive: true}
	buf := []string{"ini-aaa", "ini-bbb"}
	rp.ApplyStatus(buf, "d")

	buf[0] = "MUTATED"
	if got := rp.BeadIDs(); got[0] != "ini-aaa" {
		t.Errorf("pane aliased the caller's slice: bead 0 = %q after the caller mutated its buffer", got[0])
	}
}

// TestParity_AgentStatusBroadcastCarriesAllBeads is the wire-level half of the
// multi-bead AC: the broadcast itself must not truncate. Asserted on the
// message a real attached window receives, dispatching BY ACTION -- the
// control stream carries other unsolicited traffic (replay_start arrives
// first), a constraint recorded during ini-9ka.8 rather than rediscovered.
func TestParity_AgentStatusBroadcastCarriesAllBeads(t *testing.T) {
	panes := []*Pane{windowServerTestPane("eng1")}
	ws, addr := startTestWindowServer(t, panes)

	s2, c2, _ := dialWindow(t, addr, "window-2")
	defer s2.Close()
	defer c2.Close()
	waitForClients(t, ws, 1)

	want := []string{"ini-aaa", "ini-bbb", "ini-ccc"}
	ws.broadcastAgentStatus("eng1", want, "three beads in flight")

	c2.SetReadDeadline(time.Now().Add(5 * time.Second))
	scanner := NewIPCScanner(c2)
	found := false
	for scanner.Scan() {
		var got ControlResp
		if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
			continue
		}
		if got.Action != agentStatusAction {
			continue
		}
		found = true
		if got.Name != "eng1" {
			t.Errorf("status for %q, want eng1", got.Name)
		}
		if len(got.Beads) != len(want) {
			t.Errorf("wire carried %d beads (%v), want %d (%v) -- truncation here shows a wrong-but-populated ribbon", len(got.Beads), got.Beads, len(want), want)
		}
		if got.Bead != "ini-aaa" {
			t.Errorf("compat primary = %q, want ini-aaa", got.Bead)
		}
		if got.Text != "three beads in flight" {
			t.Errorf("description = %q", got.Text)
		}
		break
	}
	if !found {
		t.Error("no agent_status message reached the attached window")
	}
}

// TestParity_HandshakeCarriesBeadsForLateAttach covers the late-attach AC at
// its source: a window attaching AFTER an agent claimed beads must see them
// from the handshake, without waiting for a subsequent change. The data was
// always on the wire -- ini-9ka.11 stopped discarding it.
func TestParity_HandshakeCarriesBeadsForLateAttach(t *testing.T) {
	pane := windowServerTestPane("eng1")
	pane.SetBeads([]string{"ini-aaa", "ini-bbb", "ini-ccc"})

	_, addr := startTestWindowServer(t, []*Pane{pane})

	// Attach AFTER the beads were set.
	session, ctrl, ok := dialWindow(t, addr, "window-2")
	defer session.Close()
	defer ctrl.Close()

	var found *AgentStatus
	for i := range ok.Agents {
		if ok.Agents[i].Name == "eng1" {
			found = &ok.Agents[i]
		}
	}
	if found == nil {
		t.Fatal("handshake did not describe eng1")
	}
	if len(found.Beads) != 3 {
		t.Errorf("handshake carried %d beads (%v), want 3 -- a late-attaching window would show an incomplete ribbon", len(found.Beads), found.Beads)
	}
	if found.Bead != "ini-aaa" {
		t.Errorf("handshake primary bead = %q, want ini-aaa", found.Bead)
	}
}
