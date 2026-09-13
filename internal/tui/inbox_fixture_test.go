package tui

// inbox_fixture_test.go owns every goroutine an inbox test starts, and joins
// them BEFORE the test's temp root is removed (ini-yxlh).
//
// THE THIRD INSTANCE OF ONE SHAPE, SO A FIXTURE AND NOT A THIRD PATCH. An
// inbox test starts a goroutine -- a reply delivery, a window-1 control
// stream, a parked wake -- that outlives the test body, and it writes
// inbox.yaml while t.TempDir's RemoveAll walks the same directory: "directory
// not empty" / unlinkat, load-dependent, green locally and red in CI. ini-5rvq
// joined deliveries inside replyTUI; 87b17a0 released one test's wake inside
// its body; then TestInboxRoute_ReplyFromAChildWindowIsWrittenByWindowOne held
// the v2.14.1 tag, because the route helpers built TUIs no join knew about.
// Each fix was correct and each covered only the constructor it was in.
//
// ONE OWNER PER TEST. inboxRoot, replyTUI and the route helpers all reach the
// same fixture through inboxFixtureFor(t), so a test cannot build a TUI or a
// link the join does not see -- the helper that makes the thing is the helper
// that registers it.
//
// THE JOIN ORDER IS THE HAPPENS-BEFORE EDGE, not a sleep:
//  1. t.Cleanup runs last-in-first-out, and the fixture's join is registered
//     immediately after t.TempDir. So every cleanup the TEST adds -- a
//     close(release) that unblocks a parked wake -- runs before the join, and
//     the join runs before RemoveAll.
//  2. Close every control link, then wait the control-stream goroutines. A
//     stream mid-apply finishes that apply first, which is where a child's
//     routed reply calls inboxDeliveries.Add on window 1.
//  3. Only then wait each TUI's inboxDeliveries. Nothing left can Add, so the
//     Wait cannot race a new delivery.
//
// The 10s deadline names a goroutine that never finishes; it is not the
// synchronization. Out of scope on purpose: the doorbell broadcast, which
// writes to a control connection and never to the root.
import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type inboxFixture struct {
	t    *testing.T
	root string

	mu      sync.Mutex
	tuis    []*TUI
	closers []func()
	streams sync.WaitGroup
}

var (
	inboxFixturesMu sync.Mutex
	inboxFixtures   = map[*testing.T]*inboxFixture{}
)

// inboxFixtureFor returns this test's fixture, creating the root and
// registering the join on first use.
func inboxFixtureFor(t *testing.T) *inboxFixture {
	t.Helper()
	inboxFixturesMu.Lock()
	defer inboxFixturesMu.Unlock()
	if f, ok := inboxFixtures[t]; ok {
		return f
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f := &inboxFixture{t: t, root: root}
	inboxFixtures[t] = f
	// Registered IMMEDIATELY after TempDir: see the ordering note above.
	t.Cleanup(func() {
		f.join()
		inboxFixturesMu.Lock()
		delete(inboxFixtures, t)
		inboxFixturesMu.Unlock()
	})
	return f
}

// track registers a TUI whose deliveries the join must wait on.
func (f *inboxFixture) track(tui *TUI) *TUI {
	f.mu.Lock()
	f.tuis = append(f.tuis, tui)
	f.mu.Unlock()
	return tui
}

// linkDaemon connects child to a window-1 daemon over a pipe, and owns the
// control-stream goroutine it starts.
func (f *inboxFixture) linkDaemon(child *TUI, d *Daemon) {
	clientConn, serverConn := net.Pipe()
	f.mu.Lock()
	f.closers = append(f.closers, func() { clientConn.Close(); serverConn.Close() })
	f.mu.Unlock()
	f.streams.Add(1)
	go func() {
		defer f.streams.Done()
		d.handleControlStream(serverConn, bufio.NewScanner(serverConn), "window-2")
	}()
	// No stream and no Start(): this pane exists to carry the mux, which is how
	// windowOneMux finds the connection to window 1.
	child.panes = append(child.panes, NewRemotePane("eng1", "window1", nil, NewControlMux(clientConn), 80, 24))
}

func (f *inboxFixture) join() {
	f.mu.Lock()
	closers := append([]func(){}, f.closers...)
	tuis := append([]*TUI{}, f.tuis...)
	f.mu.Unlock()
	for _, c := range closers {
		c()
	}
	done := make(chan struct{})
	go func() {
		f.streams.Wait()
		for _, tui := range tuis {
			tui.inboxDeliveries.Wait()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		f.t.Error("an inbox goroutine never finished; it would still be writing when the temp dir is removed")
	}
}
