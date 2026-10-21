package tui

// ini-c5jv: a stream window 1 opens after attach is bound to the agent its
// stream_added names BY STREAM ID, never by arrival order. Before the fix the
// viewer took the next stream off the session and gave it the announced
// name, so one lost announcement bound agent A's stream to agent B, and keys
// typed into the "B" pane went to A.

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
)

// bindRig is a viewer connected to a fake window 1 that the test drives
// after the handshake: it opens streams and announces them.
type bindRig struct {
	session *yamux.Session // window 1's side
	ctrl    net.Conn       // window 1's side of the control stream
	pc      *peerConn
	pm      *peerManager

	mu     sync.Mutex
	panes  map[string]PaneView
	opened []net.Conn // window 1's ends of the streams it opened
}

func newBindRig(t *testing.T) *bindRig {
	t.Helper()
	r := &bindRig{panes: map[string]PaneView{}}
	ready := make(chan struct{})
	srv := startRemotePeerServer(t, remotePeerServerConfig{
		firstResponse: HelloOKMsg{Action: "hello_ok", Version: ProtocolVersion, PeerName: "window1"},
		agentNames:    []string{"eng0"},
		afterMap: func(s *yamux.Session, c net.Conn) {
			r.session, r.ctrl = s, c
			close(ready)
		},
	})
	pc, err := connectPeer("window1", config.Remote{Addr: srv.addr, Token: "t"}, &config.Project{PeerName: "window2"})
	if err != nil {
		t.Fatalf("connectPeer: %v", err)
	}
	<-ready
	quit, done := make(chan struct{}), make(chan struct{})
	pm := &peerManager{quit: quit, onPaneAdded: func(_ string, p PaneView) {
		r.mu.Lock()
		r.panes[p.Name()] = p
		r.mu.Unlock()
	}, onPaneRemoved: func(_, name string) {
		r.mu.Lock()
		delete(r.panes, name)
		r.mu.Unlock()
	}}
	r.pc, r.pm = pc, pm
	go pm.consumeEvents("window1", pc, done)
	t.Cleanup(func() {
		close(done)
		// Window 1's ends first: a pane's read loop exits only when the far
		// end closes, so closing panes first waits out Close's 2s bound.
		r.mu.Lock()
		for _, c := range r.opened {
			c.Close()
		}
		r.mu.Unlock()
		srv.Close()
		for _, p := range pc.panes {
			if rp, ok := p.(*RemotePane); ok {
				rp.Close()
			}
		}
		pc.Close()
	})
	return r
}

// served is a stream window 1 opened, and what the viewer typed into it.
type served struct {
	id   uint32
	conn net.Conn
	got  chan byte
}

// open opens a stream as window 1 does for a new agent, and collects every
// byte the viewer writes up it.
func (r *bindRig) open(t *testing.T) *served {
	t.Helper()
	s, err := r.session.OpenStream()
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	sv := &served{id: s.StreamID(), conn: s, got: make(chan byte, 64)}
	r.mu.Lock()
	r.opened = append(r.opened, s)
	r.mu.Unlock()
	go func() {
		// Closes its end when the viewer closes its own, as the daemon's
		// streamAgentLive does; otherwise the viewer's Close waits out its
		// 2s bound for a read loop that never ends.
		defer s.Close()
		buf := make([]byte, 64)
		for {
			n, err := s.Read(buf)
			for _, c := range buf[:n] {
				sv.got <- c
			}
			if err != nil {
				return
			}
		}
	}()
	return sv
}

func (r *bindRig) announce(t *testing.T, id uint32, name string) {
	t.Helper()
	if err := writeJSON(r.ctrl, StreamAddedMsg{Action: "stream_added", StreamID: id, Name: name}); err != nil {
		t.Fatalf("announce %s: %v", name, err)
	}
}

func (r *bindRig) pane(name string) *RemotePane {
	r.mu.Lock()
	defer r.mu.Unlock()
	rp, _ := r.panes[name].(*RemotePane)
	return rp
}

func (r *bindRig) waitPane(t *testing.T, name string) *RemotePane {
	t.Helper()
	var rp *RemotePane
	waitFor(t, 5*time.Second, func() bool { rp = r.pane(name); return rp != nil }, "no pane "+name+" was created")
	return rp
}

func typeRune(rp *RemotePane, c rune) {
	rp.SendKey(tcell.NewEventKey(tcell.KeyRune, c, tcell.ModNone))
}

