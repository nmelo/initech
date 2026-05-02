package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
)

// fakeDaemonServer accepts a single connection, performs the hello handshake,
// and replies with the given HelloOKMsg. Returns the listener address. The
// goroutine exits after the first client disconnects.
func fakeDaemonServer(t *testing.T, resp tui.HelloOKMsg, expectToken string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		session, err := yamux.Server(conn, yamux.DefaultConfig())
		if err != nil {
			return
		}
		defer session.Close()

		ctrl, err := session.Accept()
		if err != nil {
			return
		}
		defer ctrl.Close()

		scanner := bufio.NewScanner(ctrl)
		if !scanner.Scan() {
			return
		}

		var hello tui.HelloMsg
		json.Unmarshal(scanner.Bytes(), &hello)

		if expectToken != "" && hello.Token != expectToken {
			errMsg := tui.ErrorMsg{Action: "error", Error: "auth failed"}
			data, _ := json.Marshal(errMsg)
			ctrl.Write(data)
			ctrl.Write([]byte("\n"))
			return
		}

		data, _ := json.Marshal(resp)
		ctrl.Write(data)
		ctrl.Write([]byte("\n"))
	}()

	return ln.Addr().String()
}

func TestDialDoctorRemote_Reachable(t *testing.T) {
	addr := fakeDaemonServer(t, tui.HelloOKMsg{
		Action:   "hello_ok",
		Version:  1,
		PeerName: "workbench",
		Agents: []tui.AgentStatus{
			{Name: "eng1", Alive: true, Activity: "running"},
			{Name: "eng2", Alive: true, Activity: "idle"},
			{Name: "eng3", Alive: true, Activity: "idle"},
		},
	}, "")

	proj := &config.Project{
		Name:     "test",
		PeerName: "laptop",
		Remotes: map[string]config.Remote{
			"workbench": {Addr: addr},
		},
	}

	res := dialDoctorRemote(proj, "workbench", net.DialTimeout)
	if !res.Connected {
		t.Errorf("Connected = false, want true (err=%q)", res.Err)
	}
	if !res.TokenValid {
		t.Errorf("TokenValid = false, want true (err=%q)", res.Err)
	}
	if res.ServerPeerName != "workbench" {
		t.Errorf("ServerPeerName = %q, want workbench", res.ServerPeerName)
	}
	if res.AgentCount != 3 {
		t.Errorf("AgentCount = %d, want 3", res.AgentCount)
	}
	if res.ProtocolVer != 1 {
		t.Errorf("ProtocolVer = %d, want 1", res.ProtocolVer)
	}
}

func TestDialDoctorRemote_AuthFailure(t *testing.T) {
	addr := fakeDaemonServer(t, tui.HelloOKMsg{
		Action: "hello_ok", Version: 1, PeerName: "workbench",
	}, "expected-token")

	proj := &config.Project{
		Name:     "test",
		PeerName: "laptop",
		Remotes: map[string]config.Remote{
			"workbench": {Addr: addr, Token: "wrong-token"},
		},
	}

	res := dialDoctorRemote(proj, "workbench", net.DialTimeout)
	if !res.Connected {
		t.Errorf("Connected = false, want true (TCP succeeds even on auth failure)")
	}
	if res.TokenValid {
		t.Error("TokenValid = true, want false")
	}
	if !strings.Contains(res.Err, "auth failed") {
		t.Errorf("Err = %q, want to contain 'auth failed'", res.Err)
	}
}

func TestDialDoctorRemote_Unreachable(t *testing.T) {
	dial := func(network, addr string, timeout time.Duration) (net.Conn, error) {
		return nil, fmt.Errorf("connection refused")
	}
	proj := &config.Project{
		Name:     "test",
		PeerName: "laptop",
		Remotes: map[string]config.Remote{
			"deadhost": {Addr: "192.0.2.1:9999"},
		},
	}

	res := dialDoctorRemote(proj, "deadhost", dial)
	if res.Connected {
		t.Error("Connected = true, want false")
	}
	if !strings.Contains(res.Err, "connection refused") {
		t.Errorf("Err = %q, want to contain 'connection refused'", res.Err)
	}
}

func TestDialDoctorRemote_NotConfigured(t *testing.T) {
	proj := &config.Project{
		Name:     "test",
		PeerName: "laptop",
		Remotes:  map[string]config.Remote{},
	}
	res := dialDoctorRemote(proj, "missing", net.DialTimeout)
	if res.Connected {
		t.Error("should not connect when remote is not configured")
	}
	if !strings.Contains(res.Err, "not configured") {
		t.Errorf("Err = %q, want to contain 'not configured'", res.Err)
	}
}

func TestFormatRemoteCheck_PassedAndFailed(t *testing.T) {
	var buf strings.Builder
	ok := formatRemoteCheck(&buf, remoteCheckResult{
		PeerName: "wb", Addr: "h:1", Connected: true, TokenValid: true,
		ServerPeerName: "wb", ProtocolVer: 1, AgentCount: 2,
	})
	if !ok {
		t.Error("expected pass")
	}
	out := buf.String()
	for _, want := range []string{"connected", "valid", "v1", "2 running"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}

	buf.Reset()
	ok = formatRemoteCheck(&buf, remoteCheckResult{
		PeerName: "wb", Addr: "h:1", Connected: false,
		Err: "dial: timeout",
	})
	if ok {
		t.Error("expected fail when not connected")
	}
}

func TestProtocolStatus(t *testing.T) {
	if protocolStatus(1) != "OK" {
		t.Error("v1 should be OK")
	}
	if protocolStatus(2) != "WARN" {
		t.Error("v2 should warn (unsupported)")
	}
}
