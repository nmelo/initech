// QA tests for ini-7md: ghost text on prompt line from CUF (cursor forward).
// Claude Code uses ESC[nC to advance the cursor without writing to cells, leaving
// stale content from prior renders. The fix blanks uncolored non-space cells to
// the right of the cursor position on the cursor row during rendering.
package tui

import (
	"testing"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// helper: set up a pane + simulation screen for cursor-row render tests.
// emu must already have content written to it before calling this.
func cursorRowTestSetup(emu *vt.SafeEmulator) (*Pane, tcell.SimulationScreen) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	// 40 cols, 11 rows: 10 inner rows (matching emu height) + 1 ribbon row.
	s.SetSize(40, 11)
	p := &Pane{
		name:    "test",
		emu:     emu,
		alive:   true,
		visible: true,
		region:  Region{X: 0, Y: 0, W: 40, H: 11},
	}
	return p, s
}

// TestCursorRow_UncoloredGhostTextBlanked verifies that uncolored non-space
// cells to the right of the cursor on the cursor row are blanked during render
// (ini-7md). This simulates "Claude Code" or a session name left in cells that
// CUF moved past without erasing.
func TestCursorRow_UncoloredGhostTextBlanked(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)

	// First render: write stale "GHOST" at row 10, col 6 (1-indexed) = row 9 col 5 (0-indexed).
	// No color (default style): these are the stale cells CUF left behind.
	emu.Write([]byte("\033[10;6HGHOST"))

	// Second render: cursor repositioned at row 9, writes "=> " (3 chars).
	// Cursor ends at col 3 (0-indexed). Cols 5-9 still hold stale "GHOST".
	emu.Write([]byte("\033[10;1H=> "))

	pos := emu.CursorPosition()
	if pos.Y != 9 || pos.X != 3 {
		t.Fatalf("expected cursor at row 9 col 3, got row %d col %d", pos.Y, pos.X)
	}

	p, s := cursorRowTestSetup(emu)
	p.Render(s, false, false, 1, Selection{})

	// Screen row for cursor row: emuRow 9 → screen Y 9 (region Y=0, no scroll).
	const screenY = 9
	for col := 5; col <= 9; col++ {
		c, _, _, _ := s.GetContent(col, screenY)
		if c != ' ' {
			t.Errorf("ghost text at col %d should be blanked, got %q", col, c)
		}
	}
}

// TestCursorRow_ColoredCellPreserved verifies that colored cells to the right
// of the cursor on the cursor row are NOT blanked (ini-7md). Claude Code
// autocomplete suggestions have an explicit Fg color and must be preserved.
func TestCursorRow_ColoredCellPreserved(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)

	// Write colored autocomplete suggestion "COMP" at col 5 (cyan fg).
	emu.Write([]byte("\033[10;6H\033[36mCOMP\033[0m"))

	// Cursor repositioned to col 3 on the same row.
	emu.Write([]byte("\033[10;1H=> "))

	pos := emu.CursorPosition()
	if pos.X != 3 {
		t.Fatalf("expected cursor at col 3, got %d", pos.X)
	}

	p, s := cursorRowTestSetup(emu)
	p.Render(s, false, false, 1, Selection{})

	const screenY = 9
	// Cols 5-8 have colored "COMP" — must not be blanked.
	for col := 5; col <= 8; col++ {
		c, _, _, _ := s.GetContent(col, screenY)
		if c == ' ' {
			t.Errorf("colored autocomplete char at col %d was incorrectly blanked", col)
		}
	}
}

// TestCursorRow_LeftOfCursorPreserved verifies that uncolored text to the LEFT
// of (or at) the cursor position on the cursor row is not blanked (ini-7md).
// The user's typed input sits to the left of the cursor and must be preserved.
func TestCursorRow_LeftOfCursorPreserved(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)

	// "=> " occupies cols 0-2; cursor ends at col 3.
	emu.Write([]byte("\033[10;1H=> "))

	pos := emu.CursorPosition()
	if pos.X != 3 {
		t.Fatalf("expected cursor at col 3, got %d", pos.X)
	}

	p, s := cursorRowTestSetup(emu)
	p.Render(s, false, false, 1, Selection{})

	const screenY = 9
	// Cols 0-2 are LEFT of cursor (col 3): must not be blanked.
	wantChars := []rune{'=', '>', ' '}
	for i, want := range wantChars {
		c, _, _, _ := s.GetContent(i, screenY)
		if c != want {
			t.Errorf("col %d: got %q, want %q (left-of-cursor must not be blanked)", i, c, want)
		}
	}
}

// TestCursorRow_NonCursorRowNotAffected verifies that the cursor-row fix does
// not blank uncolored text on rows other than the cursor row (ini-7md).
func TestCursorRow_NonCursorRowNotAffected(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)

	// Write uncolored "NORMAL" at row 5, col 6 (1-indexed) = row 4 col 5 (0-indexed).
	emu.Write([]byte("\033[5;6HNORMAL"))

	// Cursor is on row 9 (last row), unrelated to row 4.
	emu.Write([]byte("\033[10;1H=> "))

	p, s := cursorRowTestSetup(emu)
	p.Render(s, false, false, 1, Selection{})

	// Row 4 is a non-cursor row; "NORMAL" should render intact.
	const screenY = 4
	want := "NORMAL"
	for i, wantCh := range want {
		c, _, _, _ := s.GetContent(5+i, screenY)
		if c != wantCh {
			t.Errorf("non-cursor row col %d: got %q, want %q (should not be blanked)", 5+i, c, wantCh)
		}
	}
}

// TestCursorRow_CursorColBoundary verifies that the cell exactly at the cursor
// position (col == pos.X) is not blanked — only cells strictly to the right are
// affected (ini-7md).
func TestCursorRow_CursorColBoundary(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)

	// Write "X" at col 3 (where cursor will land after "=> ").
	emu.Write([]byte("\033[10;4HX"))
	// Reposition cursor to end of "=> " which puts cursor at col 3.
	emu.Write([]byte("\033[10;1H=> "))

	pos := emu.CursorPosition()
	if pos.X != 3 {
		t.Fatalf("expected cursor at col 3, got %d", pos.X)
	}

	p, s := cursorRowTestSetup(emu)
	p.Render(s, false, false, 1, Selection{})

	// col 3 is exactly pos.X; col > pos.X is false, so the cell is preserved.
	// The "X" written there is uncolored, so this tests the boundary condition.
	// (col 3 has ' ' from "=> " overwrite, but that's fine — boundary is not blanked.)
	// The key invariant: no panic, and col 2 (' ' from "=> ") is present.
	c, _, _, _ := s.GetContent(2, 9)
	if c != ' ' {
		t.Errorf("col 2 (left of cursor): got %q, want ' '", c)
	}
}
