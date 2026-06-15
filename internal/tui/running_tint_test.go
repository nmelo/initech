package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// TestBackgroundTint_RunningShowsGreen: a pane that just produced output (and is
// therefore StateRunning) gets the green tint.
func TestBackgroundTint_RunningShowsGreen(t *testing.T) {
	p := &Pane{alive: true}
	p.lastOutputTime = time.Now()
	p.updateActivity() // StateRunning -> bumps the tint hold
	if got := p.backgroundTint(); got != runningTintColor {
		t.Errorf("backgroundTint = %v, want runningTintColor for a running pane", got)
	}
}

// TestBackgroundTint_HoldExpiredNeutral: once the hold window has elapsed the
// tint drops to neutral (ColorDefault).
func TestBackgroundTint_HoldExpiredNeutral(t *testing.T) {
	p := &Pane{alive: true}
	p.tintUntil = time.Now().Add(-time.Second) // hold expired 1s ago
	if got := p.backgroundTint(); got != tcell.ColorDefault {
		t.Errorf("backgroundTint = %v after hold expiry, want ColorDefault", got)
	}
}

// TestBackgroundTint_NeverRanNeutral: a pane that never produced output shows no
// tint (zero tintUntil).
func TestBackgroundTint_NeverRanNeutral(t *testing.T) {
	p := &Pane{alive: true}
	if got := p.backgroundTint(); got != tcell.ColorDefault {
		t.Errorf("backgroundTint = %v for a pane that never ran, want ColorDefault", got)
	}
}

// TestBackgroundTint_DeadNeutral: a dead pane is neutral even within the hold
// window (only RUNNING is tinted).
func TestBackgroundTint_DeadNeutral(t *testing.T) {
	p := &Pane{alive: true}
	p.lastOutputTime = time.Now()
	p.updateActivity() // bump while alive
	p.alive = false    // died inside the hold window
	if got := p.backgroundTint(); got != tcell.ColorDefault {
		t.Errorf("dead pane backgroundTint = %v, want ColorDefault", got)
	}
}

// TestBackgroundTint_SuspendedNeutral: a suspended pane is neutral even within
// the hold window.
func TestBackgroundTint_SuspendedNeutral(t *testing.T) {
	p := &Pane{alive: true}
	p.lastOutputTime = time.Now()
	p.updateActivity()
	p.suspended = true
	if got := p.backgroundTint(); got != tcell.ColorDefault {
		t.Errorf("suspended pane backgroundTint = %v, want ColorDefault", got)
	}
}

// TestBackgroundTint_HysteresisHoldsThroughSubWindowGap is the LOAD-BEARING
// regression lock for AC-2. After output, the pane is bumped into the hold
// window. A subsequent output gap LONGER than the 2s running threshold (so the
// dot + KITT bar flip to idle) but SHORTER than the tint hold window must NOT
// drop the tint. This proves the tint debounce is decoupled from the 2s
// ActivityState signal — re-coupling them would strobe the background.
func TestBackgroundTint_HysteresisHoldsThroughSubWindowGap(t *testing.T) {
	p := &Pane{alive: true}

	// Fresh output -> running -> tint hold bumped ~now+window.
	p.lastOutputTime = time.Now()
	p.updateActivity()
	if p.activity != StateRunning {
		t.Fatalf("precondition: activity = %v, want StateRunning", p.activity)
	}

	// Simulate a gap longer than the 2s running threshold but well inside the
	// tint hold window. The running detection must flip to idle without
	// re-bumping (or clearing) the tint hold.
	gap := ptyIdleTimeout + 3*time.Second
	p.lastOutputTime = time.Now().Add(-gap)
	p.updateActivity()

	// The responsive 2s signal (overlay dot + KITT bar) reads idle...
	if p.activity != StateIdle {
		t.Fatalf("activity = %v after a %v gap, want StateIdle (the 2s signal must flip)", p.activity, gap)
	}
	// ...but the tint must still be held by the longer window.
	if got := p.backgroundTint(); got != runningTintColor {
		t.Errorf("backgroundTint = %v during a sub-hold-window gap, want runningTintColor "+
			"(the tint must NOT re-couple to the 2s idle signal)", got)
	}
}

