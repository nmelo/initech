package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"sync"
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
	if testing.Short() {
		t.Skip("skipping slow daemon test in short mode")
	}
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

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
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
	clientSession.Close() // Terminates all streams, unblocking handleConnection.
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
	}
}

func TestDaemonIPCSend_HostRouting(t *testing.T) {
	proj := &config.Project{
		Name:     "test",
		Root:     t.TempDir(),
		PeerName: "workbench",
		Roles:    []string{"eng1"},
	}

	p := newEmuPane("eng1", 80, 24)
	d := &Daemon{
		project: proj,
		version: "test",
		panes:   []*Pane{p},
		clients: make(map[string]net.Conn),
	}

	// Use TCP loopback for client ctrl so writes have kernel buffer and
	// don't block synchronously like net.Pipe.
	ctrlLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlLn.Close()
	ctrlDial, err := net.Dial("tcp", ctrlLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlDial.Close()
	ctrlAccept, err := ctrlLn.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlAccept.Close()

	// daemon writes to ctrlAccept, test reads from ctrlDial.
	d.sessionsMu.Lock()
	d.clients["macbook"] = ctrlAccept
	d.clientCtrlMu = map[string]*sync.Mutex{"macbook": {}}
	d.sessionsMu.Unlock()

	// Simulate client: read forward_send and respond with OK.
	go func() {
		sc := bufio.NewScanner(ctrlDial)
		if sc.Scan() {
			var cmd ControlCmd
			json.Unmarshal(sc.Bytes(), &cmd)
			writeJSON(ctrlDial, ControlResp{ID: cmd.ID, OK: true})
		}
	}()

	// Read forward responses from the daemon side of the ctrl stream and
	// deliver to fwdPending (in production, handleControlStream does this).
	go func() {
		sc := bufio.NewScanner(ctrlAccept)
		for sc.Scan() {
			d.deliverForwardResp(sc.Bytes())
		}
	}()

	server, client := net.Pipe()
	defer client.Close()

	go func() {
		d.handleDaemonIPCConn(server)
		server.Close()
	}()

	// Start draining IPC response concurrently (net.Pipe is synchronous).
	respCh := make(chan IPCResponse, 1)
	go func() {
		sc := bufio.NewScanner(client)
		sc.Scan()
		var r IPCResponse
		json.Unmarshal(sc.Bytes(), &r)
		respCh <- r
	}()

	req := IPCRequest{Action: "send", Target: "super", Host: "macbook", Text: "hello from daemon", Enter: true}
	data, _ := json.Marshal(req)
	client.Write(data)
	client.Write([]byte("\n"))

	resp := <-respCh
	if !resp.OK {
		t.Errorf("IPC send should succeed, got error: %s", resp.Error)
	}

	if resp.Error != "" {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestDaemonIPCSend_HostRouting_ForwardFields(t *testing.T) {
	proj := &config.Project{
		Name:     "test",
		Root:     t.TempDir(),
		PeerName: "workbench",
		Roles:    []string{"eng1"},
	}

	d := &Daemon{
		project: proj,
		panes:   []*Pane{newEmuPane("eng1", 80, 24)},
		clients: make(map[string]net.Conn),
	}

	ctrlLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlLn.Close()
	ctrlDial, err := net.Dial("tcp", ctrlLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlDial.Close()
	ctrlAccept, err := ctrlLn.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlAccept.Close()

	d.sessionsMu.Lock()
	d.clients["macbook"] = ctrlAccept
	d.clientCtrlMu = map[string]*sync.Mutex{"macbook": {}}
	d.sessionsMu.Unlock()

	// Capture forward_send fields and respond OK.
	fwdCh := make(chan ControlCmd, 1)
	go func() {
		sc := bufio.NewScanner(ctrlDial)
		if sc.Scan() {
			var cmd ControlCmd
			json.Unmarshal(sc.Bytes(), &cmd)
			fwdCh <- cmd
			writeJSON(ctrlDial, ControlResp{ID: cmd.ID, OK: true})
		}
	}()

	// Read forward responses from daemon side ctrl stream.
	go func() {
		sc := bufio.NewScanner(ctrlAccept)
		for sc.Scan() {
			d.deliverForwardResp(sc.Bytes())
		}
	}()

	server, client := net.Pipe()
	defer client.Close()

	go func() {
		d.handleDaemonIPCConn(server)
		server.Close()
	}()

	respCh := make(chan IPCResponse, 1)
	go func() {
		sc := bufio.NewScanner(client)
		sc.Scan()
		var r IPCResponse
		json.Unmarshal(sc.Bytes(), &r)
		respCh <- r
	}()

	req := IPCRequest{Action: "send", Target: "super", Host: "macbook", Text: "hello from daemon", Enter: true}
	data, _ := json.Marshal(req)
	client.Write(data)
	client.Write([]byte("\n"))

	<-respCh

	fwd := <-fwdCh
	if fwd.Action != "forward_send" {
		t.Errorf("action = %q, want forward_send", fwd.Action)
	}
	if fwd.Target != "super" {
		t.Errorf("target = %q, want super", fwd.Target)
	}
	if fwd.Text != "hello from daemon" {
		t.Errorf("text = %q, want 'hello from daemon'", fwd.Text)
	}
}

func TestDaemonIPCSend_UnknownHost(t *testing.T) {
	proj := &config.Project{
		Name:     "test",
		Root:     t.TempDir(),
		PeerName: "workbench",
		Roles:    []string{"eng1"},
	}

	d := &Daemon{
		project: proj,
		panes:   []*Pane{newEmuPane("eng1", 80, 24)},
		clients: make(map[string]net.Conn),
	}

	server, client := net.Pipe()
	defer client.Close()

	go func() {
		d.handleDaemonIPCConn(server)
		server.Close()
	}()

	req := IPCRequest{Action: "send", Target: "super", Host: "nonexistent", Text: "msg"}
	data, _ := json.Marshal(req)
	client.Write(data)
	client.Write([]byte("\n"))

	scanner := bufio.NewScanner(client)
	scanner.Scan()
	var resp IPCResponse
	json.Unmarshal(scanner.Bytes(), &resp)
	if resp.OK {
		t.Error("send to unknown host should fail")
	}
	if resp.Error == "" {
		t.Error("should have error message")
	}
}

// TestDaemonIPCSend_AutoRouteToClient verifies that when a bare agent name
// isn't found locally, the daemon auto-routes via forward_send to connected
// TUI clients.
// TestDaemonIPCSend_HostlessNotFoundSuggestsExplicitHost verifies that a
// hostless send to a missing local pane returns a clear error suggesting
// host:agent format, instead of auto-routing nondeterministically (ini-piyb.4).
func TestDaemonIPCSend_HostlessNotFoundSuggestsExplicitHost(t *testing.T) {
	proj := &config.Project{
		Name:     "test",
		Root:     t.TempDir(),
		PeerName: "workbench",
		Roles:    []string{"eng1"},
	}

	d := &Daemon{
		project: proj,
		panes:   []*Pane{newEmuPane("eng1", 80, 24)},
		clients: make(map[string]net.Conn),
	}

	// Even with a connected peer, hostless sends should NOT auto-route.
	ctrlLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlLn.Close()
	ctrlDial, err := net.Dial("tcp", ctrlLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlDial.Close()
	ctrlAccept, err := ctrlLn.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlAccept.Close()

	d.sessionsMu.Lock()
	d.clients["macbook"] = ctrlAccept
	d.clientCtrlMu = map[string]*sync.Mutex{"macbook": {}}
	d.sessionsMu.Unlock()

	server, client := net.Pipe()
	defer client.Close()

	go func() {
		d.handleDaemonIPCConn(server)
		server.Close()
	}()

	respCh := make(chan IPCResponse, 1)
	go func() {
		sc := bufio.NewScanner(client)
		sc.Scan()
		var r IPCResponse
		json.Unmarshal(sc.Bytes(), &r)
		respCh <- r
	}()

	// Send to "super" without host. Not found locally -> error (not auto-route).
	req := IPCRequest{Action: "send", Target: "super", Text: "msg", Enter: true}
	data, _ := json.Marshal(req)
	client.Write(data)
	client.Write([]byte("\n"))

	resp := <-respCh
	if resp.OK {
		t.Fatal("hostless send to missing pane should fail, not auto-route")
	}
	if resp.Error == "" {
		t.Fatal("should have error message")
	}
	// Error should suggest using host:agent format.
	if !strings.Contains(resp.Error, "host:agent") && !strings.Contains(resp.Error, "workbench:super") {
		t.Errorf("error should suggest host:agent format, got: %s", resp.Error)
	}
}

// TestDaemonIPCSend_DeadClientTimeout verifies that forwarding to a dead
// client returns a timeout error instead of silently succeeding.
func TestDaemonIPCSend_DeadClientTimeout(t *testing.T) {
	proj := &config.Project{
		Name:     "test",
		Root:     t.TempDir(),
		PeerName: "workbench",
		Roles:    []string{"eng1"},
	}

	d := &Daemon{
		project: proj,
		panes:   []*Pane{newEmuPane("eng1", 80, 24)},
		clients: make(map[string]net.Conn),
	}

	ctrlLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlLn.Close()
	ctrlDial, err := net.Dial("tcp", ctrlLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ctrlAccept, err := ctrlLn.Accept()
	if err != nil {
		t.Fatal(err)
	}

	d.sessionsMu.Lock()
	d.clients["macbook"] = ctrlAccept
	d.clientCtrlMu = map[string]*sync.Mutex{"macbook": {}}
	d.sessionsMu.Unlock()

	// Close BOTH sides to simulate a dead client. Closing only the dial side
	// may still allow writes to kernel buffers on the accept side.
	ctrlDial.Close()
	ctrlAccept.Close()

	server, client := net.Pipe()
	defer client.Close()

	go func() {
		d.handleDaemonIPCConn(server)
		server.Close()
	}()

	respCh := make(chan IPCResponse, 1)
	go func() {
		sc := bufio.NewScanner(client)
		sc.Scan()
		var r IPCResponse
		json.Unmarshal(sc.Bytes(), &r)
		respCh <- r
	}()

	req := IPCRequest{Action: "send", Target: "super", Host: "macbook", Text: "hello", Enter: true}
	data, _ := json.Marshal(req)
	client.Write(data)
	client.Write([]byte("\n"))

	resp := <-respCh
	if resp.OK {
		t.Error("send to dead client should fail (write error)")
	}
	if resp.Error == "" {
		t.Error("should have error message for dead client")
	}
}

// TestDaemonIPCSend_MissingRemotePaneReturnsError verifies that forward_send
// to a client that reports the target pane doesn't exist returns an error
// instead of silent success (ini-piyb.1).
func TestDaemonIPCSend_MissingRemotePaneReturnsError(t *testing.T) {
	proj := &config.Project{
		Name:     "test",
		Root:     t.TempDir(),
		PeerName: "workbench",
		Roles:    []string{"eng1"},
	}

	d := &Daemon{
		project: proj,
		panes:   []*Pane{newEmuPane("eng1", 80, 24)},
		clients: make(map[string]net.Conn),
	}

	ctrlLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlLn.Close()
	ctrlDial, err := net.Dial("tcp", ctrlLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlDial.Close()
	ctrlAccept, err := ctrlLn.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer ctrlAccept.Close()

	d.sessionsMu.Lock()
	d.clients["macbook"] = ctrlAccept
	d.clientCtrlMu = map[string]*sync.Mutex{"macbook": {}}
	d.sessionsMu.Unlock()

	// Simulate client: read forward_send and respond with error (pane not found).
	go func() {
		sc := bufio.NewScanner(ctrlDial)
		if sc.Scan() {
			var cmd ControlCmd
			json.Unmarshal(sc.Bytes(), &cmd)
			writeJSON(ctrlDial, ControlResp{ID: cmd.ID, Error: "agent \"ghost\" not found"})
		}
	}()

	// Read forward responses from daemon side ctrl stream.
	go func() {
		sc := bufio.NewScanner(ctrlAccept)
		for sc.Scan() {
			d.deliverForwardResp(sc.Bytes())
		}
	}()

	server, client := net.Pipe()
	defer client.Close()

	go func() {
		d.handleDaemonIPCConn(server)
		server.Close()
	}()

	respCh := make(chan IPCResponse, 1)
	go func() {
		sc := bufio.NewScanner(client)
		sc.Scan()
		var r IPCResponse
		json.Unmarshal(sc.Bytes(), &r)
		respCh <- r
	}()

	req := IPCRequest{Action: "send", Target: "ghost", Host: "macbook", Text: "hello", Enter: true}
	data, _ := json.Marshal(req)
	client.Write(data)
	client.Write([]byte("\n"))

	resp := <-respCh
	if resp.OK {
		t.Error("send to missing remote pane should fail, got OK")
	}
	if resp.Error == "" {
		t.Error("should have error message for missing remote pane")
	}
}

func TestDaemonIPCSend_LocalDelivery(t *testing.T) {
	proj := &config.Project{
		Name:     "test",
		Root:     t.TempDir(),
		PeerName: "workbench",
		Roles:    []string{"eng1"},
	}

	p := newEmuPane("eng1", 80, 24)
	d := &Daemon{
		project: proj,
		panes:   []*Pane{p},
		clients: make(map[string]net.Conn),
	}

	server, client := net.Pipe()
	defer client.Close()

	go func() {
		d.handleDaemonIPCConn(server)
		server.Close()
	}()

	// No host = local delivery.
	req := IPCRequest{Action: "send", Target: "eng1", Text: "local msg", Enter: true}
	data, _ := json.Marshal(req)
	client.Write(data)
	client.Write([]byte("\n"))

	scanner := bufio.NewScanner(client)
	scanner.Scan()
	var resp IPCResponse
	json.Unmarshal(scanner.Bytes(), &resp)
	if !resp.OK {
		t.Errorf("local send should succeed, got: %s", resp.Error)
	}
}
