// agents_grid_transpose_test.go covers the transposed agents modal
// (ini-w771, spec section "Orientation reversal"): a group is a COLUMN of
// agents, groups sit side by side inside a monitor band, monitors stay as
// stacked bands. These tests drive the real key handlers so they compile
// against the shipped horizontal layout and were confirmed RED there first
// (negative control): every one of them encodes a movement the horizontal
// layout answers differently.
package tui

import (
	"fmt"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func selectedName(tui *TUI) string {
	if tui.agents.selected < 0 || tui.agents.selected >= len(tui.panes) {
		return ""
	}
	return tui.panes[tui.agents.selected].Name()
}

func pressAgents(tui *TUI, k tcell.Key) {
	tui.handleAgentsKey(tcell.NewEventKey(k, 0, 0))
}

// ---------- plain navigation ----------

// Up/down walk a group's agents top-to-bottom and stop at the column's ends
// when there is no adjacent band (single window: every group is in one band).
func TestAgentsTranspose_DownWalksColumnAndStopsAtEnd(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.openAgentsModal()

	pressAgents(tui, tcell.KeyDown)
	if got := selectedName(tui); got != "eng2" {
		t.Errorf("after Down: selected = %q, want eng2", got)
	}
	pressAgents(tui, tcell.KeyDown)
	if got := selectedName(tui); got != "eng3" {
		t.Errorf("after Down x2: selected = %q, want eng3", got)
	}
	pressAgents(tui, tcell.KeyDown)
	if got := selectedName(tui); got != "eng3" {
		t.Errorf("Down past the column's end: selected = %q, want eng3 (unchanged)", got)
	}
	pressAgents(tui, tcell.KeyUp)
	if got := selectedName(tui); got != "eng2" {
		t.Errorf("after Up: selected = %q, want eng2", got)
	}
}

// Left/right move between adjacent group columns in the same band and stop
// at the band's ends.
func TestAgentsTranspose_RightMovesToAdjacentColumn(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "qa1")
	tui.openAgentsModal()
	if got := tui.layoutState.GroupOf["super"]; got != "core" {
		t.Fatalf("precondition: super should seed to core, got %q", got)
	}

	pressAgents(tui, tcell.KeyRight)
	if got := selectedName(tui); got != "eng1" {
		t.Errorf("after Right from core: selected = %q, want eng1", got)
	}
	pressAgents(tui, tcell.KeyRight)
	if got := selectedName(tui); got != "qa1" {
		t.Errorf("after Right x2: selected = %q, want qa1", got)
	}
	pressAgents(tui, tcell.KeyRight)
	if got := selectedName(tui); got != "qa1" {
		t.Errorf("Right past the last column: selected = %q, want qa1 (unchanged)", got)
	}
	pressAgents(tui, tcell.KeyLeft)
	if got := selectedName(tui); got != "eng1" {
		t.Errorf("after Left: selected = %q, want eng1", got)
	}
}

// Moving sideways lands on the same row when the target column has it, else
// on the target column's last row (I6).
func TestAgentsTranspose_SidewaysLandsOnNearestRow(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "pm", "eng1", "eng2", "eng3")
	tui.openAgentsModal()

	selectAgent(t, tui, "eng3") // eng column, row 2
	pressAgents(tui, tcell.KeyLeft)
	if got := selectedName(tui); got != "pm" {
		t.Errorf("Left from eng row 2 into a 2-tall core column: selected = %q, want pm (core's last row)", got)
	}
	pressAgents(tui, tcell.KeyRight) // pm is row 1
	if got := selectedName(tui); got != "eng2" {
		t.Errorf("Right from core row 1: selected = %q, want eng2 (same row)", got)
	}
}

// A 23-agent single group is a 23-tall column, reached by Down alone.
func TestAgentsTranspose_NavigatesFullColumnWithoutScrolling(t *testing.T) {
	names := make([]string, 23)
	for i := range names {
		names[i] = fmt.Sprintf("qa%d", i+1)
	}
	tui, _ := newTestTUIWithScreen(names...)
	tui.openAgentsModal()
	for i := 0; i < 22; i++ {
		pressAgents(tui, tcell.KeyDown)
	}
	if got := selectedName(tui); got != "qa23" {
		t.Errorf("after 22 Downs in a 23-member column: selected = %q, want qa23", got)
	}
}

