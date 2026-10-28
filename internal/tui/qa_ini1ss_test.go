// QA tests for ini-1ss: Activity detection idle timeout fix.
// NOTE: ini-oiw replaced the JSONL-based mechanism (jsonlIdleTimeout/lastJsonlType/lastJsonlTime)
// with PTY output recency (lastOutputTime + ptyIdleTimeout). The JSONL-specific tests
// from the original QA pass have been retired. The overlay dot rendering tests below
// remain valid — they test ActivityState rendering, not the detection mechanism.
package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// Rendering: StateRunning → filled dot ● (U+25CF) with green color in overlay.
func TestOverlayDotRunningIsFilledGreen(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.layoutState.Overlay = true
	// Use lastOutputTime so updateActivity() (called by render) computes StateRunning.
	tui.panes[0].(*Pane).lastOutputTime = time.Now()
	tui.render()

	dotX, dotY, ok := overlayDotOf(overlayScreenRows(s), "eng1")
	if !ok {
		t.Fatal("eng1 is not drawn in the overlay")
	}

	mainc, style, _ := s.Get(dotX, dotY)
	if mainc != "\u25cf" {
		t.Errorf("running dot = %q (%q), want ● (U+25CF)", mainc, mainc)
	}
	fg, _, _ := style.Decompose()
	if fg != tcell.ColorGreen {
		t.Errorf("running dot color = %v, want Green", fg)
	}
}

// Rendering: StateIdle → hollow dot ○ (U+25CB) with gray color in overlay.
func TestOverlayDotIdleIsHollowGray(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.layoutState.Overlay = true
	// lastOutputTime is zero (default): time.Since(zero) >> ptyIdleTimeout → StateIdle.
	tui.render()

	dotX, dotY, ok := overlayDotOf(overlayScreenRows(s), "eng1")
	if !ok {
		t.Fatal("eng1 is not drawn in the overlay")
	}

	mainc, style, _ := s.Get(dotX, dotY)
	if mainc != "\u25cb" {
		t.Errorf("idle dot = %q (%q), want ○ (U+25CB)", mainc, mainc)
	}
	fg, _, _ := style.Decompose()
	if fg != tcell.ColorGray {
		t.Errorf("idle dot color = %v, want Gray", fg)
	}
}

// Rendering: two panes, one running one idle — verify each gets correct dot.
func TestOverlayDotMixedRunningAndIdle(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Overlay = true
	tui.panes[0].(*Pane).lastOutputTime = time.Now() // eng1 running: recent output
	// eng2: zero lastOutputTime → idle
	tui.render()

	rows := overlayScreenRows(s)
	x1, y1, ok1 := overlayDotOf(rows, "eng1")
	x2, y2, ok2 := overlayDotOf(rows, "eng2")
	if !ok1 || !ok2 {
		t.Fatal("eng1 or eng2 is not drawn in the overlay")
	}

	c1, style1, _ := s.Get(x1, y1)
	if c1 != "\u25cf" {
		t.Errorf("eng1 (running) dot = %q, want ●", c1)
	}
	fg1, _, _ := style1.Decompose()
	if fg1 != tcell.ColorGreen {
		t.Errorf("eng1 (running) dot color = %v, want Green", fg1)
	}

	c2, style2, _ := s.Get(x2, y2)
	if c2 != "\u25cb" {
		t.Errorf("eng2 (idle) dot = %q, want ○", c2)
	}
	fg2, _, _ := style2.Decompose()
	if fg2 != tcell.ColorGray {
		t.Errorf("eng2 (idle) dot color = %v, want Gray", fg2)
	}
}
