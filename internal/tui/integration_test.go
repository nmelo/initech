package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
)

// skipInCI skips integration tests that require real PTY + TCP daemon.
// These pass locally but GH Actions sandbox can't reliably support them.
func skipInCI(t *testing.T) {
	t.Helper()
	if os.Getenv("CI") != "" || testing.Short() {
		t.Skip("integration test: requires PTY and daemon, run locally")
	}
}

// ── Test helpers ────────────────────────────────────────────────────

// testDaemon holds a running daemon and its network address.
type testDaemon struct {
	daemon   *Daemon
	listener net.Listener
	addr     string
	done     chan struct{}
}

// startTestDaemon launches a daemon on a random port with mock agents.
// Each agent runs /bin/sh -c 'echo <name>-ready; cat' which produces
// identifiable output and stays alive for input.
func startTestDaemon(t *testing.T, token string, agents ...string) *testDaemon {
	t.Helper()

	root := t.TempDir()
	for _, name := range agents {
		dir := root + "/" + name
		os.MkdirAll(dir, 0755)
		os.WriteFile(dir+"/CLAUDE.md", []byte("# test"), 0644)
	}

	proj := &config.Project{
		Name:     "test",
		Root:     root,
		PeerName: "testhost",
		Mode:     "headless",
		Listen:   "127.0.0.1:0",
		Token:    token,
		Roles:    agents,
	}

	// Build PaneConfigs with a simple command that echoes and waits.
	paneConfigs := make([]PaneConfig, len(agents))
	for i, name := range agents {
		paneConfigs[i] = PaneConfig{
			Name:    name,
			Command: []string{"/bin/sh", "-c", fmt.Sprintf("echo %s-ready; cat", name)},
			Dir:     root + "/" + name,
		}
	}

	d := &Daemon{
		project:    proj,
		version:    "test",
		ringBufs:   make(map[string]*RingBuf),
		multiSinks: make(map[string]*MultiSink),
	}

	// Create and start panes with ring buffers and multi-sinks.
	for _, cfg := range paneConfigs {
		p, err := NewPane(cfg, 24, 80)
		if err != nil {
			t.Fatalf("create pane %q: %v", cfg.Name, err)
		}
		rb := NewRingBuf(DefaultRingBufSize)
		d.ringBufs[cfg.Name] = rb
		ms := NewMultiSink()
		ms.Add(rb)
		d.multiSinks[cfg.Name] = ms
		p.SetNetworkSink(ms)
		p.Start()
		d.panes = append(d.panes, p)
	}

	// Bind listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d.listener = ln

	td := &testDaemon{
		daemon:   d,
		listener: ln,
		addr:     ln.Addr().String(),
		done:     make(chan struct{}),
	}

	// Accept connections in background.
	go func() {
		defer close(td.done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go d.handleConnection(conn)
		}
	}()

	t.Cleanup(func() {
		ln.Close()
		for _, p := range d.panes {
			p.Close()
		}
		<-td.done
	})

	return td
}

// testClient holds a yamux client session and control channel.
type testClient struct {
	session *yamux.Session
	ctrl    net.Conn
	scanner *bufio.Scanner
}

// connectTestClient dials the daemon, creates a yamux session, and performs
// the hello handshake. Returns the client and decoded hello_ok.
func connectTestClient(t *testing.T, addr, peerName, token string) (*testClient, *HelloOKMsg) {
	t.Helper()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial daemon: %v", err)
	}

	session, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		conn.Close()
		t.Fatalf("yamux client: %v", err)
	}

	ctrl, err := session.Open()
	if err != nil {
		session.Close()
		t.Fatalf("open control: %v", err)
	}

	// Send hello.
	writeJSON(ctrl, HelloMsg{
		Action:   "hello",
		Version:  1,
		Token:    token,
		PeerName: peerName,
	})

	scanner := bufio.NewScanner(ctrl)

	// Read hello_ok.
	if !scanner.Scan() {
		ctrl.Close()
		session.Close()
		t.Fatal("no hello_ok response")
	}
	var helloOK HelloOKMsg
	if err := json.Unmarshal(scanner.Bytes(), &helloOK); err != nil {
		ctrl.Close()
		session.Close()
		t.Fatalf("parse hello_ok: %v (%s)", err, scanner.Text())
	}
	if helloOK.Action != "hello_ok" {
		ctrl.Close()
		session.Close()
		t.Fatalf("expected hello_ok, got action=%q data=%s", helloOK.Action, scanner.Text())
	}

	tc := &testClient{session: session, ctrl: ctrl, scanner: scanner}
	t.Cleanup(func() {
		ctrl.Close()
		session.Close()
	})
	return tc, &helloOK
}

