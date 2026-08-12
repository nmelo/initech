//go:build !windows

// window_server_test.go covers ini-9ka.2's in-process pane-stream listener:
// window 1 serving secondary windows while keeping direct PTY ownership.
//
// Note on temp dirs: none of these tests bind a unix socket, so t.TempDir()
// is safe here. Any test that DOES create a project root containing
// .initech/initech.sock must use a short os.MkdirTemp root instead -- see the
// comment in census_zerochange_test.go's captureCensus. The failure mode is
// silent and misleading (startup aborts, artifacts missing), so it is called
// out here too rather than left for the next person to rediscover.
package tui

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/hashicorp/yamux"

	"github.com/nmelo/initech/internal/config"
)

// windowServerTestPane builds a minimal running-ish pane the server can
// expose. No PTY is needed: these tests exercise the serving/handshake half,
// and the scope fence on this bead says to stub rather than reach into
// ini-9ka.1's roles-less-client work.
func windowServerTestPane(name string) *Pane {
	return &Pane{
		name:    name,
		emu:     vt.NewSafeEmulator(40, 10),
		alive:   true,
		visible: true,
		cfg:     PaneConfig{BeadsEnabled: true},
	}
}

func testWindowProject(listen string) *config.Project {
	return &config.Project{
		Name:         "winproj",
		Root:         "/tmp/winproj",
		PeerName:     "window1",
		WindowListen: listen,
	}
}

// startTestWindowServer starts a server on an ephemeral port and returns it
// plus its address, registering cleanup.
func startTestWindowServer(t *testing.T, panes []*Pane) (*windowServer, string) {
	t.Helper()
	ws, cleanup, err := startWindowServer(testWindowProject("127.0.0.1:0"), "test", panes, func(f func()) { go f() })
	if err != nil {
		t.Fatalf("startWindowServer: %v", err)
	}
	t.Cleanup(cleanup)
	return ws, ws.Addr()
}

// dialWindow performs the real client handshake (yamux client, control
// stream, hello) exactly as connectPeer does in production, so these tests
// exercise the shipped protocol rather than a test-only approximation.
func dialWindow(t *testing.T, addr, peerName string) (*yamux.Session, net.Conn, HelloOKMsg) {
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
	if err := writeJSON(ctrl, HelloMsg{Action: "hello", Version: 1, PeerName: peerName}); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	scanner := NewIPCScanner(ctrl)
	if !scanner.Scan() {
		t.Fatalf("no hello_ok from window server")
	}
	var ok HelloOKMsg
	if err := json.Unmarshal(scanner.Bytes(), &ok); err != nil {
		t.Fatalf("invalid hello_ok %q: %v", scanner.Bytes(), err)
	}
	ctrl.SetReadDeadline(time.Time{})
	return session, ctrl, ok
}

// TestWindowServer_NotStartedWithoutWindowListen is the zero-change guard at
// unit level: the epic claims single-window fleets see no change, and the
// mechanism for that is that an unconfigured WindowListen never starts a
// listener at all. Asserting the function refuses (rather than binding
// something harmless) keeps the guarantee structural -- there is no code path
// where a single-window fleet ends up serving.
func TestWindowServer_NotStartedWithoutWindowListen(t *testing.T) {
	panes := []*Pane{windowServerTestPane("a")}

	ws, cleanup, err := startWindowServer(testWindowProject(""), "test", panes, func(f func()) { go f() })
	if err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatal("startWindowServer succeeded with an empty WindowListen; single-window fleets must never start a listener")
	}
	if ws != nil {
		t.Error("expected no server instance when WindowListen is empty")
	}
	// The pane must be left exactly as it was -- no sink attached, since
	// attaching one is itself an observable change to a single-window fleet.
	panes[0].sinkMu.Lock()
	sink := panes[0].networkSink
	panes[0].sinkMu.Unlock()
	if sink != nil {
		t.Error("pane got a network sink despite no listener being started")
	}
}

