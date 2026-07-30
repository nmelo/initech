// running_square_test.go tests the red running square at the left of the
// pane badge (ini-z9a3). The square reuses backgroundTint()'s exact signal
// (alive && !suspended && tintUntil in the future) so it can never disagree
// with the running-pane background tint.
package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// runningSquareCell reads the badge's reserved leading cell for the given
// pane region and reports whether it holds the running square glyph.
func runningSquareCell(s tcell.SimulationScreen, r Region) bool {
	ribbonY := r.Y + r.H - 1
	mainc, _, _ := s.Get(r.X, ribbonY)
	return mainc == "■"
}

// TestRenderBadge_RunningShowsSquare: a pane within the tint hold window
// shows the square.
func TestRenderBadge_RunningShowsSquare(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.applyLayout()
	tui.layoutState.Overlay = false
	p := tui.panes[0].(*Pane)
	p.tintUntil = time.Now().Add(tintHoldWindow)

	tui.render()

	if !runningSquareCell(s, p.region) {
		t.Error("running pane (tintUntil ahead of now) should show the square")
	}
}

// TestRenderBadge_NotRunningNoSquare: a pane whose hold window has expired
// (or never started) shows no square.
func TestRenderBadge_NotRunningNoSquare(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.applyLayout()
	tui.layoutState.Overlay = false
	p := tui.panes[0].(*Pane)
	p.tintUntil = time.Time{} // never ran

	tui.render()

	if runningSquareCell(s, p.region) {
		t.Error("pane with no tint hold should not show the square")
	}
}

// TestRenderBadge_HoldExpiredNoSquare: tintUntil in the past (hold expired)
// shows no square, matching backgroundTint()'s own expiry behavior.
func TestRenderBadge_HoldExpiredNoSquare(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.applyLayout()
	tui.layoutState.Overlay = false
	p := tui.panes[0].(*Pane)
	p.tintUntil = time.Now().Add(-time.Second)

	tui.render()

	if runningSquareCell(s, p.region) {
		t.Error("pane with an expired tint hold should not show the square")
	}
}

// TestRenderBadge_SuspendedNoSquare: a suspended pane never shows the
// square, even with tintUntil ahead of now — matches backgroundTint()'s
// !p.suspended gate exactly (ini-z9a3 decision: red already means [dead]
// on a dead badge, and suspended/dead are both "not running" regardless of
// the tint hold).
func TestRenderBadge_SuspendedNoSquare(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.applyLayout()
	tui.layoutState.Overlay = false
	p := tui.panes[0].(*Pane)
	p.tintUntil = time.Now().Add(tintHoldWindow)
	p.suspended = true

	tui.render()

	if runningSquareCell(s, p.region) {
		t.Error("suspended pane should not show the square even with tintUntil ahead of now")
	}
}

// TestRenderBadge_DeadNoSquare: a dead pane never shows the square, even
// with tintUntil ahead of now.
func TestRenderBadge_DeadNoSquare(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.applyLayout()
	tui.layoutState.Overlay = false
	p := tui.panes[0].(*Pane)
	p.tintUntil = time.Now().Add(tintHoldWindow)
	p.alive = false

	tui.render()

	if runningSquareCell(s, p.region) {
		t.Error("dead pane should not show the square even with tintUntil ahead of now")
	}
}

// TestRenderBadge_ScrolledBackRunningShowsSquare: a scrolled-back pane
// ([+N] badge) whose agent is still running keeps the square — scrollback
// is a view state, not an activity state (ini-z9a3 decision).
func TestRenderBadge_ScrolledBackRunningShowsSquare(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.applyLayout()
	tui.layoutState.Overlay = false
	p := tui.panes[0].(*Pane)
	p.tintUntil = time.Now().Add(tintHoldWindow)

	for i := 0; i < 50; i++ {
		p.emu.Write([]byte("scrollback line\r\n"))
	}
	p.ScrollUp(10)

	tui.render()

	if !runningSquareCell(s, p.region) {
		t.Error("scrolled-back pane with the agent still running should keep the square")
	}
	// Confirm the [+N] badge itself still rendered (sanity: we're actually
	// exercising the scrolled-back branch, not silently falling through).
	r := p.region
	ribbonY := r.Y + r.H - 1
	found := false
	for x := r.X; x < r.X+r.W; x++ {
		mainc, _, _ := s.Get(x, ribbonY)
		if mainc == "+" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("precondition failed: [+N] scroll indicator did not render")
	}
}

// TestRenderBadge_NoLayoutShift: the badge's "N name" text starts at the
// same column whether or not the square is shown (ini-z9a3 decision: the
// leading cell is unconditionally reserved by renderRibbon regardless of
// running state, so the badge never jitters left when an agent pauses).
func TestRenderBadge_NoLayoutShift(t *testing.T) {
	tui, s := newTestTUIWithScreen("a")
	tui.applyLayout()
	tui.layoutState.Overlay = false
	p := tui.panes[0].(*Pane)
	r := p.region
	ribbonY := r.Y + r.H - 1

	// Not running: find the column of the pane index digit "1".
	p.tintUntil = time.Time{}
	tui.render()
	colIdle := -1
	for x := r.X; x < r.X+r.W; x++ {
		mainc, _, _ := s.Get(x, ribbonY)
		if mainc == "1" {
			colIdle = x
			break
		}
	}

	// Running: find it again.
	p.tintUntil = time.Now().Add(tintHoldWindow)
	tui.render()
	colRunning := -1
	for x := r.X; x < r.X+r.W; x++ {
		mainc, _, _ := s.Get(x, ribbonY)
		if mainc == "1" {
			colRunning = x
			break
		}
	}

	if colIdle == -1 || colRunning == -1 {
		t.Fatalf("could not locate badge index digit: colIdle=%d colRunning=%d", colIdle, colRunning)
	}
	if colIdle != colRunning {
		t.Errorf("badge shifted: idle at col %d, running at col %d — the leading cell must be reserved either way", colIdle, colRunning)
	}
}

