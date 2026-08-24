package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
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
// The second return is the DAEMON SIDE of the control pipe (ctrlS): a test
// that needs to observe an outbound resync request reads it from here.
// Callers that don't care ignore it -- nothing reads ctrlS in that case, so
// a background requestResync's write blocks until Cleanup closes the pipe,
// which is harmless (the goroutine just exits on the resulting closed-pipe
// error; it never blocks the test itself, which asserts synchronously
// right after pushChunk returns).
func resyncPane(t *testing.T) (*RemotePane, net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	ctrlS, ctrlC := net.Pipe()
	t.Cleanup(func() { ctrlS.Close(); ctrlC.Close() })
	return NewRemotePane("eng1", "wb", client, NewControlMux(ctrlC), 80, 24), ctrlS
}

// resyncResponder reads ControlCmd requests off the daemon-side control pipe
// and answers each with ControlResp{OK:true}, forwarding every decoded
// command onto the returned channel. Needed because ControlMux.Request
// blocks waiting for a correlated response (by ID) -- without an answer,
// requestResync would hang until the 10s request timeout, and the test
// would either wait that long or never observe the command at all.
func resyncResponder(t *testing.T, ctrlS net.Conn) <-chan ControlCmd {
	t.Helper()
	cmds := make(chan ControlCmd, 16)
	go func() {
		scanner := bufio.NewScanner(ctrlS)
		for scanner.Scan() {
			var cmd ControlCmd
			if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
				continue
			}
			resp, _ := json.Marshal(ControlResp{ID: cmd.ID, OK: true})
			if _, err := ctrlS.Write(resp); err != nil {
				return
			}
			if _, err := ctrlS.Write([]byte("\n")); err != nil {
				return
			}
			cmds <- cmd
		}
	}()
	return cmds
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
	rp, _ := resyncPane(t)
	captureLogs(t)
	fillDataCh(rp, cap(rp.dataCh))

	// Drive the REAL push-or-evict path, not noteDesync directly. Calling the
	// helper proves what the helper does; it does not prove eviction calls it.
	// Measured: with the call removed from pushChunk, a noteDesync-calling
	// cell stayed green (qa1's extraction is what makes this reachable).
	rp.pushChunk([]byte("the chunk that overflows"))

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
//
// BOTH HALVES, not just "not re-armed" (shipper's finding, ini-dr03 pre-cut
// gate). lastResync is set inside noteDesync, BEFORE "go rp.requestResync()"
// dispatches -- so a mutant that discards that goroutine entirely (no
// resync EVER requested) leaves lastResync's suppression behaviour fully
// intact, and the old version of this cell, which only checked that
// lastResync did not move a SECOND time, passed against it. The name says
// EXACTLY ONE; the old body proved AT MOST ONE. This version reads the
// actual wire via resyncResponder, so "zero requests" and "one request" are
// distinguishable.
func TestRemotePane_BurstOfEvictionsRequestsExactlyOneResync(t *testing.T) {
	rp, ctrlS := resyncPane(t)
	captureLogs(t)
	cmds := resyncResponder(t, ctrlS)

	// qa1 measured the overflow threshold at 100-300 small writes in
	// single-digit milliseconds. Their instruction, and it is the same margin
	// lesson the quarantined cell taught: treat that as a FLOOR, not a target.
	// It is one machine's number -- right order of magnitude, not a portable
	// constant across runners and hardware -- so a cell sitting just past 300
	// would be as environment-sensitive as the thing it replaces. Comfortably
	// over gets the mechanism without gambling on the margin.
	const burst = 1000
	for i := 0; i < burst; i++ {
		rp.pushChunk([]byte("x"))
	}

	// EXACTLY ONE REQUEST MUST ARRIVE. The first is dispatched async (its own
	// goroutine, per noteDesync's own doc comment: must not block readLoop),
	// so this waits for it rather than assuming it has already landed.
	var got []ControlCmd
	select {
	case cmd := <-cmds:
		got = append(got, cmd)
	case <-time.After(2 * time.Second):
		t.Fatal("no resync request arrived on the control channel at all -- the burst evicted " +
			"chunks but nothing asked the daemon to replay them; a viewer left this way never " +
			"recovers, it just shows whatever it had at the moment of eviction forever")
	}
	if got[0].Action != "resync" || got[0].Target != "eng1" {
		t.Fatalf("resync request malformed: action=%q target=%q, want action=resync target=eng1",
			got[0].Action, got[0].Target)
	}

	// A second request inside the floor must be refused -- one per eviction
	// would sustain the condition it recovers from. Give it a real window to
	// arrive wrongly before concluding it didn't.
	time.Sleep(20 * time.Millisecond)
	rp.pushChunk([]byte("x"))
	select {
	case cmd := <-cmds:
		got = append(got, cmd)
	case <-time.After(300 * time.Millisecond):
		// Nothing arrived -- correct, the floor held.
	}
	if len(got) != 1 {
		t.Errorf("got %d resync requests for one burst plus one in-floor eviction, want exactly 1: %+v",
			len(got), got)
	}
}

