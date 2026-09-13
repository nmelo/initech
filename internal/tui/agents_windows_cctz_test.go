package tui

import (
	"net"
	"reflect"
	"strings"
	"testing"
)

// ini-cctz: hover had three windows attached and the Agents panel showed two,
// because the window list was derived from GROUP ASSIGNMENTS alone. A window
// connected with nothing assigned contributed no band, so the operator could
// not tell "nothing assigned" from "never connected".

func TestAgentsWindowOrder_ConnectedButUnassignedWindowAppears(t *testing.T) {
	assign := &WindowAssignment{groupWindow: map[string]string{"eng": "window-2"}}
	connected := map[string]bool{"window-2": true, "window-3": true}
	got := agentsWindowOrder(assign, []string{"core", "eng"}, connected)
	want := []string{WindowOne, "window-2", "window-3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v — a connected window with nothing assigned must still get a band", got, want)
	}
}

func TestAgentsWindowOrder_AssignedButNotConnectedWindowStillAppears(t *testing.T) {
	assign := &WindowAssignment{groupWindow: map[string]string{"eng": "window-2"}}
	got := agentsWindowOrder(assign, []string{"eng"}, map[string]bool{})
	want := []string{WindowOne, "window-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v — an absent window's groups still belong to it", got, want)
	}
}

// nil connected is the shipped assignment-only behaviour, which the m-move
// cycle keeps: this bead is display-only.
func TestAgentsWindowOrder_NilConnectedIsTheShippedOrder(t *testing.T) {
	assign := &WindowAssignment{groupWindow: map[string]string{"qa": "window-3", "eng": "window-2"}}
	got := agentsWindowOrder(assign, []string{"eng", "qa"}, nil)
	want := []string{WindowOne, "window-2", "window-3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// The band header says which fact it is, because the operator acts
// differently on each.
func TestAgentsWindowStatusOf_DistinguishesTheTwoFacts(t *testing.T) {
	connected := map[string]bool{"window-3": true, "window-2": true}
	for _, tc := range []struct {
		name   string
		window string
		groups int
		conn   map[string]bool
		want   agentsWindowStatus
	}{
		{"connected, nothing assigned", "window-3", 0, connected, windowConnectedEmpty},
		{"assigned, not connected", "window-4", 2, connected, windowAssignedAbsent},
		{"assigned and connected", "window-2", 1, connected, windowNormal},
		{"window 1 is never flagged", WindowOne, 0, map[string]bool{}, windowNormal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentsWindowStatusOf(tc.window, tc.groups, tc.conn); got != tc.want {
				t.Errorf("status = %v, want %v", got, tc.want)
			}
		})
	}
}

// The operator's report, rendered: three windows attached, groups on only
// window 2 and window 2 NOT connected; window 3 connected with nothing. Both
// bands appear, and each header says which fact it is.
func TestAgentsPanel_ThreeWindowsRenderThreeBandsThatSayWhichFactTheyAre(t *testing.T) {
	tui, _ := tierTUI(t, true, "super", "eng1", "qa1")
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("eng", "window-2"); err != nil {
		t.Fatal(err)
	}
	tui.windowSrv = &windowServer{daemon: &Daemon{clients: map[string]net.Conn{"window-3": nil}}}
	tui.renderAgentsGrid()
	out := screenText(t, tui)
	for _, want := range []string{"monitor 1", "monitor 2", "monitor 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("panel is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "connected, nothing assigned") {
		t.Errorf("the connected-but-empty window does not say so:\n%s", out)
	}
	if !strings.Contains(out, "not connected") {
		t.Errorf("the assigned-but-absent window does not say so:\n%s", out)
	}
}

// ── ini-9e7x: the key reaches what the hint names ──────────────────

// threeWindowFleet is the operator's setup: groups on windows 1 and 2,
// window-3 attached with nothing assigned. The divergence between the
// assigned set and the connected set is the whole defect, so the fixture
// constructs it explicitly — a connected id absent from assignments.
func threeWindowFleet(t *testing.T) *TUI {
	t.Helper()
	tui, _ := tierTUI(t, true, "super", "eng1", "qa1")
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("qa", "window-2"); err != nil {
		t.Fatal(err)
	}
	tui.windowSrv = &windowServer{daemon: &Daemon{clients: map[string]net.Conn{"window-2": nil, "window-3": nil}}}
	return tui
}

// pressM presses m n times and returns the window eng lands on after each.
func pressM(tui *TUI, n int) []string {
	var seen []string
	for i := 0; i < n; i++ {
		tui.agentsMoveGroupToNextWindow()
		seen = append(seen, tui.agentsAssignment().WindowOfGroup("eng"))
	}
	return seen
}

// TestAgentsMove_ReachesTheConnectedButEmptyWindowTheHintNames is the
// ini-9e7x regression: the band said "move a group here with m" for monitor
// 3 while m cycled 1<->2. With eng on window 2, one press must land on
// window-3 — the one window that is connected but has nothing assigned —
// and the cycle must then return through 1 and 2.
func TestAgentsMove_ReachesTheConnectedButEmptyWindowTheHintNames(t *testing.T) {
	tui := threeWindowFleet(t)
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("eng", "window-2"); err != nil {
		t.Fatal(err)
	}
	selectAgent(t, tui, "eng1")

	tui.renderAgentsGrid()
	if out := screenText(t, tui); !strings.Contains(out, "connected, nothing assigned") {
		t.Fatalf("fixture does not show the hint the key must honour:\n%s", out)
	}

	tui.agentEvents = make(chan AgentEvent, 8)
	got := pressM(tui, 3)
	want := []string{"window-3", WindowOne, "window-2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("m from window 2 visited %v, want %v (the connected-but-empty window, then round)", got, want)
	}

	// The notice names the monitor number the band displays: window-3 is the
	// third tier, so the first press must say "monitor 3", not a number
	// computed from the assigned-only list.
	var first string
	for len(tui.agentEvents) > 0 {
		if ev := <-tui.agentEvents; ev.Type == EventGroupMoved && first == "" {
			first = ev.Detail
		}
	}
	if first != "eng → monitor 3" {
		t.Errorf("first move notice = %q, want %q", first, "eng → monitor 3")
	}
}

// From window 1 the cycle is 2, 3, back to 1: the fresh-window push-out is
// still appended after the rendered list, so it is not the next stop while
// another window exists — the bounded-cycle reasoning from ini-9ka.4 stands.
func TestAgentsMove_FromWindowOneVisitsEveryRenderedBandThenReturns(t *testing.T) {
	tui := threeWindowFleet(t)
	selectAgent(t, tui, "eng1")
	got := pressM(tui, 3)
	want := []string{"window-2", "window-3", WindowOne}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("m from window 1 visited %v, want %v", got, want)
	}
}

