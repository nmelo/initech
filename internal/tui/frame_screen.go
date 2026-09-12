// frame_screen.go is the frame buffer between initech's renderer and tcell
// (ini-p39).
//
// tcell's differential update compares a cell against what was last SHOWN --
// but its Put marks a cell dirty whenever the new content differs from the
// cell's CURRENT content. render() clears the screen and redraws every frame,
// so every non-blank cell goes 'L' -> ' ' -> 'L' inside one frame and is
// re-emitted to the terminal although nothing changed; anything painted over
// (the Agents overlay above pane text) hits the same rule. Measured at
// e9c0eca on an idle six-pane fleet: every one of ~30 frames a second re-sent
// every pane row, 165 KB/s of identical bytes.
//
// frameScreen keeps a back buffer the renderer draws into and a front buffer
// holding what tcell was last given. Show flushes only the cells whose FINAL
// value differs from the front buffer, and skips tcell's Show entirely when
// nothing does. tcell never sees an intermediate value, so its own diff works
// again for real changes. What a frame looks like is unchanged by
// construction: the same final values reach tcell, in the same cells.
package tui

import "github.com/gdamore/tcell/v2"

// frameBufCell is one cell of a frame: its primary rune, any combining runes,
// and style. Comparable, so a frame diff is a struct compare per cell.
type frameBufCell struct {
	main  rune
	comb  string // combining runes; "" for nearly every cell, so no allocation
	style tcell.Style
}

// blankFrameCell is what Clear leaves in every cell.
var blankFrameCell = frameBufCell{main: ' ', style: tcell.StyleDefault}

// frameScreen is a tcell.Screen whose drawing calls land in a back buffer and
// reach the wrapped screen only on Show, and only where the frame changed.
type frameScreen struct {
	tcell.Screen
	w, h  int
	back  []frameBufCell // what this frame draws
	front []frameBufCell // what the wrapped screen was last given

	// frontValid is false until the first flush and after Sync or a resize:
	// the wrapped screen's contents are then unknown and the next Show
	// flushes every cell.
	frontValid bool

	// lastFlushed counts the cells the last Show sent on; lastShown reports
	// whether it reached the wrapped screen's Show at all.
	lastFlushed int
	lastShown   bool
}

// newFrameScreen wraps s. The wrapped screen must already be initialised.
func newFrameScreen(s tcell.Screen) *frameScreen {
	f := &frameScreen{Screen: s}
	f.syncSize()
	return f
}

// syncSize follows the wrapped screen's size, reallocating both buffers when
// it changes; the front buffer is then unknown.
func (f *frameScreen) syncSize() {
	w, h := f.Screen.Size()
	if w == f.w && h == f.h && f.back != nil {
		return
	}
	f.w, f.h = w, h
	f.back = make([]frameBufCell, w*h)
	f.front = make([]frameBufCell, w*h)
	for i := range f.back {
		f.back[i] = blankFrameCell
	}
	f.frontValid = false
}

// Clear blanks the back buffer. Cheap: a slice fill, no tcell call.
func (f *frameScreen) Clear() {
	f.syncSize()
	for i := range f.back {
		f.back[i] = blankFrameCell
	}
}

// Fill sets every cell of the back buffer.
func (f *frameScreen) Fill(r rune, style tcell.Style) {
	f.syncSize()
	c := frameBufCell{main: r, style: style}
	for i := range f.back {
		f.back[i] = c
	}
}

// SetContent draws into the back buffer. Out-of-range cells are ignored, as
// tcell ignores them.
func (f *frameScreen) SetContent(x, y int, mainc rune, combc []rune, style tcell.Style) {
	f.syncSize()
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return
	}
	c := frameBufCell{main: mainc, style: style}
	if len(combc) > 0 {
		c.comb = string(combc)
	}
	f.back[y*f.w+x] = c
}

// Put draws a string into one cell of the back buffer. initech draws with
// SetContent; this exists so nothing reaches the wrapped screen around the
// buffer. Width is reported as 1: the wrapped screen measures on flush.
func (f *frameScreen) Put(x, y int, str string, style tcell.Style) (string, int) {
	f.syncSize()
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return "", 0
	}
	runes := []rune(str)
	c := frameBufCell{main: ' ', style: style}
	if len(runes) > 0 {
		c.main, c.comb = runes[0], string(runes[1:])
	}
	f.back[y*f.w+x] = c
	return "", 1
}

// GetContent reads the back buffer: what THIS frame has drawn so far, which
// is what a renderer reading back its own output wants. Width is 1.
func (f *frameScreen) GetContent(x, y int) (rune, []rune, tcell.Style, int) {
	f.syncSize()
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return 0, nil, tcell.StyleDefault, 0
	}
	c := f.back[y*f.w+x]
	var comb []rune
	if c.comb != "" {
		comb = []rune(c.comb)
	}
	return c.main, comb, c.style, 1
}

// Get reads the back buffer as text.
func (f *frameScreen) Get(x, y int) (string, tcell.Style, int) {
	f.syncSize()
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return "", tcell.StyleDefault, 0
	}
	c := f.back[y*f.w+x]
	return string(c.main) + c.comb, c.style, 1
}

// Show flushes the cells that changed since the last flush to the wrapped
// screen and shows it -- or does nothing at all when the frame is identical.
func (f *frameScreen) Show() {
	f.syncSize()
	n := 0
	for i := range f.back {
		c := f.back[i]
		if f.frontValid && f.front[i] == c {
			continue
		}
		var comb []rune
		if c.comb != "" {
			comb = []rune(c.comb)
		}
		f.Screen.SetContent(i%f.w, i/f.w, c.main, comb, c.style)
		f.front[i] = c
		n++
	}
	f.frontValid = true
	f.lastFlushed = n
	f.lastShown = n > 0
	if n > 0 {
		f.Screen.Show()
	}
}

// Sync forgets what the wrapped screen holds and resyncs it: the next Show
// flushes every cell, as the terminal is about to be repainted in full.
func (f *frameScreen) Sync() {
	f.frontValid = false
	f.Screen.Sync()
}
