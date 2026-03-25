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
