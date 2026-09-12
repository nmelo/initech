package tui

import "testing"

// agentsNavDiameter measures the any-to-any arrow-press diameter of the
// agents modal by BFS over the REAL navigation functions (agentsMoveH /
// agentsMoveV on a simulation-screen TUI), not over a model of them. The
// spec's own history (pm/specs/agents-grid-modal.md, "Navigation bound") is
// why: the first bound was asserted, not derived, and was wrong.
//
// Returns the diameter and the worst pair (by pane name). Unreachable pairs
// count as a diameter of -1 -- a finding, never a silent max.
func agentsNavDiameter(t *testing.T, tui *TUI) (int, string, string) {
	t.Helper()
	n := len(tui.panes)
	diam := 0
	worstA, worstB := "", ""
	for start := 0; start < n; start++ {
		dist := make([]int, n)
		for i := range dist {
			dist[i] = -1
		}
		dist[start] = 0
		queue := []int{start}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, step := range []func(){
				func() { tui.agentsMoveH(-1) },
				func() { tui.agentsMoveH(1) },
				func() { tui.agentsMoveV(tui.agentsCurrentCells(), -1) },
				func() { tui.agentsMoveV(tui.agentsCurrentCells(), 1) },
			} {
				tui.agents.selected = cur
				tui.agents.moving = false
				step()
				next := tui.agents.selected
				if dist[next] == -1 {
					dist[next] = dist[cur] + 1
					queue = append(queue, next)
				}
			}
		}
		for i, d := range dist {
			if d == -1 {
				return -1, tui.panes[start].Name(), tui.panes[i].Name()
			}
			if d > diam {
				diam, worstA, worstB = d, tui.panes[start].Name(), tui.panes[i].Name()
			}
		}
	}
	return diam, worstA, worstB
}

// agentsDiameterFleet23 is the spec's 23-agent fixture: 5 core, 6 eng, 12 qa.
var agentsDiameterFleet23 = []string{
	"super", "pm", "shipper", "pmm", "growth",
	"eng1", "eng2", "eng3", "eng4", "eng5", "eng6",
	"qa1", "qa2", "qa3", "qa4", "qa5", "qa6", "qa7", "qa8", "qa9", "qa10", "qa11", "qa12",
}

// TestAgentsNavDiameter_Fleet23 records the measured arrow-press diameter on
// the 23-agent fixture (ini-w771 AC10). It asserts reachability only -- the
// number is recorded on the bead, not asserted here, per the spec's rule
// that the bound is measured on the shipped implementation.
func TestAgentsNavDiameter_Fleet23(t *testing.T) {
	tui, _ := newTestTUIWithScreen(agentsDiameterFleet23...)
	tui.ensureGroups(false)
	d, a, b := agentsNavDiameter(t, tui)
	if d < 0 {
		t.Fatalf("unreachable pair: %s -> %s", a, b)
	}
	t.Logf("seeded groups (core/eng/qa): diameter %d, worst pair %s -> %s", d, a, b)

	// Operator's target grouping from the spec: pmm+growth split into mkt.
	tui2, _ := newTestTUIWithScreen(agentsDiameterFleet23...)
	tui2.ensureGroups(false)
	tui2.layoutState.Groups = []string{"core", "mkt", "eng", "qa"}
	tui2.layoutState.GroupOf["pmm"] = "mkt"
	tui2.layoutState.GroupOf["growth"] = "mkt"
	d2, a2, b2 := agentsNavDiameter(t, tui2)
	if d2 < 0 {
		t.Fatalf("unreachable pair: %s -> %s", a2, b2)
	}
	t.Logf("four groups (core/mkt/eng/qa): diameter %d, worst pair %s -> %s", d2, a2, b2)
}
