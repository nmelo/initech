package tui

import (
	"fmt"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// BenchmarkRender_IdleSixPanes is the per-frame cost of render() on an idle
// six-pane fleet through the frame buffer: the draw work an unchanged frame
// still does after ini-p39's fix (a).
func BenchmarkRender_IdleSixPanes(b *testing.B) {
	tui, sim := newTestTUIWithScreen("super", "eng1", "eng2", "eng3", "qa1", "qa2")
	tui.screen = newFrameScreen(sim)
	for _, pv := range tui.panes {
		p := pv.(*Pane)
		for l := 1; l <= 6; l++ {
			p.emu.Write([]byte(fmt.Sprintf("LINE-%d-P39-STATIC\r\n", l)))
		}
	}
	tui.render()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tui.render()
	}
	_ = tcell.StyleDefault
}
