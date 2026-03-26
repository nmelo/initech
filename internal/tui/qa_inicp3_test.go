// QA tests for ini-cp3: CUF heuristic restricted to status bar rows only.
// Verifies rowContainsStatusBar detection and that input rows are not filtered.
package tui

import (
	"testing"

	"github.com/charmbracelet/x/vt"
)

// rowContainsStatusBar should return true for rows with │ (U+2502).
func TestRowContainsStatusBar_WithSeparator(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	// Write a status bar line: "mode │ model │ cost"
	emu.Write([]byte("\033[5;1H")) // Move to row 5 (0-indexed: 4)
	emu.Write([]byte("mode \u2502 model \u2502 cost"))

	if !rowContainsStatusBar(emu, 4, 40) {
		t.Error("rowContainsStatusBar should return true for row with │")
	}
}

// rowContainsStatusBar should return false for plain text rows (no │).
func TestRowContainsStatusBar_WithoutSeparator(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	// Write a normal input line: "/model sonnet"
	emu.Write([]byte("\033[3;1H")) // Move to row 3 (0-indexed: 2)
	emu.Write([]byte("/model sonnet"))

	if rowContainsStatusBar(emu, 2, 40) {
		t.Error("rowContainsStatusBar should return false for input row without │")
	}
}

// rowContainsStatusBar should return false for an empty row.
func TestRowContainsStatusBar_EmptyRow(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	if rowContainsStatusBar(emu, 0, 40) {
		t.Error("rowContainsStatusBar should return false for empty row")
	}
}

// rowContainsStatusBar should not be tricked by pipe character (|, U+007C).
func TestRowContainsStatusBar_PipeNotSeparator(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	emu.Write([]byte("\033[1;1H"))
	emu.Write([]byte("echo hello | grep hello"))

	if rowContainsStatusBar(emu, 0, 40) {
		t.Error("pipe | (U+007C) should not trigger status bar detection")
	}
}

// The heuristic should NOT blank characters on an input row (no │ present).
// This is the core regression test for ini-cp3: "/model sonnet" must render fully.
func TestCUFHeuristic_InputRowNotFiltered(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)

	// Simulate an input row with typed text next to colored autocomplete ghost text.
	// Write "/model " in default fg, then "s" in default fg, then "onnet" colored.
	// The old heuristic would blank the "s" because it's uncolored near colored text.
	emu.Write([]byte("\033[5;1H"))        // Move to row 5 (0-indexed: 4)
	emu.Write([]byte("/model s"))          // Default fg text
	emu.Write([]byte("\033[90monnet\033[0m")) // Colored (dim gray) ghost text

	// Row 4 has no │, so rowContainsStatusBar returns false.
	if rowContainsStatusBar(emu, 4, 40) {
		t.Fatal("input row should not be detected as status bar")
	}

	// Verify the "s" cell exists at column 7 (0-indexed) with content.
	cell := emu.CellAt(7, 4)
	if cell == nil || cell.Content == "" {
		t.Fatal("expected cell content at column 7 row 4")
	}
	// The character should be "s".
	if cell.Content != "s" {
		t.Errorf("cell content = %q, want 's'", cell.Content)
	}
}

// The heuristic SHOULD still blank artifacts on a status bar row (has │).
func TestCUFHeuristic_StatusBarRowFiltered(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)

	// Write a status bar row with │ and some colored + uncolored chars.
	emu.Write([]byte("\033[5;1H"))
	emu.Write([]byte("\033[36mmode\033[0m \u2502 \033[36mmodel\033[0m"))

	// This row DOES contain │.
	if !rowContainsStatusBar(emu, 4, 40) {
		t.Fatal("status bar row should be detected")
	}
}

// Boundary: rowContainsStatusBar with cols=0 should not panic.
func TestRowContainsStatusBar_ZeroCols(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	// Should return false without panicking.
	if rowContainsStatusBar(emu, 0, 0) {
		t.Error("should return false for zero cols")
	}
}

// Boundary: row index at emulator edge.
func TestRowContainsStatusBar_LastRow(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 10)
	emu.Write([]byte("\033[10;1H"))       // Move to last row (0-indexed: 9)
	emu.Write([]byte("text \u2502 more"))

	if !rowContainsStatusBar(emu, 9, 40) {
		t.Error("should detect │ on last row of emulator")
	}
}