// Down from a column's last agent continues into the NEXT BAND's nearest
// column and lands on its top agent (I2); up from a top agent continues to
// the previous band's nearest column and lands on its bottom agent.
func TestAgentsTranspose_DownAtColumnEndContinuesToNextBand(t *testing.T) {
	tui, _ := tierTUI(t, true, "super", "pm", "eng1", "qa1", "qa2")
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("qa", "window-2"); err != nil {
		t.Fatal(err)
	}
	tui.openAgentsModal()

	selectAgent(t, tui, "pm") // core column, bottom, monitor 1
	pressAgents(tui, tcell.KeyDown)
	if got := selectedName(tui); got != "qa1" {
		t.Errorf("Down from core's last agent: selected = %q, want qa1 (top of monitor 2's nearest column)", got)
	}
	pressAgents(tui, tcell.KeyUp)
	if got := selectedName(tui); got != "pm" {
		t.Errorf("Up from monitor 2's top agent: selected = %q, want pm (bottom of monitor 1's nearest column)", got)
	}
	// Within a band, a short column never hops sideways: eng1 is alone in
	// its column next to a 2-tall core; Down from eng1 goes to the next
	// band, not to pm.
	selectAgent(t, tui, "eng1")
	pressAgents(tui, tcell.KeyDown)
	if got := selectedName(tui); got != "qa1" {
		t.Errorf("Down from a 1-tall column beside a 2-tall one: selected = %q, want qa1 (next band), never a neighbour column", got)
	}
}

// ---------- grab ----------

// Grabbed up/down swaps within the group and persists the order.
func TestAgentsTranspose_GrabDownSwapsWithinGroup(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "eng3")
	tui.openAgentsModal()

	pressAgents(tui, tcell.KeyEnter)
	pressAgents(tui, tcell.KeyDown)
	if tui.panes[0].Name() != "eng2" || tui.panes[1].Name() != "eng1" {
		t.Errorf("after grabbed Down: order = [%s, %s, %s], want [eng2, eng1, eng3]",
			tui.panes[0].Name(), tui.panes[1].Name(), tui.panes[2].Name())
	}
	if tui.agents.selected != 1 {
		t.Errorf("selected = %d, want 1 (selection follows the grabbed agent)", tui.agents.selected)
	}
	if got := tui.layoutState.GroupOf["eng1"]; got != "eng" {
		t.Errorf("a same-group swap must not change membership: eng1 in %q", got)
	}
	pressAgents(tui, tcell.KeyEnter)
	if tui.agents.moving {
		t.Error("should have dropped after Enter")
	}
	if len(tui.layoutState.Order) != 3 || tui.layoutState.Order[0] != "eng2" {
		t.Errorf("persisted order = %v, want [eng2, eng1, eng3]", tui.layoutState.Order)
	}
}

// Grabbed left/right carries the agent into the adjacent group: this is how
// membership is edited now.
func TestAgentsTranspose_GrabRightCarriesIntoAdjacentGroup(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1")
	tui.openAgentsModal()
	if got := tui.layoutState.GroupOf["super"]; got != "core" {
		t.Fatalf("precondition: super should be in core, got %q", got)
	}

	pressAgents(tui, tcell.KeyEnter) // grab super
	pressAgents(tui, tcell.KeyRight) // carry into eng's column

	if got := tui.layoutState.GroupOf["super"]; got != "eng" {
		t.Errorf("super's group after grab-right = %q, want eng", got)
	}
	if got := selectedName(tui); got != "super" {
		t.Errorf("selection should follow the grabbed agent, got %q", got)
	}
	if !tui.agents.moving {
		t.Error("still grabbed until Enter drops")
	}
	if len(tui.panes) != 2 {
		t.Errorf("pane count = %d, want 2 (nothing lost or duplicated)", len(tui.panes))
	}
	pressAgents(tui, tcell.KeyEnter)
	if tui.agents.moving {
		t.Error("Enter should drop")
	}
	if got := tui.layoutState.GroupOf["super"]; got != "eng" {
		t.Errorf("membership must survive the drop: %q", got)
	}
}

