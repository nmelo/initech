// QA tests for ini-o0j: clipboard overwrite on plain click.
// copySelection must skip zero-width selections (start == end) so that
// simple clicks to focus a pane don't overwrite the system clipboard.
package tui

import "testing"

// TestCopySelection_ZeroWidthSkipped verifies that a zero-width selection
// (start == end, i.e. a plain click with no drag) does not invoke pbcopy.
// Before the fix, every pane-focus click wrote one character to the clipboard.
func TestCopySelection_ZeroWidthSkipped(t *testing.T) {
	calls := stubPbcopy(t)

	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0]
	p.(*Pane).emu.Write([]byte("Hello\r\n"))

	// Simulate a click: start == end at col 2, row 0.
	tui.sel.pane = 0
	tui.sel.startX = 2
	tui.sel.startY = 0
	tui.sel.endX = 2
	tui.sel.endY = 0

	tui.copySelection()

	if len(*calls) != 0 {
		t.Errorf("zero-width click must not invoke pbcopy, got %d call(s): %q", len(*calls), *calls)
	}
}
