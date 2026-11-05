package tui

import (
	"context"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// memListener is a peer's listening socket in memory (ini-t0y1): Accept hands
// out the server end of a net.Pipe whose client end its Dial returns. Built
// inside a synctest bubble, every wait on it is durable, so the peer timers
// (the control request timeout, the eviction window, the reconnect backoff)
// run as written on the bubble's clock.
type memListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newMemListener() *memListener {
	return &memListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *memListener) Addr() net.Addr { return memAddr{} }

// Dial connects to this listener, as peerDial would to a real one: refused
// once the listener is closed, as a TCP dial to a closed port is.
func (l *memListener) Dial(ctx context.Context, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.done:
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	}
	client.Close()
	server.Close()
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
}

type memAddr struct{}

func (memAddr) Network() string { return "mem" }
func (memAddr) String() string  { return "mem:peer" }

// useMemPeer points peerDial at l for this test and restores the real dialer
// after it.
func useMemPeer(t *testing.T, l *memListener) {
	t.Helper()
	old := peerDial
	peerDial = l.Dial
	t.Cleanup(func() { peerDial = old })
}

// TestPeerDial_TheDefaultIsTheRealTCPDialer pins the seam's default (ini-t0y1):
// peerDial out of the box connects to a real TCP listener, with today's
// timeout and keepalive values.
func TestPeerDial_TheDefaultIsTheRealTCPDialer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
	}()
	c, err := peerDial(context.Background(), ln.Addr().String())
	if err != nil {
		t.Fatalf("the default peerDial did not reach a real listener: %v", err)
	}
	defer c.Close()
	if _, ok := c.(*net.TCPConn); !ok {
		t.Fatalf("the default peerDial gave a %T, want a TCP connection", c)
	}
	if connectTimeout != 5*time.Second || peerTCPKeepAlive != 15*time.Second {
		t.Fatalf("connectTimeout %s, keepalive %s: want 5s and 15s", connectTimeout, peerTCPKeepAlive)
	}
}

// TestPeerDial_ANilSeamDialsForReal: connectPeer falls back to the real dialer.
func TestPeerDial_ANilSeamDialsForReal(t *testing.T) {
	old := peerDial
	peerDial = nil
	t.Cleanup(func() { peerDial = old })
	srv := startRemotePeerServer(t, remotePeerServerConfig{
		firstResponse: HelloOKMsg{Action: "hello_ok", Version: ProtocolVersion, PeerName: "workbench"},
		agentNames:    []string{"eng1"},
	})
	pc, err := connectPeer("workbench", config.Remote{Addr: srv.addr, Token: "t"}, &config.Project{PeerName: "local-peer"})
	if err != nil {
		t.Fatalf("connectPeer with a nil peerDial: %v", err)
	}
	pc.Close()
}

// TestPeerTimers_KeepTheirValues pins, by value and without waiting, every
// production timer the peer, eviction and resume tests now run on a synctest
// bubble's clock (ini-t0y1): in a bubble no test would notice one changing.
func TestPeerTimers_KeepTheirValues(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want time.Duration
	}{
		{"quickEvictionWindow", quickEvictionWindow, 5 * time.Second},
		{"reconnectInitial", reconnectInitial, 1 * time.Second},
		{"reconnectMax", reconnectMax, 30 * time.Second},
		{"requestWait(interrupt)", requestWait("interrupt"), 10 * time.Second},
		{"resumeQuiesceStable", resumeQuiesceStable, 600 * time.Millisecond},
		{"resumeQuiesceCap", resumeQuiesceCap, 15 * time.Second},
		{"connectTimeout", connectTimeout, 5 * time.Second},
	} {
		if c.got != c.want {
			t.Errorf("%s = %s, want %s", c.name, c.got, c.want)
		}
	}
}