// Esc cancels the grab: the agent is no longer grabbed and no further arrow
// carries it. (What was already moved stays moved: same as today's grab.)
func TestAgentsTranspose_EscCancelsGrab_NoFurtherCarry(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "qa1")
	tui.openAgentsModal()

	pressAgents(tui, tcell.KeyEnter)
	pressAgents(tui, tcell.KeyEscape)
	if tui.agents.moving {
		t.Fatal("Esc should cancel the grab")
	}
	pressAgents(tui, tcell.KeyRight)
	if got := tui.layoutState.GroupOf["super"]; got != "core" {
		t.Errorf("plain Right after Esc must not move membership: super in %q", got)
	}
	if got := selectedName(tui); got != "eng1" {
		t.Errorf("plain Right should just select eng1, got %q", got)
	}
}

// Carrying an agent LEFT into a taller column splices it at the nearest
// row, taking over that slot, so a grab is a precise placement, not an
// append.
func TestAgentsTranspose_GrabLeftSplicesAtNearestRow(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "pm", "shipper", "eng1", "eng2")
	tui.openAgentsModal()

	selectAgent(t, tui, "eng2") // row 1 of eng
	pressAgents(tui, tcell.KeyEnter)
	pressAgents(tui, tcell.KeyLeft) // into core at row 1 (pm's slot)

	if got := tui.layoutState.GroupOf["eng2"]; got != "core" {
		t.Fatalf("eng2's group = %q, want core", got)
	}
	members := tui.agentsGroupMembers()
	var core []string
	for _, i := range members["core"] {
		core = append(core, tui.panes[i].Name())
	}
	want := []string{"super", "eng2", "pm", "shipper"}
	if fmt.Sprint(core) != fmt.Sprint(want) {
		t.Errorf("core after splice = %v, want %v (eng2 takes row 1, pm and shipper shift down)", core, want)
	}
}

// g creates an empty column right of the current one; grabbed Right lands
// in it and populates it (the only way to fill a fresh group), while plain
// Right skips it.
func TestAgentsTranspose_GrabRightIntoFreshlyCreatedEmptyGroup(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "pmm")
	tui.openAgentsModal()
	selectAgent(t, tui, "pmm")
	tui.agentsCreateGroup("mkt")
	if got := tui.layoutState.Groups; len(got) != 2 || got[1] != "mkt" {
		t.Fatalf("precondition: groups = %v, want [core mkt]", got)
	}

	before := tui.agents.selected
	pressAgents(tui, tcell.KeyRight) // plain: nothing to select there
	if tui.agents.selected != before {
		t.Errorf("plain Right onto an empty column changed selection %d -> %d, want unchanged", before, tui.agents.selected)
	}
	if got := tui.layoutState.GroupOf["pmm"]; got != "core" {
		t.Errorf("plain Right mutated pmm's group to %q", got)
	}

	pressAgents(tui, tcell.KeyEnter)
	pressAgents(tui, tcell.KeyRight)
	if got := tui.layoutState.GroupOf["pmm"]; got != "mkt" {
		t.Fatalf("pmm's group after grab into the empty column = %q, want mkt -- the grab silently no-op'd", got)
	}
	if !tui.agents.moving {
		t.Error("still grabbed until Enter drops")
	}
	members := tui.agentsGroupMembers()
	if len(members["mkt"]) != 1 || tui.panes[members["mkt"][0]].Name() != "pmm" {
		t.Errorf("mkt's members = %v, want exactly [pmm]", members["mkt"])
	}
	if len(tui.panes) != 2 {
		t.Errorf("pane count = %d, want 2", len(tui.panes))
	}
}

// I3 (operator-accepted): grabbed Down at a group's end carries the agent
// into the next band's nearest column, so a single agent can reach a group
// on another monitor.
func TestAgentsTranspose_GrabDownAtColumnEndCarriesIntoNextBand(t *testing.T) {
	tui, _ := tierTUI(t, true, "super", "eng1", "qa1")
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("qa", "window-2"); err != nil {
		t.Fatal(err)
	}
	tui.openAgentsModal()
	selectAgent(t, tui, "eng1")

	pressAgents(tui, tcell.KeyEnter)
	pressAgents(tui, tcell.KeyDown)
	if got := tui.layoutState.GroupOf["eng1"]; got != "qa" {
		t.Errorf("eng1's group after grabbed Down off the end of eng = %q, want qa (next band)", got)
	}
	if got := selectedName(tui); got != "eng1" {
		t.Errorf("selection should follow the grabbed agent, got %q", got)
	}
	// Carried in from above, it takes the top slot.
	members := tui.agentsGroupMembers()
	if len(members["qa"]) != 2 || tui.panes[members["qa"][0]].Name() != "eng1" {
		t.Errorf("qa after the carry = %v, want eng1 first (entered from above)", members["qa"])
	}
	// And back up: leaves qa, rejoins the previous band's nearest column at
	// its bottom.
	pressAgents(tui, tcell.KeyUp)
	if got := tui.layoutState.GroupOf["eng1"]; got == "qa" {
		t.Errorf("grabbed Up from the top of qa should carry eng1 back to monitor 1, still in %q", got)
	}
}

