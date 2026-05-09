// Tests for ini-jr0: pasteboard receives ghost "ll" entries from 1-cell
// drag-selections that the operator perceived as clicks.
//
// Phase 1 (extraction-math proof): the existing TestCopySelection_*ExtractsTwoChars*
// tests below show that a 1-cell delta on adjacent cells produces 2 chars in
// the extractor — confirming the bug mathematically without needing to capture
// a wild mouse twitch.
//
// Phase 2 (the fix): copySelection now skips |endX-startX| <= 1 same-row
// selections, so the buggy extraction is never sent to pbcopy. Tests below
// verify the guard fires for the bug shape AND does not over-fire on real
// drags (>= 2 cells, vertical, etc.).
//
// Phase 3 (OSC 52 verification): structured paths (mcp_modal.go,
// web_modal.go) emit base64-encoded payloads through buildOSC52, not
// extracted cells. The TestBuildOSC52_RoundTrip test pins that contract.
package tui

import (
	"encoding/base64"
	"strings"
	"testing"
)

// stubPbcopy replaces pbcopyExec with a recorder for the duration of a test.
// Returns a pointer to the slice of recorded payloads so the test can assert
// on count and content. Restores the production pbcopyExec on cleanup.
//
// Side benefit: tests using stubPbcopy cannot pollute the real macOS clipboard
// the way the previous tests did (cmd.Run errors were silently swallowed).
func stubPbcopy(t *testing.T) *[]string {
	t.Helper()
	orig := pbcopyExec
	var recorded []string
	pbcopyExec = func(text string) error {
		recorded = append(recorded, text)
		return nil
	}
	t.Cleanup(func() { pbcopyExec = orig })
	return &recorded
}

// --- Phase 1: extraction-math proof (kept from the Phase 1 commit) ---

// The 1-cell drag from col 2->3 on "Hello" extracts "ll" — that's THE
// evidence the bug is real, regardless of whether the fix is in place.
// extractSelectionText is a pure function and ignores the copySelection
// guards, so this test is unaffected by Phase 2.
func TestExtractSelectionText_OneCellDragExtractsTwoChars_ini_jr0(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0]
	p.(*Pane).emu.Write([]byte("Hello\r\n"))

	tui.sel.pane = 0
	tui.sel.startX = 2
	tui.sel.startY = 0
	tui.sel.endX = 3
	tui.sel.endY = 0

	got := tui.extractSelectionText()
	if got != "ll" {
		t.Errorf("extractSelectionText() = %q, want %q (PM hypothesis: 1-cell drag extracts 2 adjacent chars)", got, "ll")
	}
}

// Same mechanism on different words — proves it's general, not Hello-specific.
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

// --- Phase 2: the fix in copySelection ---

// 1-cell horizontal drag must NOT invoke pbcopy. This is the load-bearing
// test for the ini-jr0 fix — replaces the prior
// TestCopySelection_SingleCharDragStillWorks which asserted only "must not
// panic" while the bug shipped.
func TestCopySelection_OneCellDrag_NoClipboard_ini_jr0(t *testing.T) {
	calls := stubPbcopy(t)

	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0]
	p.(*Pane).emu.Write([]byte("Hello\r\n"))

	// Drag from col 2 to col 3 on row 0 — the suspected mouse-twitch shape.
	tui.sel.pane = 0
	tui.sel.startX = 2
	tui.sel.startY = 0
	tui.sel.endX = 3
	tui.sel.endY = 0

	tui.copySelection()

	if len(*calls) != 0 {
		t.Errorf("1-cell drag must not invoke pbcopy (ini-jr0 guard), got %d call(s): %q", len(*calls), *calls)
	}
}

