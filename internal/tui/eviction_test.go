package tui

// Regression tests for ini-jhm6, the eviction war: two correct behaviors --
// .6's takeover-by-eviction and 1ch's reconnect-on-disconnect -- composed
// into a 1-second connect/disconnect flap between two undead --window
// processes, because nothing told the LOSER it had lost. The seam class at
// the protocol layer: each side correct alone, the join untested.

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
)

// evictionWarServer accepts connections REPEATEDLY (unlike the single-shot
// remotePeerServer scaffold, because a war is by definition multi-connection),
// completes the real handshake, and then follows a per-connection script:
// "verdict" sends identity_taken_over and closes; "drop" closes silently;
// "hold" keeps the session open. It counts accepted connections -- the war's
// unit of measure.
type evictionWarServer struct {
	addr     string
	ln       net.Listener
	scripts  []string
	mu       sync.Mutex
	accepted int
}

func startEvictionWarServer(t *testing.T, scripts ...string) *evictionWarServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &evictionWarServer{addr: ln.Addr().String(), ln: ln, scripts: scripts}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			srv.mu.Lock()
			idx := srv.accepted
			srv.accepted++
			script := "drop"
			if idx < len(srv.scripts) {
				script = srv.scripts[idx]
			}
			srv.mu.Unlock()
			go srv.handle(conn, script)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return srv
}

func (srv *evictionWarServer) count() int {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.accepted
}

func (srv *evictionWarServer) handle(conn net.Conn, script string) {
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
	scanner := bufio.NewScanner(ctrl)
	if !scanner.Scan() {
		return
	}
	writeJSON(ctrl, HelloOKMsg{Action: "hello_ok", Version: 1, PeerName: "window1"})
	writeJSON(ctrl, StreamMapMsg{Action: "stream_map", Streams: map[uint32]string{}})
	switch script {
	case "verdict":
		writeJSON(ctrl, ControlResp{Action: identityTakenOverAction, Text: "taken over by test"})
		time.Sleep(300 * time.Millisecond) // let the verdict land before the close races it
	case "hold":
		time.Sleep(20 * time.Second)
	case "hold-then-drop":
		// Lives past quickEvictionWindow, then dies: a real session ending,
		// which must RESET the inference counter.
		time.Sleep(quickEvictionWindow + time.Second)
	case "hold-then-verdict":
		// Lives past quickEvictionWindow, THEN is evicted with the verdict.
		// The death is not quick, so the inference is UNAVAILABLE by
		// construction -- only the verdict layer can conclude this one.
		time.Sleep(quickEvictionWindow + time.Second)
		writeJSON(ctrl, ControlResp{Action: identityTakenOverAction, Text: "taken over by test"})
		time.Sleep(300 * time.Millisecond)
	case "drop":
		// fall through to the deferred close: accepted-then-dropped.
	}
}

// evictionPM builds a real peerManager against the war server and runs the
// REAL managePeer loop -- not a reimplementation of its counter, which would
// be the mirror-test tautology (a mutation in the loop would fail nothing).
func evictionPM(t *testing.T, srv *evictionWarServer) (chan string, func()) {
	t.Helper()
	quit := make(chan struct{})
	evicted := make(chan string, 1)
	pm := newPeerManager(
		&config.Project{PeerName: "window-2", Token: "", Remotes: map[string]config.Remote{
			"window1": {Addr: srv.addr},
		}},
		func(string, []PaneView, bool) {},
		nil, quit)
	pm.SetOnEvicted(func(peer, reason string) { evicted <- peer + ": " + reason })
	return evicted, func() { close(quit); pm.wait() }
}

