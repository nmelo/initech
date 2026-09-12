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
	last := 0
	return s, func() int {
		time.Sleep(40 * time.Millisecond) // let the master drain
		mu.Lock()
		defer mu.Unlock()
		n := out.Len() - last
		last = out.Len()
		return n
	}
}

// A static six-pane frame emits NOTHING after the first; a real change emits
// again; static again emits nothing. Before the frame buffer every frame
// re-sent every pane row (2,649 bytes per frame on this fixture).
func TestP39_StaticFrameEmitsNoBytesOnARealTerminal(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "eng2", "eng3", "qa1", "qa2")
	s, emitted := realTerminalScreen(t)
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
	tui.panes[1].(*Pane).emu.Write([]byte("CHANGED"))
	tui.render()
	if n := emitted(); n == 0 {
		t.Error("a changed pane emitted nothing: the buffer is suppressing real changes")
	}
	tui.render()
	if n := emitted(); n != 0 {
		t.Errorf("frame after the change emitted %d bytes", n)
	}
}
