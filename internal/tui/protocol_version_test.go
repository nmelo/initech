//go:build !windows

package tui

// '//go:build !windows' matches window_server_test.go, whose startTestWindowServer
// this file drives: the tui package hangs 600s on Windows CI with goroutines
// stuck in that server's TCP Accept (measured, run 31651424793). The gate's
// portable half -- that ProtocolVersion is what both sides send -- needs no
// server and stays untagged in protocol_version_portable_test.go, so the
// constraint does not propagate past the one thing that needs it (ini-47w).
//
// protocol_version_test.go covers ini-yc03's handshake gate: a mixed-version
// pair must refuse cleanly rather than attach and diverge.
//
// Before this the version was carried in the hello and only LOGGED, so two
// peers speaking different protocols attached happily and disagreed about the
// fleet afterwards -- which the operator experiences as a display bug, not as
// a version mismatch, and which is how this fleet spent four beads chasing
// symptoms.

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// dialWindowRaw performs the transport handshake WITHOUT sending a hello, so a
// test can send one with a chosen protocol version. It mirrors dialWindow's
// transport setup exactly rather than approximating it.
func dialWindowRaw(t *testing.T, addr string) (net.Conn, net.Conn) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	session, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	ctrl, err := session.Open()
	if err != nil {
		t.Fatalf("open control stream: %v", err)
	}
	ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	return conn, ctrl
}

// readOneJSON reads a single protocol line from the control stream.
func readOneJSON(t *testing.T, ctrl net.Conn) []byte {
	t.Helper()
	scanner := NewIPCScanner(ctrl)
	if !scanner.Scan() {
		t.Fatalf("no response line from window server")
	}
	return scanner.Bytes()
}

func TestHelloRefusesAMismatchedProtocolVersion(t *testing.T) {
	_, addr := startTestWindowServer(t, []*Pane{windowServerTestPane("super")})

	// ProtocolVersion is currently 1, so "one older" and "absent" are the same
	// integer; naming them separately produced two identical subtests that
	// looked like broader coverage than they were. The cases are chosen by
	// what they MEAN instead: a newer peer, an absent/zero field (an old
	// client that predates the field, or a hand-rolled one), and a far-future
	// peer.
	for _, c := range []struct {
		name   string
		theirs int
	}{
		{"newer peer", ProtocolVersion + 1},
		{"absent or zero version field", 0},
		{"far-future peer", ProtocolVersion + 99},
	} {
		theirs := c.theirs
		t.Run(c.name, func(t *testing.T) {
			conn, ctrl := dialWindowRaw(t, addr)
			defer conn.Close()

			writeJSON(ctrl, HelloMsg{
				Action:   "hello",
				Version:  theirs,
				PeerName: "window-2",
			})

			line := readOneJSON(t, ctrl)
			var resp ErrorMsg
			if err := json.Unmarshal(line, &resp); err != nil {
				t.Fatalf("unmarshal response %q: %v", string(line), err)
			}
			if resp.Action != "error" {
				t.Fatalf("a v%d peer was ACCEPTED by a v%d session (got action %q). Mixed "+
					"versions must refuse: attaching and disagreeing later is the silent "+
					"divergence this gate exists to prevent.", theirs, ProtocolVersion, resp.Action)
			}
			if !strings.Contains(resp.Error, "protocol version mismatch") {
				t.Errorf("refusal message %q does not name the cause; an operator seeing this "+
					"must know to upgrade rather than to debug their layout", resp.Error)
			}
			if !strings.Contains(resp.Error, "upgrade") {
				t.Errorf("refusal message %q does not name the fix", resp.Error)
			}
		})
	}
}

func TestHelloAcceptsAMatchingProtocolVersion(t *testing.T) {
	_, addr := startTestWindowServer(t, []*Pane{windowServerTestPane("super")})
	conn, ctrl := dialWindowRaw(t, addr)
	defer conn.Close()

	writeJSON(ctrl, HelloMsg{
		Action:   "hello",
		Version:  ProtocolVersion,
		PeerName: "window-2",
	})

	line := readOneJSON(t, ctrl)
	var ok HelloOKMsg
	if err := json.Unmarshal(line, &ok); err != nil {
		t.Fatalf("unmarshal %q: %v", string(line), err)
	}
	if ok.Action != "hello_ok" {
		t.Fatalf("a matching-version peer was refused (action %q) -- the gate must reject only "+
			"mismatches, or it breaks every normal attach", ok.Action)
	}
}
