package tui

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// rejectingDaemon answers every ControlCmd with a rejection carrying reason,
// and a NIL transport error -- which is exactly how the daemon reports an
// unknown agent or an oversize body.
func rejectingDaemon(t *testing.T, reason string) *ControlMux {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	go func() {
		dec := json.NewDecoder(server)
		enc := json.NewEncoder(server)
		for {
			var cmd ControlCmd
			if err := dec.Decode(&cmd); err != nil {
				return
			}
			_ = enc.Encode(ControlResp{ID: cmd.ID, Error: reason})
		}
	}()
	return NewControlMux(client)
}

// A daemon rejection must not read as a delivery. Before this, SendText threw
// the response away and checked only the transport error, so every rejection
// -- unknown agent, >64KB body -- succeeded silently for every cross-window
// send.
func TestRemoteSendText_DaemonRejectionIsLogged(t *testing.T) {
	logs := captureLogs(t)
	rp := &RemotePane{name: "eng9", mux: rejectingDaemon(t, "agent not found: eng9")}

	rp.SendText("hello", true)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), "SEND NOT DELIVERED") {
			if !strings.Contains(logs.String(), "agent not found") {
				t.Errorf("record does not carry the daemon's reason: %q", logs.String())
			}
			if strings.Contains(logs.String(), "level=DEBUG msg=\"[remote] SEND NOT DELIVERED") {
				t.Error("the record is below the default level, so it is invisible in practice")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("a rejected send left NO record; it reads as delivered: %q", logs.String())
}

// The oversize case is the other rejection shape and takes the same path.
func TestRemoteSendText_OversizeRejectionIsLogged(t *testing.T) {
	logs := captureLogs(t)
	rp := &RemotePane{name: "eng1", mux: rejectingDaemon(t, "text too long: 70000 > 65536")}

	rp.SendText(strings.Repeat("x", 70000), false)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), "text too long") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("an oversize send was rejected and left no record: %q", logs.String())
}

// A successful send must stay quiet on this path -- the fix must not turn
// every delivery into a failure record.
func TestRemoteSendText_SuccessIsNotReportedAsFailure(t *testing.T) {
	logs := captureLogs(t)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		dec := json.NewDecoder(server)
		enc := json.NewEncoder(server)
		for {
			var cmd ControlCmd
			if err := dec.Decode(&cmd); err != nil {
				return
			}
			_ = enc.Encode(ControlResp{ID: cmd.ID, OK: true})
		}
	}()
	rp := &RemotePane{name: "eng1", mux: NewControlMux(client)}

	rp.SendText("hello", true)
	time.Sleep(400 * time.Millisecond)

	if strings.Contains(logs.String(), "SEND NOT DELIVERED") {
		t.Errorf("a successful send was recorded as undelivered: %q", logs.String())
	}
}
