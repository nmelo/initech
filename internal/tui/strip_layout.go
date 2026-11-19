package tui

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"
)

// The list strip: a few lines an agent sets with `initech strip`, drawn by
// initech directly above the pane's bottom bar and below the agent's own
// screen. Each line is " ▶ " and an item's text; an item that does not fit
// wraps onto lines indented three columns, under its text, and is never cut.

// stripIndent is the columns before an item's text: a space, the mark, a space.
const stripIndent = 3

// stripMinText is the narrowest text column the strip draws in (D6).
const stripMinText = 10

// stripAgentFloor is the rows the agent always keeps, the height a send needs
// (D4). It matches shortPaneRows, so a strip never pushes a pane into the
// short-tile terminal.
const stripAgentFloor = 6

// stripLine is one row of the strip: mark says it starts an item (or the
// "+N more" line), otherwise it continues the item above.
type stripLine struct {
	mark bool
	text string
}

// stripLayout is the strip as a pane of rows x cols content draws it.
type stripLayout struct {
	lines []stripLine
	// height is the rows the strip takes from the agent; len(lines).
	height int
	// more is how many items did not fit and are counted on the last line.
	more int
	// barCount is how many items the bottom bar counts when the strip cannot
	// be drawn at all; 0 when it is drawn or there is nothing to draw.
	barCount int
}

// stripPlan lays items out for a pane whose content (the agent's terminal
// before the strip) is rows x cols. The strip takes at most a third of the
// rows and leaves the agent at least stripAgentFloor; items that do not fit
// show whole from the top with a last line "+N more". When no row can be
// spared, or the text column would be narrower than stripMinText, nothing is
// drawn and barCount carries the number of items.
func stripPlan(items []string, rows, cols int) stripLayout {
	if len(items) == 0 {
		return stripLayout{}
	}
	maxH := min(rows/3, rows-stripAgentFloor)
	textW := cols - stripIndent
	if maxH < 1 || textW < stripMinText {
		return stripLayout{barCount: len(items)}
	}

	wrapped := make([][]string, len(items))
	total := 0
	for i, it := range items {
		wrapped[i] = wrapStripText(it, textW)
		total += len(wrapped[i])
	}

	var sp stripLayout
	add := func(rows []string) {
		for j, r := range rows {
			sp.lines = append(sp.lines, stripLine{mark: j == 0, text: r})
		}
	}
	if total <= maxH {
		for _, w := range wrapped {
			add(w)
		}
	} else {
		room := maxH - 1 // the last row says how many more
		shown := 0
		for _, w := range wrapped {
			if len(sp.lines)+len(w) > room {
				break
			}
			add(w)
			shown++
		}
		sp.more = len(items) - shown
		sp.lines = append(sp.lines, stripLine{mark: true, text: fmt.Sprintf("+%d more", sp.more)})
	}
	sp.height = len(sp.lines)
	return sp
}

// wrapStripText breaks text into rows of at most width columns, at spaces
// where it can; a word wider than a row is broken where the row ends. No
// character is dropped except the spaces a break replaces.
func wrapStripText(text string, width int) []string {
	var rows []string
	var cur strings.Builder
	curW := 0
	flush := func() {
		rows = append(rows, cur.String())
		cur.Reset()
		curW = 0
	}
	for _, word := range strings.Fields(text) {
		ww := runewidth.StringWidth(word)
		if curW > 0 && curW+1+ww <= width {
			cur.WriteByte(' ')
			cur.WriteString(word)
			curW += 1 + ww
			continue
		}
		if curW > 0 {
			flush()
		}
		for _, r := range word {
			rw := runewidth.RuneWidth(r)
			if curW+rw > width {
				flush()
			}
			cur.WriteRune(r)
			curW += rw
		}
	}
	if curW > 0 || len(rows) == 0 {
		flush()
	}
	return rows
}
