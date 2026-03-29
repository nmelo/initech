package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
)

func TestDaemonHelloHandshake(t *testing.T) {
	// Create a daemon with mock agents (INITECH_MOCK_AGENT=cat for simple PTY).
	proj := &config.Project{
		Name:     "test",
		Root:     t.TempDir(),
		PeerName: "testhost",
		Mode:     "headless",
		Listen:   "127.0.0.1:0", // OS picks a free port.
		Token:    "test-token",
		Roles:    []string{"eng1"},
	}

	d := &Daemon{
		project: proj,
		version: "test",
	}

	// Create a pipe pair for testing (no real TCP needed).
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	// Run daemon handler in background.
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	// Client side: create yamux client session.
	clientSession, err := yamux.Client(clientConn, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux client init: %v", err)
	}
	defer clientSession.Close()

	// Open control stream.
	ctrl, err := clientSession.Open()
	if err != nil {
		t.Fatalf("open control stream: %v", err)
	}
	defer ctrl.Close()

	// Send hello.
	hello := HelloMsg{
		Action:   "hello",
		Version:  1,
		Token:    "test-token",
		PeerName: "testclient",
	}
	data, _ := json.Marshal(hello)
	ctrl.Write(data)
	ctrl.Write([]byte("\n"))

	// Read hello_ok.
	scanner := bufio.NewScanner(ctrl)
	if !scanner.Scan() {
		t.Fatal("no hello_ok response")
	}
	var helloOK HelloOKMsg
	if err := json.Unmarshal(scanner.Bytes(), &helloOK); err != nil {
		t.Fatalf("parse hello_ok: %v", err)
	}
	if helloOK.Action != "hello_ok" {
		t.Errorf("action = %q, want hello_ok", helloOK.Action)
	}
	if helloOK.PeerName != "testhost" {
		t.Errorf("peer_name = %q, want testhost", helloOK.PeerName)
	}
	if helloOK.Version != 1 {
		t.Errorf("version = %d, want 1", helloOK.Version)
	}
}

func TestDaemonAuthFailure(t *testing.T) {
	proj := &config.Project{
		Name:     "test",
		Root:     t.TempDir(),
		PeerName: "testhost",
		Mode:     "headless",
		Listen:   "127.0.0.1:0",
		Token:    "correct-token",
		Roles:    []string{"eng1"},
	}

	d := &Daemon{project: proj, version: "test"}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientSession, _ := yamux.Client(clientConn, yamux.DefaultConfig())
	defer clientSession.Close()

	ctrl, _ := clientSession.Open()
	defer ctrl.Close()

	// Send hello with wrong token.
	hello := HelloMsg{Action: "hello", Version: 1, Token: "wrong-token", PeerName: "bad"}
	data, _ := json.Marshal(hello)
	ctrl.Write(data)
	ctrl.Write([]byte("\n"))

	scanner := bufio.NewScanner(ctrl)
	if !scanner.Scan() {
		t.Fatal("no response")
	}
	var errMsg ErrorMsg
	json.Unmarshal(scanner.Bytes(), &errMsg)
	if errMsg.Action != "error" || errMsg.Error != "auth failed" {
		t.Errorf("expected auth failed, got %+v", errMsg)
	}

	// Wait for handler to finish.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("handler didn't finish after auth failure")
	}
}

func TestDaemonControlSend(t *testing.T) {
	proj := &config.Project{
		Name:     "test",
		Root:     t.TempDir(),
		PeerName: "testhost",
		Mode:     "headless",
		Token:    "",
		Roles:    []string{"eng1"},
	}

	// Create a real pane for the daemon to manage.
	p := newEmuPane("eng1", 80, 24)
	d := &Daemon{
		project: proj,
		version: "test",
		panes:   []*Pane{p},
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientSession, _ := yamux.Client(clientConn, yamux.DefaultConfig())
	defer clientSession.Close()

	ctrl, _ := clientSession.Open()

	// Hello (no token).
	hello := HelloMsg{Action: "hello", Version: 1, PeerName: "client"}
	data, _ := json.Marshal(hello)
	ctrl.Write(data)
	ctrl.Write([]byte("\n"))

	scanner := bufio.NewScanner(ctrl)
	// Read hello_ok.
	scanner.Scan()
	// Read stream_map.
	scanner.Scan()
	// Read replay_start.
	scanner.Scan()
	// Read replay_done.
	scanner.Scan()

	// Send a control command: peek.
	peek := ControlCmd{Action: "peek", Target: "eng1", Lines: 5}
	data, _ = json.Marshal(peek)
	ctrl.Write(data)
	ctrl.Write([]byte("\n"))

	if !scanner.Scan() {
		t.Fatal("no peek response")
	}
	var resp ControlResp
	json.Unmarshal(scanner.Bytes(), &resp)
	if !resp.OK {
		t.Errorf("peek should succeed, got error: %s", resp.Error)
	}

	// Send to unknown agent.
	bad := ControlCmd{Action: "send", Target: "nonexistent", Text: "hi"}
	data, _ = json.Marshal(bad)
	ctrl.Write(data)
	ctrl.Write([]byte("\n"))

	scanner.Scan()
	json.Unmarshal(scanner.Bytes(), &resp)
	if resp.OK {
		t.Error("send to nonexistent agent should fail")
	}

	ctrl.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}
