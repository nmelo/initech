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

	// applyScrollAnchor must run before contentOffset (as Render does).
	p.applyScrollAnchor()
	startRow2, _ := p.contentOffset()

	if startRow1 != startRow2 {
		t.Errorf("view drifted: startRow before=%d, after=%d (should be stable)", startRow1, startRow2)
	}
}

func TestScrollAnchor_CompensationBeforeDraw(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	for i := 0; i < 100; i++ {
		emu.Write([]byte("line of content\r\n"))
	}

	p := &Pane{emu: emu, region: Region{X: 0, Y: 0, W: 82, H: 26}}
	p.ScrollUp(20)
	origOffset := p.scrollOffset

	// New output arrives.
	for i := 0; i < 5; i++ {
		emu.Write([]byte("new line\r\n"))
	}

	// Before applyScrollAnchor, scrollOffset is stale.
	if p.scrollOffset != origOffset {
		t.Fatalf("scrollOffset changed before applyScrollAnchor: got %d, want %d", p.scrollOffset, origOffset)
	}

	p.applyScrollAnchor()

	// After applyScrollAnchor, scrollOffset must include the delta.
	if p.scrollOffset != origOffset+5 {
		t.Errorf("scrollOffset after applyScrollAnchor: got %d, want %d", p.scrollOffset, origOffset+5)
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

func TestMaxScrollOffset_StopsAtTopOfBuffer(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	for i := 0; i < 100; i++ {
		emu.Write([]byte("line of content\r\n"))
	}
	p := &Pane{emu: emu, region: Region{X: 0, Y: 0, W: 82, H: 26}}

	max := p.maxScrollOffset()
	scrollbackLen := emu.ScrollbackLen()
	emuHeight := emu.Height()
	_, termRows := p.region.TerminalSize()
	want := scrollbackLen + emuHeight - termRows
	if max != want {
		t.Errorf("maxScrollOffset = %d, want %d", max, want)
	}

	p.ScrollUp(999999)
	p.applyScrollAnchor()
	startRow, _ := p.contentOffset()
	if startRow != 0 {
		t.Errorf("startRow at max scroll = %d, want 0 (top of buffer)", startRow)
	}
}

func TestScrollUp_ViewTopClampsToZero(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	for i := 0; i < 10; i++ {
		emu.Write([]byte("short scrollback\r\n"))
	}
	p := &Pane{emu: emu, region: Region{X: 0, Y: 0, W: 82, H: 26}}

	p.ScrollUp(999)
	p.applyScrollAnchor()
	startRow, _ := p.contentOffset()
	if startRow != 0 {
		t.Errorf("startRow = %d, want 0", startRow)
	}

	scrollbackLen := emu.ScrollbackLen()
	emuHeight := emu.Height()
	_, termRows := p.region.TerminalSize()
	viewBottom := startRow + termRows
	totalVirtual := scrollbackLen + emuHeight
	if viewBottom > totalVirtual {
		t.Errorf("viewBottom %d > totalVirtual %d (would render past buffer)", viewBottom, totalVirtual)
	}
}

func TestApplyScrollAnchor_ReclampsAfterGrowth(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	for i := 0; i < 100; i++ {
		emu.Write([]byte("line\r\n"))
	}
	p := &Pane{emu: emu, region: Region{X: 0, Y: 0, W: 82, H: 26}}

	p.ScrollUp(999)
	maxBefore := p.maxScrollOffset()

	for i := 0; i < 50; i++ {
		emu.Write([]byte("new line\r\n"))
	}

	p.applyScrollAnchor()
	maxAfter := p.maxScrollOffset()
	if p.scrollOffset > maxAfter {
		t.Errorf("scrollOffset %d > maxScrollOffset %d after anchor compensation", p.scrollOffset, maxAfter)
	}
	if maxAfter < maxBefore {
		t.Errorf("maxScrollOffset decreased: %d -> %d", maxBefore, maxAfter)
	}
}
