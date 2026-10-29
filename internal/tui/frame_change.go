package tui

import (
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
)

// frameClock is the frame loop's clock for the background cap; tests step it.
var frameClock = time.Now

// backgroundRedrawsPerSec is the most a background pane's body is drawn in a
// second while its bytes keep coming (ini-ysr.4).
const backgroundRedrawsPerSec = 4

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
// lastLook is the chrome last drawn (main loop only), so chrome that would
// look the same is not drawn again (ini-ysr.3). writes counts the changes
// marked, so the frame loop can tell bytes that arrived since the last frame
// from ones it held; seenWrites and bodyAt (main loop only) are that count at
// the last frame and the time of the last body draw (ini-ysr.4).
type frameChange struct {
	dirty       atomic.Bool
	writes      atomic.Uint64
	renderFinal bool
	lastLook    ribbonLook
	lookDrawn   bool
	seenWrites  uint64
	bodyAt      time.Time
}

// holdBody reports whether the pane's new content waits for a later frame:
// a background pane whose bytes keep coming is drawn at most
// backgroundRedrawsPerSec times a second (ini-ysr.4). Only while they keep
// coming: a frame with no new byte draws what was held, so the frame after
// the last byte shows the final content. Called once a frame per pane.
func (f *frameChange) holdBody(background bool, now time.Time) bool {
	w := f.writes.Load()
	fresh := w != f.seenWrites
	f.seenWrites = w
	// A clock that stepped back draws the pane rather than holding it
	// until the clock catches up.
	since := now.Sub(f.bodyAt)
	return background && fresh && since >= 0 && since < time.Second/backgroundRedrawsPerSec
}

// noteBody records that the frame loop drew the pane's body at now.
func (f *frameChange) noteBody(now time.Time) { f.bodyAt = now }

// ribbonLook is everything a pane's chrome is drawn from: the ribbon's title,
// its style, the bead, the running square, and the top edge (the sweep's
// step, or -1 for a flat edge). The chrome is drawn from this value alone, so
// two equal looks draw the same cells.
type ribbonLook struct {
	title           string
	style           tcell.Style
	bead, beadTitle string
	running         bool
	bar             int
}

// chromeUnchanged reports whether the chrome last drawn looks like l.
func (f *frameChange) chromeUnchanged(l ribbonLook) bool { return f.lookDrawn && f.lastLook == l }

// noteChrome records the chrome just drawn.
func (f *frameChange) noteChrome(l ribbonLook) { f.lastLook, f.lookDrawn = l, true }

// renderPending reports whether the pane's last render left its body short
// of real, so it must be drawn again whatever else changed.
func (f *frameChange) renderPending() bool { return !f.renderFinal }

// markFrameChanged records that the pane's content changed since the last
// frame was drawn.
func (f *frameChange) markFrameChanged() {
	f.writes.Add(1)
	f.dirty.Store(true)
}

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
	pinned          bool        // the live mode [P] badge
}

// frameChangeTaker is the pane half's reader, met by Pane and RemotePane.
type frameChangeTaker interface{ takeFrameChanged() bool }

// bodyCapped is met by panes the background cap applies to (ini-ysr.4).
type bodyCapped interface {
	holdBody(background bool, now time.Time) bool
	noteBody(now time.Time)
}

// paneChangedForFrame is the whole fact for one pane in this frame: its own
// flag (taken, so cleared), or a difference from how it was last drawn, or
// never drawn before. It records this frame's state in next, which becomes
// the last-drawn record once the frame is done, so a pane that leaves the
// plan is forgotten. hold leaves the pane's own flag for a later frame (the
// background cap) unless the pane is drawn anyway for a view change.
func paneChangedForFrame(last, next map[PaneView]paneFrameState, pr PaneRender, sel Selection, pinned, hold bool) bool {
	changed := false
	f, taker := pr.Pane.(frameChangeTaker)
	if taker && !hold {
		changed = f.takeFrameChanged()
	}
	st := paneFrameState{focused: pr.Focused, dimmed: pr.Dimmed, index: pr.Index, region: pr.Region, sel: sel, pinned: pinned}
	if tp, ok := pr.Pane.(interface{ backgroundTint() tcell.Color }); ok {
		st.tint = tp.backgroundTint()
	}
	if prev, ok := last[pr.Pane]; !ok || prev != st {
		changed = true
		if taker && hold {
			f.takeFrameChanged() // drawn now, held bytes included
		}
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
// show again. A cell inside a pane comes back from what the pane last wrote
// there (shownCells), chrome or body, local or window 2 alike. Any other
// cell is blanked; inside a pane with nothing recorded there, the pane is
// marked changed too, so the next frame draws it.
func restoreUncovered(s tcell.Screen, last, now []bool, w int, panes []PaneRender, shown map[PaneView]*shownCells) {
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
			if shown[pr.Pane].restore(s, x, y) {
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

// shownCells is what a pane last wrote to each cell of its region, recorded
// as it draws (ini-ysr.3). A pane at rest is not drawn, so the screen keeps
// its cells; when a layer over it goes, the cells come back from here.
type shownCells struct {
	r     Region
	cells []frameCell
	set   []bool
}

// at readies the record for a pane drawing at r; a new region starts empty.
func (c *shownCells) at(r Region) {
	n := max(r.W, 0) * max(r.H, 0)
	if c.r == r && len(c.cells) == n {
		return
	}
	c.r = r
	c.cells = make([]frameCell, n)
	c.set = make([]bool, n)
}

// forget empties the record: the screen was cleared.
func (c *shownCells) forget() { clear(c.set) }

func (c *shownCells) index(x, y int) (int, bool) {
	if x < c.r.X || y < c.r.Y || x >= c.r.X+c.r.W || y >= c.r.Y+c.r.H {
		return 0, false
	}
	return (y-c.r.Y)*c.r.W + (x - c.r.X), true
}

// restore draws the cell as the pane last wrote it. False when nothing is
// recorded there (or no record).
func (c *shownCells) restore(s tcell.Screen, x, y int) bool {
	if c == nil {
		return false
	}
	i, ok := c.index(x, y)
	if !ok || !c.set[i] {
		return false
	}
	s.SetContent(x, y, c.cells[i].ch, nil, c.cells[i].style)
	return true
}

// shownRecorder is the screen a pane draws through: it passes every write on
// and records the ones inside the pane's region.
type shownRecorder struct {
	tcell.Screen
	c *shownCells
}

func (r *shownRecorder) SetContent(x, y int, ch rune, comb []rune, st tcell.Style) {
	if i, ok := r.c.index(x, y); ok {
		r.c.cells[i] = frameCell{ch: ch, style: st}
		r.c.set[i] = true
	}
	r.Screen.SetContent(x, y, ch, comb, st)
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
// renderChromeOnly draws it only when it would look different from the last
// chrome drawn, and reports whether it drew.
type chromeOnly interface {
	renderChromeOnly(screen tcell.Screen, focused bool, index int) bool
	renderPending() bool
}