// After the move the band that said "connected, nothing assigned" is a
// normal band: hint and key agree in both directions.
func TestAgentsMove_TheHintClearsOnceAGroupArrives(t *testing.T) {
	tui := threeWindowFleet(t)
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("eng", "window-2"); err != nil {
		t.Fatal(err)
	}
	selectAgent(t, tui, "eng1")
	pressM(tui, 1)
	tui.renderAgentsGrid()
	out := screenText(t, tui)
	if strings.Contains(out, "connected, nothing assigned") {
		t.Errorf("window-3 holds eng now but its band still invites a move:\n%s", out)
	}
	if !strings.Contains(out, "monitor 3") {
		t.Errorf("monitor 3 band vanished after the move:\n%s", out)
	}
}

// Two windows and no connected-but-empty one: identical to today, including
// the push-out from window 1 onto a fresh window and the return.
func TestAgentsMove_TwoWindowsUnchangedIncludingTheFreshPushOut(t *testing.T) {
	tui, _ := tierTUI(t, true, "super", "eng1", "qa1")
	tui.windowSrv = &windowServer{daemon: &Daemon{clients: map[string]net.Conn{}}}
	selectAgent(t, tui, "eng1")
	got := pressM(tui, 2)
	if got[0] == WindowOne {
		t.Fatalf("first m did not push eng off window 1: %v", got)
	}
	if got[1] != WindowOne {
		t.Errorf("second m did not return eng to window 1: %v", got)
	}
}

// An assigned window whose client is NOT attached stays in the cycle: its
// band renders and its groups still belong to it.
func TestAgentsMove_AssignedButDisconnectedWindowStaysInTheCycle(t *testing.T) {
	tui, _ := tierTUI(t, true, "super", "eng1", "qa1")
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("qa", "window-2"); err != nil {
		t.Fatal(err)
	}
	tui.windowSrv = &windowServer{daemon: &Daemon{clients: map[string]net.Conn{"window-3": nil}}}
	selectAgent(t, tui, "eng1")
	got := pressM(tui, 3)
	want := []string{"window-2", "window-3", WindowOne}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("visited %v, want %v (window-2 is assigned though not attached)", got, want)
	}
}
