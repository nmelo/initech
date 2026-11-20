package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

// render is a plan as the strip's rows read on screen, prefix included.
func (sp stripLayout) render() []string {
	out := make([]string, len(sp.lines))
	for i, l := range sp.lines {
		if l.mark {
			out[i] = " ▶ " + l.text
		} else {
			out[i] = "   " + l.text
		}
	}
	return out
}

// The picture's three items, at its width: one item per line, in order, the
// second wrapping under its own text (points 3 and 6).
func TestStripPlan_ItemsInOrderEachWithTheMark_ALongOneWrapsUnderItsText(t *testing.T) {
	items := []string{
		"Release north? Recommended yes.",
		"Put the drone on the bench, then run the split-screen read with benchy (about 15 min, props off, battery charged).",
		"CI runner for eastrig's lab: register it?",
	}
	sp := stripPlan(items, 40, 61) // 58 columns of text: the second item's first row is exactly 58
	want := []string{
		"   ",
		" ▶ Release north? Recommended yes.",
		" ▶ Put the drone on the bench, then run the split-screen read",
		"   with benchy (about 15 min, props off, battery charged).",
		" ▶ CI runner for eastrig's lab: register it?",
		"   ",
	}
	if got := sp.render(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if sp.height != 6 || sp.more != 0 || sp.barCount != 0 {
		t.Errorf("height %d more %d bar %d, want 6 0 0 (two pads, four item lines)", sp.height, sp.more, sp.barCount)
	}
}

// No item is ever cut: every word of every shown item is on screen, every row
// fits the width, at every width from the narrowest that draws upward.
func TestStripPlan_NothingIsCutAtAnyWidth(t *testing.T) {
	item := "Approve the v2.55 release: it carries the strip, the push fix and an unbreakablewordthatislongerthanthepane"
	for cols := 13; cols <= 120; cols++ {
		sp := stripPlan([]string{item}, 200, cols)
		if sp.barCount != 0 {
			t.Fatalf("cols %d: not drawn", cols)
		}
		var text []string
		for _, r := range sp.render() {
			if w := runewidth.StringWidth(r); w > cols {
				t.Fatalf("cols %d: row %q is %d wide", cols, r, w)
			}
			text = append(text, strings.TrimSpace(strings.TrimPrefix(r, " ▶ ")))
		}
		if joined := strings.ReplaceAll(strings.Join(text, ""), " ", ""); joined != strings.ReplaceAll(item, " ", "") {
			t.Fatalf("cols %d: text lost or changed:\n%q\nwant\n%q", cols, joined, item)
		}
		rows := sp.render()
		if rows[0] != "   " || rows[len(rows)-1] != "   " {
			t.Fatalf("cols %d: pads %q and %q, want blank", cols, rows[0], rows[len(rows)-1])
		}
		for i, r := range rows[2 : len(rows)-1] { // between the item's first row and the bottom pad
			if !strings.HasPrefix(r, "   ") || r[3] == ' ' {
				t.Fatalf("cols %d: continuation row %d %q is not indented under the text", cols, i+2, r)
			}
		}
	}
}

// Wide characters count by their width, never their bytes or runes.
func TestStripPlan_WideCharactersWrapByWidth(t *testing.T) {
	sp := stripPlan([]string{strings.Repeat("界", 12)}, 40, 13) // 10 columns of text, 5 per row
	for _, r := range sp.render() {
		if w := runewidth.StringWidth(r); w > 13 {
			t.Fatalf("row %q is %d wide", r, w)
		}
	}
	if sp.height != 5 {
		t.Errorf("height %d, want 5 (two pads, three item rows): %q", sp.height, sp.render())
	}
}

// D4: at most a third of the rows, and the agent keeps at least 6; whole
// items from the top, then "+N more".
func TestStripPlan_Crowding_ShowsWholeItemsThenMore_AndKeepsTheAgentSixRows(t *testing.T) {
	var items []string
	for i := 1; i <= 10; i++ {
		items = append(items, fmt.Sprintf("Item %d: %s", i, strings.Repeat("words ", 12)))
	}
	for rows := 7; rows <= 60; rows++ {
		sp := stripPlan(items, rows, 50)
		if sp.height == 0 {
			if sp.barCount != 10 {
				t.Fatalf("rows %d: no strip and bar count %d, want 10", rows, sp.barCount)
			}
			continue
		}
		if rows-sp.height < 6 {
			t.Fatalf("rows %d: strip %d leaves the agent %d rows", rows, sp.height, rows-sp.height)
		}
		if sp.height > rows/3 {
			t.Fatalf("rows %d: strip %d is over a third", rows, sp.height)
		}
		if sp.height != len(sp.lines) {
			t.Fatalf("rows %d: height %d but %d lines", rows, sp.height, len(sp.lines))
		}
		if first, final := sp.lines[0], sp.lines[len(sp.lines)-1]; first != (stripLine{}) || final != (stripLine{}) {
			t.Fatalf("rows %d: the strip does not start and end with a blank pad: %q", rows, sp.render())
		}
		last := sp.render()[len(sp.lines)-2] // the line above the bottom pad
		shown := 0
		for _, l := range sp.lines {
			if l.mark && !strings.HasPrefix(l.text, "+") {
				shown++
			}
		}
		if shown+sp.more != 10 {
			t.Fatalf("rows %d: %d shown + %d more is not 10", rows, shown, sp.more)
		}
		if sp.more > 0 && last != fmt.Sprintf(" ▶ +%d more", sp.more) {
			t.Fatalf("rows %d: last row %q, want +%d more", rows, last, sp.more)
		}
	}
}

// A tile too small to spare a row, or too narrow for 10 columns of text,
// draws no strip and puts the count in the bar (D4, D6).
func TestStripPlan_TooSmallOrTooNarrow_DrawsNothingAndCountsInTheBar(t *testing.T) {
	two := []string{"a", "b"}
	for _, c := range []struct{ rows, cols int }{{8, 80}, {7, 80}, {6, 80}, {5, 80}, {1, 80}, {40, 12}, {40, 3}} {
		sp := stripPlan(two, c.rows, c.cols)
		if sp.height != 0 || len(sp.lines) != 0 || sp.barCount != 2 {
			t.Errorf("rows %d cols %d: %+v, want no strip and bar 2", c.rows, c.cols, sp)
		}
	}
	// Twins: one row more (room for both pads and one item line), or one
	// column more, and it draws, padded.
	if sp := stripPlan(two[:1], 9, 80); sp.height != 3 || sp.barCount != 0 {
		t.Errorf("9 rows: %+v, want a pad, one item, a pad", sp)
	}
	if sp := stripPlan(two, 40, 13); sp.height != 4 || sp.barCount != 0 {
		t.Errorf("13 cols: %+v, want both drawn between pads", sp)
	}
}

// No items: nothing at all, so the pane is today's pane.
func TestStripPlan_NoItemsIsNothing(t *testing.T) {
	for _, items := range [][]string{nil, {}} {
		if sp := stripPlan(items, 40, 80); sp.height != 0 || len(sp.lines) != 0 || sp.barCount != 0 || sp.more != 0 {
			t.Errorf("%v: %+v, want nothing", items, sp)
		}
	}
}
