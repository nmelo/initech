// altscreen_resize_test.go tests ini-y97's fix: a pane whose child enters
// alt-screen mode (vim, htop, Claude Code's "tui":"fullscreen") must be told
// its TRUE visible dimensions, not the inflated scrollable-live-region
// height ini-44hp gives normal scrolling children. Bottom-anchoring alone
// cannot fix this -- alt-screen content is drawn absolutely across whatever
// height the child is told it has, so a 3x-tall report can never fit a 1x
// window regardless of how the render path windows it.
package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// newAltScreenTestPane builds a bare Pane with a real emulator but no PTY
// (p.ptmx stays nil, which resizeLocked already guards), so resize behavior
// is testable directly and deterministically without a child process.
// visibleRows/visibleCols set BOTH the initial emulator size and p.region,
// matching how real layout code keeps them in sync (tui.go's applyLayout
// sets p.region from the same Region it derives Resize's rows/cols from) --
// checkAltScreenTransition reads p.region, not whatever was last passed to
// Resize, so the two must agree here exactly as they do in production.
func newAltScreenTestPane(visibleCols, visibleRows int) *Pane {
	return &Pane{
		name:   "test",
		emu:    vt.NewSafeEmulator(visibleCols, visibleRows),
		alive:  true,
		region: Region{X: 0, Y: 0, W: visibleCols, H: visibleRows + 2}, // +2: activity bar + ribbon
	}
}

// TestResize_NormalChildGetsInflatedRows is the ini-44hp regression guard:
// a non-alt-screen child must still see the 3x-inflated scrollable
// live-region height, unchanged by this bead's fix.
func TestResize_NormalChildGetsInflatedRows(t *testing.T) {
	p := newAltScreenTestPane(80, 10)
	p.Resize(10, 80)

	want := effectiveEmuRows(10)
	if got := p.emu.Height(); got != want {
		t.Errorf("normal child emulator height = %d, want %d (effectiveEmuRows(10), the ini-44hp inflated height)", got, want)
	}
	if p.emu.IsAltScreen() {
		t.Fatal("precondition: emulator should not be in alt-screen mode")
	}
	if want == 10 {
		t.Fatal("test setup invalid: effectiveEmuRows(10) should differ from 10 for this assertion to mean anything")
	}
}

// TestResize_AltScreenChildGetsTrueVisibleRows is the core fix: a child
// already in alt-screen mode at the time of a resize must see the true
// visible rows, not the inflated height.
func TestResize_AltScreenChildGetsTrueVisibleRows(t *testing.T) {
	p := newAltScreenTestPane(80, 10)
	p.emu.Write([]byte("\x1b[?1049h")) // enter alt-screen
	if !p.emu.IsAltScreen() {
		t.Fatal("precondition: emulator should be in alt-screen mode after 1049h")
	}

	p.Resize(10, 80)

	if got := p.emu.Height(); got != 10 {
		t.Errorf("alt-screen child emulator height = %d, want 10 (true visible rows, not inflated)", got)
	}
}

// TestCheckAltScreenTransition_EntryShrinksToVisible is the entry-transition
// regression: a child that starts normal (inflated) and then enters
// alt-screen mid-session -- without any accompanying layout/region change --
// must have its emulator shrunk to true visible rows by the transition
// check alone, exactly as readLoop triggers it on every PTY write.
func TestCheckAltScreenTransition_EntryShrinksToVisible(t *testing.T) {
	p := newAltScreenTestPane(80, 10)
	p.Resize(10, 80) // establish inflated baseline, not alt-screen
	if got, want := p.emu.Height(), effectiveEmuRows(10); got != want {
		t.Fatalf("precondition: emulator height = %d, want %d (inflated baseline)", got, want)
	}

	p.emu.Write([]byte("\x1b[?1049h")) // child switches to alt-screen
	p.checkAltScreenTransition()

	if got := p.emu.Height(); got != 10 {
		t.Errorf("after alt-screen entry, emulator height = %d, want 10 (true visible rows)", got)
	}
}

// TestCheckAltScreenTransition_ExitRestoresInflated is the exit-transition
// regression: a child that leaves alt-screen mode (closes vim, or Claude
// Code drops "tui":"fullscreen") must have its emulator restored to the
// inflated scrollable-live-region height, not left at the visible size.
func TestCheckAltScreenTransition_ExitRestoresInflated(t *testing.T) {
	p := newAltScreenTestPane(80, 10)
	p.emu.Write([]byte("\x1b[?1049h"))
	p.Resize(10, 80) // establish visible-size baseline, alt-screen
	if got := p.emu.Height(); got != 10 {
		t.Fatalf("precondition: emulator height = %d, want 10 (visible baseline)", got)
	}

	p.emu.Write([]byte("\x1b[?1049l")) // child exits alt-screen
	p.checkAltScreenTransition()

	want := effectiveEmuRows(10)
	if got := p.emu.Height(); got != want {
		t.Errorf("after alt-screen exit, emulator height = %d, want %d (restored inflated height)", got, want)
	}
}

// TestCheckAltScreenTransition_ToggleSequence is the AC's explicit "a child
// that toggles between them sees the switch" case, exercised as one
// continuous sequence rather than two isolated entry/exit tests, the way a
// real child (open vim, quit vim, reopen vim) would actually behave.
func TestCheckAltScreenTransition_ToggleSequence(t *testing.T) {
	p := newAltScreenTestPane(80, 10)
	p.Resize(10, 80)
	inflated := effectiveEmuRows(10)

	steps := []struct {
		seq        string
		wantHeight int
		label      string
	}{
		{"\x1b[?1049h", 10, "1st entry -> visible"},
		{"\x1b[?1049l", inflated, "1st exit -> inflated"},
		{"\x1b[?1049h", 10, "2nd entry -> visible"},
		{"\x1b[?1049l", inflated, "2nd exit -> inflated"},
	}
	for _, step := range steps {
		p.emu.Write([]byte(step.seq))
		p.checkAltScreenTransition()
		if got := p.emu.Height(); got != step.wantHeight {
			t.Errorf("%s: emulator height = %d, want %d", step.label, got, step.wantHeight)
		}
	}
}

// TestCheckAltScreenTransition_UnchangedModeSkipsResize confirms the guard
// clause: calling the check when the mode hasn't actually changed must not
// re-trigger a resize (no PTY/emulator churn, no repeated resize-settle
// suppression window on every single PTY write -- readLoop calls this after
// EVERY write, so a non-guarded version would resize on every byte).
func TestCheckAltScreenTransition_UnchangedModeSkipsResize(t *testing.T) {
	p := newAltScreenTestPane(80, 10)
	p.Resize(10, 80)
	// Reset the settle markers post-baseline-resize so a subsequent no-op
	// call is unambiguous: if resizeLocked ran again, these would be reset.
	p.resizeSettleFrames = 0
	p.resizeSettleDeadline = time.Time{}

	p.emu.Write([]byte("normal output, no mode change\r\n"))
	p.checkAltScreenTransition()

	if p.resizeSettleFrames != 0 || !p.resizeSettleDeadline.IsZero() {
		t.Error("checkAltScreenTransition resized despite no alt-screen mode change")
	}
	if got, want := p.emu.Height(), effectiveEmuRows(10); got != want {
		t.Errorf("emulator height changed unexpectedly: got %d, want %d (unchanged inflated baseline)", got, want)
	}
}
