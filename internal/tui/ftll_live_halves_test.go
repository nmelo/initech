package tui

import (
	"testing"
	"time"
)

// ini-ftll: in the live focus split, two showing panes share the width half
// and half; any other count keeps today's split. Widths only: no clock.

// AC1: two showing panes, each half the width, give or take one column for
// an odd width and the left pane's divider column.
func TestLiveFocusHalves_TwoShowingShareTheWidth(t *testing.T) {
	for _, c := range []struct {
		w, h        int
		left, right Region
	}{
		{200, 50, Region{0, 0, 99, 50}, Region{100, 0, 100, 50}},
		{201, 51, Region{0, 0, 99, 51}, Region{100, 0, 101, 51}},
	} {
		got := calcLiveFocusSplit(2, c.w, c.h)
		if len(got) != 2 || got[0] != c.left || got[1] != c.right {
			t.Errorf("%dx%d: %v, want %v and %v", c.w, c.h, got, c.left, c.right)
		}
	}
}

// AC2: one, three, four and seven showing, and the static focus split with
// two, keep exactly the regions they had before this change (captured at
// base 010ef2e2).
func TestLiveFocusHalves_OtherCountsKeepTodaysRegions(t *testing.T) {
	golden := map[string][]Region{
		"live 1 200x50":   {{0, 0, 79, 50}},
		"live 3 200x50":   {{0, 0, 79, 50}, {80, 0, 59, 50}, {140, 0, 60, 50}},
		"live 4 200x50":   {{0, 0, 79, 50}, {80, 0, 59, 25}, {140, 0, 60, 25}, {80, 25, 120, 25}},
		"live 7 200x50":   {{0, 0, 79, 50}, {80, 0, 39, 25}, {120, 0, 39, 25}, {160, 0, 40, 25}, {80, 25, 39, 25}, {120, 25, 39, 25}, {160, 25, 40, 25}},
		"static 2 200x50": {{0, 0, 79, 50}, {80, 0, 120, 50}},
		"live 1 201x51":   {{0, 0, 79, 51}},
		"live 3 201x51":   {{0, 0, 79, 51}, {80, 0, 60, 51}, {141, 0, 60, 51}},
		"live 4 201x51":   {{0, 0, 79, 51}, {80, 0, 60, 26}, {141, 0, 60, 26}, {80, 26, 121, 25}},
		"live 7 201x51":   {{0, 0, 79, 51}, {80, 0, 40, 26}, {121, 0, 39, 26}, {161, 0, 40, 26}, {80, 26, 40, 25}, {121, 26, 39, 25}, {161, 26, 40, 25}},
		"static 2 201x51": {{0, 0, 79, 51}, {80, 0, 121, 51}},
	}
	for _, wh := range [][2]int{{200, 50}, {201, 51}} {
		w, h := wh[0], wh[1]
		for _, n := range []int{1, 3, 4, 7} {
			key := "live " + itoa(n) + " " + itoa(w) + "x" + itoa(h)
			if got := calcLiveFocusSplit(n, w, h); !regionsEqual(got, golden[key]) {
				t.Errorf("%s: %v, want today's %v", key, got, golden[key])
			}
		}
		key := "static 2 " + itoa(w) + "x" + itoa(h)
		if got := calcMainVertical(2, w, h); !regionsEqual(got, golden[key]) {
			t.Errorf("%s (Option+F, not changed): %v, want today's %v", key, got, golden[key])
		}
	}
}

func regionsEqual(a, b []Region) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AC3: the split follows the count, read each time the layout is computed:
// two showing are halves; a second agent starting to work brings today's
// split back; when it stops (after its hold), halves again. The right set is
// driven by tickLiveFocus at chosen times, as applyLayout drives it.
func TestLiveFocusHalves_TheSplitFollowsTheCount(t *testing.T) {
	tui := liveFocusTUI(t) // a held; b and c working; d idle
	setActivity(tui, "c", StateIdle)
	altShiftF(tui)
	t0 := time.Now()
	tui.tickLiveFocus(t0)
	leftWidth := func(step string, want int) {
		t.Helper()
		plan := computeLayout(tui.layoutState, tui.visiblePanesForWindow(), 200, 50)
		if len(plan.Panes) == 0 || plan.Panes[0].Region.W != want {
			var got []string
			for _, p := range plan.Panes {
				got = append(got, p.Pane.Name()+":"+itoa(p.Region.W))
			}
			t.Fatalf("%s: panes %v, want the held pane %d wide", step, got, want)
		}
	}
	leftWidth("a and b showing", 99)
	setActivity(tui, "c", StateRunning)
	tui.tickLiveFocus(t0.Add(time.Second))
	leftWidth("c starts working: three showing", 79)
	setActivity(tui, "c", StateIdle)
	tui.tickLiveFocus(t0.Add(liveHoldDuration + 2*time.Second))
	leftWidth("c stops, past its hold: two showing", 99)
}
