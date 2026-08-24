package tui

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

// TestRemotePaneReadLoop_ChannelFullDropsOldestChunkTearsEscapeSequence
// examines ini-91kj's unexamined second candidate.
//
// WHAT THIS TEST PROVES AND WHAT IT DOES NOT, read this before citing it
// elsewhere: it proves the DROP-OLDEST MECHANISM IS REAL AND CAN CORRUPT
// RENDERING in the shipped code, driven through the actual production
// function with a genuinely full channel. It does NOT prove this is
// REACHABLE under real traffic -- nobody has yet measured whether a real
// yamux/TCP connection ever delivers 64+ discrete reads before one 33ms
// render tick's DrainData clears them, which is what real reachability
// depends on (OS socket buffering, yamux's own frame/window sizing). A
// green run here is evidence the bug EXISTS in the code, not evidence it
// BITES in production. Do not read this test's presence, on its own, as
// proof of a live user-facing incident -- see ini-91kj for the reachability
// measurement this is gated behind.
//
// THE MECHANISM: when dataCh (cap 64) is full, readLoop drops the OLDEST
// buffered chunk to make room for the newest. A chunk boundary is a network
// read boundary, not a terminal-grammar boundary, so a CSI escape sequence
// split across two chunks can have its FIRST half evicted while its SECOND
// half survives -- the surviving half then arrives at the emulator with no
// preceding ESC, and its bytes render as literal text instead of being
// consumed as an escape sequence.
//
// This drives the REAL production path (NewRemotePane, Start, the real
// readLoop goroutine, the real DrainData/writeEmu) over a net.Pipe, not a
// stand-in: net.Pipe is synchronous and unbuffered, so each server.Write
// call blocks until readLoop's matching stream.Read (and its send-or-drop)
// completes, which lets the test control the exact chunk boundaries readLoop
// sees without needing to drain concurrently.
//
// QUARANTINED, PREMISE FALSIFIED (eng2, ini-dr03; exposed by ini-a9d8).
//
// The parenthetical above -- "(and its send-or-drop)" -- is not true.
// net.Pipe's Write unblocks when a READ CONSUMES THE BYTES; it does not wait
// for anything the reader goroutine does afterwards. readLoop's select (send
// to dataCh, or evict-then-send) runs AFTER its Read returns and is entirely
// unsynchronised with the test's next Write. So this cell does not control
// how many chunks are in the channel at the moment of eviction -- only the
// order bytes are handed to Read. Its arithmetic (73 chunks written, 9
// evicted, MARKER at index 10) holds only while the scheduler happens to let
// readLoop finish its select before the next Write lands.
//
// It was load-sensitive from its first commit rather than broken by anyone.
// Measured while attributing an unrelated failure: the cell passes alone and
// fails under the full package; it also fails with ini-a9d8's commit REVERTED
// entirely, so the commit that exposed it is not the commit that broke it.
// Two attempts to reduce ambient load changed WHEN it failed, not whether --
// which is what a false timing premise looks like from the outside.
//
// The MECHANISM it demonstrates is real and is not in question. ini-dr03
// removes drop-oldest (an evicted chunk marks the pane desynced and requests
// a ring-buffer replay), and inverts this cell as part of that work: same
// setup, asserting a resync instead of a tear, with dataCh driven to the full
// state EXPLICITLY rather than raced into it. Its substance is eng2's to
// rewrite; nothing here touches what it asserts.
func TestRemotePaneReadLoop_ChannelFullDropsOldestChunkTearsEscapeSequence(t *testing.T) {
	t.Skip("ini-dr03: stated premise false -- net.Pipe synchronises Read, not " +
		"readLoop's subsequent send-or-drop, so the eviction index is scheduler-" +
		"dependent. Quarantined, not deleted: dr03 inverts this cell with " +
		"explicitly-driven state and unskips it.")

	server, client := net.Pipe()
	defer server.Close()
	ctrlS, ctrlC := net.Pipe()
	defer ctrlS.Close()
	defer ctrlC.Close()

	// Tall enough (80x100) that none of the 64 surviving one-line chunks
	// scrolls off screen before the assertion -- the corruption this test
	// looks for is orthogonal to scrolling and a scrolled-away line would be
	// a false negative about the wrong mechanism.
	rp := NewRemotePane("eng1", "wb", client, NewControlMux(ctrlC), 80, 100)
	rp.Start()
	defer rp.Close()

	// dataCh cap is 64. Send exactly 73 chunks with NO draining in between,
	// so 9 are evicted (chunks 1-9) and 64 survive (chunks 10-73), the oldest
	// surviving chunk landing exactly at index 10 -- the precise boundary the
	// eviction leaves behind, not an approximation of it.
	//
	// Chunk 9 carries only the OPENING of a CSI sequence, no final byte.
	// Chunk 10 carries the CSI's final byte plus the payload. Chunk 9 is
	// evicted; chunk 10 is not.
	var chunks [][]byte
	for i := 1; i <= 8; i++ {
		chunks = append(chunks, []byte(fmt.Sprintf("filler-%d\n", i)))
	}
	chunks = append(chunks, []byte("\x1b[31"))          // chunk 9: torn CSI, no final byte -- EVICTED
	chunks = append(chunks, []byte("mMARKER\x1b[0m\n")) // chunk 10: CSI final byte + payload -- SURVIVES, oldest kept
	for i := 1; i <= 63; i++ {
		chunks = append(chunks, []byte(fmt.Sprintf("later-%d\n", i)))
	}
	if len(chunks) != 73 {
		t.Fatalf("test setup: built %d chunks, want 73", len(chunks))
	}

	for i, c := range chunks {
		if _, err := server.Write(c); err != nil {
			t.Fatalf("write %d failed: %v", i+1, err)
		}
	}

	// Confirm eviction actually happened -- the channel is genuinely full at
	// its cap, not coincidentally under it.
	if n := len(rp.dataCh); n != 64 {
		t.Fatalf("dataCh len = %d, want 64 (full after 73 pushes with a 64 cap)", n)
	}

	rp.DrainData()

	screen := remotePaneScreenText(rp)
	if !strings.Contains(screen, "mMARKER") {
		t.Fatalf("emulator screen does not contain the literal corruption signature %q "+
			"(the CSI final byte 'm' printed as text because its opening ESC+CSI-prefix "+
			"chunk was evicted) -- if this is absent, either the drop-oldest eviction did "+
			"not land where the test placed it, or the mechanism no longer corrupts escape "+
			"sequences and this cell should be revisited.\nscreen:\n%s", "mMARKER", screen)
	}
	if strings.Contains(screen, "filler-1\n") {
		t.Fatalf("emulator screen includes chunk 1 ('filler-1'), which should have been "+
			"evicted -- the eviction did not happen where this test assumed.\nscreen:\n%s", screen)
	}
}

// remotePaneScreenText reads every row's rendered content as one string,
// newline-separated, same cell-by-cell walk as TestRemotePaneReadLoopFeedsEmulator.
func remotePaneScreenText(rp *RemotePane) string {
	emu := rp.Emulator()
	cols := emu.Width()
	rows := emu.Height()
	var b strings.Builder
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			cell := emu.CellAt(col, row)
			if cell != nil && cell.Content != "" {
				b.WriteString(cell.Content)
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
