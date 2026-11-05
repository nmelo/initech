//go:build !windows

package tui

import "testing"

// TestRigPanel_AStaticFrameWithThePanelEmitsNothing is P39's claim with the
// panel shown: an unchanged frame costs a real terminal nothing.
func TestRigPanel_AStaticFrameWithThePanelEmitsNothing(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "eng2", "eng3", "qa1", "qa2")
	tui.layoutState.Overlay = true
	s, emitted := realTerminalScreen(t)
	tui.screen = newFrameScreen(s)
	emitted()
	tui.render()
	if n := emitted(); n == 0 {
		t.Fatal("fixture: the first frame emitted nothing")
	}
	for f := 2; f <= 6; f++ {
		tui.render()
		if n := emitted(); n != 0 {
			t.Errorf("static frame %d with the panel emitted %d bytes", f, n)
		}
	}
}
