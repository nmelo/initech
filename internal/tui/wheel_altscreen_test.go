// wheel_altscreen_test.go tests ini-i3v's fix: wheel scroll over an
// alt-screen pane must be forwarded to the child (which owns and repaints
// its whole grid, per Nelson's confirmed decision) instead of mutating
// p.scrollOffset, which contentOffset() ignores entirely for alt-screen
// content. It also covers the accumulation trap called out in the bead: a
// pane already mid-scrollback in normal mode before its child enters
// alt-screen must not carry that stale offset across the transition.
package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// TestHandleMouseWheelUp_AltScreenDoesNotMutateScrollOffset is the core
// regression guard for the bug ini-i3v exists to fix: over an alt-screen
// pane, wheel input must not accumulate in scrollOffset (contentOffset's
// alt-screen identity branch would silently discard it anyway -- this
// proves the OLD code path, which caused exactly that silent no-op, is no
// longer taken).
func TestHandleMouseWheelUp_AltScreenDoesNotMutateScrollOffset(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0].(*Pane)
	p.emu.Write([]byte("\x1b[?1049h")) // enter alt-screen
	if !p.emu.IsAltScreen() {
		t.Fatal("precondition: pane should be in alt-screen mode")
	}

	r := p.region
	ev := tcell.NewEventMouse(r.X+1, r.Y+1, tcell.WheelUp, tcell.ModNone)
	tui.handleMouse(ev)

	if p.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d, want 0 (alt-screen wheel input must not mutate scrollOffset)", p.scrollOffset)
	}
	if p.InScrollback() {
		t.Error("alt-screen pane should never report InScrollback() from wheel input")
	}
}

// TestHandleMouseWheelDown_AltScreenDoesNotMutateScrollOffset mirrors the
// WheelUp case for WheelDown. Starts scrollOffset at a nonzero value: from
// 0, ScrollDown(3) clamps right back to 0 either way (pre- or post-fix), so
// a 0-starting test would pass against the old buggy code too and prove
// nothing. Starting nonzero makes the old mutation ("5 -> 2") observably
// different from the fix's "untouched, stays 5".
func TestHandleMouseWheelDown_AltScreenDoesNotMutateScrollOffset(t *testing.T) {
	tui, _ := newTestTUIWithScreen("a")
	tui.applyLayout()
	p := tui.panes[0].(*Pane)
	p.emu.Write([]byte("\x1b[?1049h"))
	if !p.emu.IsAltScreen() {
		t.Fatal("precondition: pane should be in alt-screen mode")
	}
	p.scrollOffset = 5

	r := p.region
	ev := tcell.NewEventMouse(r.X+1, r.Y+1, tcell.WheelDown, tcell.ModNone)
	tui.handleMouse(ev)

	if p.scrollOffset != 5 {
		t.Errorf("scrollOffset = %d, want 5 (unchanged -- alt-screen wheel input must not mutate scrollOffset)", p.scrollOffset)
	}
}

