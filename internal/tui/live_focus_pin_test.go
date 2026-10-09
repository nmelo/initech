package tui

// Live pins in the live focus split (ini-sbhq): one pin store, honoured on
// the split's right grid as it is in live mode.

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func onRight(tui *TUI, name string) bool {
	return strings.Contains(","+liveFocusJoined(tui.layoutState.RightSet)+",", ","+name+",")
}

func pinBefore(t *testing.T, tui *TUI, name string) {
	t.Helper()
	if err := tui.setLiveSlot(name, 0, true); err != nil {
		t.Fatalf("setLiveSlot: %v", err)
	}
}

// AC 1: p in the split pins with no error, and the idle agent is on the right
// on the next tick.
func TestLiveFocusPin_PanelPPinsInTheSplitWithNoError(t *testing.T) {
	tui := liveFocusTUI(t)
	altShiftF(tui)
	if onRight(tui, "d") {
		t.Fatalf("setup: idle d is already on the right: %v", tui.layoutState.RightSet)
	}
	selectAgent(t, tui, "d")

	tui.agentsToggleLivePin()

	if tui.agents.error != "" {
		t.Fatalf("p in the live focus split said %q; one pin store is honoured in both live modes", tui.agents.error)
	}
	if _, ok := tui.layoutState.LivePinned["d"]; !ok {
		t.Fatal("p did not pin d in the store")
	}
	tui.tickLiveFocus(time.Now().Add(time.Second))
	if !onRight(tui, "d") {
		t.Errorf("pinned idle d is not on the right grid after a tick: %v", tui.layoutState.RightSet)
	}
}

// AC 2: a pin that exists at entry is on the right from the first frame.
func TestLiveFocusPin_APinSetBeforeEntryIsOnTheRightAtEntry(t *testing.T) {
	tui := liveFocusTUI(t)
	pinBefore(t, tui, "d")

	altShiftF(tui)

	if !onRight(tui, "d") {
		t.Errorf("d was pinned before entry and is idle; it must be on the right from the first frame: %v",
			tui.layoutState.RightSet)
	}
}

// AC 2: a pin made in the split is the same store live mode reads.
func TestLiveFocusPin_APinMadeInTheSplitSurvivesIntoLive(t *testing.T) {
	tui := liveFocusTUI(t)
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.LiveAuto = true
	tui.liveEngine = NewLiveEngine(0, nil, nil)
	altShiftF(tui)
	selectAgent(t, tui, "d")
	tui.agentsToggleLivePin()

	altShiftF(tui) // back to live
	tui.applyLayout()

	if tui.layoutState.Mode != LayoutLive {
		t.Fatalf("exit restored mode %v, want live", tui.layoutState.Mode)
	}
	if _, ok := tui.liveEngine.Pinned["d"]; !ok {
		t.Errorf("the pin made in the split did not reach live mode's engine: %v", tui.liveEngine.Pinned)
	}
}

// AC 3: a pinned agent that is the focused pane stays left only -- while a
// different pinned agent IS on the right, so this cannot pass on a split that
// ignores pins altogether.
func TestLiveFocusPin_TheFocusedPinnedAgentStaysLeftOnly(t *testing.T) {
	tui := liveFocusTUI(t)
	pinBefore(t, tui, "a") // a is focused
	if err := tui.setLiveSlot("d", 1, true); err != nil {
		t.Fatal(err)
	}

	altShiftF(tui)

	if got := liveFocusPlan(tui); len(got) == 0 || got[0] != "a" {
		t.Errorf("plan = %v, want the focused pinned a on the left", got)
	}
	if onRight(tui, "a") {
		t.Errorf("focused a is also on the right grid: %v", tui.layoutState.RightSet)
	}
	if !onRight(tui, "d") {
		t.Errorf("control: pinned idle d is not on the right, so the split is ignoring pins: %v", tui.layoutState.RightSet)
	}
}

// AC 3: hidden wins over a pin -- shown on the right first, then hidden.
func TestLiveFocusPin_AHiddenPinnedAgentIsNotDrawn(t *testing.T) {
	tui := liveFocusTUI(t)
	pinBefore(t, tui, "d")
	altShiftF(tui)
	if !onRight(tui, "d") {
		t.Fatalf("control: pinned d is not on the right before hiding: %v", tui.layoutState.RightSet)
	}
	if tui.layoutState.Hidden == nil {
		tui.layoutState.Hidden = map[string]bool{}
	}
	tui.layoutState.Hidden["d"] = true

	tui.tickLiveFocus(time.Now())

	if onRight(tui, "d") {
		t.Errorf("hidden pinned d is still drawn on the right: %v", tui.layoutState.RightSet)
	}
}

// AC 1: unpinned and idle, the agent follows the working rule -- it stays for
// the hold and leaves after it.
func TestLiveFocusPin_UnpinThenIdleLeavesAfterTheHold(t *testing.T) {
	tui := liveFocusTUI(t)
	pinBefore(t, tui, "d")
	altShiftF(tui)
	t0 := time.Now()
	selectAgent(t, tui, "d")

	tui.agentsToggleLivePin() // unpin

	if _, ok := tui.layoutState.LivePinned["d"]; ok {
		t.Fatal("p again did not unpin d")
	}
	tui.tickLiveFocus(t0.Add(2 * time.Second))
	if !onRight(tui, "d") {
		t.Errorf("unpinned d left before its hold expired: %v", tui.layoutState.RightSet)
	}
	tui.tickLiveFocus(t0.Add(liveHoldDuration + 3*time.Second))
	if onRight(tui, "d") {
		t.Errorf("unpinned idle d is still on the right after the hold: %v", tui.layoutState.RightSet)
	}
}

// AC 4: a digit in the panel inside the split stays what it is today outside
// live mode -- it pins nothing (a slot number is a live-grid position the
// auto-sized right grid does not have).
func TestLiveFocusPin_ADigitInTheSplitPinsNothing(t *testing.T) {
	tui := liveFocusTUI(t)
	altShiftF(tui)
	tui.agents.active = true
	tui.agents.searching = false
	selectAgent(t, tui, "d")

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, '3', tcell.ModNone))

	if _, ok := tui.layoutState.LivePinned["d"]; ok {
		t.Errorf("a digit in the split pinned d to a slot: %v", tui.layoutState.LivePinned)
	}
}