// landsOn reports which of the streams received c within a second.
func landsOn(c byte, streams ...*served) *served {
	deadline := time.After(time.Second)
	for {
		for _, s := range streams {
			select {
			case b := <-s.got:
				if b == c {
					return s
				}
			default:
			}
		}
		select {
		case <-deadline:
			return nil
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// AC 1: A's announcement is lost and B's arrives. B's pane is wired to B's
// stream, so keys typed into it reach B; A gets no pane until its own
// announcement arrives, and then it is wired to A.
func TestStreamBind_ALostAnnouncementNeverBindsAnotherAgentsStream(t *testing.T) {
	r := newBindRig(t)
	a := r.open(t)
	b := r.open(t)
	r.announce(t, b.id, "engb") // A's stream_added never arrives

	pb := r.waitPane(t, "engb")
	typeRune(pb, 'Z')
	switch landsOn('Z', a, b) {
	case b:
	case a:
		t.Fatal(`a key typed into "engb" reached the stream window 1 opened for another agent`)
	default:
		t.Fatal(`a key typed into "engb" reached neither stream`)
	}
	if r.pane("enga") != nil {
		t.Fatal("a pane exists for an agent whose announcement never arrived")
	}

	r.announce(t, a.id, "enga")
	pa := r.waitPane(t, "enga")
	typeRune(pa, 'Y')
	if landsOn('Y', a, b) != a {
		t.Fatal(`once its announcement arrived, "enga" was not wired to its own stream`)
	}
}

// AC 1, the unannounced half: a stream nobody announces is logged and closed
// once the bound passes, and no pane is made for it.
func TestStreamBind_AnUnannouncedStreamIsLoggedAndClosedAfterTheBound(t *testing.T) {
	orig := streamBindTimeout
	streamBindTimeout = 200 * time.Millisecond
	t.Cleanup(func() { streamBindTimeout = orig })
	logs := captureLogs(t)

	r := newBindRig(t)
	a := r.open(t)
	closed := make(chan struct{})
	go func() {
		buf := make([]byte, 8)
		for {
			if _, err := a.conn.Read(buf); err != nil {
				close(closed)
				return
			}
		}
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the unannounced stream was never closed")
	}
	if !strings.Contains(logs.String(), "stream never announced") {
		t.Fatalf("the unannounced stream was not logged:\n%s", logs.String())
	}
	r.mu.Lock()
	n := len(r.panes)
	r.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d pane(s) were made for a stream with no announcement", n)
	}
}

// binderRecorder records every pane a binder creates.
type binderRecorder struct {
	mu    sync.Mutex
	bound map[string]net.Conn
	calls int
}

func (r *binderRecorder) bind(name string, c net.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bound == nil {
		r.bound = map[string]net.Conn{}
	}
	r.bound[name] = c
	r.calls++
}

func (r *binderRecorder) get(name string) net.Conn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bound[name]
}

// isClosed reports whether c was closed by our side: a write fails with
// io.ErrClosedPipe. An open pipe with no reader times out instead.
func isClosed(c net.Conn) bool {
	c.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	_, err := c.Write([]byte("x"))
	return errors.Is(err, io.ErrClosedPipe)
}

func pipeConn(t *testing.T) net.Conn {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	return a
}

// AC 2: each announcement binds the stream with ITS id, whichever arrives
// first and however the two agents' halves interleave.
func TestStreamBind_EveryArrivalOrderBindsByID(t *testing.T) {
	type step struct {
		stream bool // true: the stream arrives; false: the announcement
		id     uint32
	}
	names := map[uint32]string{4: "enga", 6: "engb"}
	orders := map[string][]step{
		"announcement first":       {{false, 4}, {true, 4}},
		"stream first":             {{true, 4}, {false, 4}},
		"both streams, then names": {{true, 4}, {true, 6}, {false, 6}, {false, 4}},
		"both names, then streams": {{false, 6}, {false, 4}, {true, 4}, {true, 6}},
		"interleaved, crossed":     {{false, 4}, {true, 6}, {false, 6}, {true, 4}},
		"interleaved, reversed":    {{true, 6}, {false, 4}, {true, 4}, {false, 6}},
	}
	for name, order := range orders {
		t.Run(name, func(t *testing.T) {
			rec := &binderRecorder{}
			b := newStreamBinder("window1", rec.bind)
			conns := map[uint32]net.Conn{}
			for _, s := range order {
				if s.stream {
					conns[s.id] = pipeConn(t)
					b.offerStream(s.id, conns[s.id])
				} else {
					b.announce(s.id, names[s.id])
				}
			}
			for id, c := range conns {
				if got := rec.get(names[id]); got != c {
					t.Errorf("%s is bound to the wrong stream (or none)", names[id])
				}
			}
			if rec.calls != len(conns) {
				t.Errorf("%d panes made, want %d", rec.calls, len(conns))
			}
		})
	}
}

// Feed it nothing: an empty name, a stream id of zero, and a second
// announcement or stream for an id already bound are refused, and nothing
// is ever rebound.
func TestStreamBind_EmptyZeroAndAlreadyBoundAreRefused(t *testing.T) {
	logs := captureLogs(t)
	rec := &binderRecorder{}
	b := newStreamBinder("window1", rec.bind)

	b.announce(4, "")
	b.offerStream(4, pipeConn(t))
	b.announce(0, "engz")
	zero := pipeConn(t)
	b.offerStream(0, zero)
	if rec.calls != 0 {
		t.Fatalf("an empty name or a zero id made %d pane(s)", rec.calls)
	}
	if !isClosed(zero) {
		t.Error("a stream with id 0 was kept open")
	}

	b.announce(4, "enga") // the stream for 4 is already waiting
	first := rec.get("enga")
	if first == nil {
		t.Fatal("setup: enga did not bind")
	}
	b.announce(4, "engb")
	second := pipeConn(t)
	b.offerStream(4, second)
	if rec.calls != 1 || rec.get("enga") != first || rec.get("engb") != nil {
		t.Fatalf("an id already bound was rebound: %d pane(s)", rec.calls)
	}
	if !isClosed(second) {
		t.Error("a second stream for a bound id was kept open")
	}
	for _, want := range []string{"needs a name and a stream id", "stream with id 0 refused", "id already bound", "second stream for an already bound id"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("no log line %q", want)
		}
	}
}

