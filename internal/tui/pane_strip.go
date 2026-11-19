package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
)

// The strip's colours, from the picture (initech-list-strip-mock.html): a
// background a step lighter than the pane's, the text near white, the mark
// amber.
var (
	stripBG        = tcell.NewHexColor(0x2b303b)
	stripTextStyle = tcell.StyleDefault.Background(stripBG).Foreground(tcell.NewHexColor(0xe3e6ec))
	stripMarkStyle = tcell.StyleDefault.Background(stripBG).Foreground(tcell.NewHexColor(0xe5c07b))
	stripBarStyle  = tcell.StyleDefault.Background(trueBlack).Foreground(tcell.NewHexColor(0xe5c07b))
)

// stripMark begins each item's first line.
const stripMark = '▶'

// stripKeySep joins items into a look's strip key; items never hold a control
// character (the command refuses them), so it cannot appear inside one.
const stripKeySep = "\x1f"

// stripKey is items as one comparable string, for a look's ==.
func stripKey(items []string) string { return strings.Join(items, stripKeySep) }

// stripItemsOf reads a look's key back into items.
func stripItemsOf(key string) []string {
	if key == "" {
		return nil
	}
	return strings.Split(key, stripKeySep)
}

// StripItems is the pane's list strip, in the agent's order; nil when none.
func (p *Pane) StripItems() []string {
	if v := p.strip.Load(); v != nil {
		return *v
	}
	return nil
}

// stripHeight is the rows the strip takes from a terminal of rows x cols.
func stripHeight(items []string, rows, cols int) int {
	return stripPlan(items, rows, cols).height
}

// drawStrip draws the strip in the rows directly above the region's bottom
// bar, edge to edge, and returns its height. Nothing is drawn, and 0 comes
// back, when there are no items or the tile cannot spare a row.
func drawStrip(s *clampedScreen, r Region, items []string) int {
	cols, rows := r.TerminalSize()
	sp := stripPlan(items, rows, cols)
	top := r.Y + r.H - 1 - sp.height
	for i, l := range sp.lines {
		y := top + i
		for x := r.X; x < r.X+r.W; x++ {
			s.SetContent(x, y, ' ', nil, stripTextStyle)
		}
		col := r.X + 1
		if l.mark {
			s.SetContent(col, y, stripMark, nil, stripMarkStyle)
		}
		col = r.X + stripIndent
		for _, ch := range l.text {
			s.SetContent(col, y, ch, nil, stripTextStyle)
			col += max(1, runewidth.RuneWidth(ch))
		}
	}
	return sp.height
}

// stripBarCount is how many items a tile too small for the strip counts in
// its bottom bar: 0 when the strip is drawn or there is none.
func stripBarCount(items []string, r Region) int {
	cols, rows := r.TerminalSize()
	return stripPlan(items, rows, cols).barCount
}

// renderStripCount draws "▶ N" in the bar from col, for a strip the tile cannot
// show, so a list is never hidden without a sign. Returns the column after it.
func renderStripCount(s *clampedScreen, r Region, col, n int) int {
	if n <= 0 {
		return col
	}
	ribbonY := r.Y + r.H - 1
	for _, ch := range fmt.Sprintf(" %c %d ", stripMark, n) {
		if col < r.X+r.W {
			s.SetContent(col, ribbonY, ch, nil, stripBarStyle)
			col++
		}
	}
	return col
}

// stripRows is the rows a terminal requested at rows x cols keeps once the
// strip has taken its share (D8: the owner takes them off every request).
func (p *Pane) stripRows(rows, cols int) int {
	return rows - stripHeight(p.StripItems(), rows, cols)
}

// noteRequest records a size request before the strip takes its rows.
func (p *Pane) noteRequest(rows, cols int, exact bool) {
	p.reqRows.Store(int32(rows))
	p.reqCols.Store(int32(cols))
	p.reqExact.Store(exact)
}

// adoptStrip gives a new pane its agent's stored list and records the size
// it was asked for before the strip took its rows.
func (p *Pane) adoptStrip(items []string, reqRows, cols int) {
	if len(items) > 0 {
		cp := append([]string(nil), items...)
		p.strip.Store(&cp)
	}
	p.noteRequest(reqRows, cols, true)
}
