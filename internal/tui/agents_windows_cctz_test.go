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
