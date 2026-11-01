package tui

import (
	"strings"
	"testing"
	"time"
)

// ini-82c.2: an agent waiting at a permission prompt is not idle. The live
// focus split and the live list split keep it on the right, as they keep a
// running or background pane; live mode's ranking and the overlay keep
// paneIsWorking, which waiting does not satisfy.

// AC1, focus split: running -> waiting keeps the place across the hold;
// waiting -> idle leaves once the hold is over.
func TestLiveFocus_AWaitingAgentKeepsItsPlaceOnTheRight(t *testing.T) {
	tui := liveFocusTUI(t) // a held; b, c running; d idle
	altShiftF(tui)
	if !strings.Contains(liveFocusJoined(tui.layoutState.RightSet), "b") {
		t.Fatalf("precondition: running b is not on the right: %v", tui.layoutState.RightSet)
	}
	setActivity(tui, "b", StateWaitingInput)
	t0 := time.Now()
	for _, at := range []time.Duration{time.Second, liveHoldDuration + time.Second, 3 * liveHoldDuration} {
		tui.tickLiveFocus(t0.Add(at))
		if !strings.Contains(liveFocusJoined(tui.layoutState.RightSet), "b") {
			t.Fatalf("at +%v waiting b left the right side: %v", at, tui.layoutState.RightSet)
		}
	}
	setActivity(tui, "b", StateIdle)
	end := t0.Add(3*liveHoldDuration + time.Second)
	tui.tickLiveFocus(end)
	if strings.Contains(liveFocusJoined(tui.layoutState.RightSet), "b") {
		t.Errorf("b answered its prompt and is idle past its hold, yet stays on the right: %v", tui.layoutState.RightSet)
	}
}

// AC1, list split: the same, for a box in the live list.
func TestLiveList_AWaitingAgentKeepsItsBoxUntilAnswered(t *testing.T) {
	tui := listFleet(t, "super", "eng1", "eng2")
	setActivity(tui, "eng2", StateIdle)
	altShiftL(tui)
	if !strings.Contains(listJoined(tui), "eng1") {
		t.Fatalf("precondition: running eng1 has no box: %s", listJoined(tui))
	}
	setActivity(tui, "eng1", StateWaitingInput)
	t0 := time.Now()
	for _, at := range []time.Duration{time.Second, liveHoldDuration + time.Second, 3 * liveHoldDuration} {
		tui.tickLiveList(t0.Add(at))
		if !strings.Contains(listJoined(tui), "eng1") {
			t.Fatalf("at +%v waiting eng1 lost its box: %s", at, listJoined(tui))
		}
	}
	setActivity(tui, "eng1", StateIdle)
	end := t0.Add(3*liveHoldDuration + time.Second)
	tui.tickLiveList(end)
	if strings.Contains(listJoined(tui), "eng1") {
		t.Errorf("eng1 answered its prompt and is idle past its hold, yet keeps a box: %s", listJoined(tui))
	}
}

// AC2: a waiting pane as the only other agent, waiting already at entry: it
// is on the right from the first frame, and the right side never says nobody
// is working.
func TestLiveFocus_OnlyAWaitingAgentNeverReadsNoAgentWorking(t *testing.T) {
	tui, s := newTestTUIWithScreen("a", "b")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.Focused = "a"
	setActivity(tui, "b", StateWaitingInput)
	altShiftF(tui)
	if got := liveFocusJoined(tui.layoutState.RightSet); got != "b" {
		t.Fatalf("right side = %q at entry, want the waiting b", got)
	}
	t0 := time.Now()
	for _, at := range []time.Duration{time.Second, 3 * liveHoldDuration} {
		// render runs the activity detector, and this test pane's screen has
		// no prompt on it, so a frame reads it idle. A real pane's detector
		// keeps finding the open dialog every frame; re-stating waiting
		// before each tick stands in for that.
		setActivity(tui, "b", StateWaitingInput)
		tui.tickLiveFocus(t0.Add(at))
		tui.applyLayout()
		tui.render()
		if got := simScreenText(s); strings.Contains(got, liveFocusNoneWorking) {
			t.Fatalf("at +%v the right side says %q while b waits on a prompt", at, liveFocusNoneWorking)
		}
	}
	// Positive twin: once b is idle past its hold the hint does show, so the
	// check above can see it.
	setActivity(tui, "b", StateIdle)
	end := t0.Add(4 * liveHoldDuration)
	tui.tickLiveFocus(end)
	tui.applyLayout()
	tui.render()
	if got := simScreenText(s); !strings.Contains(got, liveFocusNoneWorking) {
		t.Errorf("b idle past its hold, but the right side does not say %q", liveFocusNoneWorking)
	}
}

