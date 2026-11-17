package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// ini-hkxn: Claude Code 2.1.326 draws a text taller than the input box under
// a "─── ↑ N more ───" top rule, with no ❯ prompt; 2.1.325 put ❯ on the first
// row it showed. The reader found no composer, so the belt withheld the
// Enter of every long send to a narrow pane. Both fixtures are live captures:
// a Haiku pane at 49 columns at the moment of a WITHHELD, and hover's growth
// pane at 47 columns one second after one (its words replaced, its shape
// kept; account names replaced by a placeholder of the same length).

type hkxnFixture struct {
	file, firstRow, model string
}

var hkxnFixtures = []hkxnFixture{
	{"haiku326_scrolled_box_w49.txt", "read. Put the question and your", "Haiku 5.5"},
	{"fable326_scrolled_box_w47.txt", "today (the reviewer reads the code first,", "Fable 5.2"},
}

// hkxnRows is a fixture's rows and the pane width, read from its widest rule.
func hkxnRows(t *testing.T, file string) ([]string, int) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	width := 0
	for _, r := range rows {
		if t := strings.TrimSpace(r); t != "" && strings.Trim(t, "─") == "" {
			width = max(width, utf8.RuneCountInString(t))
		}
	}
	return rows, width
}

func hkxnBoxRow(rows []string) int {
	for i, r := range rows {
		if strings.Contains(r, "↑ 10 more") {
			return i + 1
		}
	}
	return -1
}

// AC2, the reader: every reader of the input box finds the scrolled box:
// the send's composer reader (its first row, as 2.1.325's ❯ row), the frame
// the footer, pending input and the dialog guard share, and the footer.
func TestScrolledBox326_EveryReaderFindsTheBox(t *testing.T) {
	for _, f := range hkxnFixtures {
		rows, width := hkxnRows(t, f.file)
		tail, row, found := composerTailFromRows(rows)
		if !found || row != hkxnBoxRow(rows) || tail != f.firstRow {
			t.Errorf("%s: composer = %q at row %d (found %v), want %q at row %d", f.file, tail, row, found, f.firstRow, hkxnBoxRow(rows))
		}
		if _, _, ok := composerFrame(rows, width); !ok {
			t.Errorf("%s: no composer frame", f.file)
		}
		if text, ok := pendingInputText(rows, width); !ok || text != f.firstRow {
			t.Errorf("%s: pending input = %q (%v), want %q: a pane left with overflowing text reads pending input", f.file, text, ok, f.firstRow)
		}
		if ft := parseFooter(rows, width); ft.Model == nil || *ft.Model != f.model {
			t.Errorf("%s: footer model = %v, want %q", f.file, ft.Model, f.model)
		}
	}
}

// AC2, the belt: before the paste the box was an empty ❯ row; after it, the
// scrolled box. The belt sees the row change and lets the Enter go.
func TestScrolledBox326_TheBeltLetsTheEnterGo(t *testing.T) {
	for _, f := range hkxnFixtures {
		rows, width := hkxnRows(t, f.file)
		p, _, _ := proofPaneSized(t, width, len(rows))
		paintRows(t, p, rows)
		ps := newPendingSubmit("a long body whose tail is in the box", "bracketed", "")
		if accepted, last := composerAcceptedBody(p, "", ps); !accepted {
			t.Errorf("%s: the belt withheld a body sitting in the scrolled box (last polled %q)", f.file, last)
		}
	}
}

// Only the measured rule shape is a scrolled box's top.
func TestScrolledBox326_OnlyTheMeasuredRuleShape(t *testing.T) {
	dash := strings.Repeat("─", 30)
	cases := map[string]bool{
		"─── ↑ 10 more " + dash:          true,
		"  ─── ↑ 3 more " + dash + "  ":  true,
		"─── ↓ 3 more " + dash:           false,
		"─── ↑ ten more " + dash:         false,
		"─── ↑ 10 more lines " + dash:    false,
		"↑ 10 more":                      false,
		"─ ↑ 1 more ─":                   true,
		"─── ↑ 10 more " + dash + " end": false,
		dash:                             false,
		"":                               false,
	}
	for row, want := range cases {
		if got := isScrolledBoxRule(row); got != want {
			t.Errorf("isScrolledBoxRule(%q) = %v, want %v", row, got, want)
		}
	}
}

// A "↑ N more" rule with no box under it at the bottom of the screen (text
// in the transcript quoting one, with the real box below it) is not the box.
func TestScrolledBox326_ARuleWithNoBoxUnderItIsNotTheBox(t *testing.T) {
	rule := strings.Repeat("─", 40)
	quoted := []string{"─── ↑ 4 more " + rule, "  some quoted text", rule, "more transcript", rule, "  ⎇ main │ Haiku 5.5"}
	if _, ok := scrolledBoxFirstRow(quoted); ok {
		t.Fatal("a quoted scrolled box above another box read as the input box")
	}
	if _, _, found := composerTailFromRows(quoted); found {
		t.Fatal("a screen with no prompt and no box at the bottom found a composer")
	}
	withPrompt := []string{"─── ↑ 4 more " + rule, "  quoted", rule, rule, "❯ ours", rule}
	if tail, _, found := composerTailFromRows(withPrompt); !found || tail != "ours" {
		t.Fatalf("❯ must win over a scrolled rule: %q %v", tail, found)
	}
}

// The scrolled box is read before the › and > fallbacks: a ">" in the text
// is never taken for a prompt.
func TestScrolledBox326_ReadBeforeTheGlyphFallbacks(t *testing.T) {
	rule := strings.Repeat("─", 40)
	rows := []string{"output", "─── ↑ 7 more " + rule, "  first visible row", "  then a -> b in the text", rule, "  ⎇ main │ Haiku 5.5"}
	tail, row, found := composerTailFromRows(rows)
	if !found || row != 2 || tail != "first visible row" {
		t.Fatalf("composer = %q at row %d (%v), want the box's first row 2", tail, row, found)
	}
}

// qa2's probe (S3): a "↑ N more" rule sitting directly on the bottom rule has
// no text row between them, so it is no box: no first row, no composer.
func TestScrolledBox326_ARuleOnTheBottomRuleWithNoTextIsNotTheBox(t *testing.T) {
	rule := strings.Repeat("─", 40)
	rows := []string{"transcript", "─── ↑ 4 more " + rule, rule, "  ⎇ main │ Haiku 5.5"}
	if _, ok := scrolledBoxFirstRow(rows); ok {
		t.Fatal("a scrolled rule with no text row under it read as a box")
	}
	if tail, row, found := composerTailFromRows(rows); found {
		t.Fatalf("found a composer %q at row %d with no text row in the box", tail, row)
	}
}