// TestRenderBadge_ClearsWithTintAfterHoldWindowElapses is a genuine
// real-elapsed-time boundary test (ini-gb4j coverage gap: the existing tint
// hysteresis tests either fabricate an already-past tintUntil or only cover
// a ~5s gap well inside the 12s hold window; neither observes the actual
// boundary crossing in real time). Confirms AC verify item 2: ~12s after an
// agent genuinely stops, the square clears at the same render call the
// background tint clears — both read the exact same tintUntil field.
//
// Slow by design (waits out the real 12s tintHoldWindow); skipped in -short.
func TestRenderBadge_ClearsWithTintAfterHoldWindowElapses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-elapsed tintHoldWindow boundary test in short mode")
	}

	tui, s := newTestTUIWithScreen("a")
	tui.applyLayout()
	tui.layoutState.Overlay = false
	p := tui.panes[0].(*Pane)

	// Real output, real updateActivity bump -- not a fabricated timestamp.
	p.lastOutputTime = time.Now()
	p.updateActivity()
	tui.render()

	if !runningSquareCell(s, p.region) {
		t.Fatal("precondition failed: square should be lit immediately after real output")
	}
	if got := p.backgroundTint(); got != runningTintColor {
		t.Fatalf("precondition failed: background tint should be lit immediately after real output, got %v", got)
	}

	// Wait out the real hold window plus a margin for scheduling slack.
	time.Sleep(tintHoldWindow + 2*time.Second)
	tui.render()

	if runningSquareCell(s, p.region) {
		t.Error("square should have cleared ~12s (tintHoldWindow) after output genuinely stopped")
	}
	if got := p.backgroundTint(); got != tcell.ColorDefault {
		t.Errorf("background tint should have cleared at the same moment, got %v", got)
	}
}

// TestRenderBadge_RemotePaneNeverShowsSquareEvenWhenLocalTintWouldFire is the
// adversarial regression for ini-2bu: RemotePane's badge must never show the
// running square, regardless of any local tint state.
//
// remote_pane.go:395 passes a hardcoded literal false for the `running`
// parameter -- there is no tintUntil field on RemotePane and no code path by
// which the value could evaluate true today. That makes the exclusion
// correct but defends it only by a comment (ini-zmzg's tint-is-local-only-
// for-v1 decision, restated in ini-z9a3). A future refactor that turns the
// literal into a computed value would silently reintroduce the exact
// fabricated-parallel-signal that decision forbids, with nothing to catch it.
//
// The test is adversarial, not vacuous: it renders a LOCAL pane in the same
// scene with the identical tintUntil value, as a positive control proving
// the fixture and detection method actually produce a square when they
// should. Only then does the remote pane's absence mean something -- a bare
// "remote pane has no square" would prove nothing, since nothing was going
// to draw one on a struct with no tintUntil field at all.
//
// Do NOT add a tintUntil equivalent to RemotePane to make this test easier;
// the exclusion is the intended v1 design, not a gap to fill.
func TestRenderBadge_RemotePaneNeverShowsSquareEvenWhenLocalTintWouldFire(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer s.Fini()
	s.SetSize(80, 40)

	localRegion := Region{X: 0, Y: 0, W: 40, H: 20}
	remoteRegion := Region{X: 40, Y: 0, W: 40, H: 20}

	// Positive control: a local pane with the same tintUntil value the
	// remote pane's scenario is meant to resist. This MUST produce a square,
	// or the negative assertion below is meaningless.
	local := &Pane{
		name:      "local",
		emu:       vt.NewSafeEmulator(40, 18),
		alive:     true,
		visible:   true,
		region:    localRegion,
		tintUntil: time.Now().Add(tintHoldWindow),
	}

	// The pane under test: no tintUntil field exists to set "as if" running --
	// that absence is the point.
	remote := &RemotePane{
		name:   "eng1",
		host:   "wb",
		emu:    vt.NewSafeEmulator(40, 18),
		alive:  true,
		region: remoteRegion,
	}

	local.Render(s, false, false, 1, Selection{})
	remote.Render(s, false, false, 2, Selection{})
	s.Show()

	if !runningSquareCell(s, localRegion) {
		t.Fatal("positive control failed: a local pane with tintUntil ahead of now should show the square -- " +
			"if this fails, the negative assertion on the remote pane below proves nothing")
	}
	if runningSquareCell(s, remoteRegion) {
		t.Error("remote pane must never show the running square, even with local tint state that would " +
			"produce one on a local pane (ini-zmzg: tint is local-only for v1; ini-z9a3: the square reuses " +
			"that exact scoping decision) -- remote_pane.go's hardcoded `false` for `running` must not " +
			"become a computed value")
	}
}