// AC2 for the list split: a waiting agent at entry gets a box.
func TestLiveList_AWaitingAgentAtEntryGetsABox(t *testing.T) {
	tui := listFleet(t, "super", "eng1", "eng2")
	setActivity(tui, "eng1", StateIdle)
	setActivity(tui, "eng2", StateWaitingInput)
	altShiftL(tui)
	if got := listJoined(tui); !strings.Contains(got, "eng2") || strings.Contains(got, "eng1") {
		t.Fatalf("boxes = %q at entry, want the waiting eng2 and not the idle eng1", got)
	}
}

// AC3: the split predicate adds waiting; the working predicate (live mode,
// the overlay, the top title) does not change.
func TestSplitPredicate_WaitingJoinsWorkingStaysAsItWas(t *testing.T) {
	for st, want := range map[ActivityState]bool{
		StateRunning: true, StateBackground: true, StateWaitingInput: true,
		StateIdle: false, StatePendingInput: false, StateSuspended: false, StateDead: false,
	} {
		p := &mockPaneView{name: "x", alive: true, activity: st}
		if got := paneKeptOnRight(p); got != want {
			t.Errorf("paneKeptOnRight(%v) = %v, want %v", st, got, want)
		}
	}
	if paneIsWorking(&mockPaneView{name: "x", alive: true, activity: StateWaitingInput}) {
		t.Error("paneIsWorking now counts waiting; live mode ranking and the overlay columns would change")
	}
}

// AC4, feed it nothing: an empty fleet places nobody, and a hidden waiting
// pane is never on the right.
func TestSplitPredicate_EmptyFleetAndHiddenWaitingPane(t *testing.T) {
	le := NewLiveEngine(0, nil, nil)
	le.Eligible = paneKeptOnRight
	if got := le.TickAuto(nil, time.Now()); len(got) != 0 {
		t.Errorf("an empty fleet places %v", got)
	}
	tui := liveFocusTUI(t)
	setActivity(tui, "d", StateWaitingInput)
	tui.layoutState.Hidden = map[string]bool{"d": true}
	altShiftF(tui)
	t0 := time.Now()
	for _, at := range []time.Duration{0, time.Second, 3 * liveHoldDuration} {
		tui.tickLiveFocus(t0.Add(at))
		if strings.Contains(liveFocusJoined(tui.layoutState.RightSet), "d") {
			t.Fatalf("at +%v the hidden waiting d is on the right: %v", at, tui.layoutState.RightSet)
		}
	}
	// Positive twin: unhidden, the same waiting d is placed.
	delete(tui.layoutState.Hidden, "d")
	for i := 1; i <= 3; i++ {
		tui.tickLiveFocus(t0.Add(3*liveHoldDuration + time.Duration(i)*time.Second))
	}
	if !strings.Contains(liveFocusJoined(tui.layoutState.RightSet), "d") {
		t.Errorf("unhidden waiting d is not placed: %v", tui.layoutState.RightSet)
	}
}

// AC1 at entry: several agents already waiting are all on the right on the
// first frame, as working ones are. The engine's ticks add one pane per
// change, so only the entry seed can place them together; with a single
// waiting pane the first tick would hide a seed that skips waiting.
func TestLiveFocus_SeveralWaitingAgentsAtEntryAllStartOnTheRight(t *testing.T) {
	tui := liveFocusTUI(t)
	for _, n := range []string{"b", "c", "d"} {
		setActivity(tui, n, StateWaitingInput)
	}
	altShiftF(tui)
	if got := liveFocusJoined(tui.layoutState.RightSet); got != "b,c,d" {
		t.Fatalf("right side at entry = %q, want all three waiting agents b,c,d", got)
	}
}

func TestLiveList_SeveralWaitingAgentsAtEntryAllGetBoxes(t *testing.T) {
	tui := listFleet(t, "super", "eng1", "eng2", "eng3")
	for _, n := range []string{"eng1", "eng2", "eng3"} {
		setActivity(tui, n, StateWaitingInput)
	}
	altShiftL(tui)
	got := listJoined(tui)
	for _, n := range []string{"eng1", "eng2", "eng3"} {
		if !strings.Contains(got, n) {
			t.Fatalf("boxes at entry = %q, want every waiting agent; %s is missing", got, n)
		}
	}
}