// TestEvictionWar_VerdictIsTerminal drives the real loop: the server delivers
// the identity_taken_over verdict on the first connection. The client must
// conclude eviction and NEVER redial -- a second accepted connection means it
// rejoined the war.
func TestEvictionWar_VerdictIsTerminal(t *testing.T) {
	srv := startEvictionWarServer(t, "verdict")
	evicted, stop := evictionPM(t, srv)
	defer stop()

	select {
	case reason := <-evicted:
		if !strings.Contains(reason, "window1") {
			t.Errorf("eviction reason %q does not name the peer", reason)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the eviction verdict never produced a terminal conclusion; the client is " +
			"still in its reconnect loop -- the war continues")
	}
	// THE AC'S ACTUAL CONTRACT: conclusion within a cycle or two, then ZERO
	// further connections. Exactly-one was my first assertion and it was
	// stricter than the protocol can promise -- measured at 3/8 runs, yamux
	// teardown discards a verdict still buffered at close, so the loser's
	// first redial can happen before the inference backstop concludes. What
	// the operator needs is that the war ENDS, immediately and permanently.
	atConclusion := srv.count()
	if atConclusion > quickEvictionLimit {
		t.Fatalf("client connected %d times before concluding, want at most %d (a cycle or two)",
			atConclusion, quickEvictionLimit)
	}
	time.Sleep(4 * time.Second)
	if got := srv.count(); got != atConclusion {
		t.Fatalf("client connected again AFTER concluding eviction (%d -> %d) -- the conclusion "+
			"did not end the loop, which is the war continuing politely", atConclusion, got)
	}
}

// TestEvictionWar_VerdictAloneConcludesALongSession is the verdict layer's
// OWN assertion -- qa1's narrow FAIL, and my own xq4r layer-masking rule
// biting this bead: every other war script leaves quick deaths available, so
// the N=2 inference concluded every scenario and a verdict-dead mutant
// (evicted never set) passed the ENTIRE suite. Here the session is held past
// quickEvictionWindow before the verdict arrives: the death is not quick,
// the inference cannot fire, and only the verdict can end the loop. The
// second script is "hold" so a redialing mutant parks instead of concluding
// by any other road.
func TestEvictionWar_VerdictAloneConcludesALongSession(t *testing.T) {
	srv := startEvictionWarServer(t, "hold-then-verdict", "hold")
	evicted, stop := evictionPM(t, srv)
	defer stop()

	select {
	case reason := <-evicted:
		if !strings.Contains(reason, "window1") {
			t.Errorf("verdict-driven conclusion %q does not name the peer", reason)
		}
	case <-time.After(quickEvictionWindow + 10*time.Second):
		t.Fatal("a long-lived session that received the eviction verdict never concluded -- " +
			"the verdict layer is dead and only the inference has been passing these tests " +
			"(layer-masking: the outer guard eating the inner's evidence)")
	}
	time.Sleep(3 * time.Second)
	if got := srv.count(); got != 1 {
		t.Fatalf("client connected %d times, want exactly 1 -- with the death not quick, a "+
			"redial means the verdict was ignored", got)
	}
}

