package tui

import (
	"sync/atomic"

	"github.com/gdamore/tcell/v2"
)

// The "changed since the last frame" fact (ini-ysr.1), the base the redraw
// work stands on: a pane needs drawing only when its content changed or the
// way it is drawn changed. Two halves make it.
//
// What the pane alone knows is a flag on the pane, set where it happens:
// bytes reached a local pane's emulator, a window 2 pane's stream delivered a
// frame, or the pane was scrolled. Any goroutine may set it; the frame loop
// takes it (reads and clears) once per drawn pane.
//
// What the frame loop knows -- focus, dimming, the pane's number, its region
// (a resize or a layout change) and the selection -- is compared with what the
// pane was last drawn with. One place, so a focus change made by any of the
// paths that move focus is seen without a hook in each.
//
// Nothing acts on the answer yet (step 2 of the epic does).

// frameChange is the pane's half of the fact. renderFinal is whether the
// last Render drew the pane's real body (main loop only): false while a
// resize settles, after a screen read that failed, and before the first
// draw, so such a pane is drawn again until its body is real (ini-ysr.2).
type frameChange struct {
	dirty       atomic.Bool
	renderFinal bool
}

// renderPending reports whether the pane's last render left its body short
// of real, so it must be drawn again whatever else changed.
func (f *frameChange) renderPending() bool { return !f.renderFinal }

// markFrameChanged records that the pane's content changed since the last
// frame was drawn.
func (f *frameChange) markFrameChanged() { f.dirty.Store(true) }

// takeFrameChanged reports whether the content changed since the last call,
// and clears the fact. Called by the frame loop when it draws the pane.
func (f *frameChange) takeFrameChanged() bool { return f.dirty.Swap(false) }

// paneFrameState is how the frame loop last drew a pane.
type paneFrameState struct {
	focused, dimmed bool
	index           int
	region          Region
	sel             Selection
	tint            tcell.Color // the running tint: it fades on a timer, with no bytes
}

// frameChangeTaker is the pane half's reader, met by Pane and RemotePane.
type frameChangeTaker interface{ takeFrameChanged() bool }

// paneChangedForFrame is the whole fact for one pane in this frame: its own
// flag (taken, so cleared), or a difference from how it was last drawn, or
// never drawn before. It records this frame's state in next, which becomes
// the last-drawn record once the frame is done, so a pane that leaves the
// plan is forgotten.
func paneChangedForFrame(last, next map[PaneView]paneFrameState, pr PaneRender, sel Selection) bool {
	changed := false
	if f, ok := pr.Pane.(frameChangeTaker); ok {
		changed = f.takeFrameChanged()
	}
	st := paneFrameState{focused: pr.Focused, dimmed: pr.Dimmed, index: pr.Index, region: pr.Region, sel: sel}
	if tp, ok := pr.Pane.(interface{ backgroundTint() tcell.Color }); ok {
		st.tint = tp.backgroundTint()
	}
	if prev, ok := last[pr.Pane]; !ok || prev != st {
		changed = true
	}
	next[pr.Pane] = st
	return changed
}

// layerRecorder is the screen the layers drawn over the panes write through
// (the overlay, help, the inbox, toasts, the status bar...): it passes every
// write on and records which cells were written. Next frame those cells are
// restored: blanked where no pane lies, and the panes under them drawn again,
// because the layer may be gone (ini-ysr.2). Every layer writes through
// SetContent only.
type layerRecorder struct {
	tcell.Screen
	w, h  int
	cells []bool
}

func newLayerRecorder(s tcell.Screen) *layerRecorder {
	w, h := s.Size()
	return &layerRecorder{Screen: s, w: w, h: h, cells: make([]bool, w*h)}
}

func (l *layerRecorder) SetContent(x, y int, primary rune, combining []rune, style tcell.Style) {
	if x >= 0 && y >= 0 && x < l.w && y < l.h {
		l.cells[y*l.w+x] = true
	}
	l.Screen.SetContent(x, y, primary, combining, style)
}

// restoreUncovered puts back the cells a layer wrote last frame and did not
// write this frame: the layer is gone or smaller, and what it covered must
// show again. Inside a pane, a chrome row was drawn this frame, and a body
// cell comes back from the pane's cached last body, which a body drawn this
// frame has just refreshed; any other cell, or a pane with no usable cache,
// is blanked and the pane is marked changed so the next frame draws its body.
func restoreUncovered(s tcell.Screen, last, now []bool, w int, panes []PaneRender) {
	if len(last) != len(now) {
		return
	}
	for i, was := range last {
		if !was || now[i] {
			continue
		}
		x, y := i%w, i/w
		restored := false
		for _, pr := range panes {
			r := pr.Region
			if x < r.X || x >= r.X+r.W || y < r.Y || y >= r.Y+r.H {
				continue
			}
			if y == r.Y || y == r.Y+r.H-1 {
				restored = true // the chrome drew this cell this frame
			} else if c, ok := pr.Pane.(interface {
				restoreBodyCell(s tcell.Screen, x, y int) bool
			}); ok && c.restoreBodyCell(s, x, y) {
				restored = true
			} else if f, ok := pr.Pane.(interface{ markFrameChanged() }); ok {
				f.markFrameChanged()
			}
			break
		}
		if !restored {
			s.SetContent(x, y, ' ', nil, tcell.StyleDefault)
		}
	}
}

// blankRegion fills r with blank default cells, as a cleared screen holds.
func blankRegion(s tcell.Screen, r Region) {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			s.SetContent(x, y, ' ', nil, tcell.StyleDefault)
		}
	}
}

// planRegions is the frame's pane layout: which pane sits where. A different
// layout from last frame draws the whole screen afresh.
func planRegions(panes []PaneRender) []paneAt {
	out := make([]paneAt, len(panes))
	for i, pr := range panes {
		out[i] = paneAt{pr.Pane, pr.Region}
	}
	return out
}

type paneAt struct {
	pane   PaneView
	region Region
}

// chromeOnly is met by panes that can draw their chrome without their body.
type chromeOnly interface {
	renderChromeOnly(screen tcell.Screen, focused bool, index int)
	renderPending() bool
}