// TestWindowServer_SecondaryWindowAttaches is the core capability: a second
// local process completes the real handshake and is told which agents exist.
func TestWindowServer_SecondaryWindowAttaches(t *testing.T) {
	panes := []*Pane{windowServerTestPane("eng1"), windowServerTestPane("eng2")}
	_, addr := startTestWindowServer(t, panes)

	session, ctrl, ok := dialWindow(t, addr, "window2")
	defer session.Close()
	defer ctrl.Close()

	if ok.Action != "hello_ok" {
		t.Fatalf("handshake action = %q, want hello_ok", ok.Action)
	}
	if ok.PeerName != "window1" {
		t.Errorf("server peer_name = %q, want window1", ok.PeerName)
	}
	got := map[string]bool{}
	for _, a := range ok.Agents {
		got[a.Name] = true
	}
	for _, want := range []string{"eng1", "eng2"} {
		if !got[want] {
			t.Errorf("attached window was not offered agent %q (got %v)", want, got)
		}
	}
}

// TestWindowServer_ExposesLocalPanesWithSinks verifies the wiring that makes
// live streaming possible at all: every served pane must get a network sink,
// because that is how PTY bytes reach an attached window. Without it the
// handshake would succeed and the window would render nothing.
func TestWindowServer_ExposesLocalPanesWithSinks(t *testing.T) {
	panes := []*Pane{windowServerTestPane("eng1"), windowServerTestPane("eng2")}
	startTestWindowServer(t, panes)

	for _, p := range panes {
		p.sinkMu.Lock()
		sink := p.networkSink
		p.sinkMu.Unlock()
		if sink == nil {
			t.Errorf("pane %q has no network sink; attached windows would receive no output", p.Name())
		}
	}
}

// TestWindowServer_DistinctPeerNamesBothStayConnected is the peer_name
// requirement from the AC. The daemon keys clients by peer_name and evicts a
// colliding one (daemon.go:545/551), which for multi-monitor would mean
// window 3 silently killing window 2's stream. Two windows presenting
// distinct names must both survive.
func TestWindowServer_DistinctPeerNamesBothStayConnected(t *testing.T) {
	panes := []*Pane{windowServerTestPane("eng1")}
	ws, addr := startTestWindowServer(t, panes)

	s2, c2, _ := dialWindow(t, addr, "window2")
	defer s2.Close()
	defer c2.Close()
	s3, c3, _ := dialWindow(t, addr, "window3")
	defer s3.Close()
	defer c3.Close()

	waitForClients(t, ws, 2)

	ws.daemon.sessionsMu.Lock()
	_, has2 := ws.daemon.clients["window2"]
	_, has3 := ws.daemon.clients["window3"]
	n := len(ws.daemon.clients)
	ws.daemon.sessionsMu.Unlock()

	if !has2 || !has3 {
		t.Errorf("both windows should be registered: window2=%v window3=%v", has2, has3)
	}
	if n != 2 {
		t.Errorf("registered client count = %d, want 2", n)
	}
}

