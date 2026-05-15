// QA test for ini-szir: scrollback above zoom-fold reflows when the pane
// resizes. Pre-fix, x/vt's Screen.Resize touched only the live render
// buffer; scrollback rows stayed frozen at their pre-resize wrap and
// produced a visible width seam where wrapping changed at the resize
// moment.
//
// Phase 1 added wrap-continuation metadata to x/vt's Scrollback plus a
// Reflow method called from Screen.Resize. This test pins the end-to-end
// behavior through initech's view of the emulator API: a wrapped chain
// of physical rows must collapse into a single logical row when the
// emulator widens, and split back into multiple rows when it narrows.
//
// The test exercises x/vt directly because pane.Resize() merely forwards
// to emu.Resize(). No initech-side change was needed to consume the fix —
// the bump of the github.com/nmelo/x/vt replace pointer in go.mod is
// what wires it in.
package tui

import (
	"testing"

	"github.com/charmbracelet/x/vt"
)

func TestScrollbackReflow_OnWiden_ini_szir(t *testing.T) {
	emu := vt.NewSafeEmulator(20, 4)

	// 60 'A' characters at width 20 wrap into 3 physical rows of 20.
	// Follow with a few short lines to push the wrapped chain into
	// scrollback (the live buffer holds only 4 rows).
	var as []byte
	for i := 0; i < 60; i++ {
		as = append(as, 'A')
	}
	emu.Write(as)
	emu.Write([]byte("\r\nL2\r\nL3\r\nL4\r\nL5\r\nL6\r\n"))

	if got := emu.ScrollbackLen(); got != 5 {
		t.Fatalf("pre-resize scrollback = %d, want 5 (3 wrap rows + L2 + L3)", got)
	}

	emu.Resize(60, 4)

	if got := emu.ScrollbackLen(); got != 3 {
		t.Fatalf("post-resize scrollback = %d, want 3 (1 collapsed wrap row + L2 + L3)", got)
	}
	row0 := emu.Scrollback().Line(0)
	if len(row0) != 60 {
		t.Errorf("post-resize row 0 len = %d, want 60", len(row0))
	}
	for i, cell := range row0 {
		if cell.Content != "A" {
			t.Errorf("post-resize row 0 cell %d = %q, want %q", i, cell.Content, "A")
			break
		}
	}
}

func TestScrollbackReflow_OnNarrow_ini_szir(t *testing.T) {
	// Start wide (60), write a 60-char run that does NOT wrap, then push
	// to scrollback. Resize narrower (20). The single-row 60-A line should
	// reflow into 3 physical rows of 20.
	emu := vt.NewSafeEmulator(60, 4)

	var as []byte
	for i := 0; i < 60; i++ {
		as = append(as, 'A')
	}
	emu.Write(as)
	emu.Write([]byte("\r\nL2\r\nL3\r\nL4\r\nL5\r\nL6\r\n"))

	if got := emu.ScrollbackLen(); got != 3 {
		t.Fatalf("pre-narrow scrollback = %d, want 3", got)
	}

	emu.Resize(20, 4)

	// 1 logical 60-A line splits into 3 rows of 20, plus L2 + L3 = 5 total.
	if got := emu.ScrollbackLen(); got != 5 {
		t.Fatalf("post-narrow scrollback = %d, want 5 (3 split A rows + L2 + L3)", got)
	}
	for i := 0; i < 3; i++ {
		row := emu.Scrollback().Line(i)
		if len(row) != 20 {
			t.Errorf("post-narrow row %d len = %d, want 20", i, len(row))
		}
	}
}

func TestScrollbackReflow_DoesNotJoinNonWrapped_ini_szir(t *testing.T) {
	// 7 unrelated short lines. None wrap. Resizing must NOT silently
	// join them into a long line — a regression here would break log-style
	// content where each short line is semantically distinct.
	emu := vt.NewSafeEmulator(20, 4)
	emu.Write([]byte("aaa\r\nbbb\r\nccc\r\nddd\r\neee\r\nfff\r\nggg\r\n"))

	preLen := emu.ScrollbackLen()
	if preLen == 0 {
		t.Fatal("expected scrollback content to test")
	}

	emu.Resize(60, 4)

	postLen := emu.ScrollbackLen()
	if postLen != preLen {
		t.Errorf("non-wrapped scrollback changed count after resize: pre=%d, post=%d", preLen, postLen)
	}
}