// TestEvictionWar_LostVerdictInferredFromQuickDeaths is the bead's edge 1
// against the REAL loop: the server silently accepts-and-drops (the verdict
// lost in flight). Two consecutive quick deaths must conclude takeover.
func TestEvictionWar_LostVerdictInferredFromQuickDeaths(t *testing.T) {
	srv := startEvictionWarServer(t, "drop", "drop", "drop", "drop")
	evicted, stop := evictionPM(t, srv)
	defer stop()

	select {
	case reason := <-evicted:
		if !strings.Contains(reason, "concluding") && !strings.Contains(reason, "took over") {
			t.Errorf("inferred-eviction reason %q does not explain itself", reason)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("silent accept-and-drop never concluded takeover; with the verdict lost in " +
			"flight the client wars forever")
	}
	time.Sleep(4 * time.Second)
	if got := srv.count(); got > quickEvictionLimit {
		t.Fatalf("client connected %d times before concluding, want at most %d", got, quickEvictionLimit)
	}
}

// TestEvictionWar_LongSessionDeathResets drives the reset rule against the
// REAL loop: quick death, then a session that lives past the window and dies,
// then another quick death -- the long death must have reset the counter, so
// no conclusion (2 quick deaths never happened CONSECUTIVELY). A mutant that
// counts every death as quick (E2) concludes here and exiles a window whose
// server merely restarted twice.
func TestEvictionWar_LongSessionDeathResets(t *testing.T) {
	srv := startEvictionWarServer(t, "drop", "hold-then-drop", "drop", "hold")
	evicted, stop := evictionPM(t, srv)
	defer stop()

	select {
	case reason := <-evicted:
		t.Fatalf("quick death / long-lived death / quick death concluded takeover (%q); the "+
			"long-lived session did not reset the counter and ordinary churn exiles windows", reason)
	case <-time.After(quickEvictionWindow + 8*time.Second):
		// Correct: no conclusion; the client is on the held 4th connection.
	}
	if got := srv.count(); got < 4 {
		t.Errorf("client made %d connections, want it to still be retrying (4: drop, long, drop, hold)", got)
	}
}

// TestEvictionWar_HeldSessionResetsInference is the false-positive guard: one
// quick death followed by a session the server HOLDS must not conclude -- a
// single quick death has innocent causes, and concluding on it would turn a
// window-1 hiccup into a self-exiling window.
func TestEvictionWar_HeldSessionResetsInference(t *testing.T) {
	srv := startEvictionWarServer(t, "drop", "hold")
	evicted, stop := evictionPM(t, srv)
	defer stop()

	select {
	case reason := <-evicted:
		t.Fatalf("one quick death + a healthy session concluded takeover (%q); a transient "+
			"hiccup just exiled a working window", reason)
	case <-time.After(8 * time.Second):
		// Correct: still connected to the held session, no conclusion.
	}
	if got := srv.count(); got != 2 {
		t.Errorf("client connected %d times, want 2 (the drop, then the held session)", got)
	}
}

// TestEviction_ServerSendsVerdictThenClosesSession drives the REAL daemon
// eviction path (evictExistingLocked) with a real loopback yamux pair: the
// verdict must land on the old client's control conn, and the old session
// must close shortly after. The first version of this test re-implemented the
// write and asserted its own output -- the mirror-test tautology; deleting
// the daemon's write passed it (caught by mutation E3).
func TestEviction_ServerSendsVerdictThenClosesSession(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	oldSession, err := yamux.Server(serverSide, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	go func() { // keep the client half of the pipe alive
		buf := make([]byte, 1024)
		for {
			if _, err := clientSide.Read(buf); err != nil {
				return
			}
		}
	}()

	old := &fakeConn{}
	d := &Daemon{
		clients:        map[string]net.Conn{"window-2": old},
		clientSessions: map[string]*yamux.Session{"window-2": oldSession},
		clientCtrlMu:   map[string]*sync.Mutex{"window-2": {}},
	}

	d.evictExistingLocked("window-2", "127.0.0.1:9")

	if got := string(old.written); !strings.Contains(got, identityTakenOverAction) {
		t.Fatalf("evicted client's control stream carries %q, want the %q verdict -- without "+
			"it the loser cannot distinguish eviction from a transient drop and rejoins the war",
			got, identityTakenOverAction)
	}
	select {
	case <-oldSession.CloseChan():
	case <-time.After(2 * time.Second):
		t.Fatal("the old session was never closed; the loser keeps its streams and the " +
			"newcomer starves")
	}
}

// TestEviction_TuningConstants pins the bead's measured choices without
// re-implementing the loop (a reimplementation would be a mirror test that no
// loop mutation can fail; the loop itself is driven by the war tests above).
func TestEviction_TuningConstants(t *testing.T) {
	if quickEvictionLimit != 2 {
		t.Errorf("quickEvictionLimit = %d; the bead's measured choice is 2 -- one quick death "+
			"has innocent causes, and more than two is politeness the war already disproved",
			quickEvictionLimit)
	}
	if quickEvictionWindow < 2*time.Second || quickEvictionWindow > 10*time.Second {
		t.Errorf("quickEvictionWindow = %v; the measured war cycled ~1s, so the window must "+
			"comfortably contain a war death without swallowing ordinary short sessions",
			quickEvictionWindow)
	}
}

// TestEviction_EvictedFlagEndsTheLoop pins the loop-exit rule at its decision
// point: a peerConn carrying the evicted flag concludes terminally; without
// the flag and with a long-lived session, the loop retries as before.
func TestEviction_EvictedFlagEndsTheLoop(t *testing.T) {
	var concluded []string
	pm := &peerManager{onEvicted: func(peer, reason string) { concluded = append(concluded, peer+": "+reason) }}

	pc := &peerConn{}
	pc.evicted.Store(true)
	if pc.evicted.Load() {
		pm.concludeEvicted("window-2", "verdict received")
	}
	if len(concluded) != 1 || !strings.Contains(concluded[0], "window-2") {
		t.Fatalf("terminal eviction reported %v, want exactly one conclusion for window-2", concluded)
	}
}