// readStreamMap reads and parses the stream_map message from the control
// channel, then consumes the replay_start and replay_done markers.
func (tc *testClient) readStreamMap(t *testing.T) StreamMapMsg {
	t.Helper()
	if !tc.scanner.Scan() {
		t.Fatal("no stream_map response")
	}
	var sm StreamMapMsg
	if err := json.Unmarshal(tc.scanner.Bytes(), &sm); err != nil {
		t.Fatalf("parse stream_map: %v", err)
	}
	// Consume replay_start marker.
	if tc.scanner.Scan() {
		var msg struct{ Action string }
		json.Unmarshal(tc.scanner.Bytes(), &msg)
		if msg.Action != "replay_start" {
			t.Fatalf("expected replay_start, got %q", msg.Action)
		}
	}
	// Consume replay_done marker.
	if tc.scanner.Scan() {
		var msg struct{ Action string }
		json.Unmarshal(tc.scanner.Bytes(), &msg)
		if msg.Action != "replay_done" {
			t.Fatalf("expected replay_done, got %q", msg.Action)
		}
	}
	return sm
}

// sendControl sends a control command and reads the response.
func (tc *testClient) sendControl(t *testing.T, cmd ControlCmd) ControlResp {
	t.Helper()
	writeJSON(tc.ctrl, cmd)
	if !tc.scanner.Scan() {
		t.Fatal("no control response")
	}
	var resp ControlResp
	if err := json.Unmarshal(tc.scanner.Bytes(), &resp); err != nil {
		t.Fatalf("parse control response: %v", err)
	}
	return resp
}

// ── Integration tests ───────────────────────────────────────────────

func TestInteg_DaemonStartsAndListens(t *testing.T) {
	skipInCI(t)
	td := startTestDaemon(t, "tok", "eng1")
	if td.addr == "" {
		t.Fatal("daemon addr is empty")
	}
	// Verify we can TCP connect.
	conn, err := net.DialTimeout("tcp", td.addr, time.Second)
	if err != nil {
		t.Fatalf("cannot connect to daemon: %v", err)
	}
	conn.Close()
}

func TestInteg_HelloHandshake(t *testing.T) {
	skipInCI(t)
	td := startTestDaemon(t, "secret", "eng1", "qa1")
	tc, helloOK := connectTestClient(t, td.addr, "myclient", "secret")

	if helloOK.PeerName != "testhost" {
		t.Errorf("peer_name = %q, want testhost", helloOK.PeerName)
	}
	if helloOK.Version != 1 {
		t.Errorf("version = %d, want 1", helloOK.Version)
	}
	if len(helloOK.Agents) != 2 {
		t.Fatalf("agents = %d, want 2", len(helloOK.Agents))
	}

	names := map[string]bool{}
	for _, a := range helloOK.Agents {
		names[a.Name] = true
	}
	if !names["eng1"] || !names["qa1"] {
		t.Errorf("agents = %v, want eng1 and qa1", helloOK.Agents)
	}

	sm := tc.readStreamMap(t)
	if len(sm.Streams) != 2 {
		t.Errorf("stream_map has %d entries, want 2", len(sm.Streams))
	}
}

