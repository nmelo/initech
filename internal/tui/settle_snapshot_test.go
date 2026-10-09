package tui

// ini-206s: while a resized pane settles (ini-yah), its body showed nothing,
// so every membership change in the live focus split blacked out the whole
// right side for ~160 ms. The last drawn body is now shown, clipped and
// bottom-anchored to the new region, until the emulator's real content is
// readable again.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// settlePane is a pane with real emulator content, sized to region.
func settlePane(t *testing.T, region Region, lines int) *Pane {
	t.Helper()
	cr := Region{X: region.X, Y: region.Y + 1, W: region.W, H: region.H - 1}
	cols, rows := cr.InnerSize()
	p := &Pane{name: "b", emu: vt.NewSafeEmulator(cols, rows), alive: true, region: region, activity: StateIdle}
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(p.emu, "line %02d of the body\r\n", i)
	}
	return p
}

// renderBody renders p into a fresh screen and returns its body rows.
func renderBody(t *testing.T, p *Pane) []string {
	t.Helper()
	r := p.region
	scr := tcell.NewSimulationScreen("")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	scr.SetSize(r.X+r.W+2, r.Y+r.H+2)
	p.Render(scr, false, false, 1, Selection{})
	cr := Region{X: r.X, Y: r.Y + 1, W: r.W, H: r.H - 1}
	cols, rows := cr.InnerSize()
	out := make([]string, rows)
	for row := 0; row < rows; row++ {
		var b strings.Builder
		for col := 0; col < cols; col++ {
			ch, _, _, _ := scr.GetContent(cr.X+col, cr.Y+row)
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		out[row] = strings.TrimRight(b.String(), " ")
	}
	return out
}

func nonBlank(rows []string) int {
	n := 0
	for _, r := range rows {
		if strings.TrimSpace(r) != "" {
			n++
		}
	}
	return n
}

func clip(s string, w int) string {
	if len(s) > w {
		return strings.TrimRight(s[:w], " ")
	}
	return s
}

// resizeTo moves p to region the way applyLayout does and starts the settle.
func resizeTo(p *Pane, region Region) {
	p.region = region
	cr := Region{X: region.X, Y: region.Y + 1, W: region.W, H: region.H - 1}
	cols, rows := cr.InnerSize()
	p.Resize(rows, cols)
}

// assertBottomAnchored: the last min(old,new) body rows of the new frame equal
// the old frame's last rows, clipped to the new width.
func assertBottomAnchored(t *testing.T, before, after []string, newCols int) {
	t.Helper()
	n := len(before)
	if len(after) < n {
		n = len(after)
	}
	for i := 1; i <= n; i++ {
		want := clip(before[len(before)-i], newCols)
		if got := after[len(after)-i]; got != want {
			t.Errorf("body row %d from the bottom = %q, want %q (the old body, bottom-anchored)", i, got, want)
		}
	}
}

// Join with a pane on screen: the staying pane SHRINKS. Its body must not
// go black on the first frame after the resize.
func TestSettleSnapshot_JoinWithAPaneOnScreenKeepsItsBody(t *testing.T) {
	p := settlePane(t, Region{X: 0, Y: 0, W: 60, H: 24}, 18)
	before := renderBody(t, p)
	if nonBlank(before) == 0 {
		t.Fatal("setup: the pane drew no body before the resize")
	}

	small := Region{X: 0, Y: 0, W: 40, H: 12}
	resizeTo(p, small)
	after := renderBody(t, p)

	if nonBlank(after) == 0 {
		t.Fatalf(`THE STAYING PANE WENT BLACK ON THE FIRST FRAME AFTER THE RESIZE.

In the live focus split every membership change resizes every right pane, so a
blank settle frame blacks out the whole right side for ~160 ms (ini-206s).
before: %q`, before)
	}
	assertBottomAnchored(t, before, after, 40-0)
}

// Leave with a pane staying: the staying pane GROWS. The old body sits at the
// bottom and the rows above it are blank.
func TestSettleSnapshot_LeaveWithAPaneStayingKeepsItsBody(t *testing.T) {
	p := settlePane(t, Region{X: 0, Y: 0, W: 40, H: 12}, 8)
	before := renderBody(t, p)
	if nonBlank(before) == 0 {
		t.Fatal("setup: the pane drew no body before the resize")
	}

	big := Region{X: 0, Y: 0, W: 60, H: 24}
	resizeTo(p, big)
	after := renderBody(t, p)

	if nonBlank(after) == 0 {
		t.Fatal("the staying pane went black on the first frame after it grew")
	}
	assertBottomAnchored(t, before, after, 60)
	for i := 0; i < len(after)-len(before); i++ {
		if after[i] != "" {
			t.Errorf("row %d above the anchored old body = %q, want blank", i, after[i])
		}
	}
}

// ini-yah's guarantee: once the window closes, the body is the emulator's
// real content and NONE of the snapshot.
func TestSettleSnapshot_NoSnapshotSurvivesTheSettleWindow(t *testing.T) {
	p := settlePane(t, Region{X: 0, Y: 0, W: 60, H: 24}, 18)
	renderBody(t, p)
	resizeTo(p, Region{X: 0, Y: 0, W: 40, H: 12})
	if snap := renderBody(t, p); !strings.Contains(strings.Join(snap, "\n"), "line 18") {
		t.Fatalf("setup: the settle frame did not show the snapshot: %q", snap)
	}

	// The child redraws into the new size; the window closes.
	fmt.Fprint(p.emu, "\x1b[2J\x1b[H")
	fmt.Fprint(p.emu, "fresh content after resize\r\n")
	p.resizeSettleFrames = 0
	p.resizeSettleDeadline = time.Now().Add(-time.Millisecond)

	after := strings.Join(renderBody(t, p), "\n")
	if strings.Contains(after, "line ") {
		t.Errorf("pre-resize content bled past the settle window (ini-yah):\n%s", after)
	}
	if !strings.Contains(after, "fresh content after resize") {
		t.Errorf("the first post-settle frame is not the emulator's content:\n%s", after)
	}
}

// A pane never drawn before has no last body; its settle frame stays as today.
func TestSettleSnapshot_APaneWithNoLastBodyDrawsNothing(t *testing.T) {
	p := settlePane(t, Region{X: 0, Y: 0, W: 40, H: 12}, 8)
	resizeTo(p, Region{X: 0, Y: 0, W: 60, H: 24})

	if after := renderBody(t, p); nonBlank(after) != 0 {
		t.Errorf("a pane with no cached body drew %d rows during settle: %q", nonBlank(after), after)
	}
}
