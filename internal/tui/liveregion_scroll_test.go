package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// renderAtCurrentOffset reproduces pane_render's view-window mapping for BOTH
// modes (pane_render.go:124-199): scrollback mode (scrollOffset>0) and live
// mode (scrollOffset==0). It returns what the operator sees in the visible
// window at the pane's current scrollOffset.
func renderAtCurrentOffset(p *Pane) string {
	cols, rows := p.region.TerminalSize()
	startRow, renderOffset := p.contentOffset()
	sb := p.Emulator().ScrollbackLen()
	emuRows := p.Emulator().Height()
	var b strings.Builder
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			var content string
			if p.scrollOffset > 0 {
				vRow := startRow + row
				if vRow < sb {
					if c := p.Emulator().ScrollbackCellAt(col, vRow); c != nil {
						content = c.Content
					}
				} else if vRow-sb >= 0 && vRow-sb < emuRows {
					if c := p.Emulator().CellAt(col, vRow-sb); c != nil {
						content = c.Content
					}
				}
			} else {
				emuRow := startRow + (row - renderOffset)
				if emuRow >= 0 && emuRow < emuRows {
					if c := p.Emulator().CellAt(col, emuRow); c != nil {
						content = c.Content
					}
				}
			}
			if content == "" {
				content = " "
			}
			b.WriteString(content)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// visibleAtAnyScroll reports whether the marker is visible in the rendered
// window at ANY reachable scroll offset (what the operator can do with the
// wheel), live edge through maxScrollOffset.
func visibleAtAnyScroll(p *Pane, marker string) bool {
	orig := p.scrollOffset
	defer func() { p.scrollOffset = orig }()
	for off := 0; off <= p.maxScrollOffset(); off++ {
		p.scrollOffset = off
		if strings.Contains(renderAtCurrentOffset(p), marker) {
			return true
		}
	}
	return false
}

// TestLiveRegionScroll_ClippedContentRecoverable is the ini-44hp AC#5
// regression test. It simulates claude's LIVE region (rendered IN PLACE via
// absolute cursor positioning, so nothing commits to scrollback) taller than
// the visible pane. With the taller-emulator fix, the clipped top must be
// recoverable by scrolling; without it, the off-screen rows are clamped/lost.
func TestLiveRegionScroll_ClippedContentRecoverable(t *testing.T) {
	const visible, cols = 8, 40
	emu := vt.NewSafeEmulator(cols, visible)
	p := &Pane{emu: emu, alive: true}
	p.region = Region{X: 0, Y: 0, W: cols, H: visible + 2} // TerminalSize() => (cols, visible)
	p.Resize(visible, cols)                                // fix: emu grows to min(300, 3*visible)

	// Simulate an in-place live region of 20 lines (taller than the 8-row
	// window). Absolute positioning (CUP) means no newline scrolling, so a
	// correct emulator keeps it all on the SCREEN, not in scrollback.
	const n = 20
	for i := 1; i <= n; i++ {
		emu.Write([]byte(fmt.Sprintf("\x1b[%d;1HL%02d", i, i)))
	}

	if got := emu.ScrollbackLen(); got != 0 {
		t.Fatalf("precondition: in-place render must not commit to scrollback, got ScrollbackLen=%d", got)
	}

	// The clipped middle/top lines must be reachable by scrolling.
	for _, marker := range []string{"L01", "L15"} {
		if !visibleAtAnyScroll(p, marker) {
			t.Errorf("clipped live-region line %s not recoverable at any scroll offset "+
				"(emuHeight=%d, visible=%d, maxScroll=%d)", marker, emu.Height(), visible, p.maxScrollOffset())
		}
	}
}

// TestEffectiveEmuRows pins the K=3 / cap=300 design decision (ini-44hp): the
// emulator grows to 3x the visible height, capped at 300, never below visible.
func TestEffectiveEmuRows(t *testing.T) {
	cases := []struct{ visible, want int }{
		{10, 30},  // confirmed-repro pane: 3x covers a ~25-30 row modal
		{14, 42},  // typical small multi-pane
		{1, 3},    // floor
		{100, 300}, // 3x=300 exactly at cap
		{150, 300}, // capped
		{400, 400}, // never smaller than visible (huge pane: no growth, no shrink)
	}
	for _, c := range cases {
		if got := effectiveEmuRows(c.visible); got != c.want {
			t.Errorf("effectiveEmuRows(%d) = %d, want %d", c.visible, got, c.want)
		}
	}
}

// TestLiveRegionScroll_NormalModeAnchorsToContent guards gate-(b): with a
// taller emulator but SHORT content, the live window must show the active
// content, not a blank window anchored to the (taller) emulator bottom — i.e.
// no spurious blank-row drift.
func TestLiveRegionScroll_NormalModeAnchorsToContent(t *testing.T) {
	const visible, cols = 8, 40
	emu := vt.NewSafeEmulator(cols, visible)
	p := &Pane{emu: emu, alive: true}
	p.region = Region{X: 0, Y: 0, W: cols, H: visible + 2}
	p.Resize(visible, cols) // emu grows to 24; content stays short

	for i := 1; i <= 4; i++ {
		emu.Write([]byte(fmt.Sprintf("\x1b[%d;1Hline%02d", i, i)))
	}
	emu.Write([]byte("\x1b[5;1H> prompt")) // active prompt line at row 4

	view := renderAtCurrentOffset(p) // live edge (scrollOffset=0)
	if !strings.Contains(view, "line01") || !strings.Contains(view, "> prompt") {
		t.Errorf("normal-mode live window dropped active content (blank-row drift to emu bottom?). view:\n%s", view)
	}
}

// TestLiveRegionScroll_AnchorsToContentBelowCursor is the regression test for
// the gate-(b) failure the real-claude probe caught (ini-44hp inc1b): with a
// taller emulator, claude parks its cursor mid-screen and draws its STATUS BAR
// BELOW the cursor. The live window must anchor to the bottom of the FULL drawn
// screen so that below-cursor content is visible — not anchor via the cursor
// row (which clips it).
func TestLiveRegionScroll_AnchorsToContentBelowCursor(t *testing.T) {
	const visible, cols = 8, 40
	emu := vt.NewSafeEmulator(cols, visible)
	p := &Pane{emu: emu, alive: true}
	p.region = Region{X: 0, Y: 0, W: cols, H: visible + 2}
	p.Resize(visible, cols) // emu grows to 24

	// 20 lines drawn in place, then park the cursor at row 15 — ABOVE the last
	// drawn line (L20 at row 20). This mimics claude: prompt/cursor mid-screen,
	// status bar below it.
	for i := 1; i <= 20; i++ {
		emu.Write([]byte(fmt.Sprintf("\x1b[%d;1HL%02d", i, i)))
	}
	emu.Write([]byte("\x1b[15;1H")) // cursor to row 15 (0-indexed 14), above L20

	// Live edge must show the bottom-most drawn content (L20 = the "status bar"),
	// not anchor above the cursor and clip it.
	p.scrollOffset = 0
	view := renderAtCurrentOffset(p)
	if !strings.Contains(view, "L20") {
		t.Errorf("live window clipped content below the cursor (L20 missing) — cursor-bounded anchoring drift.\nview:\n%s", view)
	}
}
