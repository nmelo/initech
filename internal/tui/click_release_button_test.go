// click_release_button_test.go tests ini-82k's fix: a forwarded mouse
// release must carry the SAME SGR button code as the preceding press, not
// the generic X10 "any button released" sentinel (Cb=3) that
// ansi.EncodeMouseButton(MouseNone, ...) collapses to. A real terminal's SGR
// release preserves the actual button; a client that validates press/release
// button identity before completing a click (a common defensive UI pattern)
// silently drops a click whose release doesn't match its press. Confirmed
// against real Claude Code (see clickbadge_live_test.go,
// INITECH_ALTPROBE=1): with the mismatch, its "Jump to bottom" badge did not
// respond to a click; with matching button codes, it did.
package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// captureForwardedBytes drives fire() (a tui.handleMouse call) and returns
// whatever bytes land on the pane's emulator response pipe as a result. The
// reader must start before fire() runs: the pipe is synchronous, so a write
// inside SendMouse blocks until a reader is already waiting.
func captureForwardedBytes(t *testing.T, p *Pane, fire func()) string {
	t.Helper()
	buf := make([]byte, 64)
	done := make(chan string, 1)
	go func() {
		n, _ := p.emu.Read(buf)
		done <- string(buf[:n])
	}()
	fire()
	select {
	case s := <-done:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded bytes")
		return ""
	}
}

// decodeSGRMouse parses "\x1b[<Cb;Cx;CyM" (press) or "...m" (release) and
// returns the button code, column, row, and whether it was a release.
func decodeSGRMouse(t *testing.T, s string) (cb, cx, cy int, release bool) {
	t.Helper()
	if !strings.HasPrefix(s, "\x1b[<") {
		t.Fatalf("not an SGR mouse sequence: %q", s)
	}
	body := s[3:]
	release = strings.HasSuffix(body, "m")
	body = strings.TrimSuffix(strings.TrimSuffix(body, "M"), "m")
	parts := strings.Split(body, ";")
	if len(parts) != 3 {
		t.Fatalf("malformed SGR mouse body: %q (from %q)", body, s)
	}
	var err error
	if cb, err = strconv.Atoi(parts[0]); err != nil {
		t.Fatalf("bad Cb in %q: %v", s, err)
	}
	if cx, err = strconv.Atoi(parts[1]); err != nil {
		t.Fatalf("bad Cx in %q: %v", s, err)
	}
	if cy, err = strconv.Atoi(parts[2]); err != nil {
		t.Fatalf("bad Cy in %q: %v", s, err)
	}
	return
}

// TestClickRelease_ForwardsSameButtonCodeAsPress is the core ini-82k
// regression test: a left-click press+release, driven through the real
// production path (tui.handleMouse, not forwardMouseEvent directly), must
// produce matching SGR button codes (Cb) on both ends -- the bug was that
// the release always encoded Cb=3 (the X10 "any release" sentinel) while
// the press encoded Cb=0 (left), a mismatch a real terminal's SGR release
// never produces.
func TestClickRelease_ForwardsSameButtonCodeAsPress(t *testing.T) {
	p := testPane("a")
	tui := newTestTUI(p)
	tui.applyLayout()

	p.emu.Write([]byte("\x1b[?1000h")) // enable mouse reporting
	p.emu.Write([]byte("\x1b[?1006h")) // SGR extended mode
	p.emu.Write([]byte("\x1b[?1049h")) // alt-screen (Claude "tui":"fullscreen" scenario)
	if !p.emu.IsAltScreen() {
		t.Fatal("precondition: alt-screen")
	}
	if len(tui.plan.Panes) == 0 {
		t.Fatal("computed layout has no panes")
	}

	r := tui.plan.Panes[0].Region
	clickRow := r.Y + 3

	pressBytes := captureForwardedBytes(t, p, func() {
		tui.handleMouse(tcell.NewEventMouse(r.X+1, clickRow, tcell.Button1, tcell.ModNone))
	})
	pressCb, pressCx, pressCy, pressIsRelease := decodeSGRMouse(t, pressBytes)
	if pressIsRelease {
		t.Fatalf("press event decoded as a release: %q", pressBytes)
	}

	releaseBytes := captureForwardedBytes(t, p, func() {
		tui.handleMouse(tcell.NewEventMouse(r.X+1, clickRow, tcell.ButtonNone, tcell.ModNone))
	})
	releaseCb, releaseCx, releaseCy, releaseIsRelease := decodeSGRMouse(t, releaseBytes)
	if !releaseIsRelease {
		t.Fatalf("release event did not decode as a release: %q", releaseBytes)
	}

	if releaseCb != pressCb {
		t.Errorf("release Cb = %d, want %d (must match the press's button code, not the X10 release sentinel)", releaseCb, pressCb)
	}
	if releaseCx != pressCx || releaseCy != pressCy {
		t.Errorf("release coords = (%d,%d), want (%d,%d) matching the press (same click position)", releaseCx, releaseCy, pressCx, pressCy)
	}
}
