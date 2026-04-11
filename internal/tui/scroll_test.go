package tui

import (
	"testing"

	"github.com/charmbracelet/x/vt"
)

func TestScrollUpClampsToScrollbackLen(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{emu: emu}

	// No scrollback content yet, so ScrollUp should clamp to 0.
	p.ScrollUp(10)
	if p.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d, want 0 (no scrollback)", p.scrollOffset)
	}
}

func TestScrollDownClampsToZero(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{emu: emu}

	p.scrollOffset = 5
	p.ScrollDown(10)
	if p.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d, want 0", p.scrollOffset)
	}
}

func TestScrollUpThenDown(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	// Write enough content to create scrollback. Each line causes a scroll
	// once the screen fills (24 rows), pushing lines into the scrollback buffer.
	for i := 0; i < 100; i++ {
		emu.Write([]byte("line of content\r\n"))
	}

	p := &Pane{emu: emu}
	scrollbackLen := emu.ScrollbackLen()
	if scrollbackLen == 0 {
		t.Fatal("expected scrollback content after writing 100 lines to 24-row emulator")
	}

	// Scroll up partway.
	p.ScrollUp(10)
	if p.scrollOffset != 10 {
		t.Errorf("after ScrollUp(10): scrollOffset = %d, want 10", p.scrollOffset)
	}
	if !p.InScrollback() {
		t.Error("InScrollback() should be true when scrollOffset > 0")
	}

	// Scroll down back to live.
	p.ScrollDown(10)
	if p.scrollOffset != 0 {
		t.Errorf("after ScrollDown(10): scrollOffset = %d, want 0", p.scrollOffset)
	}
	if p.InScrollback() {
		t.Error("InScrollback() should be false when scrollOffset == 0")
	}
}

func TestScrollAnchor_NewOutputDoesNotDrift(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	// Fill screen + scrollback with 100 lines.
	for i := 0; i < 100; i++ {
		emu.Write([]byte("line of content\r\n"))
	}

	p := &Pane{emu: emu, region: Region{X: 0, Y: 0, W: 82, H: 26}} // 80 inner cols, 24 inner rows

	// Scroll up 20 lines, record where we are.
	p.ScrollUp(20)
	startRow1, _ := p.contentOffset()

	// Simulate 10 new lines of output (scrollback grows).
	for i := 0; i < 10; i++ {
		emu.Write([]byte("new output line\r\n"))
	}

	// Render again: contentOffset should compensate for the new lines.
	startRow2, _ := p.contentOffset()

	if startRow1 != startRow2 {
		t.Errorf("view drifted: startRow before=%d, after=%d (should be stable)", startRow1, startRow2)
	}
}

func TestScrollAnchor_ClearedAtLiveEdge(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	for i := 0; i < 50; i++ {
		emu.Write([]byte("line\r\n"))
	}

	p := &Pane{emu: emu}
	p.ScrollUp(10)
	if p.scrollAnchorLen == 0 {
		t.Error("scrollAnchorLen should be set after ScrollUp")
	}

	// Scroll back to live edge.
	p.ScrollDown(10)
	if p.scrollAnchorLen != 0 {
		t.Errorf("scrollAnchorLen = %d, want 0 (cleared at live edge)", p.scrollAnchorLen)
	}
}

func TestScrollUpClampsToMaxScrollback(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	for i := 0; i < 50; i++ {
		emu.Write([]byte("line\r\n"))
	}

	p := &Pane{emu: emu}
	scrollbackLen := emu.ScrollbackLen()

	// Try to scroll way past available scrollback.
	p.ScrollUp(999999)
	if p.scrollOffset != scrollbackLen {
		t.Errorf("scrollOffset = %d, want %d (clamped to scrollback len)", p.scrollOffset, scrollbackLen)
	}
}
