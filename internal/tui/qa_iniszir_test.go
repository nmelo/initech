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
	"fmt"
	"testing"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
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

// TestRender_AppWrappedScrollback_PaddedToCurrentWidth_ini_szir is the
// load-bearing regression test for Phase 1 round 2. It replays the exact
// content shape that let v1.24.2 ship broken: lines emitted by an
// application that pre-wraps at the original PTY width with explicit
// \r\n, then resized to a wider pane.
//
// Round 1's Reflow code targets continuation chains produced by emulator
// autowrap. Pre-wrapped content (the dominant pattern for ink-based agents
// like Claude Code per ini-szir Phase 1A capture data) never triggers
// autowrap, so wrappedToNext stays false and Reflow no-ops. The scrollback
// rows stay at their original 20-cell width.
//
// What the user must still see correctly: the visible pane at the new
// width must NOT show stale pre-resize content in the cells beyond the
// stored row length. Every cell at col 0..paneWidth-1 must be either
// real content (for cols within the row) or an explicit blank cell with
// default style (for cols past the row's stored length).
//
// The test pre-pollutes the SimulationScreen at cols 20-59 with sentinel
// cells before rendering. If render-time padding isn't happening, the
// pollution survives in the scrollback view and the test fails. This is
// exactly the gap that let v1.24.2 ship without anyone noticing the
// app-wrap case was unfixed.
func TestRender_AppWrappedScrollback_PaddedToCurrentWidth_ini_szir(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	defer s.Fini()
	s.SetSize(80, 40)

	// Build a Pane at width 20 with a draining emulator. The drain
	// prevents emu.Write from blocking on the internal response pipe.
	emu := vt.NewSafeEmulator(20, 8)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	p := &Pane{
		name:    "szir",
		emu:     emu,
		alive:   true,
		visible: true,
		region:  Region{X: 0, Y: 0, W: 20, H: 20},
	}

	// App-wrap pattern: rows of exactly 20 'A's terminated by \r\n. This
	// mirrors what ink (Claude Code's renderer) emits when it pre-wraps
	// prose to fit a 20-col terminal — full-row content followed by an
	// explicit newline. No autowrap event fires; wrappedToNext flags in
	// scrollback stay false. Write enough to fill the scrollback well
	// past the visible viewport so the entire view-window-into-scrollback
	// is rendered from app-wrapped narrow rows.
	twentyAs := make([]byte, 20)
	for i := range twentyAs {
		twentyAs[i] = 'A'
	}
	for i := 0; i < 30; i++ {
		emu.Write(twentyAs)
		emu.Write([]byte("\r\n"))
	}

	if got := emu.ScrollbackLen(); got < 20 {
		t.Fatalf("expected substantial scrollback; got %d (test setup wrong)", got)
	}
	row0 := emu.Scrollback().Line(0)
	if len(row0) != 20 {
		t.Errorf("scrollback row 0 stored len = %d, want 20 (app-wrap content fills the row exactly)", len(row0))
	}

	// Resize EMU to width 60. We bypass Pane.Resize because that sets
	// resizeSettleFrames which suppresses content rendering for ~150ms —
	// long enough to skew the test. The settle behavior is irrelevant to
	// the property under test (cell-level padding).
	emu.Resize(60, 8)
	p.region = Region{X: 0, Y: 0, W: 60, H: 20}

	// Enter scrollback view mode so the render takes the scrollback path
	// (pane_render.go:128-146). Set scrollOffset deep enough that the
	// entire visible viewport (termRows = region.H-2 = 18) is from
	// scrollback, not the live screen. The viewBottom math is
	// totalVirtual - scrollOffset = viewBottom; we need
	// viewBottom <= scrollbackLen so vRow < scrollbackLen for every
	// visible row. With scrollbackLen ~= 23 (30 lines - 7 live) and
	// totalVirtual = scrollbackLen + 8, choose scrollOffset = 10 so
	// viewBottom = scrollbackLen + 8 - 10 = scrollbackLen - 2 — inside
	// scrollback.
	p.scrollOffset = 10

	// Pre-pollute the SimulationScreen at cols 20-59 of every visible
	// content row. If the render path doesn't iterate the full new pane
	// width (i.e., padding isn't happening), this pollution survives
	// because nothing overwrites it. The test then fails.
	pollutionStyle := tcell.StyleDefault.Background(tcell.ColorRed).Foreground(tcell.ColorYellow)
	for y := 0; y < 20; y++ {
		for x := 20; x < 60; x++ {
			s.SetContent(x, y, 'X', nil, pollutionStyle)
		}
	}

	p.Render(s, true, false, 0, Selection{})

	// Render writes the ribbon (y=0) and activity bar (y=1) before
	// content. Content rows start at y >= 2 within the pane region. Scan
	// the content rows; assert no pollution survived at cols 20-59.
	var pollutionSurvived []string
	var foundContent bool
	for y := 2; y < 19; y++ {
		// Check pollution overwrite at cols 20-59. Every cell must NOT
		// still be the sentinel 'X' with the red background.
		for x := 20; x < 60; x++ {
			mainc, _, style, _ := s.GetContent(x, y)
			if mainc == 'X' && style == pollutionStyle {
				pollutionSurvived = append(pollutionSurvived,
					fmt.Sprintf("(%d,%d)", x, y))
			}
		}
		// Verify we actually rendered scrollback content (cols 0..19
		// should contain 'A's for the wrapped scrollback rows).
		for x := 0; x < 20; x++ {
			mainc, _, _, _ := s.GetContent(x, y)
			if mainc == 'A' {
				foundContent = true
			}
		}
	}

	if !foundContent {
		t.Errorf("test setup wrong: no 'A' content found in cols 0..19; scrollback render did not draw the rows")
	}
	if len(pollutionSurvived) > 0 {
		// Cap the reported list to avoid flooding test output.
		preview := pollutionSurvived
		if len(preview) > 10 {
			preview = preview[:10]
		}
		t.Errorf("render did not pad scrollback rows to current pane width — pollution survived at %d cells (first 10): %v",
			len(pollutionSurvived), preview)
	}
}

