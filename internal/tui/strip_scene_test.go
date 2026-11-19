package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// This file compiles on a tree without the strip, on purpose: the 0-item
// golden is written by the commit before the strip existed, so "equals
// today's pane byte for byte" is a comparison with the old code, not with
// itself.

// stripSceneLines is what the agent's screen holds in every strip golden: the
// picture's reply, prompt and status, so the strip's rows sit under real text.
var stripSceneLines = []string{
	"● I sent hover the release order. It's cutting a release from main now.",
	"",
	"  Once the release is live, hover checks production.",
	"",
	"✻ Crunched for 10s · done 8:52 AM",
	"────────────────────────────────────────",
	"❯ ",
	"────────────────────────────────────────",
	"  ⎇ main │ Fable 5.2 │ Camila │ 19% │ 41%",
	"  ⏵⏵ bypass permissions on (shift+tab to cycle)",
}

// newStripScene is a pane of tile cols x tileRows (activity bar and bottom bar
// included) with the scene written at the bottom of its screen, as a Claude
// pane draws its composer.
func newStripScene(cols, tileRows int) (*Pane, tcell.SimulationScreen) {
	termRows := tileRows - 2
	p := newEmuPane("super", cols, termRows)
	p.region = Region{X: 0, Y: 0, W: cols, H: tileRows}
	return p, newSceneScreen(cols, tileRows)
}

func newSceneScreen(cols, rows int) tcell.SimulationScreen {
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		panic(err)
	}
	s.SetSize(cols, rows)
	return s
}

// writeScene writes the scene so its last line is the terminal's last row.
func writeScene(p *Pane, termRows int) {
	var b strings.Builder
	b.WriteString("\x1b[2J")
	start := termRows - len(stripSceneLines) + 1
	for i, l := range stripSceneLines {
		fmt.Fprintf(&b, "\x1b[%d;1H%s", start+i, l)
	}
	p.emu.Write([]byte(b.String()))
}

// renderScene draws the pane the way a frame does, past the resize settle.
func renderScene(p *Pane, s tcell.SimulationScreen) {
	p.resizeSettleFrames = 0
	p.resizeSettleDeadline = time.Time{}
	p.Render(s, false, false, 1, Selection{})
	s.Show()
}

// screenDump is every cell of the screen: its rune and its foreground,
// background and attributes, row by row. Two dumps are equal only when the
// screens are equal cell for cell.
func screenDump(s tcell.SimulationScreen) string {
	cells, w, h := s.GetContents()
	var b strings.Builder
	for y := 0; y < h; y++ {
		var text strings.Builder
		var styles []string
		last := ""
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			r := ' '
			if len(c.Runes) > 0 {
				r = c.Runes[0]
			}
			text.WriteRune(r)
			fg, bg, attr := c.Style.Decompose()
			st := fmt.Sprintf("%06x/%06x/%d", fg.Hex(), bg.Hex(), attr)
			if st != last {
				styles = append(styles, fmt.Sprintf("%d:%s", x, st))
				last = st
			}
		}
		fmt.Fprintf(&b, "%02d|%s|\n   %s\n", y, strings.TrimRight(text.String(), " "), strings.Join(styles, " "))
	}
	return b.String()
}

// checkGolden compares got with testdata/strip/<name>.golden, or writes it
// with -update-golden.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "strip", name+".golden")
	if *updateGoldenFlag {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s: %v (write it with -update-golden)", path, err)
	}
	if string(want) != got {
		t.Errorf("%s differs from its golden:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// The sizes the goldens are drawn at: a super's tile in Nelson's window, and a
// narrow pane.
var stripSceneSizes = []struct {
	name       string
	cols, rows int
}{{"wide", 120, 40}, {"narrow", 60, 30}}

// AC1, 0 items: the pane with no strip is today's pane, cell for cell. The
// goldens were written by the commit before the strip.
func TestStripGolden_NoItemsIsTodaysPane(t *testing.T) {
	for _, sz := range stripSceneSizes {
		p, s := newStripScene(sz.cols, sz.rows)
		writeScene(p, sz.rows-2)
		renderScene(p, s)
		checkGolden(t, "items0-"+sz.name, screenDump(s))
	}
}
