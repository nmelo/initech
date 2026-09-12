// screen_read.go is the ONE way the UI main loop touches a pane's emulator
// (ini-psjt).
//
// The emulator's lock can be held indefinitely by a goroutine that is not the
// main loop: readLoop blocked inside emu.Write on the emulator's response pipe
// while responseLoop is blocked in ptmx.Write because the child stopped
// reading. hover's window froze for 30+ hours behind exactly that. Every
// main-loop read that took the lock parked the whole window -- first the
// modal tick, then attention detection, and next in frame order pane
// rendering itself. Patching each caller is the wrong level: the guarantee is
// "the main loop never waits on a pane", and it lives here.
//
// withScreen and withEmulator hand the caller the embedded UNLOCKED Emulator
// inside a bounded try of the lock. Inside the region nothing may take the
// SafeEmulator's lock again: an RWMutex prefers a queued writer, so a nested
// RLock behind one deadlocks. A pane whose lock stays held is SKIPPED -- the
// caller keeps its previous state or frame, says so at INFO (rate-limited),
// and the rest of the window keeps working. lock_discipline_test.go keeps
// every locked emulator call outside this file on an explicit allowlist, so
// a new main-loop caller cannot reintroduce the hang by accident.
package tui

import (
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// screenTryBudget bounds how long a main-loop reader waits for a pane's
// locks. A healthy writer holds the emulator for microseconds per chunk, so
// a transient hold does not cost a frame; a wedged writer costs each frame
// at most this per wedged pane.
const screenTryBudget = 2 * time.Millisecond

// screenTryStep is the pause between retries inside screenTryBudget.
const screenTryStep = 50 * time.Microsecond

// peekTryBudget is the bound for :peek/patrol reads. They are on demand, not
// per frame, so they may wait longer for a busy pane before giving up.
const peekTryBudget = 250 * time.Millisecond

// screenFrozenAfter is how long a pane must stay unreadable before its
// rendered region says so. Shorter, and a heavy but healthy write burst would
// flash the marker; longer, and a wedged pane looks merely idle.
const screenFrozenAfter = 2 * time.Second

// screenFrozenMarker is drawn on the bottom row of a pane that has been
// unreadable for screenFrozenAfter.
const screenFrozenMarker = " pane not updating: emulator lock held "

// peekUnreadable is what :peek and patrol return for a pane whose emulator
// could not be read within peekTryBudget.
const peekUnreadable = "[pane unreadable: emulator lock held]"

// tryLockFor retries a non-blocking acquire until it succeeds or budget
// elapses. Never waits on the lock itself.
func tryLockFor(try func() bool, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for {
		if try() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(screenTryStep)
	}
}

// withEmulator runs fn against emu's embedded Emulator under the emulator's
// READ lock, acquired with a bounded try. Reports false, without running fn,
// when the lock stayed held for budget. A nil emulator reads as an empty
// screen: fn does not run and the result is true, matching what every screen
// reader has always seen for a suspended pane.
func withEmulator(emu *vt.SafeEmulator, budget time.Duration, fn func(e *vt.Emulator)) bool {
	if emu == nil {
		return true
	}
	if !tryLockFor(emu.TryRLock, budget) {
		return false
	}
	defer emu.RUnlock()
	fn(emu.Emulator)
	return true
}

// withScreen runs fn against the pane's emulator with renderMu and the
// emulator's read lock held, both acquired with a bounded try, in the same
// order readLoop and Resize take them. Reports false, without running fn,
// when either stayed held. On success the emulator's size is remembered for
// emuSize's fallback.
func (p *Pane) withScreen(fn func(e *vt.Emulator)) bool {
	if p == nil || p.emu == nil {
		return false
	}
	if !tryLockFor(p.renderMu.TryLock, screenTryBudget) {
		return false
	}
	defer p.renderMu.Unlock()
	return withEmulator(p.emu, screenTryBudget, func(e *vt.Emulator) {
		fn(e)
		p.noteEmuSize(e.Width(), e.Height())
	})
}

// withScreenWrite is withScreen with the emulator's WRITE lock, for the main
// loop's resize.
func (p *Pane) withScreenWrite(fn func(e *vt.Emulator)) bool {
	if p == nil || p.emu == nil {
		return false
	}
	if !tryLockFor(p.renderMu.TryLock, screenTryBudget) {
		return false
	}
	defer p.renderMu.Unlock()
	if !tryLockFor(p.emu.TryLock, screenTryBudget) {
		return false
	}
	defer p.emu.Unlock()
	fn(p.emu.Emulator)
	p.noteEmuSize(p.emu.Emulator.Width(), p.emu.Emulator.Height())
	return true
}

// readScreenRows copies every row of the screen as text, one string per row,
// empty cells as spaces. For use inside a withScreen/withEmulator region.
func readScreenRows(e *vt.Emulator) []string {
	cols, height := e.Width(), e.Height()
	rows := make([]string, height)
	buf := make([]rune, 0, cols)
	for y := 0; y < height; y++ {
		buf = buf[:0]
		for col := 0; col < cols; col++ {
			cell := e.CellAt(col, y)
			if cell != nil && cell.Content != "" {
				buf = append(buf, []rune(cell.Content)...)
			} else {
				buf = append(buf, ' ')
			}
		}
		rows[y] = string(buf)
	}
	return rows
}

// tryScreenRows reads a pane's screen WITHOUT BLOCKING, or reports false when
// the pane's locks stayed held. A pane with no emulator (suspended) reads as
// an empty screen, as it always has.
func tryScreenRows(p *Pane) ([]string, bool) {
	if p == nil || p.emu == nil {
		return nil, true
	}
	var rows []string
	ok := p.withScreen(func(e *vt.Emulator) { rows = readScreenRows(e) })
	return rows, ok
}

// noteEmuSize remembers the emulator's size as last seen under its lock.
func (p *Pane) noteEmuSize(cols, rows int) {
	p.mu.Lock()
	p.lastEmuCols, p.lastEmuRows = cols, rows
	p.mu.Unlock()
}

// emuSize returns the emulator's columns and rows without waiting on it: the
// live size when the lock is free, otherwise the size last seen under the
// lock. Zero when the pane has never been sized. For respawn and resize
// callers, which only need the geometry, not the cells.
func (p *Pane) emuSize() (cols, rows int) {
	if p.withScreen(func(e *vt.Emulator) { cols, rows = e.Width(), e.Height() }) {
		return cols, rows
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastEmuCols, p.lastEmuRows
}

// frameCell is one cell of a pane's last successfully rendered content region.
type frameCell struct {
	ch    rune
	style tcell.Style
}

// cacheFrame copies the content region just drawn so the next frame can show
// it again if the pane cannot be read. Read back from the screen rather than
// recorded on the way in so every drawing helper stays a plain SetContent.
func (p *Pane) cacheFrame(s *clampedScreen, cr Region, cols, rows int) {
	n := cols * rows
	if cap(p.lastFrame) < n {
		p.lastFrame = make([]frameCell, n)
	}
	p.lastFrame = p.lastFrame[:n]
	p.lastFrameCols, p.lastFrameRows = cols, rows
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			ch, _, style, _ := s.Screen.GetContent(cr.X+col, cr.Y+row)
			p.lastFrame[row*cols+col] = frameCell{ch: ch, style: style}
		}
	}
}

