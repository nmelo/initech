package tui

// The portable half of ini-cxia's tests: no window server (which hangs the
// package on windows CI, see window_server_test.go's exemption), so the
// hello check itself is covered on every platform.

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/nmelo/initech/internal/config"
)

// testWindowToken is the configured token the shared window fixtures use
// (ini-cxia): window 1 now refuses a hello without the session's token, and
// a configured token keeps those tests off the generated-token path, so no
// token file is ever written under a fixture's fixed root. The generated
// path has its own tests, each in its own temp root (window_token_test.go).
// Here, untagged, because fixtures on every platform use it.
const testWindowToken = "test-window-token"

// The hello check fails closed: a daemon with no token accepts nobody, not
// even an empty hello (ConstantTimeCompare of two empty strings is a match).
// This guards the check itself, independent of window 1 generating a token.
func TestDaemon_ATokenlessDaemonAcceptsNobody(t *testing.T) {
	d := &Daemon{project: &config.Project{Name: "t", Root: t.TempDir(), PeerName: "window1"}, version: "test"}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	go d.handleConnection(server)
	session, err := yamux.Client(client, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	ctrl, err := session.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(ctrl, HelloMsg{Action: "hello", Version: ProtocolVersion, PeerName: "window2"}); err != nil {
		t.Fatal(err)
	}
	ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	sc := NewIPCScanner(ctrl)
	if !sc.Scan() {
		t.Fatal("no reply")
	}
	var reply struct {
		Action string `json:"action"`
		Error  string `json:"error"`
	}
	json.Unmarshal(sc.Bytes(), &reply)
	if reply.Action != "error" || reply.Error != "auth failed" {
		t.Fatalf("a tokenless daemon answered an empty hello with %q/%q, want error/auth failed", reply.Action, reply.Error)
	}
}