// A remote machine's band is derived, never a destination: carrying a local
// agent into it must not persist a machine label as its group.
func TestAgentsTranspose_GrabDownRefusesMachineBand(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1")
	tui.panes = append(tui.panes,
		&RemotePane{name: "super", host: "support", alive: true},
		&RemotePane{name: "pm", host: "support", alive: true})
	tui.openAgentsModal()
	selectAgent(t, tui, "eng1")

	// Plain Down reaches the machine band (it is navigable)...
	pressAgents(tui, tcell.KeyDown)
	if got := selectedName(tui); got != "super" {
		t.Fatalf("plain Down into the machine band: selected = %q, want super", got)
	}
	pressAgents(tui, tcell.KeyUp)
	// ...but a grabbed Down does not move membership.
	pressAgents(tui, tcell.KeyEnter)
	pressAgents(tui, tcell.KeyDown)
	if got := tui.layoutState.GroupOf["eng1"]; got != "eng" {
		t.Errorf("eng1's group after grabbed Down toward a machine band = %q, want eng (unchanged)", got)
	}
	if got := selectedName(tui); got != "eng1" {
		t.Errorf("selection = %q, want eng1 (stays put)", got)
	}
}

// Machine bands are single columns: arrows within them are vertical now.
// The two local eng agents keep the shipped layout's cells-per-row above
// one, so this discriminates (a lone machine band wrapped to one cell per
// row on the shipped code and read as vertical by accident).
func TestAgentsTranspose_ArrowsMoveVerticallyWithinMachineBand(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.panes = append(tui.panes,
		&RemotePane{name: "super", host: "support", alive: true},
		&RemotePane{name: "pm", host: "support", alive: true})
	tui.openAgentsModal()
	selectAgent(t, tui, "super")
	pressAgents(tui, tcell.KeyDown)
	if got := selectedName(tui); got != "pm" {
		t.Errorf("Down within the machine band: selected = %q, want pm", got)
	}
	pressAgents(tui, tcell.KeyUp)
	if got := selectedName(tui); got != "super" {
		t.Errorf("Up back: selected = %q, want super", got)
	}
}

// ---------- search ----------

// Search steps through matches in READING order: band, column left-to-right,
// row top-to-bottom -- column-major, not pane order.
func TestAgentsTranspose_SearchStepsInColumnMajorOrder(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "pm", "eng2")
	tui.openAgentsModal()
	cells := tui.agentsCurrentCells()
	var order []string
	for _, c := range cells {
		order = append(order, tui.panes[c.paneIdx].Name())
	}
	want := []string{"super", "pm", "eng1", "eng2"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Errorf("cell order = %v, want %v (core column first, top to bottom, then eng)", order, want)
	}
}

// ---------- open anchor ----------

// The modal opens on the TOP-LEFT cell in reading order, not on t.panes[0]:
// with monitors, pane 0 can be an agent whose group lives on monitor 2 while
// the first cell on screen is on monitor 1. Anchoring on pane 0 made the
// first arrow press start from a place the operator did not see as "first"
// (the ini-uz42 composed rig hit exactly this after the transposition).
func TestAgentsTranspose_OpensOnFirstCellInReadingOrder(t *testing.T) {
	tui, _ := tierTUI(t, true, "eng1", "eng2", "super", "qa1")
	if err := mustAssignWriter(t, tui.agentsAssignment()).MoveGroup("eng", "window-2"); err != nil {
		t.Fatal(err)
	}
	tui.openAgentsModal()
	cells := tui.agentsCurrentCells()
	if len(cells) == 0 {
		t.Fatal("no cells")
	}
	first := tui.panes[cells[0].paneIdx].Name()
	if first != "super" {
		t.Fatalf("precondition: first cell in reading order should be super (monitor 1), got %q", first)
	}
	if got := selectedName(tui); got != "super" {
		t.Errorf("modal opened on %q, want the top-left cell super", got)
	}
}
