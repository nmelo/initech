package tui

import "testing"

// ini-5maq: p never removes another agent's pin. A new pin in a live split
// never takes the agent's stale Live slot; in plain Live the agent's current
// slot is taken only when no other agent is pinned there.

// liveTwoByOne is Live 2x1, no pins, with b in slot 0 and d in slot 1, as
// plain Live last placed them (qa1's repro shape on ini-dc33).
func liveTwoByOne(t *testing.T) *TUI {
	t.Helper()
	tui := liveFocusTUI(t)
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.LiveAuto = true
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 1
	tui.layoutState.LiveSlots = []string{"b", "d"}
	tui.liveEngine = NewLiveEngine(0, nil, nil)
	return tui
}

// pinPress selects name in the agents panel and presses p.
func pinPress(t *testing.T, tui *TUI, name string) {
	t.Helper()
	selectAgent(t, tui, name)
	tui.agentsToggleLivePin()
	if tui.agents.error != "" {
		t.Fatalf("p on %s said %q", name, tui.agents.error)
	}
}

// bothPinnedApart asserts each name is pinned and no two share a slot.
func bothPinnedApart(t *testing.T, tui *TUI, names ...string) {
	t.Helper()
	seen := map[int]string{}
	for _, n := range names {
		s, ok := tui.layoutState.LivePinned[n]
		if !ok {
			t.Fatalf("%s lost its pin: %v", n, tui.layoutState.LivePinned)
		}
		if other, dup := seen[s]; dup {
			t.Fatalf("%s and %s share slot %d: %v", n, other, s, tui.layoutState.LivePinned)
		}
		seen[s] = n
	}
}

// AC1, red first: qa1's repro. Focus Live; p on c, then p on b: both pinned,
// in different slots; c's pin is not dropped.
func TestLivePinEvict_ASecondPinInFocusLiveKeepsTheFirst(t *testing.T) {
	tui := liveTwoByOne(t)
	altShiftF(tui)
	pinPress(t, tui, "c")
	pinPress(t, tui, "b")
	bothPinnedApart(t, tui, "c", "b")
}

// AC2: the same in the live list split.
func TestLivePinEvict_ASecondPinInTheLiveListKeepsTheFirst(t *testing.T) {
	tui := liveTwoByOne(t)
	tui.toggleLiveListSplit()
	if tui.liveList == nil {
		t.Fatal("setup: not in the live list split")
	}
	pinPress(t, tui, "c")
	pinPress(t, tui, "b")
	bothPinnedApart(t, tui, "c", "b")
}

// AC3, the golden from base: in plain Live, p on an agent in its current,
// free slot pins it where it stands.
func TestLivePinEvict_PlainLivePinsWhereTheAgentStands(t *testing.T) {
	tui := liveTwoByOne(t)
	pinPress(t, tui, "d")
	if s, ok := tui.layoutState.LivePinned["d"]; !ok || s != 1 {
		t.Errorf("d pinned %v at %d, want slot 1 where it stands", ok, s)
	}
}

// Rule 1 in plain Live: an agent whose (stale) current slot another pin
// holds takes a free slot instead; the holder keeps its pin.
func TestLivePinEvict_PlainLiveNeverTakesAHeldSlot(t *testing.T) {
	tui := liveTwoByOne(t)
	if err := tui.setLiveSlot("c", 0, true); err != nil { // c holds slot 0; LiveSlots still says b
		t.Fatal(err)
	}
	pinPress(t, tui, "b")
	bothPinnedApart(t, tui, "c", "b")
	if tui.layoutState.LivePinned["c"] != 0 || tui.layoutState.LivePinned["b"] != 1 {
		t.Errorf("pins %v, want c at 0 and b at 1", tui.layoutState.LivePinned)
	}
}

// Feed-it-nothing: with no pins, p in plain Live pins as today.
func TestLivePinEvict_NoPinsPinsAsToday(t *testing.T) {
	tui := liveTwoByOne(t)
	pinPress(t, tui, "b")
	if len(tui.layoutState.LivePinned) != 1 || tui.layoutState.LivePinned["b"] != 0 {
		t.Errorf("pins %v, want b at 0", tui.layoutState.LivePinned)
	}
}
