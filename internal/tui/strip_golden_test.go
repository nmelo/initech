package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// The picture's items: a short one, one that wraps at the wide size, a third.
var pictureItems = []string{
	"Release hover? Recommended yes: it ships the recording fix and frees 48w5p's last steps.",
	"Put the drone on the bench, then run the split-screen read on hov-tze7.1 with benchy (about 15 min, props off, battery charged).",
	"CI runner for nayutal's lab: register it? Victor-san has write access to that repo.",
}

// stripScene renders the scene with items, after the pane took its rows.
func stripScene(cols, tileRows int, items []string) (*Pane, tcell.SimulationScreen) {
	p, s := newStripScene(cols, tileRows)
	p.setStripForTest(items)
	tc, tr := p.region.TerminalSize()
	p.ResizeExact(tr, tc)
	writeScene(p, tr-stripHeight(items, tr, tc))
	renderScene(p, s)
	return p, s
}

func (p *Pane) setStripForTest(items []string) {
	if len(items) == 0 {
		p.strip.Store(nil)
		return
	}
	cp := append([]string(nil), items...)
	p.strip.Store(&cp)
}

// rowText is one screen row's text, trailing spaces cut.
func stripRowText(s tcell.SimulationScreen, y int) string {
	cells, w, _ := s.GetContents()
	var b strings.Builder
	for x := 0; x < w; x++ {
		c := cells[y*w+x]
		if len(c.Runes) > 0 {
			b.WriteRune(c.Runes[0])
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func cellStyle(s tcell.SimulationScreen, x, y int) (fg, bg tcell.Color) {
	cells, w, _ := s.GetContents()
	fg, bg, _ = cells[y*w+x].Style.Decompose()
	return
}

// AC1, 1 and 3 items at both sizes: the layout, tint, mark, wrap and indent,
// cell for cell.
func TestStripGolden_OneAndThreeItems(t *testing.T) {
	for _, sz := range stripSceneSizes {
		for _, n := range []int{1, 3} {
			_, s := stripScene(sz.cols, sz.rows, pictureItems[:n])
			checkGolden(t, fmt.Sprintf("items%d-%s", n, sz.name), screenDump(s))
		}
	}
}

// Point 1: the strip's rows are the ones directly above the bottom bar, below
// the agent's last row; the bar stays the tile's last row.
func TestStrip_SitsDirectlyAboveTheBar_BelowTheAgent(t *testing.T) {
	_, s := stripScene(120, 40, pictureItems)
	// 3 items at 120 columns: the second wraps, so 4 rows, 35..38; bar 39.
	if got := stripRowText(s, 39); !strings.Contains(got, "super") {
		t.Fatalf("bar moved: row 39 is %q", got)
	}
	for y, want := range map[int]string{35: " ▶ Release hover?", 36: " ▶ Put the drone", 37: "   ", 38: " ▶ CI runner"} {
		if got := stripRowText(s, y); !strings.HasPrefix(got, want) {
			t.Errorf("row %d is %q, want it to start %q", y, got, want)
		}
	}
	if got := stripRowText(s, 34); !strings.Contains(got, "bypass permissions") {
		t.Errorf("row 34, the agent's last, is %q: the agent's screen does not end above the strip", got)
	}
}

// Point 2: every strip cell, edge to edge, has the strip's background; the
// mark is amber and the text near white. Twin: an agent cell has not.
func TestStrip_TintEdgeToEdge_MarkAndTextColours(t *testing.T) {
	_, s := stripScene(120, 40, pictureItems)
	for y := 35; y <= 38; y++ {
		for _, x := range []int{0, 1, 2, 60, 119} {
			if _, bg := cellStyle(s, x, y); bg != stripBG {
				t.Fatalf("cell %d,%d background %06x, want %06x", x, y, bg.Hex(), stripBG.Hex())
			}
		}
	}
	if fg, _ := cellStyle(s, 1, 35); fg.Hex() != 0xe5c07b {
		t.Errorf("mark colour %06x, want e5c07b", fg.Hex())
	}
	if fg, _ := cellStyle(s, 3, 35); fg.Hex() != 0xe3e6ec {
		t.Errorf("text colour %06x, want e3e6ec", fg.Hex())
	}
	if _, bg := cellStyle(s, 10, 34); bg == stripBG {
		t.Error("the agent's last row has the strip's background")
	}
}

// Point 3: a wrapped line continues under the text (column 3), never under
// the mark, and nothing is cut, in a narrow pane too.
func TestStrip_WrapsUnderTheText_InANarrowPane(t *testing.T) {
	_, s := stripScene(60, 30, pictureItems[1:2])
	var got []string
	for y := 0; y < 29; y++ {
		if _, bg := cellStyle(s, 0, y); bg == stripBG {
			got = append(got, stripRowText(s, y))
		}
	}
	if len(got) < 2 {
		t.Fatalf("strip rows %q, want the item wrapped", got)
	}
	var text []string
	for i, r := range got {
		if i == 0 {
			if !strings.HasPrefix(r, " ▶ ") {
				t.Fatalf("first row %q has no mark", r)
			}
			text = append(text, strings.TrimPrefix(r, " ▶ "))
			continue
		}
		if !strings.HasPrefix(r, "   ") || r[3] == ' ' {
			t.Fatalf("row %q does not continue at column 3", r)
		}
		text = append(text, strings.TrimSpace(r))
	}
	if strings.Join(text, " ") != pictureItems[1] {
		t.Fatalf("item cut or changed:\n%q\nwant\n%q", strings.Join(text, " "), pictureItems[1])
	}
}

// Point 7 and D8: the agent's terminal is the tile less the strip, for any
// request; with no strip it is the whole tile.
func TestStrip_TheAgentsTerminalGivesUpTheStripsRows(t *testing.T) {
	p, _ := stripScene(120, 40, pictureItems)
	if cols, rows := p.emuSize(); rows != 38-4 || cols != 120 {
		t.Fatalf("terminal %dx%d, want 120x34 (38 rows less a 4-row strip)", cols, rows)
	}
	p.setStripForTest(nil)
	p.ResizeExact(38, 120)
	if _, rows := p.emuSize(); rows != 38 {
		t.Fatalf("terminal %d rows with no strip, want 38", rows)
	}
}

// D4: a tile too small for the strip draws none and counts the items in the
// bar, after the bead; twin: one row taller draws it.
func TestStrip_TooSmallATile_CountsInTheBar(t *testing.T) {
	_, s := stripScene(80, 8, pictureItems) // 6 terminal rows: none to spare
	bar := stripRowText(s, 7)
	if !strings.Contains(bar, "▶ 3") {
		t.Fatalf("bar %q has no ▶ 3", bar)
	}
	for y := 0; y < 7; y++ {
		if _, bg := cellStyle(s, 0, y); bg == stripBG {
			t.Fatalf("row %d drawn as strip in a tile too small", y)
		}
	}
	_, s = stripScene(80, 9, pictureItems)
	if bar := stripRowText(s, 8); strings.Contains(bar, "▶ 3") {
		t.Errorf("bar %q counts items the strip shows", bar)
	}
	if _, bg := cellStyle(s, 0, 7); bg != stripBG {
		t.Error("a 9-row tile drew no strip row")
	}
}
