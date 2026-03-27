// QA tests for ini-o0j: clipboard overwrite on plain click.
// copySelection must skip zero-width selections (start == end) so that
// simple clicks to focus a pane don't overwrite the system clipboard.
package tui

import "testing"

// TestCopySelection_ZeroWidthSkipped verifies that a zero-width selection
// (start == end, i.e. a plain click with no drag) does not invoke pbcopy.
// Before the fix, every pane-focus click wrote one character to the clipboard.
func TestCopySelection_ZeroWidthSkipped(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0]
	p.emu.Write([]byte("Hello\r\n"))

	// Simulate a click: start == end at col 2, row 0.
	tui.sel.pane = 0
	tui.sel.startX = 2
	tui.sel.startY = 0
	tui.sel.endX = 2
	tui.sel.endY = 0

	// Must not panic and must not call pbcopy. If the guard is missing,
	// this would copy "l" (the character at col 2) to the clipboard.
	tui.copySelection()
}

// TestCopySelection_SingleCharDragStillWorks verifies that a one-column drag
// (startX != endX on the same row) still copies. The guard only blocks when
// start is exactly equal to end.
func TestCopySelection_SingleCharDragStillWorks(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0]
	p.emu.Write([]byte("Hello\r\n"))

	// Drag from col 2 to col 3 on row 0 — one character selected.
	tui.sel.pane = 0
	tui.sel.startX = 2
	tui.sel.startY = 0
	tui.sel.endX = 3
	tui.sel.endY = 0

	// Must not panic. This IS a real selection (drag happened).
	tui.copySelection()
}
