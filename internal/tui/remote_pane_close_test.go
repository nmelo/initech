package tui

import (
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// ini-p2ve.1: closing a remote pane returns promptly when the peer never
// closes its end of the stream. A yamux Close only half-closes, so readLoop
// stayed blocked until RemotePane.Close's 2 s timeout gave up on it; every
// remote pane close paid that, in the product and in each test with a peer.
func TestRemotePaneClose_ReturnsWithoutWaitingForThePeer(t *testing.T) {
	a, b := net.Pipe()
	client, err := yamux.Client(a, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := yamux.Server(b, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		s, err := server.Accept()
		if err == nil {
			accepted <- s // held open, never closed: a peer that does not answer FIN
		}
	}()
	stream, err := client.Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte{0}); err != nil { // the server learns of the stream
		t.Fatal(err)
	}
	select {
	case s := <-accepted:
		defer s.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("setup: the server never accepted the stream")
	}

	rp := NewRemotePane("eng1", "workbench", stream, nil, 80, 24)
	rp.Start()
	start := time.Now()
	rp.Close()
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("Close took %v with the peer's side still open; readLoop waited for the peer instead of being released", d)
	}
}
