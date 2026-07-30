package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
)

// fakeStopAgentServer runs a background goroutine that reads stop_agent
// requests off server and replies with the given ok/error for each, in the
// order received. Returns a cleanup channel closed once nReqs replies have
// been sent.
func fakeStopAgentServer(t *testing.T, server net.Conn, ok bool, errMsg string, nReqs int) {
	t.Helper()
	go func() {
		scanner := bufio.NewScanner(server)
		for i := 0; i < nReqs; i++ {
			if !scanner.Scan() {
				return
			}
			var cmd StopAgentCmd
			json.Unmarshal(scanner.Bytes(), &cmd)
			resp, _ := json.Marshal(ControlResp{ID: cmd.ID, OK: ok, Error: errMsg})
			server.Write(resp)
			server.Write([]byte("\n"))
		}
	}()
}

// TestRemoteStopPeer_AllRejectedReturnsError is a regression test for
// ini-om0: remoteStopPeer used to return (0, nil) when every stop_agent
// attempt was rejected -- indistinguishable from "there was nothing to
// stop." remote-stop is a safety action taken before an intentional
// disconnect; reporting success while every attempt actually failed
// (most commonly because this client never established ownership) is
// exactly the misleading shape that let super misattribute the failure to
// four different agents.
func TestRemoteStopPeer_AllRejectedReturnsError(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	fakeStopAgentServer(t, server, false, `agent "eng9" is owned by "someone-else", not "wb"`, 2)

	mux := NewControlMux(client)
	tu := &TUI{panes: []PaneView{
		NewRemotePane("eng9", "wb", server, mux, 80, 24),
		NewRemotePane("eng10", "wb", server, mux, 80, 24),
	}}

	stopped, err := tu.remoteStopPeer("wb")
	if stopped != 0 {
		t.Errorf("stopped = %d, want 0", stopped)
	}
	if err == nil {
		t.Fatal("expected an error when every stop_agent attempt was rejected, got nil")
	}
}

// TestRemoteStopPeer_SucceedsWhenOwned is the control: at least one
// successful stop must NOT be treated as an error, and the returned count
// must reflect it.
func TestRemoteStopPeer_SucceedsWhenOwned(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	fakeStopAgentServer(t, server, true, "", 1)

	mux := NewControlMux(client)
	tu := &TUI{panes: []PaneView{
		NewRemotePane("eng9", "wb", server, mux, 80, 24),
	}}

	stopped, err := tu.remoteStopPeer("wb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stopped != 1 {
		t.Errorf("stopped = %d, want 1", stopped)
	}
}

// TestRemoteStopPeer_NoAgentsFromPeerErrors confirms the pre-existing
// "nothing to even attempt" path is unchanged by this fix.
func TestRemoteStopPeer_NoAgentsFromPeerErrors(t *testing.T) {
	tu := &TUI{}
	stopped, err := tu.remoteStopPeer("wb")
	if stopped != 0 || err == nil {
		t.Errorf("stopped = %d, err = %v, want 0 and an error", stopped, err)
	}
}