// TestUpdateActivity_RunningBumpsTintHold: the running branch of updateActivity
// pushes tintUntil into the future.
func TestUpdateActivity_RunningBumpsTintHold(t *testing.T) {
	p := &Pane{alive: true}
	p.lastOutputTime = time.Now()
	before := time.Now()
	p.updateActivity()
	if !p.tintUntil.After(before) {
		t.Errorf("tintUntil = %v, want bumped into the future when running", p.tintUntil)
	}
}

// TestUpdateActivity_IdleDoesNotBumpTintHold: an idle updateActivity must not
// extend the hold (otherwise the tint could never expire).
func TestUpdateActivity_IdleDoesNotBumpTintHold(t *testing.T) {
	p := &Pane{alive: true}
	p.lastOutputTime = time.Now().Add(-(ptyIdleTimeout + time.Second)) // idle
	p.updateActivity()
	if !p.tintUntil.IsZero() {
		t.Errorf("tintUntil = %v, want zero (an idle pane must not bump the tint hold)", p.tintUntil)
	}
}

// TestTintStyle_FillsDefaultBgOnly proves AC-1's cell rule: a default-bg cell
// receives the tint; an agent-colored cell keeps its own background.
func TestTintStyle_FillsDefaultBgOnly(t *testing.T) {
	if _, bg, _ := tintStyle(tcell.StyleDefault, runningTintColor).Decompose(); bg != runningTintColor {
		t.Errorf("default-bg cell: bg = %v, want tint %v", bg, runningTintColor)
	}
	colored := tcell.StyleDefault.Background(tcell.ColorRed)
	if _, bg, _ := tintStyle(colored, runningTintColor).Decompose(); bg != tcell.ColorRed {
		t.Errorf("agent-colored cell: bg = %v, want ColorRed preserved (tint must not override)", bg)
	}
}

// TestTint_SurvivesUnfocusedDimming confirms the orthogonality AC: an unfocused
// running pane keeps its tint. dimStyle preserves background, and the tint is
// applied before dimming, so a background running pane still shows the green
// (just with dimmed foreground text).
func TestTint_SurvivesUnfocusedDimming(t *testing.T) {
	tinted := tintStyle(tcell.StyleDefault, runningTintColor)
	dimmed := dimStyle(tinted)
	if _, bg, _ := dimmed.Decompose(); bg != runningTintColor {
		t.Errorf("dimmed tinted cell bg = %v, want tint preserved through unfocused dim %v", bg, runningTintColor)
	}
}

// TestTintStyle_NoTintIsNoOp: a ColorDefault tint leaves the style untouched
// (idle panes render exactly as before).
func TestTintStyle_NoTintIsNoOp(t *testing.T) {
	plain := tcell.StyleDefault.Foreground(tcell.ColorWhite)
	if got := tintStyle(plain, tcell.ColorDefault); got != plain {
		t.Errorf("tintStyle with ColorDefault changed the style: %v", got)
	}
}

// TestRenderCellRow_TintsDefaultBgPreservesColored is the end-to-end AC-1 check:
// render a row with a default-bg cell and an agent-colored-bg cell through the
// real render path; the plain cell gets the green, the colored cell keeps its bg.
func TestRenderCellRow_TintsDefaultBgPreservesColored(t *testing.T) {
	emu := vt.NewSafeEmulator(10, 2)
	// "A" = default bg; "B" = explicit truecolor bg RGB(200,0,0) via SGR 48;2.
	emu.Write([]byte("A\x1b[48;2;200;0;0mB\x1b[0m"))

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(20, 5)
	cs := &clampedScreen{Screen: screen, r: Region{X: 0, Y: 0, W: 20, H: 5}}

	renderCellRow(cs, emu, 0, 0, 0, 2, false, runningTintColor)
	screen.Show()

	_, _, styleA, _ := screen.GetContent(0, 0)
	if _, bg, _ := styleA.Decompose(); bg != runningTintColor {
		t.Errorf("default-bg cell rendered bg = %v, want tint %v", bg, runningTintColor)
	}
	wantB := tcell.NewRGBColor(200, 0, 0)
	_, _, styleB, _ := screen.GetContent(1, 0)
	if _, bg, _ := styleB.Decompose(); bg != wantB {
		t.Errorf("agent-colored cell rendered bg = %v, want %v preserved (tint must not override)", bg, wantB)
	}
}
