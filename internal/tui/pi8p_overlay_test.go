package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-pi8p: the overlay row of a background agent says background and the
// job's age, with idle's hollow grey dot (not producing output).
func TestOverlayStatus_ABackgroundAgentShowsItsAge(t *testing.T) {
	tui, screen := newTestTUIWithScreen("eng1")
	lp := tui.panes[0].(*Pane)
	lp.mu.Lock()
	lp.activity = StateIdle
	lp.mu.Unlock()
	lp.observeBackground(time.Now().Add(-90*time.Second), true, parseFooter(lvxwFixture(t, "haiku310_landing_shell.txt"), 160))

	tui.renderOverlay()
	screen.Show()
	w, h := screen.Size()
	for y := 0; y < h; y++ {
		row := make([]rune, 0, w)
		for x := 0; x < w; x++ {
			ch, _, _, _ := screen.GetContent(x, y)
			row = append(row, ch)
		}
		line := string(row)
		if !strings.Contains(line, "eng1") {
			continue
		}
		if !strings.Contains(line, "background 1m") {
			t.Errorf("overlay row %q, want it to say background 1m", strings.TrimSpace(line))
		}
		x := strings.IndexRune(line, '○')
		if x < 0 {
			t.Fatalf("overlay row %q has no hollow dot", strings.TrimSpace(line))
		}
		_, _, style, _ := screen.GetContent(len([]rune(line[:x])), y)
		if fg, _, _ := style.Decompose(); fg != tcell.ColorGray {
			t.Errorf("dot colour %v, want idle's grey", fg)
		}
		return
	}
	t.Fatal("no overlay row for eng1")
}
