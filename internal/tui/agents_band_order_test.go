package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// Tests for ini-mchr: < and > in the Agents panel move the band holding the
// highlighted agent one place left or right as drawn, saved with the layout.

func bandKey(tui *TUI, r rune) {
	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
}

func groupsEqual(got []string, want ...string) bool {
	return strings.Join(got, ",") == strings.Join(want, ",")
}

// AC1: > and < swap the band with its neighbour, the highlight stays on the
// same agent, and the members of every band are unchanged.
func TestAgentsBand_AngleKeysMoveTheCurrentBandAndKeepTheSelection(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "eng2", "qa1")
	openAgentsList(tui)
	if !groupsEqual(tui.layoutState.Groups, "core", "eng", "qa") {
		t.Fatalf("precondition: groups = %v, want [core eng qa]", tui.layoutState.Groups)
	}
	selectAgent(t, tui, "eng2")
	before := map[string]string{}
	for k, v := range tui.layoutState.GroupOf {
		before[k] = v
	}

	bandKey(tui, '<')
	if !groupsEqual(tui.layoutState.Groups, "eng", "core", "qa") {
		t.Errorf("< from eng: groups = %v, want [eng core qa]", tui.layoutState.Groups)
	}
	if agentsSelName(tui) != "eng2" {
		t.Errorf("< moved the highlight to %s, want it to stay on eng2", agentsSelName(tui))
	}
	bandKey(tui, '>')
	bandKey(tui, '>')
	if !groupsEqual(tui.layoutState.Groups, "core", "qa", "eng") {
		t.Errorf("> twice: groups = %v, want [core qa eng]", tui.layoutState.Groups)
	}
	if agentsSelName(tui) != "eng2" {
		t.Errorf("> moved the highlight to %s, want it to stay on eng2", agentsSelName(tui))
	}
	for k, v := range before {
		if tui.layoutState.GroupOf[k] != v {
			t.Errorf("%s changed band %q -> %q; band moves must not touch members", k, v, tui.layoutState.GroupOf[k])
		}
	}
}

// AC1: < on the first band and > on the last are silent no-ops.
func TestAgentsBand_FirstAndLastAreSilentNoOps(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "qa1")
	openAgentsList(tui)
	selectAgent(t, tui, "super")
	bandKey(tui, '<')
	if !groupsEqual(tui.layoutState.Groups, "core", "eng", "qa") || tui.agents.error != "" {
		t.Errorf("< on the first band: groups=%v error=%q, want unchanged and no error", tui.layoutState.Groups, tui.agents.error)
	}
	selectAgent(t, tui, "qa1")
	bandKey(tui, '>')
	if !groupsEqual(tui.layoutState.Groups, "core", "eng", "qa") || tui.agents.error != "" {
		t.Errorf("> on the last band: groups=%v error=%q, want unchanged and no error", tui.layoutState.Groups, tui.agents.error)
	}
}

// AC1 "as drawn" with two windows: eng is on window 2, so window 1 draws
// [core qa] and qa's left neighbour is core even though eng sits between
// them in the groups list. eng keeps its slot.
func TestAgentsBand_TwoWindowsSwapAmongTheSameWindowsBands(t *testing.T) {
	tui, _ := tierTUI(t, true, "super", "eng1", "qa1")
	if !groupsEqual(tui.layoutState.Groups, "core", "eng", "qa") {
		t.Fatalf("precondition: groups = %v, want [core eng qa]", tui.layoutState.Groups)
	}
	if err := tui.moveGroupToWindow("eng", "2"); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if w := tui.agentsAssignment().WindowOfGroup("eng"); w != "2" {
		t.Fatalf("fixture: eng on window %q, want 2", w)
	}
	tui.agents.active = true
	selectAgent(t, tui, "qa1")
	bandKey(tui, '<')
	if !groupsEqual(tui.layoutState.Groups, "qa", "eng", "core") {
		t.Errorf("< on qa (window 1 draws [core qa]): groups = %v, want [qa eng core]", tui.layoutState.Groups)
	}
	// eng is alone on window 2: both directions are no-ops.
	selectAgent(t, tui, "eng1")
	bandKey(tui, '<')
	bandKey(tui, '>')
	if !groupsEqual(tui.layoutState.Groups, "qa", "eng", "core") {
		t.Errorf("eng alone on its window moved: groups = %v", tui.layoutState.Groups)
	}
}

// AC2: the new order is written to the layout store and loads back in that
// order.
func TestAgentsBand_MovedOrderSurvivesSaveAndLoad(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "qa1")
	root := t.TempDir()
	tui.projectRoot = root
	openAgentsList(tui)
	selectAgent(t, tui, "qa1")
	bandKey(tui, '<')
	got, ok := LoadLayout(root, []string{"super", "eng1", "qa1"})
	if !ok {
		t.Fatal("no layout file after a band move; the move was not saved")
	}
	if !groupsEqual(got.Groups, "core", "qa", "eng") {
		t.Errorf("loaded groups = %v, want [core qa eng]", got.Groups)
	}
}

// AC3: in the search box < and > are text; nothing moves.
func TestAgentsBand_AngleKeysTypeInTheSearchBox(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "qa1")
	tui.openAgentsModal()
	bandKey(tui, '<')
	bandKey(tui, '>')
	if string(tui.agents.searchBuf) != "<>" {
		t.Errorf("search box holds %q, want <>", string(tui.agents.searchBuf))
	}
	if !groupsEqual(tui.layoutState.Groups, "core", "eng", "qa") {
		t.Errorf("typing < > in the box moved bands: %v", tui.layoutState.Groups)
	}
}

// AC3: while an agent is grabbed, < and > do nothing.
func TestAgentsBand_GrabIgnoresAngleKeys(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "qa1")
	openAgentsList(tui)
	selectAgent(t, tui, "eng1")
	agentsKey(tui, tcell.KeyEnter) // grab
	if !tui.agents.moving {
		t.Fatal("fixture: Enter did not grab")
	}
	bandKey(tui, '<')
	bandKey(tui, '>')
	if !groupsEqual(tui.layoutState.Groups, "core", "eng", "qa") || !tui.agents.moving {
		t.Errorf("during a grab: groups=%v moving=%v, want unchanged and still grabbed", tui.layoutState.Groups, tui.agents.moving)
	}
}

// AC4: the footer names the keys within the width budget; the help overlay
// says the panel is where grouping lives.
func TestAgentsBand_FooterAndHelpNameTheKeys(t *testing.T) {
	if !strings.Contains(agentsHelpText, "< > band") {
		t.Errorf("footer does not name < > band: %q", agentsHelpText)
	}
	if !strings.Contains(strings.Join(getHelpLines(), "\n"), "Manage visibility, order, grouping and pinning") {
		t.Error("help overlay's agents line does not mention grouping")
	}
}