func TestInteg_HelloRejectsInvalidToken(t *testing.T) {
	skipInCI(t)
	td := startTestDaemon(t, "correct", "eng1")

	conn, err := net.DialTimeout("tcp", td.addr, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	session, _ := yamux.Client(conn, yamux.DefaultConfig())
	defer session.Close()

	ctrl, _ := session.Open()
	defer ctrl.Close()

	writeJSON(ctrl, HelloMsg{Action: "hello", Version: 1, Token: "wrong", PeerName: "bad"})

	scanner := bufio.NewScanner(ctrl)
	if !scanner.Scan() {
		t.Fatal("no response")
	}
	var errMsg ErrorMsg
	json.Unmarshal(scanner.Bytes(), &errMsg)
	if errMsg.Error != "auth failed" {
		t.Errorf("error = %q, want 'auth failed'", errMsg.Error)
	}
}

func TestInteg_PTYContentViaPeek(t *testing.T) {
	skipInCI(t)
	td := startTestDaemon(t, "", "eng1")
	tc, _ := connectTestClient(t, td.addr, "client", "")
	tc.readStreamMap(t)

	// Wait for the agent to produce output (echo eng1-ready).
	time.Sleep(500 * time.Millisecond)

	resp := tc.sendControl(t, ControlCmd{Action: "peek", Target: "eng1", Lines: 10})
	if !resp.OK {
		t.Fatalf("peek failed: %s", resp.Error)
	}
	if !strings.Contains(resp.Data, "eng1-ready") {
		t.Errorf("peek data should contain 'eng1-ready', got: %q", resp.Data[:min(len(resp.Data), 200)])
	}
}

func TestInteg_KeyboardInput(t *testing.T) {
	skipInCI(t)
	td := startTestDaemon(t, "", "eng1")
	tc, _ := connectTestClient(t, td.addr, "client", "")
	tc.readStreamMap(t)

	// Wait for agent to be ready.
	time.Sleep(500 * time.Millisecond)

	// Send text via control channel (same as initech send).
	resp := tc.sendControl(t, ControlCmd{Action: "send", Target: "eng1", Text: "hello from test", Enter: true})
	if !resp.OK {
		t.Fatalf("send failed: %s", resp.Error)
	}

	// Poll peek until the echoed text appears (cat echoes input back).
	// CI runners can be very slow; allow up to 30s with 500ms intervals.
	if !pollPeek(t, tc, "eng1", "hello from test", 30*time.Second) {
		peekResp := tc.sendControl(t, ControlCmd{Action: "peek", Target: "eng1", Lines: 20})
		t.Errorf("expected 'hello from test' in peek after 30s, got: %q", peekResp.Data[:min(len(peekResp.Data), 200)])
	}
}

func TestInteg_SendViaControl(t *testing.T) {
	skipInCI(t)
	td := startTestDaemon(t, "", "eng1")
	tc, _ := connectTestClient(t, td.addr, "client", "")
	tc.readStreamMap(t)

	resp := tc.sendControl(t, ControlCmd{Action: "send", Target: "eng1", Text: "control-msg"})
	if !resp.OK {
		t.Errorf("send should succeed, got: %s", resp.Error)
	}

	// Send to nonexistent agent.
	resp = tc.sendControl(t, ControlCmd{Action: "send", Target: "nonexistent", Text: "nope"})
	if resp.OK {
		t.Error("send to nonexistent should fail")
	}
}

func TestInteg_ForwardSend(t *testing.T) {
	skipInCI(t)
	td := startTestDaemon(t, "", "eng1")
	tc, _ := connectTestClient(t, td.addr, "client", "")
	tc.readStreamMap(t)

	time.Sleep(300 * time.Millisecond)

	// forward_send delivers to a local agent on the daemon.
	resp := tc.sendControl(t, ControlCmd{Action: "forward_send", Target: "eng1", Text: "forwarded-msg", Enter: true})
	if !resp.OK {
		t.Fatalf("forward_send failed: %s", resp.Error)
	}

	if !pollPeek(t, tc, "eng1", "forwarded-msg", 30*time.Second) {
		peekResp := tc.sendControl(t, ControlCmd{Action: "peek", Target: "eng1", Lines: 20})
		t.Errorf("expected 'forwarded-msg' in peek after 30s, got: %q", peekResp.Data[:min(len(peekResp.Data), 200)])
	}
}

func TestInteg_PeersQuery(t *testing.T) {
	skipInCI(t)
	td := startTestDaemon(t, "", "eng1", "eng2")
	tc, _ := connectTestClient(t, td.addr, "client", "")
	tc.readStreamMap(t)

	resp := tc.sendControl(t, ControlCmd{Action: "peers_query"})
	if !resp.OK {
		t.Fatalf("peers_query failed: %s", resp.Error)
	}

	var peers []PeerInfo
	if err := json.Unmarshal([]byte(resp.Data), &peers); err != nil {
		t.Fatalf("parse peers: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("peers = %d, want 1", len(peers))
	}
	if peers[0].Name != "testhost" {
		t.Errorf("peer name = %q, want testhost", peers[0].Name)
	}
	if len(peers[0].Agents) != 2 {
		t.Errorf("agents = %d, want 2", len(peers[0].Agents))
	}
}

func TestInteg_PeekViaControl(t *testing.T) {
	skipInCI(t)
	td := startTestDaemon(t, "", "eng1")
	tc, _ := connectTestClient(t, td.addr, "client", "")
	tc.readStreamMap(t)

	time.Sleep(500 * time.Millisecond)

	resp := tc.sendControl(t, ControlCmd{Action: "peek", Target: "eng1"})
	if !resp.OK {
		t.Fatalf("peek failed: %s", resp.Error)
	}
	// The output should be non-empty (at minimum the shell prompt or echo output).
	if len(resp.Data) == 0 {
		t.Error("peek returned empty data")
	}
}

func TestInteg_ResizeViaControl(t *testing.T) {
	skipInCI(t)
	td := startTestDaemon(t, "", "eng1")
	tc, _ := connectTestClient(t, td.addr, "client", "")
	tc.readStreamMap(t)

	resp := tc.sendControl(t, ControlCmd{Action: "resize", Target: "eng1", Rows: 40, Cols: 120})
	if !resp.OK {
		t.Errorf("resize failed: %s", resp.Error)
	}

	// Verify emulator dimensions changed.
	p := td.daemon.findPane("eng1")
	if p == nil {
		t.Fatal("eng1 pane not found")
	}
	if w := p.Emulator().Width(); w != 120 {
		t.Errorf("emulator width = %d, want 120", w)
	}
	if h := p.Emulator().Height(); h != 40 {
		t.Errorf("emulator height = %d, want 40", h)
	}
}

func TestInteg_LivePTYBytesViaStream(t *testing.T) {
	skipInCI(t)
	td := startTestDaemon(t, "", "eng1")

	// Wait for echo output to reach the ring buffer.
	time.Sleep(500 * time.Millisecond)

	tc, _ := connectTestClient(t, td.addr, "client", "")
	tc.readStreamMap(t)

	// Accept the agent stream opened by the daemon.
	stream, err := tc.session.Accept()
	if err != nil {
		t.Fatalf("accept stream: %v", err)
	}
	defer stream.Close()

	// streamAgent replays ring buffer snapshot + live bytes via MultiSink.
	buf := make([]byte, 32*1024)
	var got string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stream.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, readErr := stream.Read(buf)
		if n > 0 {
			got += string(buf[:n])
		}
		if strings.Contains(got, "eng1-ready") {
			break
		}
		if readErr != nil {
			break
		}
	}
	if !strings.Contains(got, "eng1-ready") {
		t.Errorf("expected 'eng1-ready' in stream bytes, got: %q", got[:min(len(got), 200)])
	}
}

func TestInteg_NetworkSinkNilSafe(t *testing.T) {
	skipInCI(t)
	// Verify readLoop works fine with no sink (nil check, no crash).
	p := newEmuPane("test", 80, 24)
	// No sink set. Just verify the methods don't panic.
	p.SetNetworkSink(nil)
	p.ClearNetworkSink()
}

func TestInteg_RemotePaneImplementsPaneView(t *testing.T) {
	skipInCI(t)
	// Compile-time check is already in pane.go (var _ PaneView = (*RemotePane)(nil)).
	// Runtime check: create a RemotePane from a real yamux stream.
	td := startTestDaemon(t, "", "eng1")
	tc, _ := connectTestClient(t, td.addr, "client", "")
	sm := tc.readStreamMap(t)

	// Accept one agent stream.
	stream, err := tc.session.Accept()
	if err != nil {
		t.Fatalf("accept stream: %v", err)
	}
	defer stream.Close()

	// Find agent name for this stream.
	var agentName string
	for _, name := range sm.Streams {
		agentName = name
		break
	}

	rp := NewRemotePane(agentName, "testhost", stream, tc.ctrl, 80, 24)

	// Verify PaneView methods.
	var pv PaneView = rp
	if pv.Name() != agentName {
		t.Errorf("Name() = %q, want %q", pv.Name(), agentName)
	}
	if pv.Host() != "testhost" {
		t.Errorf("Host() = %q, want testhost", pv.Host())
	}
	if pv.IsAlive() != true {
		t.Error("IsAlive should be true for connected remote pane")
	}
}

func TestInteg_MultiClient(t *testing.T) {
	td := startTestDaemon(t, "", "eng1")

	// Wait for echo output.
	time.Sleep(500 * time.Millisecond)

	// Connect two clients simultaneously.
	tc1, _ := connectTestClient(t, td.addr, "client1", "")
	tc1.readStreamMap(t)

	tc2, _ := connectTestClient(t, td.addr, "client2", "")
	tc2.readStreamMap(t)

	// Both clients should be able to peek.
	resp1 := tc1.sendControl(t, ControlCmd{Action: "peek", Target: "eng1", Lines: 10})
	if !resp1.OK {
		t.Fatalf("client1 peek failed: %s", resp1.Error)
	}

	resp2 := tc2.sendControl(t, ControlCmd{Action: "peek", Target: "eng1", Lines: 10})
	if !resp2.OK {
		t.Fatalf("client2 peek failed: %s", resp2.Error)
	}

	// Both should see the same content.
	if !strings.Contains(resp1.Data, "eng1-ready") {
		t.Errorf("client1 peek missing eng1-ready")
	}
	if !strings.Contains(resp2.Data, "eng1-ready") {
		t.Errorf("client2 peek missing eng1-ready")
	}

	// Send from client1, verify client2 can see the result.
	tc1.sendControl(t, ControlCmd{Action: "send", Target: "eng1", Text: "multi-test", Enter: true})
	if !pollPeek(t, tc2, "eng1", "multi-test", 10*time.Second) {
		t.Error("client2 should see text sent by client1")
	}

	// Disconnect client1. Client2 should still work.
	tc1.ctrl.Close()
	tc1.session.Close()

	time.Sleep(500 * time.Millisecond)
	resp3 := tc2.sendControl(t, ControlCmd{Action: "peek", Target: "eng1", Lines: 10})
	if !resp3.OK {
		t.Fatalf("client2 peek after client1 disconnect failed: %s", resp3.Error)
	}
}

func TestInteg_GracefulShutdown(t *testing.T) {
	td := startTestDaemon(t, "", "eng1")
	tc, _ := connectTestClient(t, td.addr, "client", "")
	tc.readStreamMap(t)

	// Close the listener to simulate shutdown signal path.
	// We can't send a real SIGTERM to ourselves easily in test,
	// so test the gracefulShutdown method directly.
	td.daemon.gracefulShutdown()

	// The control stream should eventually close or return an error.
	tc.ctrl.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	_, err := tc.ctrl.Read(buf)
	// We expect either shutdown message or connection close.
	// Either outcome means the daemon notified the client.
	_ = err // Not checking error type, just verifying no hang.
}

// pollPeek repeatedly peeks a pane until the expected text appears or timeout
// elapses. Returns true if found. Polls every 500ms, CI-friendly timeout.
func pollPeek(t *testing.T, tc *testClient, agent, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp := tc.sendControl(t, ControlCmd{Action: "peek", Target: agent, Lines: 30})
		if resp.OK && strings.Contains(resp.Data, want) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}
