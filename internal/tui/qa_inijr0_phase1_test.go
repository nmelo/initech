// Phase 1 confirmation test for ini-jr0: pasteboard receives 'll' from a
// 1-cell drag-selection that the operator perceived as a click.
//
// PM's hypothesis (in the bead): mouse-twitch motion events between Button1
// press and release upgrade what the operator perceives as a click into a
// 1-cell drag (startX != endX by exactly 1). The current zero-width guard
// (ini-o0j) only catches start == end, so a 1-cell drag falls through and
// extracts 2 adjacent cells.
//
// This test pins the underlying extraction math without needing to reproduce
// a wild mouse twitch:
//   - Pane content: "Hello" (cols 0..4 = 'H', 'e', 'l', 'l', 'o')
//   - Selection: startX=2, endX=3 on the same row (a 1-cell delta)
//   - Expected result if PM is right: extractSelectionText returns "ll"
//
// The existing TestCopySelection_SingleCharDragStillWorks runs this exact
// scenario but only asserts "must not panic" — so the bug shipped under
// nominally green tests. This test closes that gap.
package tui

import "testing"

func TestExtractSelectionText_OneCellDragExtractsTwoChars_ini_jr0(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0]
	p.(*Pane).emu.Write([]byte("Hello\r\n"))

	// 1-cell horizontal drag: the suspected mouse-twitch pattern.
	tui.sel.pane = 0
	tui.sel.startX = 2
	tui.sel.startY = 0
	tui.sel.endX = 3
	tui.sel.endY = 0

	got := tui.extractSelectionText()

	// PM's hypothesis confirmed if got == "ll". This is THE evidence that
	// the bug is real: a 1-cell delta selection on adjacent 'l' characters
	// produces "ll" in the clipboard. Operators experience this as ghost
	// "ll" entries because mouse-click events frequently include a single
	// motion event that bumps endX by 1 cell.
	if got != "ll" {
		t.Errorf("extractSelectionText() = %q, want %q (PM hypothesis: 1-cell drag extracts 2 adjacent chars)", got, "ll")
	}
}

// TestExtractSelectionText_OneCellDragOnDifferentWords confirms the mechanism
// is general — not specific to "ll". A 1-cell drag at any column produces 2
// adjacent characters from the underlying pane. This rules out the "maybe
// it's a Hello-specific quirk" alternative explanation.
func TestExtractSelectionText_OneCellDragOnDifferentWords_ini_jr0(t *testing.T) {
	tests := []struct {
		name    string
		content string
		startX  int
		endX    int
		want    string
	}{
		{"calls col 2->3", "calls", 2, 3, "ll"}, // adjacent l's
		{"hello col 2->3", "hello", 2, 3, "ll"}, // adjacent l's, what Nelson sees
		{"world col 0->1", "world", 0, 1, "wo"}, // any pair works, not just ll
		{"abcdef col 3->4", "abcdef", 3, 4, "de"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tui, _ := newTestTUIWithScreen("a")
			tui.applyLayout()
			p := tui.panes[0]
			p.(*Pane).emu.Write([]byte(tt.content + "\r\n"))

			tui.sel.pane = 0
			tui.sel.startX = tt.startX
			tui.sel.startY = 0
			tui.sel.endX = tt.endX
			tui.sel.endY = 0

			got := tui.extractSelectionText()
			if got != tt.want {
				t.Errorf("extractSelectionText() = %q, want %q", got, tt.want)
			}
		})
	}
}