// The reset must DISCARD what is buffered and clear the screen, so the replay
// is the whole truth rather than history applied over newer bytes.
func TestRemotePane_ResetDiscardsBufferedChunksAndClearsTheScreen(t *testing.T) {
	rp, _ := resyncPane(t)
	captureLogs(t)

	rp.writeEmu([]byte("STALE-CONTENT"))
	if !strings.Contains(rp.emu.Render(), "STALE-CONTENT") {
		t.Fatal("fixture failed: the emulator never showed the stale content it is supposed to lose")
	}
	// Distinctive content, because "the channel is empty afterwards" does NOT
	// distinguish DISCARDED from APPLIED -- DrainData's normal loop empties it
	// either way, and a mutant that skipped the discard survived an
	// emptiness assertion. What must be true is that these bytes never reached
	// the screen.
	for i := 0; i < 10; i++ {
		rp.dataCh <- []byte("BUFFERED-STALE")
	}
	rp.mu.Lock()
	rp.resetPending = true
	rp.mu.Unlock()

	rp.DrainData()

	if got := rp.emu.Render(); strings.Contains(got, "BUFFERED-STALE") {
		t.Errorf("reset APPLIED the buffered chunks instead of discarding them; they land "+
			"after the replay and re-corrupt the pane: %q", got)
	}
	if n := len(rp.dataCh); n != 0 {
		t.Errorf("reset left %d buffered chunks", n)
	}
	if got := rp.emu.Render(); strings.Contains(got, "STALE-CONTENT") {
		t.Errorf("reset did not clear the screen, so the replay lands on top of older bytes: %q", got)
	}
}

// Without a reset pending, DrainData must apply chunks normally -- the fix must
// not turn ordinary draining into a reset.
func TestRemotePane_DrainWithoutResetAppliesChunksNormally(t *testing.T) {
	rp, _ := resyncPane(t)
	captureLogs(t)
	rp.dataCh <- []byte("HELLO-NORMAL")
	rp.DrainData()
	if got := rp.emu.Render(); !strings.Contains(got, "HELLO-NORMAL") {
		t.Errorf("ordinary drain lost its chunk: %q", got)
	}
}

// LOSS-SIDE (shipper's finding, ini-dr03 pre-cut gate): "no torn sequences"
// cannot tell recovery from loss, because STALE or BLANK content contains no
// torn escape sequence either -- a resync that silently does nothing (a
// skipped reset, a dropped replay, a request that never fires) passes a
// tear-absence check as easily as a correct recovery does. This is the
// assertion that can tell them apart: after a simulated desync and replay,
// the viewer's screen must be IDENTICAL to a pane that received the exact
// same bytes and was NEVER evicted at all, not merely free of visible tears.
//
// Unit-level against the emulator, not the live rig, on purpose: the live
// rig (dr03_rig_test.go) cannot reliably force a genuine dataCh overflow on
// every machine/runner (documented there, four burst shapes tried), so a
// content-equality assertion pinned to that rig would inherit the same
// environment-sensitivity the quarantined predecessor cell was removed for.
// This drives the real DrainData/reset/replay-application path directly and
// deterministically instead.
func TestRemotePane_ReplayRecoversTheSameScreenAsNeverEvicted(t *testing.T) {
	content := "\x1b[31mHISTORY-LINE-ONE\x1b[0m\r\nHISTORY-LINE-TWO\r\n"

	// REFERENCE: the exact same bytes applied to a pane with no eviction
	// anywhere in its history.
	ref := vt.NewSafeEmulator(80, 24)
	ref.Write([]byte(content))
	want := ref.Render()

	// rp: desynced first (garbage buffered, reset pending -- exactly what an
	// eviction leaves behind), then the replay arrives as handleResync's
	// ring-buffer snapshot would deliver it: ordinary bytes on the same
	// stream, applied by the same DrainData that handled the reset.
	rp, _ := resyncPane(t)
	captureLogs(t)
	rp.dataCh <- []byte("GARBAGE-FROM-BEFORE-THE-DESYNC")
	rp.mu.Lock()
	rp.resetPending = true
	rp.mu.Unlock()
	rp.DrainData() // discards the garbage, RIS-clears the screen

	rp.dataCh <- []byte(content)
	rp.DrainData()

	if got := rp.emu.Render(); got != want {
		t.Errorf("replayed pane does not match a never-evicted pane shown the identical bytes -- "+
			"stale or blank content would pass a \"no torn sequence\" check but fails this one.\n"+
			"got:\n%s\nwant:\n%s", got, want)
	}
}