// An agent removed before its stream arrived gets no pane when the stream
// does, and the stream waits out the bound like any unannounced one.
func TestStreamBind_ARemovedAgentsLateStreamMakesNoPane(t *testing.T) {
	rec := &binderRecorder{}
	b := newStreamBinder("window1", rec.bind)
	b.announce(4, "enga")
	b.forget("enga")
	b.offerStream(4, pipeConn(t))
	if rec.calls != 0 {
		t.Fatal("a removed agent's late stream made a pane")
	}
}

// AC 3: a burst of status events that overflows the events buffer while a
// stream_added is in flight does not lose it: the pane binds to its stream.
func TestStreamBind_AStatusBurstDoesNotLoseAnAnnouncement(t *testing.T) {
	r := newBindRig(t)
	hold := make(chan struct{})
	r.pm.onAgentStatus = func(string, []string, string, string, string, WaitingState, bool) { <-hold }

	writeStatus := func() {
		if err := writeJSON(r.ctrl, ControlResp{Action: agentStatusAction, Name: "eng0"}); err != nil {
			t.Fatalf("status: %v", err)
		}
	}
	writeStatus() // the consumer takes this one and blocks in it
	time.Sleep(50 * time.Millisecond)
	s := r.open(t)
	r.announce(t, s.id, "engs")
	for i := 0; i < 40; i++ {
		writeStatus()
	}
	time.Sleep(100 * time.Millisecond) // let the reader overflow the buffer
	close(hold)

	p := r.waitPane(t, "engs")
	typeRune(p, 'Q')
	if landsOn('Q', s) != s {
		t.Fatal("the announced agent's pane is not wired to its stream")
	}
}

// An agent announced and then removed leaves no pane in the viewer: the
// announcement, on its own queue, is still handled before the removal that
// followed it on the wire. The consumer is held in a status event while each
// pair queues, so both queues are ready at once when it looks; a consumer
// that does not take announcements first then picks the removal half the
// time, and six rounds make that show.
func TestStreamBind_AnAddThenRemoveLeavesNoPane(t *testing.T) {
	r := newBindRig(t)
	var hold chan struct{}
	var holdMu sync.Mutex
	r.pm.onAgentStatus = func(string, []string, string, string, string, WaitingState, bool) {
		holdMu.Lock()
		h := hold
		holdMu.Unlock()
		<-h
	}
	for round := 0; round < 6; round++ {
		holdMu.Lock()
		hold = make(chan struct{})
		h := hold
		holdMu.Unlock()
		if err := writeJSON(r.ctrl, ControlResp{Action: agentStatusAction, Name: "eng0"}); err != nil {
			t.Fatalf("status: %v", err)
		}
		time.Sleep(30 * time.Millisecond) // the consumer is now inside the status event
		for i := 0; i < 2; i++ {
			name := fmt.Sprintf("eng%d_%d", round, i)
			s := r.open(t)
			r.announce(t, s.id, name)
			if err := writeJSON(r.ctrl, ControlResp{Action: "agent_removed", Name: name}); err != nil {
				t.Fatalf("agent_removed: %v", err)
			}
		}
		time.Sleep(30 * time.Millisecond) // both queues are full before the consumer returns
		close(h)
		time.Sleep(100 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.panes) != 0 {
		var left []string
		for n := range r.panes {
			left = append(left, n)
		}
		t.Fatalf("panes for removed agents survived: %v", left)
	}
}