// replayFrame draws the cached content region for a pane that could not be
// read this frame, and after screenFrozenAfter of that, says why on its
// bottom row. A wedged pane's content is not changing, so its last frame is
// the truth; a blank region would read as the pane having died.
func (p *Pane) replayFrame(s *clampedScreen, cr Region, cols, rows int, now time.Time) {
	if p.lastFrameCols == cols && p.lastFrameRows == rows && len(p.lastFrame) == cols*rows {
		for row := 0; row < rows; row++ {
			for col := 0; col < cols; col++ {
				fc := p.lastFrame[row*cols+col]
				s.SetContent(cr.X+col, cr.Y+row, fc.ch, nil, fc.style)
			}
		}
	}
	p.mu.Lock()
	since := p.screenSkipSince
	p.mu.Unlock()
	if since.IsZero() || now.Sub(since) < screenFrozenAfter || rows < 1 {
		return
	}
	style := tcell.StyleDefault.Background(tcell.ColorDarkRed).Foreground(tcell.ColorWhite).Bold(true)
	y := cr.Y + rows - 1
	for col := 0; col < cols; col++ {
		ch := ' '
		if col < len(screenFrozenMarker) {
			ch = rune(screenFrozenMarker[col])
		}
		s.SetContent(cr.X+col, y, ch, nil, style)
	}
}
