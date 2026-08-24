package tui

import (
	"net"
	"strings"
	"testing"
	"time"
)

// resyncPane builds a RemotePane without starting readLoop, so a test drives
// the state it measures instead of racing a reader.
//
// THE PREDECESSOR CELL RACED IT AND THAT IS WHY IT WAS QUARANTINED (ini-dr03,
// premise falsified): it wrote chunks over a net.Pipe and asserted an exact
// eviction index, on the stated belief that "each server.Write blocks until
// readLoop's matching stream.Read AND ITS SEND-OR-DROP completes". net.Pipe's
// Write unblocks when a READ CONSUMES THE BYTES; it does not wait for what the
// reader does next. So the cell never controlled how many chunks sat in the
// channel at eviction, only the order bytes reached Read -- it held while the
// scheduler cooperated and failed under package load. Measured, not argued:
// eng1 found it RED with ini-a9d8 reverted out of the tree entirely, so the
// commit that exposed it was not the commit that broke it.
func resyncPane(t *testing.T) *RemotePane {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	ctrlS, ctrlC := net.Pipe()
	t.Cleanup(func() { ctrlS.Close(); ctrlC.Close() })
	return NewRemotePane("eng1", "wb", client, NewControlMux(ctrlC), 80, 24)
}

// fillDataCh puts n chunks into dataCh directly. Deterministic by construction:
// the channel's state is set, not raced into existence.
func fillDataCh(rp *RemotePane, n int) {
	for i := 0; i < n; i++ {
		rp.dataCh <- []byte("x")
	}
}

// An evicted chunk must mark the pane desynced. This is the inverted assertion:
// the old cell proved the eviction TEARS a sequence; the fix means an eviction
// is now recorded and recovered from instead.
func TestRemotePane_EvictionMarksDesyncedInsteadOfTearingSilently(t *testing.T) {
	rp := resyncPane(t)
	captureLogs(t)
	fillDataCh(rp, cap(rp.dataCh))

	rp.noteDesync()

	rp.mu.Lock()
	pending, reset := rp.resyncPending, rp.resetPending
	rp.mu.Unlock()
	if !pending {
		t.Error("eviction did not request a resync: the chunk is gone and nothing recovers it")
	}
	if !reset {
		t.Error("eviction did not ask the main goroutine to reset; a replay would land on top of stale bytes")
	}
}

// COALESCING. A burst evicts many chunks and every replay is a full ring
// snapshot written back down the same stream -- one request per eviction would
// sustain the condition it is recovering from.
func TestRemotePane_BurstOfEvictionsRequestsExactlyOneResync(t *testing.T) {
	rp := resyncPane(t)
	captureLogs(t)
	for i := 0; i < 200; i++ {
		rp.noteDesync()
	}
	rp.mu.Lock()
	last := rp.lastResync
	rp.mu.Unlock()
	// A second request inside the floor must be refused. lastResync moving
	// would mean each eviction re-armed it.
	time.Sleep(20 * time.Millisecond)
	rp.noteDesync()
	rp.mu.Lock()
	again := rp.lastResync
	rp.mu.Unlock()
	if !again.Equal(last) {
		t.Errorf("a burst re-armed the resync clock: %v -> %v; that is a request per eviction", last, again)
	}
}

// The reset must DISCARD what is buffered and clear the screen, so the replay
// is the whole truth rather than history applied over newer bytes.
func TestRemotePane_ResetDiscardsBufferedChunksAndClearsTheScreen(t *testing.T) {
	rp := resyncPane(t)
	captureLogs(t)

	rp.writeEmu([]byte("STALE-CONTENT"))
	if !strings.Contains(rp.emu.Render(), "STALE-CONTENT") {
		t.Fatal("fixture failed: the emulator never showed the stale content it is supposed to lose")
	}
	fillDataCh(rp, 10)
	rp.mu.Lock()
	rp.resetPending = true
	rp.mu.Unlock()

	rp.DrainData()

	if n := len(rp.dataCh); n != 0 {
		t.Errorf("reset left %d buffered chunks; they would apply after the replay", n)
	}
	if got := rp.emu.Render(); strings.Contains(got, "STALE-CONTENT") {
		t.Errorf("reset did not clear the screen, so the replay lands on top of older bytes: %q", got)
	}
}

// Without a reset pending, DrainData must apply chunks normally -- the fix must
// not turn ordinary draining into a reset.
func TestRemotePane_DrainWithoutResetAppliesChunksNormally(t *testing.T) {
	rp := resyncPane(t)
	captureLogs(t)
	rp.dataCh <- []byte("HELLO-NORMAL")
	rp.DrainData()
	if got := rp.emu.Render(); !strings.Contains(got, "HELLO-NORMAL") {
		t.Errorf("ordinary drain lost its chunk: %q", got)
	}
}