// TestHandleMouseWheelUp_AltScreenForwardsToChild proves forwarding actually
// reaches the child, not just that the old scrolling path is skipped
// (option 3, silent no-op, would also pass the tests above). SendMouse only
// writes bytes into the emulator's response pipe when the child has enabled
// a mouse-tracking DEC mode -- otherwise it silently drops, by design,
// exactly like the existing forwardMouseEvent path for clicks. Enabling
// ModeMouseNormal (1000) here mimics a real alt-screen program (vim
// with :set mouse=a, Claude Code's fullscreen mode) that wants mouse input.
// Uses testPane/newTestTUI (no screen), NOT newTestTUIWithScreen -- the
// latter spawns a background goroutine that continuously drains emu.Read()
// for every pane it creates (to keep SendKey from blocking), which would
// race this test's own read of the forwarded bytes off the same pipe.
func TestHandleMouseWheelUp_AltScreenForwardsToChild(t *testing.T) {
	p := testPane("a")
	tui := newTestTUI(p)
	tui.applyLayout() // no screen: still computes tui.plan.Panes with real regions

	p.emu.Write([]byte("\x1b[?1000h")) // enable mouse reporting (X10/normal)
	p.emu.Write([]byte("\x1b[?1049h")) // enter alt-screen
	if !p.emu.IsAltScreen() {
		t.Fatal("precondition: pane should be in alt-screen mode")
	}
	if len(tui.plan.Panes) == 0 {
		t.Fatal("computed layout has no panes")
	}

	// Start the reader BEFORE forwarding: the emulator's response pipe is a
	// synchronous io.Pipe, so a write (inside SendMouse, called from
	// handleMouse below) blocks until a reader is waiting. Reading only
	// after handleMouse returns would deadlock -- nothing ever drains the
	// pipe, so handleMouse itself never returns.
	buf := make([]byte, 64)
	done := make(chan int, 1)
	go func() {
		n, _ := p.emu.Read(buf)
		done <- n
	}()

	r := tui.plan.Panes[0].Region
	ev := tcell.NewEventMouse(r.X+1, r.Y+1, tcell.WheelUp, tcell.ModNone)
	tui.handleMouse(ev)

	select {
	case n := <-done:
		if n == 0 {
			t.Fatal("expected forwarded mouse-wheel bytes on the emulator's response pipe, got 0")
		}
		got := string(buf[:n])
		if got == "" || got[0] != 0x1b {
			t.Errorf("forwarded bytes = %q, want an escape sequence (mouse report)", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded wheel event -- forwardWheelEvent did not reach the child")
	}
}

// TestCheckAltScreenTransition_EntryResetsStaleScrollOffset covers the trap
// called out on the bead: a pane already mid-scrollback in NORMAL mode
// before its child switches to alt-screen (e.g. running vim from a
// scrolled-back shell) must not carry that scrollOffset across the
// transition. Without this reset, exiting alt-screen later would silently
// drop the pane into the stale pre-existing scrollback position instead of
// resuming the live view -- a bug invisible until the child exits.
func TestCheckAltScreenTransition_EntryResetsStaleScrollOffset(t *testing.T) {
	p := newAltScreenTestPane(80, 10)
	p.Resize(10, 80)
	p.scrollOffset = 5
	p.scrollAnchorLen = 3

	p.emu.Write([]byte("\x1b[?1049h")) // child enters alt-screen
	p.checkAltScreenTransition()

	if p.scrollOffset != 0 {
		t.Errorf("scrollOffset after alt-screen entry = %d, want 0 (stale pre-entry scrollback must be cleared)", p.scrollOffset)
	}
	if p.scrollAnchorLen != 0 {
		t.Errorf("scrollAnchorLen after alt-screen entry = %d, want 0", p.scrollAnchorLen)
	}
}

// TestCheckAltScreenTransition_ExitDoesNotResetScrollOffset confirms the
// reset is entry-specific: since wheel input is forwarded (never mutates
// scrollOffset) for the entire alt-screen duration, scrollOffset must still
// be 0 at exit from the entry-reset alone, with no separate exit-time
// clearing required. This guards against a future change reintroducing
// scrollOffset mutation during alt-screen without also handling exit.
func TestCheckAltScreenTransition_ExitDoesNotResetScrollOffset(t *testing.T) {
	p := newAltScreenTestPane(80, 10)
	p.Resize(10, 80)
	p.emu.Write([]byte("\x1b[?1049h"))
	p.checkAltScreenTransition() // entry: resets to 0 (verified above)

	if p.scrollOffset != 0 {
		t.Fatalf("precondition: scrollOffset after entry = %d, want 0", p.scrollOffset)
	}

	p.emu.Write([]byte("\x1b[?1049l")) // child exits alt-screen
	p.checkAltScreenTransition()

	if p.scrollOffset != 0 {
		t.Errorf("scrollOffset after alt-screen exit = %d, want 0 (live view)", p.scrollOffset)
	}
}
