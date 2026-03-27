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
	tui.panes[0].lastOutputTime = time.Now()
	tui.render()

	sw, _ := s.Size()
	maxNameLen := 4
	statusMaxLen := 7
	panelW := 4 + maxNameLen + 1 + statusMaxLen + 2
	px := sw - panelW - 1
	dotX := px + 2
	dotY := 2

	mainc, _, style, _ := s.GetContent(dotX, dotY)
	if mainc != '\u25cf' {
		t.Errorf("running dot = %q (%U), want ● (U+25CF)", mainc, mainc)
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

	sw, _ := s.Size()
	panelW := 4 + 4 + 1 + 7 + 2
	px := sw - panelW - 1
	dotX := px + 2
	dotY := 2

	mainc, _, style, _ := s.GetContent(dotX, dotY)
	if mainc != '\u25cb' {
		t.Errorf("idle dot = %q (%U), want ○ (U+25CB)", mainc, mainc)
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
	tui.panes[0].lastOutputTime = time.Now() // eng1 running: recent output
	// eng2: zero lastOutputTime → idle
	tui.render()

	sw, _ := s.Size()
	panelW := 4 + 4 + 1 + 7 + 2
	px := sw - panelW - 1
	dotX := px + 2

	c1, _, style1, _ := s.GetContent(dotX, 2)
	if c1 != '\u25cf' {
		t.Errorf("eng1 (running) dot = %q, want ●", c1)
	}
	fg1, _, _ := style1.Decompose()
	if fg1 != tcell.ColorGreen {
		t.Errorf("eng1 (running) dot color = %v, want Green", fg1)
	}

	c2, _, style2, _ := s.GetContent(dotX, 3)
	if c2 != '\u25cb' {
		t.Errorf("eng2 (idle) dot = %q, want ○", c2)
	}
	fg2, _, _ := style2.Decompose()
	if fg2 != tcell.ColorGray {
		t.Errorf("eng2 (idle) dot color = %v, want Gray", fg2)
	}
}


