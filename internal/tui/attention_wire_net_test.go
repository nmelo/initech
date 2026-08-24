//go:build !windows

package tui

// The send half of ini-35ak, over a real window server and a real dialed
// client.
//
// SPLIT FROM attention_wire_test.go ON A BUILD TAG, deliberately: this file's
// helpers (startWindowServer, dialWindow) are unix-only, while every cell in
// the portable file runs everywhere. Tagging the whole suite to satisfy one
// networked test would silently drop the portable assertions from the Windows
// build -- the census rule (ini-47w) is that the portable half stays untagged.

import (
	"encoding/json"
	"testing"
	"time"
)

// TestBroadcastAgentStatusChanges_FiresOnBothEdgesOnce is the SEND side, over
// a real window server and a real dialed client.
//
// It exists because every other cell in this file applies state directly to a
// remote pane, which cannot detect a change detector that never fires. Without
// it, a mutant that drops waiting from agentStatusSnapshot survives everything
// and the kill matrix would be reporting on a claim no test spans.
//
// Asserted as exactly-once per edge, not merely "at least one": the detector's
// whole job is that a wait produces one frame when it starts and one when it
// ends, and a per-frame writer would also satisfy a weaker assertion while
// putting a socket write on every render.
func TestBroadcastAgentStatusChanges_FiresOnBothEdgesOnce(t *testing.T) {
	p := windowServerTestPane("eng1")
	ws, cleanup, err := startWindowServer(
		testWindowProject("127.0.0.1:0"), "test",
		[]*Pane{p}, func(f func()) { go f() }, nil, nil, nil)
	if err != nil {
		t.Fatalf("startWindowServer: %v", err)
	}
	t.Cleanup(cleanup)

	session, ctrl, hello := dialWindow(t, ws.Addr(), "window-2")
	defer session.Close()
	if hello.Action != "hello_ok" {
		t.Fatalf("handshake failed: %+v", hello)
	}

	frames := make(chan ControlResp, 16)
	go func() {
		scanner := NewIPCScanner(ctrl)
		for scanner.Scan() {
			var ev ControlResp
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				continue
			}
			if ev.Action == agentStatusAction {
				frames <- ev
			}
		}
	}()

	tui := newTestTUI()
	tui.windowSrv = ws
	tui.panes = []PaneView{p}
	// Prime the detector so the assertions below measure TRANSITIONS rather
	// than the first-sight frame every agent produces.
	tui.broadcastAgentStatusChanges()
	drainAgentStatus(frames, 200*time.Millisecond)

	// RAISE.
	p.waitingSince = time.Now()
	p.waitingPreview = "Continue? (y/n)"
	tui.broadcastAgentStatusChanges()

	ev := awaitAgentStatus(t, frames, "raise")
	if !ev.Waiting {
		t.Errorf("the raise frame carries waiting=false; window 2 is told about the agent "+
			"and not about the question (%+v)", ev.WaitingState)
	}
	if ev.Preview != "Continue? (y/n)" {
		t.Errorf("the raise frame carries preview %q", ev.Preview)
	}

	// NO CHANGE: the detector must stay quiet.
	tui.broadcastAgentStatusChanges()
	if extra := drainAgentStatus(frames, 300*time.Millisecond); extra != 0 {
		t.Errorf("an unchanged wait produced %d further frame(s); the detector is writing "+
			"to the wire on every call, which on the render path is a socket write per "+
			"frame", extra)
	}

	// CLEAR.
	p.waitingSince = time.Time{}
	p.waitingPreview = ""
	tui.broadcastAgentStatusChanges()

	ev = awaitAgentStatus(t, frames, "clear")
	if ev.Waiting {
		t.Errorf("the clear frame still carries waiting=true; the operator answered on "+
			"window 1 and window 2 keeps pointing at an answered question (%+v)",
			ev.WaitingState)
	}
}

func awaitAgentStatus(t *testing.T, frames chan ControlResp, edge string) ControlResp {
	t.Helper()
	select {
	case ev := <-frames:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatalf("no agent_status frame after the %s edge: the state changed on window 1 "+
			"and nothing crossed the wire, so no viewer can ever learn it", edge)
		return ControlResp{}
	}
}

func drainAgentStatus(frames chan ControlResp, window time.Duration) int {
	n := 0
	deadline := time.After(window)
	for {
		select {
		case <-frames:
			n++
		case <-deadline:
			return n
		}
	}
}
