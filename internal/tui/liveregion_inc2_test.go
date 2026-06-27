package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// Increment 2 of ini-44hp: prove the scroll/mouse/cursor plumbing is correct
// when the emulator is taller than the visible window (emuHeight != visible).
// Emulator-only (no real claude); runs in `make test`.

func rowText(emu *vt.SafeEmulator, row, cols int) string {
	var b strings.Builder
	for col := 0; col < cols; col++ {
		if c := emu.CellAt(col, row); c != nil && c.Content != "" {
			b.WriteString(c.Content)
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// fillScreenInPlace draws `n` uniquely-marked rows via absolute cursor
// positioning (no newline scrolling), so a taller emulator keeps them all on
// the SCREEN with empty scrollback.
func fillScreenInPlace(emu *vt.SafeEmulator, n int, prefix string) {
	for i := 1; i <= n; i++ {
		emu.Write([]byte(fmt.Sprintf("\x1b[%d;1H%s%02d", i, prefix, i)))
	}
}

// TestMaxScrollOffset_AccountsForScreenOverflow proves maxScrollOffset includes
// the screen overflow (emuHeight-visible) so the wheel reaches the clipped top
// and no further — no over-scroll above the top, no under-scroll past live.
func TestMaxScrollOffset_AccountsForScreenOverflow(t *testing.T) {
	const visible, cols = 8, 40
	emu := vt.NewSafeEmulator(cols, visible)
	p := &Pane{emu: emu, alive: true}
	p.region = Region{X: 0, Y: 0, W: cols, H: visible + 2}
	p.Resize(visible, cols) // emu -> 24
	fillScreenInPlace(emu, emu.Height(), "R")
	if sb := emu.ScrollbackLen(); sb != 0 {
		t.Fatalf("precondition: want empty scrollback, got %d", sb)
	}

	if got, want := p.maxScrollOffset(), emu.Height()-visible; got != want {
		t.Errorf("maxScrollOffset = %d, want %d (emuHeight %d - visible %d)", got, want, emu.Height(), visible)
	}

	// Live edge shows the BOTTOM rows, not the top (no under-scroll).
	p.scrollOffset = 0
	if live := renderAtCurrentOffset(p); !strings.Contains(live, "R24") || strings.Contains(live, "R01") {
		t.Errorf("live edge should show bottom (R24) and not top (R01):\n%s", live)
	}

	// At max scroll the TOP row is reachable.
	p.scrollOffset = p.maxScrollOffset()
	if top := renderAtCurrentOffset(p); !strings.Contains(top, "R01") {
		t.Errorf("at maxScroll the top row R01 should be visible:\n%s", top)
	}

	// ScrollUp past the top clamps (no over-scroll).
	p.scrollOffset = 0
	p.ScrollUp(p.maxScrollOffset() + 50)
	if p.scrollOffset != p.maxScrollOffset() {
		t.Errorf("ScrollUp past top: scrollOffset = %d, want clamp at %d", p.scrollOffset, p.maxScrollOffset())
	}
}

// TestMouseCoordMapping_TallerEmu proves a click in the visible window maps to
// the correct row of the taller emulator (forwardMouseEvent uses
// emuY = startRow + (ly - renderOffset)).
func TestMouseCoordMapping_TallerEmu(t *testing.T) {
	const visible, cols = 8, 40
	emu := vt.NewSafeEmulator(cols, visible)
	p := &Pane{emu: emu, alive: true}
	p.region = Region{X: 0, Y: 0, W: cols, H: visible + 2}
	p.Resize(visible, cols) // emu -> 24
	fillScreenInPlace(emu, emu.Height(), "M")

	p.scrollOffset = 0
	startRow, renderOffset := p.contentOffset()
	if startRow == 0 {
		t.Fatalf("precondition: expected a bottom-anchored startRow>0 for a taller emu, got 0")
	}

	// Bottom visible row maps to the bottom drawn emu row (M24).
	lyBottom := visible - 1
	emuYBottom := startRow + (lyBottom - renderOffset)
	if got := rowText(emu, emuYBottom, cols); !strings.Contains(got, "M24") {
		t.Errorf("bottom window row (ly=%d) mapped to emu row %d = %q, want bottom content M24 (startRow=%d)",
			lyBottom, emuYBottom, strings.TrimSpace(got), startRow)
	}
	// Top visible row maps to startRow (the window top), not emu row 0.
	emuYTop := startRow + (0 - renderOffset)
	if emuYTop != startRow {
		t.Errorf("top window row mapped to emu row %d, want %d (startRow)", emuYTop, startRow)
	}
	if got := rowText(emu, emuYTop, cols); strings.Contains(got, "M01") {
		t.Errorf("top window row mapped to emu row %d which is M01 — taller-emu offset ignored", emuYTop)
	}
}

// TestCursorRender_TallerEmuMapsIntoWindow proves the cursor's visible row is
// computed against the windowed startRow (renderCursor: visRow = pos.Y -
// emuStartRow), so the cursor lands inside the visible window at the live edge.
func TestCursorRender_TallerEmuMapsIntoWindow(t *testing.T) {
	const visible, cols = 8, 40
	emu := vt.NewSafeEmulator(cols, visible)
	p := &Pane{emu: emu, alive: true}
	p.region = Region{X: 0, Y: 0, W: cols, H: visible + 2}
	p.Resize(visible, cols) // emu -> 24
	fillScreenInPlace(emu, emu.Height(), "C")
	emu.Write([]byte("\x1b[21;1H")) // park cursor at emu row 20 (0-indexed)

	pos := emu.CursorPosition()
	if pos.Y != 20 {
		t.Fatalf("precondition: cursor row = %d, want 20", pos.Y)
	}

	// Live edge: emuStartRow passed to renderCursor is startRow-renderOffset.
	p.scrollOffset = 0
	startRow, renderOffset := p.contentOffset()
	emuStartRow := startRow - renderOffset
	visRow := pos.Y - emuStartRow
	if visRow < 0 || visRow >= visible {
		t.Errorf("cursor visible row = %d, want within [0,%d) (cursor emu row %d, startRow %d)",
			visRow, visible, pos.Y, startRow)
	}
	// With content to row 23 and visible 8, startRow=16 -> cursor at visRow 4.
	if want := pos.Y - startRow; visRow != want {
		t.Errorf("cursor visRow = %d, want %d (pos.Y %d - startRow %d)", visRow, want, pos.Y, startRow)
	}
}
