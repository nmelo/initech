package tui

// ini-hl6r AC 4: the headless daemon's token never reaches its log, at any
// level. The log is captured at Debug across a refused and an accepted
// hello. Portable: no window server involved.

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/nmelo/initech/internal/config"
)

func helloOverPipe(t *testing.T, d *Daemon, token string) string {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close(); client.Close() })
	go d.handleConnection(server)
	session, err := yamux.Client(client, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	ctrl, err := session.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(ctrl, HelloMsg{Action: "hello", Version: ProtocolVersion, Token: token, PeerName: "probe"}); err != nil {
		t.Fatal(err)
	}
	ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	sc := NewIPCScanner(ctrl)
	if !sc.Scan() {
		t.Fatal("no reply to hello")
	}
	var reply struct {
		Action string `json:"action"`
	}
	json.Unmarshal(sc.Bytes(), &reply)
	return reply.Action
}

func TestDaemon_TheTokenNeverReachesTheLogAtAnyLevel(t *testing.T) {
	logs := captureLogs(t) // Debug level
	const tok = "serve-token-under-test-0123456789abcdef"
	d := &Daemon{project: &config.Project{Name: "t", Root: t.TempDir(), PeerName: "host", Mode: "headless", Token: tok}, version: "test"}

	if got := helloOverPipe(t, d, "wrong-token"); got != "error" {
		t.Fatalf("wrong token: %q, want error", got)
	}
	if got := helloOverPipe(t, d, tok); got != "hello_ok" {
		t.Fatalf("right token: %q, want hello_ok", got)
	}
	if strings.Contains(logs.String(), tok) {
		t.Fatal("the daemon token appears in its log")
	}
	if !strings.Contains(logs.String(), "auth failed") {
		t.Fatal("the capture saw no auth line: it is not reading the daemon's log")
	}
}
