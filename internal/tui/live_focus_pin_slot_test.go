package tui

import (
	"testing"
	"time"
)

// ini-dc33: a pin in Focus Live is never refused for lack of a slot; plain
// Live keeps its limit and its message.

// fullLiveTUI is a rig in Live 2x1 with both slots pinned (a in 0, b in 1).
func fullLiveTUI(t *testing.T) *TUI {
	t.Helper()
	tui := liveFocusTUI(t)
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.LiveAuto = true
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 1
	tui.liveEngine = NewLiveEngine(0, nil, nil)
	for slot, name := range []string{"a", "b"} {
		if err := tui.setLiveSlot(name, slot, true); err != nil {
			t.Fatalf("setLiveSlot %s: %v", name, err)
		}
	}
	return tui
}

// AC1 and AC2, red first: entered from a Live grid whose slots are all
// pinned, p on another agent pins it, and it shows on the right.
func TestLiveFocusPinSlot_AFullLiveGridDoesNotRefuseAPinInFocusLive(t *testing.T) {
	tui := fullLiveTUI(t)
	altShiftF(tui)
	if tui.liveFocus == nil {
		t.Fatal("setup: not in Focus Live")
	}
	selectAgent(t, tui, "d")
	tui.agentsToggleLivePin()
	if tui.agents.error != "" {
		t.Fatalf("p in Focus Live said %q, want the pin made", tui.agents.error)
	}
	if _, ok := tui.layoutState.LivePinned["d"]; !ok {
		t.Fatalf("d is not pinned: %v", tui.layoutState.LivePinned)
	}
	tui.tickLiveFocus(time.Now().Add(time.Second))
	if !onRight(tui, "d") {
		t.Errorf("pinned d is not on the right: %v", tui.layoutState.RightSet)
	}
}

// AC3: plain Live with every slot pinned still refuses, with the same words,
// and stores nothing.
func TestLiveFocusPinSlot_PlainLiveKeepsItsLimit(t *testing.T) {
	tui := fullLiveTUI(t)
	selectAgent(t, tui, "d")
	tui.agentsToggleLivePin()
	if tui.agents.error != "all live slots are pinned" {
		t.Errorf("plain Live said %q, want all live slots are pinned", tui.agents.error)
	}
	if _, ok := tui.layoutState.LivePinned["d"]; ok {
		t.Errorf("plain Live pinned d past its grid: %v", tui.layoutState.LivePinned)
	}
}

// AC4 and AC5: two extra pins take distinct numbers past the grid; one pin
// is unpinned in Focus Live; on exit the lowest-numbered extra fills the
// freed slot, the other stays past the grid, nothing lands on a's pin, and
// the bar names the one left, once.
func TestLiveFocusPinSlot_OnExitExtrasFillFreeSlotsLowestFirst(t *testing.T) {
	tui := fullLiveTUI(t)
	altShiftF(tui)
	for _, name := range []string{"d", "c"} {
		selectAgent(t, tui, name)
		tui.agentsToggleLivePin()
	}
	if d, c := tui.layoutState.LivePinned["d"], tui.layoutState.LivePinned["c"]; d != 2 || c != 3 {
		t.Fatalf("extra pins d=%d c=%d, want 2 and 3 (distinct, past the 2-slot grid)", d, c)
	}
	selectAgent(t, tui, "b")
	tui.agentsToggleLivePin() // AC5: unpin in Focus Live
	if _, ok := tui.layoutState.LivePinned["b"]; ok {
		t.Fatal("b is still pinned after p in Focus Live")
	}

	altShiftF(tui) // back to Live
	want := map[string]int{"a": 0, "d": 1, "c": 3}
	for k, v := range want {
		if got, ok := tui.layoutState.LivePinned[k]; !ok || got != v {
			t.Errorf("%s: slot %d (pinned %v), want %d", k, got, ok, v)
		}
	}
	if len(tui.layoutState.LivePinned) != len(want) {
		t.Errorf("pins %v, want %v", tui.layoutState.LivePinned, want)
	}
	if tui.cmd.notice != "c is pinned with no free live slot; it shows when one is free" {
		t.Errorf("bar %q, want the line naming c", tui.cmd.notice)
	}
	// Once per exit, never per tick.
	tui.cmd.notice = ""
	for i := 0; i < 3; i++ {
		tui.applyLayout()
	}
	if tui.cmd.notice != "" {
		t.Errorf("the line came back on a tick: %q", tui.cmd.notice)
	}
}

// AC4, the room case: with no free slot every extra stays past the grid, and
// one line names them all.
func TestLiveFocusPinSlot_OneLineNamesEveryPinLeftWaiting(t *testing.T) {
	tui := fullLiveTUI(t)
	altShiftF(tui)
	for _, name := range []string{"c", "d"} {
		selectAgent(t, tui, name)
		tui.agentsToggleLivePin()
	}
	altShiftF(tui)
	if tui.layoutState.LivePinned["a"] != 0 || tui.layoutState.LivePinned["b"] != 1 {
		t.Errorf("a or b moved: %v", tui.layoutState.LivePinned)
	}
	if tui.cmd.notice != "c, d are pinned with no free live slot; they show when one is free" {
		t.Errorf("bar %q", tui.cmd.notice)
	}
}

// AC5: in Live, p unpins a pin whatever its slot number, a past-grid one too.
func TestLiveFocusPinSlot_UnpinInLiveWhateverTheNumber(t *testing.T) {
	tui := fullLiveTUI(t)
	if err := tui.setLiveSlot("c", 5, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"c", "a"} {
		selectAgent(t, tui, name)
		tui.agentsToggleLivePin()
		if _, ok := tui.layoutState.LivePinned[name]; ok || tui.agents.error != "" {
			t.Errorf("%s: still pinned or refused (%q)", name, tui.agents.error)
		}
	}
}

// The live list split shares the store and the rule: never refused there.
func TestLiveFocusPinSlot_TheLiveListSplitDoesNotRefuseEither(t *testing.T) {
	tui := fullLiveTUI(t)
	tui.toggleLiveListSplit()
	if tui.liveList == nil {
		t.Fatal("setup: not in the live list split")
	}
	selectAgent(t, tui, "d")
	tui.agentsToggleLivePin()
	if tui.agents.error != "" || tui.layoutState.LivePinned["d"] != 2 {
		t.Errorf("live list: error %q, pins %v; want d at 2", tui.agents.error, tui.layoutState.LivePinned)
	}
}

// Feed-it-nothing: with no pins, entering and leaving Focus Live changes no
// pin and says nothing.
func TestLiveFocusPinSlot_NoPinsNoChange(t *testing.T) {
	tui := liveFocusTUI(t)
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 1
	tui.liveEngine = NewLiveEngine(0, nil, nil)
	altShiftF(tui)
	altShiftF(tui)
	if len(tui.layoutState.LivePinned) != 0 || tui.cmd.notice != "" {
		t.Errorf("pins %v, bar %q; want none", tui.layoutState.LivePinned, tui.cmd.notice)
	}
}
