package tui

import (
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Regression tests for ini-wizq: SafeEmulator.CellAt returned a *uv.Cell pointer
// into the live screen buffer and released its read lock on return, so every
// caller dereferenced cell.Content with no lock while readLoop's emu.Write
// mutated those same cells. The render path was safe (Render and readLoop both
// hold p.renderMu); no other cell reader did.
//
// THE ORACLE FOR THESE TESTS IS THE RACE DETECTOR. Run them with -race:
//
//	go test -race -run TestPeek ./internal/tui/
//
// `make test` does not enable -race, so these pass there either way. On the
// unpatched code, -race reports a write in ultraviolet.(*Buffer).DeleteLineArea
// (via readLoop) against a read in peekContent. A torn string header is what
// made this a P0: a data pointer paired with another string's length either
// SIGSEGVs inside strings.Builder.WriteString or silently injects garbage into
// peek output and clipboard content.

// noisyPane starts a pane whose child prints continuously, so readLoop is
// actively writing to the emulator (including scroll, which moves whole Lines
// via copy) for the duration of the test.
func noisyPane(t *testing.T, name, payload string) *Pane {
	t.Helper()
	p, err := NewPane(PaneConfig{
		Name:    name,
		Command: []string{"/bin/sh", "-c", "while :; do printf '" + payload + "\\n'; done"},
	}, 24, 80)
	if err != nil {
		t.Fatalf("NewPane(%q): %v", name, err)
	}
	p.Start()
	t.Cleanup(p.Close)
	// Let the child produce output so the emulator is being written to.
	time.Sleep(150 * time.Millisecond)
	return p
}

// hammer runs fn from n goroutines for d, then waits for them to finish.
func hammer(n int, d time.Duration, fn func()) {
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				fn()
			}
		}()
	}
	time.Sleep(d)
	close(stop)
	wg.Wait()
}

// peekContent is reached from the IPC peek handler, the daemon control peek
// handler, handleIPCPatrol and the :peek command. None of
// them hold renderMu, so all of them raced readLoop.
func TestPeekContent_IsRaceFreeAgainstLivePTYOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: noisyPane hardcodes /bin/sh as the pane's command (peek_race_test.go:34) to produce continuous PTY output for the race detector; needs a cross-platform equivalent (e.g. a small Go helper binary), product code's own shell resolution is unaffected (pane_cmd_unix.go / pane_cmd_windows.go already split)")
	}
	p := noisyPane(t, "eng1", "abcdefghijklmnopqrstuvwxyz")
	hammer(4, 900*time.Millisecond, func() { _ = peekContent(p, 20) })
}

// promptHasContent feeds the submit-retry decision in sendPaneTextLocked. A torn
// read there fires a spurious duplicate Enter (submitting the operator's
// unfinished prompt, the ini-vxw hazard) or swallows a message.
func TestPromptHasContent_IsRaceFreeAgainstLivePTYOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: noisyPane hardcodes /bin/sh (peek_race_test.go:34); see TestPeekContent_IsRaceFreeAgainstLivePTYOutput for the full reason")
	}
	p := noisyPane(t, "eng2", "> some prompt text")
	hammer(4, 900*time.Millisecond, func() { _ = promptHasContent(p) })
}

// paneHasModal reads the emulator's bottom rows from the send/inject path. Its
// doc comment used to claim SafeEmulator was safe for concurrent reads, which
// is what licensed the whole family of unsynchronized readers.
func TestPaneHasModal_IsRaceFreeAgainstLivePTYOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: noisyPane hardcodes /bin/sh (peek_race_test.go:34); see TestPeekContent_IsRaceFreeAgainstLivePTYOutput for the full reason")
	}
	p := noisyPane(t, "eng3", "Do you want to proceed?")
	hammer(4, 900*time.Millisecond, func() { _ = paneHasModal(p) })
}

// The selection-copy path (mouse.go) walks cells to build clipboard text. A
// torn read puts garbage on the operator's clipboard.
func TestSelectionCopy_IsRaceFreeAgainstLivePTYOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: noisyPane hardcodes /bin/sh (peek_race_test.go:34); see TestPeekContent_IsRaceFreeAgainstLivePTYOutput for the full reason")
	}
	p := noisyPane(t, "eng4", "selectable content here")
	hammer(4, 900*time.Millisecond, func() {
		emu := p.Emulator()
		cols, rows := emu.Width(), emu.Height()
		var sb strings.Builder
		for row := 0; row < rows; row++ {
			sb.WriteString(emu.RowText(row, cols))
		}
		_ = sb.String()
	})
}

// extractSelectionText (mouse.go) is what actually runs on a text-selection
// drag/copy; it walks cells via virtualCellAt/CellAt on the main goroutine
// with no renderMu. This exercises the REAL function, in both live-screen and
// scrollback-mode branches, against a live producing PTY.
func TestExtractSelectionText_IsRaceFreeAgainstLivePTYOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: noisyPane hardcodes /bin/sh (peek_race_test.go:34); see TestPeekContent_IsRaceFreeAgainstLivePTYOutput for the full reason")
	}
	p := noisyPane(t, "eng6", "selectable content for the clipboard test")
	p.region = Region{X: 0, Y: 0, W: 80, H: 24}

	tui := &TUI{panes: []PaneView{p}}
	tui.sel.pane = 0
	tui.sel.startX, tui.sel.startY = 0, 0
	tui.sel.endX, tui.sel.endY = 10, 2

	hammer(4, 900*time.Millisecond, func() { _ = tui.extractSelectionText() })
}

// Same, but forces the scrollback branch (virtualCellValueAt / ScrollbackLen >
// 0) by scrolling the pane up first, so the copy path reads from the combined
// scrollback+screen buffer while readLoop keeps writing to the live screen.
func TestExtractSelectionText_ScrollbackMode_IsRaceFreeAgainstLivePTYOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: noisyPane hardcodes /bin/sh (peek_race_test.go:34); see TestPeekContent_IsRaceFreeAgainstLivePTYOutput for the full reason")
	}
	p := noisyPane(t, "eng7", "scrollback selection content")
	p.region = Region{X: 0, Y: 0, W: 80, H: 24}
	p.ScrollUp(5)

	tui := &TUI{panes: []PaneView{p}}
	tui.sel.pane = 0
	tui.sel.startX, tui.sel.startY = 0, 0
	tui.sel.endX, tui.sel.endY = 10, 2
	tui.sel.startRow = 0

	hammer(4, 900*time.Millisecond, func() { _ = tui.extractSelectionText() })
}

// Peek output must be readable text, never garbage from a torn string header.
// Every rune the child emits is printable ASCII, so any control byte or
// replacement char in the output means a read observed a corrupted cell.
func TestPeekContent_ReturnsUncorruptedTextDuringLiveOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: noisyPane hardcodes /bin/sh (peek_race_test.go:34); see TestPeekContent_IsRaceFreeAgainstLivePTYOutput for the full reason")
	}
	const payload = "STABLEPAYLOAD0123456789"
	p := noisyPane(t, "eng5", payload)

	sawPayload := false
	deadline := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(deadline) {
		out := peekContent(p, 20)
		if strings.Contains(out, payload) {
			sawPayload = true
		}
		for _, r := range out {
			if r == '\n' || r == ' ' {
				continue
			}
			if r == '�' || r < 0x20 || r > 0x7e {
				t.Fatalf("corrupted rune %q (%U) in peek output: %q", r, r, out)
			}
		}
	}
	if !sawPayload {
		t.Errorf("never observed the child's payload %q in peek output; test is not exercising the read path", payload)
	}
}
