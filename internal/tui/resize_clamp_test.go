package tui

import (
	"math"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// TestPaneResize_HugeColsClampedToCap is the ini-hup3 regression: a
// resize with an out-of-range column count (e.g. cols=MaxInt32 from a yamux
// resize control message) must be clamped, not passed straight to the emulator
// buffer where make(Line, width) panics with "makeslice: len out of range"
// (or, for a large finite width, allocates multiple GB and OOMs the daemon).
func TestPaneResize_HugeColsClampedToCap(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{name: "eng1", emu: emu}

	// Before the fix this panics inside p.emu.Resize -> NewBuffer -> makeslice.
	p.Resize(24, math.MaxInt32)

	if w := emu.Width(); w != emuColsCap {
		t.Errorf("emulator width after huge-cols resize = %d, want clamped to %d", w, emuColsCap)
	}
}

// TestEffectiveEmuCols_Clamps pins the column clamp bounds (ini-hup3).
func TestEffectiveEmuCols_Clamps(t *testing.T) {
	cases := []struct{ in, want int }{
		{-5, 1},
		{0, 1},
		{1, 1},
		{80, 80},
		{emuColsCap, emuColsCap},
		{emuColsCap + 1, emuColsCap},
		{math.MaxInt32, emuColsCap},
	}
	for _, c := range cases {
		if got := effectiveEmuCols(c.in); got != c.want {
			t.Errorf("effectiveEmuCols(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
