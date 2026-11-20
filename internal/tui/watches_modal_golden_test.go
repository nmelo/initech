package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/watch"
)

// The watches window's goldens (ini-9krg AC1): at 332x67, the picture's
// window, and in a small pane, each with a full list and with one row. Each
// screen row is its text, then a line naming every cell's background, so the
// golden pins the inset, padding, two-row bands, spacing, shades and backdrop:
//
//	. backdrop   m window   s column shade   h column names   b band
//	S selected   ? anything else
//
// Regenerate only on purpose: go test ./internal/tui/ -run TestWatchesModal_Golden -update-golden

// The classes are the palette's values, written out here rather than read
// from the code, so a changed colour fails the golden instead of moving with it.
func wmBgClass(bg tcell.Color) rune {
	switch bg {
	case tcell.NewRGBColor(7, 8, 10): // the panel under the backdrop
		return '.'
	case tcell.NewRGBColor(20, 20, 20): // panel
		return 'm'
	case tcell.NewRGBColor(27, 27, 27): // column shade
		return 's'
	case tcell.NewRGBColor(10, 10, 10): // header band
		return 'h'
	case tcell.NewRGBColor(44, 44, 44): // group band
		return 'b'
	case tcell.ColorDarkBlue: // selection
		return 'S'
	}
	return '?'
}

func wmGoldenScreen(s tcell.SimulationScreen) string {
	w, h := s.Size()
	var b strings.Builder
	for y := 0; y < h; y++ {
		var text, class strings.Builder
		for x := 0; x < w; x++ {
			r, _, st, cw := s.GetContent(x, y)
			_, bg, _ := st.Decompose()
			text.WriteRune(r)
			class.WriteRune(wmBgClass(bg))
			if cw > 1 {
				x += cw - 1
				class.WriteString(strings.Repeat(string(wmBgClass(bg)), cw-1))
			}
		}
		fmt.Fprintf(&b, "%s\n%s\n", strings.TrimRight(text.String(), " "), strings.TrimRight(class.String(), "."))
	}
	return b.String()
}

func TestWatchesModal_Golden(t *testing.T) {
	full, world := wmFixture()
	one := []watch.Watch{wmWatch("w1", "eng8", watch.KindSendDelivered,
		map[string]string{"pane": "pm", "receipt": "r9"}, "pm re-checks item 4", -2*time.Minute, 8*time.Minute)}
	for _, c := range []struct {
		name string
		w, h int
		ws   []watch.Watch
	}{
		{"332x67_full", 332, 67, full},
		{"332x67_one", 332, 67, one},
		{"100x30_full", 100, 30, full},
		{"100x30_one", 100, 30, one},
		// A pane shorter than the list: the window keeps its 2-row margin.
		{"100x16_full", 100, 16, full},
	} {
		t.Run(c.name, func(t *testing.T) {
			tu, s := wmScreen(t)
			s.SetSize(c.w, c.h)
			s.Clear()
			tu.drawWatchesModal(s, buildWatchesView(c.ws, world, notStarting, wmT0), wmT0)
			got := wmGoldenScreen(s)
			path := filepath.Join("testdata", "watches_modal_"+c.name+".golden")
			if updateGolden() {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (regenerate with -update-golden, only on purpose): %v", err)
			}
			if w := strings.ReplaceAll(string(want), "\r\n", "\n"); got != w {
				gl, wl := strings.Split(got, "\n"), strings.Split(w, "\n")
				for i := 0; i < len(gl) && i < len(wl); i++ {
					if gl[i] != wl[i] {
						t.Fatalf("%s differs at line %d:\n got %q\nwant %q", c.name, i+1, gl[i], wl[i])
					}
				}
				t.Fatalf("%s: %d lines, want %d", c.name, len(gl), len(wl))
			}
		})
	}
}