// TestWindowServer_CollidingPeerNameEvictsPrior documents the flip side, and
// is why ini-9ka.6 must give each window a distinct identity: a second client
// reusing a peer_name replaces the first rather than joining it. Asserting it
// here means the CLI bead has a specified behavior to design against instead
// of discovering it as a bug in a live two-monitor session.
func TestWindowServer_CollidingPeerNameEvictsPrior(t *testing.T) {
	panes := []*Pane{windowServerTestPane("eng1")}
	ws, addr := startTestWindowServer(t, panes)

	s1, c1, _ := dialWindow(t, addr, "dupe")
	defer s1.Close()
	defer c1.Close()
	waitForClients(t, ws, 1)

	s2, c2, _ := dialWindow(t, addr, "dupe")
	defer s2.Close()
	defer c2.Close()

	// Still exactly one registration under that name -- the newcomer took it.
	deadline := time.Now().Add(3 * time.Second)
	var n int
	for time.Now().Before(deadline) {
		ws.daemon.sessionsMu.Lock()
		n = len(ws.daemon.clients)
		ws.daemon.sessionsMu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n != 1 {
		t.Errorf("client count with colliding peer_name = %d, want 1 (newcomer evicts prior)", n)
	}
}

// TestWindowServer_CleanupStopsListenerAndDetachesSinks covers the
// close-ends-session direction that belongs to this component: when window 1
// tears down, the listener must stop accepting and the panes must be left
// without sinks, so nothing keeps teeing PTY bytes into a server that is
// gone.
func TestWindowServer_CleanupStopsListenerAndDetachesSinks(t *testing.T) {
	panes := []*Pane{windowServerTestPane("eng1")}
	ws, cleanup, err := startWindowServer(testWindowProject("127.0.0.1:0"), "test", panes, func(f func()) { go f() })
	if err != nil {
		t.Fatalf("startWindowServer: %v", err)
	}
	addr := ws.Addr()

	cleanup()

	if _, err := net.DialTimeout("tcp", addr, 1*time.Second); err == nil {
		t.Error("listener still accepting connections after cleanup")
	}
	panes[0].sinkMu.Lock()
	sink := panes[0].networkSink
	panes[0].sinkMu.Unlock()
	if sink != nil {
		t.Error("pane still has a network sink after cleanup")
	}
}

// waitForClients blocks until the server has n registered clients or the
// deadline passes. Registration happens on the server's goroutine after the
// handshake returns, so a bare read races it.
func waitForClients(t *testing.T, ws *windowServer, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ws.daemon.sessionsMu.Lock()
		got := len(ws.daemon.clients)
		ws.daemon.sessionsMu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d registered clients", n)
}

// TestWindowServer_InputFromWindowReachesAgentPTY closes the AC's input-routing
// requirement with an end-to-end assertion rather than an inference from the
// handshake succeeding. An attached window issues a real "send" control
// command over the attach path, and the bytes are read back off the target
// pane's PTY. Attach proving the connection works says nothing about whether
// input lands, and lands on the RIGHT agent -- so the pane is one of two and
// the other is checked for silence.
func TestWindowServer_InputFromWindowReachesAgentPTY(t *testing.T) {
	target, targetRead := paneWithPipe(t)
	target.name = "eng1"
	target.emu = vt.NewSafeEmulator(40, 10)
	target.alive = true
	drainEmulator(t, target)

	other, otherRead := paneWithPipe(t)
	other.name = "eng2"
	other.emu = vt.NewSafeEmulator(40, 10)
	other.alive = true
	drainEmulator(t, other)

	_, addr := startTestWindowServer(t, []*Pane{target, other})
	session, ctrl, _ := dialWindow(t, addr, "window2")
	defer session.Close()
	defer ctrl.Close()

	if err := writeJSON(ctrl, ControlCmd{ID: "1", Action: "send", Target: "eng1", Text: "PING", Enter: false}); err != nil {
		t.Fatalf("send control cmd: %v", err)
	}

	// Read the response so a protocol-level rejection surfaces as a clear
	// failure instead of an empty-read timeout further down.
	ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	scanner := NewIPCScanner(ctrl)
	if !scanner.Scan() {
		t.Fatal("no response to send command")
	}
	var resp ControlResp
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("invalid control response %q: %v", scanner.Bytes(), err)
	}
	if resp.Error != "" {
		t.Fatalf("send rejected: %s", resp.Error)
	}

	got := readAvailable(t, targetRead, "PING", 5*time.Second)
	if !strings.Contains(got, "PING") {
		t.Errorf("target pane PTY received %q, want it to contain PING (input from the attached window did not reach the agent)", got)
	}

	// Wrong-agent guard: eng2 must not have received it. Non-blocking read
	// with a short deadline -- an empty result is the pass here.
	otherRead.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 64)
	if n, _ := otherRead.Read(buf); n > 0 && strings.Contains(string(buf[:n]), "PING") {
		t.Errorf("non-target pane eng2 also received the input (%q); input must route to the addressed agent only", buf[:n])
	}
}

// readAvailable reads from r until want appears or the deadline passes.
func readAvailable(t *testing.T, r *os.File, want string, d time.Duration) string {
	t.Helper()
	var acc strings.Builder
	deadline := time.Now().Add(d)
	buf := make([]byte, 256)
	for time.Now().Before(deadline) {
		r.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, err := r.Read(buf)
		if n > 0 {
			acc.Write(buf[:n])
			if strings.Contains(acc.String(), want) {
				return acc.String()
			}
		}
		if err != nil && n == 0 {
			continue
		}
	}
	return acc.String()
}

// drainEmulator consumes the emulator's response pipe in the background.
// Required for any pane built by hand rather than via Start(): the default
// send path calls emu.SendKey before writing to the PTY, the response pipe is
// a SYNCHRONOUS io.Pipe, and in production responseLoop is what drains it.
// Without a drain the send blocks forever holding sendMu, and the symptom --
// the PTY never receives the text -- looks exactly like input routing being
// broken rather than the pane being under-constructed.
func drainEmulator(t *testing.T, p *Pane) {
	t.Helper()
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := p.emu.Read(buf); err != nil {
				return
			}
		}
	}()
}