// Reverse direction matches: dragging right-to-left by 1 cell must also
// trigger the guard. The abs() in the production code is what makes this
// work; a missing abs would let backwards 1-cell drags through.
func TestCopySelection_OneCellReverseDrag_NoClipboard_ini_jr0(t *testing.T) {
	calls := stubPbcopy(t)

	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0]
	p.(*Pane).emu.Write([]byte("Hello\r\n"))

	// Reverse: startX=3, endX=2 (drag left).
	tui.sel.pane = 0
	tui.sel.startX = 3
	tui.sel.startY = 0
	tui.sel.endX = 2
	tui.sel.endY = 0

	tui.copySelection()

	if len(*calls) != 0 {
		t.Errorf("1-cell reverse drag must not invoke pbcopy, got %d call(s): %q", len(*calls), *calls)
	}
}

// Regression guard: a real 2-cell drag MUST still copy. Without this we'd
// have no signal that the fix didn't accidentally disable all small drags.
func TestCopySelection_TwoCellDrag_StillCopies_ini_jr0(t *testing.T) {
	calls := stubPbcopy(t)

	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0]
	p.(*Pane).emu.Write([]byte("Hello\r\n"))

	tui.sel.pane = 0
	tui.sel.startX = 1
	tui.sel.startY = 0
	tui.sel.endX = 3 // 2-cell delta -> 3 cells extracted
	tui.sel.endY = 0

	tui.copySelection()

	if len(*calls) != 1 {
		t.Fatalf("2-cell drag should copy exactly once, got %d call(s)", len(*calls))
	}
	got := (*calls)[0]
	want := "ell" // cols 1, 2, 3 of "Hello"
	if got != want {
		t.Errorf("clipboard payload = %q, want %q", got, want)
	}
}

// Vertical 1-cell drag (startX==endX, |endY-startY|==1) is deliberately
// NOT widened by the guard. A 1-row drag extracts a long L-shape across
// two rows, which is visually obvious selection — not a ghost copy. This
// test pins that decision so a future "let's also skip 1-row drags"
// refactor would have to consciously break it.
func TestCopySelection_VerticalOneCellDrag_StillCopies_ini_jr0(t *testing.T) {
	calls := stubPbcopy(t)

	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0]
	p.(*Pane).emu.Write([]byte("first line\r\nsecond line\r\n"))

	tui.sel.pane = 0
	tui.sel.startX = 5
	tui.sel.startY = 0
	tui.sel.endX = 5
	tui.sel.endY = 1

	tui.copySelection()

	if len(*calls) != 1 {
		t.Errorf("vertical 1-row drag should copy (different bug shape than horizontal twitch), got %d call(s)", len(*calls))
	}
}

// --- Phase 3: OSC 52 verification ---

// TestBuildOSC52_RoundTrip pins the structured-clipboard contract for the
// MCP token and web URL paths. Per PM's triage, those paths are
// "vanishingly unlikely" to leak literal substrings because they encode
// through base64 — this test makes that confidence explicit.
//
// The escape sequence format \033]52;c;<base64>\a is fixed by terminal
// convention; a regression here would mean we shipped a clipboard payload
// that real terminals can't decode.
func TestBuildOSC52_RoundTrip_ini_jr0(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"mcp token style", "Bearer eyJhbGciOiJIUzI1NiJ9.token-here.signature"},
		{"web url style", "http://192.168.1.42:7890"},
		{"empty", ""},
		{"unicode", "café résumé naïve"},
		{"multiline", "line1\nline2"},
		{"contains ll", "calls and hello"}, // even if the input has 'll', the OSC 52 path encodes it safely
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			osc := buildOSC52(tt.content)

			if !strings.HasPrefix(osc, "\033]52;c;") {
				t.Errorf("OSC sequence missing \\033]52;c; prefix: %q", osc)
			}
			if !strings.HasSuffix(osc, "\a") {
				t.Errorf("OSC sequence missing \\a (BEL) terminator: %q", osc)
			}

			// Strip prefix and BEL, decode base64, must round-trip to content.
			payload := strings.TrimPrefix(osc, "\033]52;c;")
			payload = strings.TrimSuffix(payload, "\a")
			decoded, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				t.Fatalf("base64 decode failed: %v (payload %q)", err, payload)
			}
			if string(decoded) != tt.content {
				t.Errorf("round-trip failed: decoded = %q, want %q", string(decoded), tt.content)
			}
		})
	}
}
