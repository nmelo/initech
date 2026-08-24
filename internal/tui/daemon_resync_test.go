package tui

import (
	"net"
	"strings"
	"testing"
	"time"
)

// The end-to-end half: a desynced viewer's request must produce a replay that
// ARRIVES on that viewer's stream and re-renders. Everything else in this bead
// asserts intent -- flags set, requests coalesced. This asserts delivery.
func TestDaemonResync_ReplaysRingBufferToTheRequestingViewer(t *testing.T) {
	captureLogs(t)
	d := &Daemon{
		ringBufs:   map[string]*RingBuf{},
		multiSinks: map[string]*MultiSink{},
	}
	p := &Pane{name: "eng1"}
	d.panes = []*Pane{p}

	rb := NewRingBuf(64 * 1024)
	d.ringBufs["eng1"] = rb
	// History the viewer lost: a complete, well-formed colour sequence.
	history := "\x1b[31mHISTORY-MARKER\x1b[0m\n"
	rb.Write([]byte(history))

	viewer, daemonSide := net.Pipe()
	defer viewer.Close()
	defer daemonSide.Close()
	d.registerLiveStream("window-2", "eng1", newSyncStream(daemonSide))

	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := viewer.Read(buf)
		got <- string(buf[:n])
	}()

	resp := d.handleResync("eng1", "window-2")
	if !resp.OK {
		t.Fatalf("resync refused: %q", resp.Error)
	}
	select {
	case s := <-got:
		if !strings.Contains(s, "HISTORY-MARKER") {
			t.Errorf("replay arrived without the history it exists to restore: %q", s)
		}
		if !strings.Contains(s, "\x1b[31m") {
			t.Errorf("replay lost the escape sequence, which is the whole point: %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no replay arrived on the requesting viewer's stream")
	}
}

// A resync must reach ONLY the window that asked. Replaying through the
// MultiSink would reset panes on windows that were never desynced.
func TestDaemonResync_DoesNotReplayToAWindowThatDidNotAsk(t *testing.T) {
	captureLogs(t)
	d := &Daemon{ringBufs: map[string]*RingBuf{}, multiSinks: map[string]*MultiSink{}}
	d.panes = []*Pane{{name: "eng1"}}
	rb := NewRingBuf(64 * 1024)
	d.ringBufs["eng1"] = rb
	rb.Write([]byte("HISTORY"))

	askerV, askerD := net.Pipe()
	defer askerV.Close()
	defer askerD.Close()
	otherV, otherD := net.Pipe()
	defer otherV.Close()
	defer otherD.Close()
	d.registerLiveStream("window-2", "eng1", newSyncStream(askerD))
	d.registerLiveStream("window-3", "eng1", newSyncStream(otherD))

	quiet := make(chan int, 1)
	go func() {
		buf := make([]byte, 256)
		_ = otherV.SetReadDeadline(time.Now().Add(600 * time.Millisecond))
		n, _ := otherV.Read(buf)
		quiet <- n
	}()
	go func() {
		buf := make([]byte, 256)
		askerV.Read(buf) // drain so the asker's write completes
	}()

	if resp := d.handleResync("eng1", "window-2"); !resp.OK {
		t.Fatalf("resync refused: %q", resp.Error)
	}
	if n := <-quiet; n != 0 {
		t.Errorf("window-3 received %d bytes of a replay it never asked for", n)
	}
}

// An unknown agent or a peer with no live stream must be refused, not
// silently treated as success.
func TestDaemonResync_RefusesWhenThereIsNoStreamToReplayTo(t *testing.T) {
	captureLogs(t)
	d := &Daemon{ringBufs: map[string]*RingBuf{}, multiSinks: map[string]*MultiSink{}}
	d.panes = []*Pane{{name: "eng1"}}
	d.ringBufs["eng1"] = NewRingBuf(1024)

	if resp := d.handleResync("eng1", "window-9"); resp.OK {
		t.Error("resync reported success with no stream for that peer")
	}
	if resp := d.handleResync("nosuch", "window-2"); resp.OK {
		t.Error("resync reported success for an agent that does not exist")
	}
}
