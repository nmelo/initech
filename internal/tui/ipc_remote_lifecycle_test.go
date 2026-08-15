package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

// ── ini-ap3i: hub-side routing of lifecycle verbs to remote machines ─
//
// The peer daemon has spoken stop_agent/restart_agent all along; the hub said
// "pane not found" instead of forwarding. These tests pin the routing and its
// honesty rules: a disconnected peer errors loudly, a success names the
// machine, and a non-remote target falls through untouched to the local path.

// ipcRespFromRemoteLifecycle drives remoteLifecycleIfRemote through a pipe and
// returns (handled, response).
func ipcRespFromRemoteLifecycle(t *testing.T, tui *TUI, target, action string) (bool, IPCResponse) {
	t.Helper()
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	// net.Pipe writes are synchronous, and the fall-through case writes
	// nothing — so the reader must be concurrent, and must be unblocked (by
	// the deferred close) when no response ever comes.
	respCh := make(chan IPCResponse, 1)
	go func() {
		var r IPCResponse
		sc := bufio.NewScanner(c2)
		if sc.Scan() {
			json.Unmarshal(sc.Bytes(), &r)
		}
		respCh <- r
	}()
	handled := tui.remoteLifecycleIfRemote(c1, IPCRequest{Target: target}, action)
	if !handled {
		return false, IPCResponse{}
	}
	return true, <-respCh
}

func TestRemoteLifecycle_RestartForwardsToOwningPeer(t *testing.T) {
	tui := newTestTUI()
	client, server := net.Pipe()
	defer client.Close()
	fakeStopAgentServer(t, server, true, "", 1) // replies OK to any agent-ctrl cmd
	rp := &RemotePane{name: "super", host: "support", alive: true, mux: NewControlMux(client)}
	tui.panes = append(tui.panes, rp)

	handled, resp := ipcRespFromRemoteLifecycle(t, tui, "support:super", "restart")
	if !handled {
		t.Fatal("remote target was not handled — restart would have fallen through to pane-not-found")
	}
	if !resp.OK {
		t.Fatalf("forwarded restart failed: %s", resp.Error)
	}
	if !strings.Contains(resp.Data, "support") {
		t.Errorf("success does not name the machine that acted: %q", resp.Data)
	}
}

func TestRemoteLifecycle_DisconnectedPeerErrorsLoudly(t *testing.T) {
	tui := newTestTUI()
	rp := &RemotePane{name: "super", host: "support", alive: true} // mux nil = disconnected
	tui.panes = append(tui.panes, rp)

	handled, resp := ipcRespFromRemoteLifecycle(t, tui, "support:super", "restart")
	if !handled {
		t.Fatal("disconnected remote target must still be handled (with an error), not fall through")
	}
	if resp.OK || !strings.Contains(resp.Error, "not connected") {
		t.Errorf("want loud not-connected error, got OK=%v %q", resp.OK, resp.Error)
	}
}

func TestRemoteLifecycle_LocalTargetFallsThrough(t *testing.T) {
	tui := newTestTUI()
	handled, _ := ipcRespFromRemoteLifecycle(t, tui, "eng1", "restart")
	if handled {
		t.Fatal("local target must fall through to the local lifecycle path")
	}
}

func TestRemoteLifecycle_StartIsHonestlyUnsupported(t *testing.T) {
	tui := newTestTUI()
	client, _ := net.Pipe()
	defer client.Close()
	rp := &RemotePane{name: "super", host: "support", alive: true, mux: NewControlMux(client)}
	tui.panes = append(tui.panes, rp)

	handled, resp := ipcRespFromRemoteLifecycle(t, tui, "support:super", "start")
	if !handled || resp.OK {
		t.Fatalf("start on a remote target must be handled with an honest error, got handled=%v OK=%v", handled, resp.OK)
	}
	if !strings.Contains(resp.Error, "restart") {
		t.Errorf("the error should name the working alternative: %q", resp.Error)
	}
}
