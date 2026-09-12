// peek_blocking_test.go: peek callers that MAY wait on a pane get the whole
// screen even while a writer holds the emulator (ini-oxnl); the main-loop
// form keeps psjt's bounded try. On Linux a child in a sustained output burst
// never yields the lock inside peekTryBudget, so the try form returned
// "unreadable" for the whole burst through initech peek, patrol and the
// daemon's cross-machine peek -- callers that had no reason not to wait.
package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// heldFor takes the emulator's write lock for d on a goroutine and returns
// once it is taken. Longer than peekTryBudget, so the try form gives up while
// a blocking read still gets its answer when the hold ends.
func heldFor(t *testing.T, p *Pane, d time.Duration) {
	t.Helper()
	taken := make(chan struct{})
	go func() {
		p.emu.Lock()
		close(taken)
		time.Sleep(d)
		p.emu.Unlock()
	}()
	<-taken
}

// The two forms, side by side, against one held pane: the main-loop form
// answers inside its budget with the marker; the blocking form waits out the
// hold and answers with the screen.
func TestPeekContent_TryFormGivesUpAndBlockingFormWaits(t *testing.T) {
	p := newEmuPane("eng1", 40, 10)
	if _, err := p.emu.Write([]byte("PEEK_ME_31")); err != nil {
		t.Fatal(err)
	}
	hold := 2 * peekTryBudget
	heldFor(t, p, hold)

	start := time.Now()
	got := peekContent(p, 5)
	if !strings.Contains(got, peekUnreadable) {
		t.Errorf("main-loop peek under a held lock = %q, want the unreadable marker (psjt's guarantee is unchanged)", got)
	}
	if time.Since(start) > hold {
		t.Errorf("main-loop peek waited %v, longer than the hold; it must never block", time.Since(start))
	}

	start = time.Now()
	got = peekContentBlocking(p, 5)
	if !strings.Contains(got, "PEEK_ME_31") {
		t.Fatalf("blocking peek = %q, want the screen content once the hold ends", got)
	}
	if strings.Contains(got, peekUnreadable) {
		t.Errorf("blocking peek returned the unreadable marker; it must wait, not give up")
	}
	t.Logf("blocking peek waited %v behind a %v hold (budget %v)", time.Since(start).Round(time.Millisecond), hold, peekTryBudget)
}

// peekHost is the smallest IPCHost: one pane, no send.
type peekHost struct{ p *Pane }

func (h peekHost) FindPaneView(name string) (PaneView, bool) {
	if name == h.p.name {
		return h.p, true
	}
	return nil, true
}
func (h peekHost) AllPanes() ([]PaneInfo, bool)                     { return nil, true }
func (h peekHost) HandleSend(net.Conn, IPCRequest)                  {}
func (h peekHost) HandleExtended(net.Conn, IPCRequest, []byte) bool { return false }

// initech peek is the IPC "peek" action on a handler goroutine. Under a held
// pane its response must carry the screen, never the marker: this is the
// path a Linux user hit during a burst (v2.13.0 release run 34702544631).
func TestDispatchIPC_PeekWaitsForAHeldPaneAndReturnsContent(t *testing.T) {
	p := newEmuPane("eng1", 40, 10)
	if _, err := p.emu.Write([]byte("IPC_PEEK_44")); err != nil {
		t.Fatal(err)
	}
	heldFor(t, p, 2*peekTryBudget)

	server, client := net.Pipe()
	defer client.Close()
	go func() {
		defer server.Close()
		dispatchIPC(peekHost{p}, server, IPCRequest{Action: "peek", Target: "eng1", Lines: 5}, nil)
	}()
	scanner := bufio.NewScanner(client)
	if !scanner.Scan() {
		t.Fatal("no IPC response")
	}
	var resp IPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("peek error: %s", resp.Error)
	}
	if strings.Contains(resp.Data, peekUnreadable) {
		t.Fatalf("initech peek reported the pane unreadable under a %v hold; the IPC goroutine may wait and must return the screen", 2*peekTryBudget)
	}
	if !strings.Contains(resp.Data, "IPC_PEEK_44") {
		t.Fatalf("initech peek returned %q, want the screen content", resp.Data)
	}
}
