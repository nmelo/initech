//go:build !windows

package tui

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gdamore/tcell/v2"
)

// ini-p39's fast instrument: initech's real render path on tcell's real
// terminfo screen, in-process, over a pty pair. No build, no fleet, no gate --
// and it measures the thing itself: bytes the terminal receives per frame.
// The simulation screen could not show this defect (it exposes current cells,
// not what tcell decided to emit); this can, and did.

// realTerminalScreen returns a terminfo screen on a fresh pty slave and a
// function returning the bytes the master received since its last call.
func realTerminalScreen(t *testing.T) (tcell.Screen, func() int) {
	return realTerminalScreenReader(t, 0)
}

// realTerminalScreenReader is realTerminalScreen with the master's reader
// pausing readerDelay before each read, as a loaded machine schedules it late.
//
// The count waits for the bytes by effect (ini-ol29). It writes a numbered
// sentinel into the slave after the frame: tcell has already written the
// frame there synchronously, and the slave's output queue keeps order, so
// once the reader has seen the sentinel every byte of the frame is in, and
// the count is the bytes before it. The old count slept 40 ms and read
// whatever had arrived; with the reader late, the tail of one frame (its
// last styles, the clock, cursor hide, end of synchronized update) landed
// in the next frame's count and a static frame "emitted" 58-92 bytes.
func realTerminalScreenReader(t *testing.T, readerDelay time.Duration) (tcell.Screen, func() int) {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120})
	t.Setenv("TERM", "xterm-256color")
	dev, err := tcell.NewDevTtyFromDev(tty.Name())
	if err != nil {
		t.Fatal(err)
	}
	s, err := tcell.NewTerminfoScreenFromTty(dev)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var out bytes.Buffer
	go func() {
		b := make([]byte, 64*1024)
		for {
			if readerDelay > 0 {
				time.Sleep(readerDelay)
				b = b[:512] // small reads, so a frame arrives in many late pieces
			}
			n, err := ptmx.Read(b)
			if n > 0 {
				mu.Lock()
				out.Write(b[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { s.Fini(); ptmx.Close(); tty.Close() })
	last, seq := 0, 0
	return s, func() int {
		seq++
		mark := []byte(fmt.Sprintf("\x1b[P39-SYNC-%d]", seq))
		if _, err := tty.Write(mark); err != nil {
			t.Fatalf("fixture: writing the sync mark: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			mu.Lock()
			i := bytes.Index(out.Bytes()[last:], mark)
			if i >= 0 {
				last += i + len(mark)
				mu.Unlock()
				return i
			}
			mu.Unlock()
			if time.Now().After(deadline) {
				t.Fatalf("fixture: the master never received sync mark %d within 5s; the reader is not draining the terminal", seq)
			}
			time.Sleep(time.Millisecond)
		}
	}
}

// A static six-pane frame emits NOTHING after the first; a real change emits
// again; static again emits nothing. Before the frame buffer every frame
// re-sent every pane row (2,649 bytes per frame on this fixture).
func TestP39_StaticFrameEmitsNoBytesOnARealTerminal(t *testing.T) {
	p39StaticFrame(t, realTerminalScreen)
}

// The same frames with the master's reader running late, as on a loaded CI
// runner (ini-ol29: this failed twice in one day with the fixed-sleep count).
func TestP39_StaticFrameEmitsNoBytesWithALateReader(t *testing.T) {
	p39StaticFrame(t, func(t *testing.T) (tcell.Screen, func() int) {
		return realTerminalScreenReader(t, 30*time.Millisecond)
	})
}

func p39StaticFrame(t *testing.T, screen func(*testing.T) (tcell.Screen, func() int)) {
	// Hold the status bar clock still: a minute rolling over between two
	// frames redraws its digit (65 bytes, measured), a real change, which
	// failed about one run in eighty (ini-ol29).
	clock := time.Date(2026, 10, 26, 16, 52, 30, 0, time.Local)
	statusClock = func() time.Time { return clock }
	t.Cleanup(func() { statusClock = time.Now })
	tui, _ := newTestTUIWithScreen("super", "eng1", "eng2", "eng3", "qa1", "qa2")
	s, emitted := screen(t)
	tui.screen = newFrameScreen(s)
	for _, pv := range tui.panes {
		p := pv.(*Pane)
		for l := 1; l <= 6; l++ {
			p.emu.Write([]byte(fmt.Sprintf("LINE-%d-P39-STATIC\r\n", l)))
		}
	}
	emitted()
	tui.render()
	if n := emitted(); n == 0 {
		t.Fatal("fixture: the first frame emitted nothing; the terminal is not being drawn at all")
	}
	for f := 2; f <= 6; f++ {
		tui.render()
		if n := emitted(); n != 0 {
			t.Errorf("static frame %d emitted %d bytes; an unchanged frame must cost the terminal nothing", f, n)
		}
	}

	// Positive control: a pane byte is a real change and must reach the
	// terminal on the next frame.
	// Pane 1 sits at x=60 and is on screen; the fixture parks panes 2+ past
	// the 120th column, where a change would rightly emit nothing.
	// Written as the read loop writes it: the bytes, then the pane marked
	// changed, which is what makes the frame draw its body (ini-ysr.2).
	tui.panes[1].(*Pane).emu.Write([]byte("CHANGED"))
	tui.panes[1].(*Pane).markFrameChanged()
	tui.render()
	if n := emitted(); n == 0 {
		t.Error("a changed pane emitted nothing: the buffer is suppressing real changes")
	}
	tui.render()
	if n := emitted(); n != 0 {
		t.Errorf("frame after the change emitted %d bytes", n)
	}

	// The held clock is the one the bar draws: moving it a minute is a real
	// change and reaches the terminal.
	clock = clock.Add(time.Minute)
	tui.render()
	if n := emitted(); n == 0 {
		t.Error("the clock moved a minute and the frame emitted nothing: the bar is not drawing statusClock")
	}
}
