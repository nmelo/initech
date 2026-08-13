package tui

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// TestDrainData_OversizedReplayRendersClampedNotPanicked proves the ini-w6z
// fork fix is compiled into THIS build, not just pinned in go.mod (the
// ini-i3v module-cache lesson: a go.mod line nobody verified compiled-in
// proves nothing). It drives the exact production path from the crash.log
// stack — dataCh -> DrainData -> writeEmu -> SafeEmulator.Write — with the
// crash's byte shape: a 53-row terminal's scroll-region replay landing in an
// 80x24 RemotePane emulator.
//
// The emuPanicked assertion is what makes this test containment-independent:
// 6m4's writeEmu barrier would swallow a regression's panic, but it records
// every panic it absorbs, so a regressed clamp fails here even though the
// process survives. On the pre-fix fork (ac21d5e) the emulator panics with
// "index out of range [24] with length 24" in ultraviolet
// Buffer.DeleteLineArea; on the clamped fork (4049f44) the oversized region
// clamps and the pane renders degraded.
func TestDrainData_OversizedReplayRendersClampedNotPanicked(t *testing.T) {
	rp := &RemotePane{
		name:   "eng1",
		host:   "window1",
		alive:  true,
		emu:    vt.NewSafeEmulator(80, 24), // NewRemotePane's construction size
		dataCh: make(chan []byte, 64),
	}

	// The crash.log sequence shape: DECSTBM sized for a 53-row screen,
	// scrollback-generating content, then CSI 4 S (scroll up 4) — the frame
	// that walked ultraviolet's DeleteLineArea past row 24.
	rp.dataCh <- []byte("\x1b[1;53r")
	for i := 0; i < 30; i++ {
		rp.dataCh <- []byte(fmt.Sprintf("line %d\r\n", i))
	}
	rp.dataCh <- []byte("\x1b[4S")
	rp.DrainData()

	if rp.emuPanicked {
		t.Fatal("emulator panicked on oversized replay: the fork's margin clamp is not in " +
			"this build (go.mod pin regressed or module cache served a stale fork); " +
			"containment absorbed the panic but the pane renders garbled")
	}
	if h := rp.emu.Height(); h != 24 {
		t.Fatalf("emulator height = %d, want 24", h)
	}
}
