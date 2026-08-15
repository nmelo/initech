package tui

import (
	"regexp"
	"strings"
	"testing"
)

// ── ini-ap3i: the two-families rule applied to the GRID ─────────────
//
// The overlay, needs-input list, and ribbon all learned in ini-9isx that a
// window alias is presentation (stripped) while a cross-machine host is
// identity (kept). The agents grid never went through paneDisplayName, so a
// remote peer's agent rendered as its bare local namesake: support:super
// showed as a second "super", and — because cross-machine panes attach after
// stampFleetThenApplyOrder and carry no fleet number — it fell back to its
// slice index and COLLIDED with a stamped agent's number (two rows both
// numbered 1, observed live by the operator on 2026-08-15).

// TestAgentsGrid_CrossMachineAgentKeepsHostAndUniqueNumber renders the real
// grid and reads the screen back: the remote agent must appear under its
// host-qualified name, and no grid number may name two different agents.
func TestAgentsGrid_CrossMachineAgentKeepsHostAndUniqueNumber(t *testing.T) {
	tui, _ := tierTUI(t, false, "super", "eng1")
	// Locals carry served fleet numbers, as in production (stampFleetThenApplyOrder).
	for i, p := range tui.panes {
		if lp, ok := p.(*Pane); ok {
			lp.SetFleetIdx(i)
		}
	}
	// A cross-machine peer attaches later and is never stamped.
	tui.panes = append(tui.panes, &RemotePane{name: "super", host: "support", alive: true})

	tui.renderAgentsGrid()
	out := screenText(t, tui)

	if !strings.Contains(out, "support:super") {
		t.Errorf("grid collapsed the cross-machine agent onto its local namesake — "+
			"want support:super rendered, got:\n%s", out)
	}

	// No number names two agents: number-addressed search and grab depend on it.
	rows := regexp.MustCompile(`(\d+) \[.\] ([A-Za-z0-9:_-]+)`).FindAllStringSubmatch(out, -1)
	if len(rows) < 3 {
		t.Fatalf("expected at least 3 numbered rows, parsed %d from:\n%s", len(rows), out)
	}
	byNum := map[string]string{}
	for _, m := range rows {
		num, name := m[1], m[2]
		if prev, ok := byNum[num]; ok && prev != name {
			t.Errorf("grid number %s names two different agents (%q and %q)", num, prev, name)
		}
		byNum[num] = name
	}
}

// TestAgentsGrid_SearchMatchesTheDisplayedName pins search to the same string
// the cell displays (the search site's own comment: search and display
// disagreeing would be worse than either bug). "support" must find the remote
// agent; pre-fix it matched nothing because search read the bare Name().
func TestAgentsGrid_SearchMatchesTheDisplayedName(t *testing.T) {
	tui, _ := tierTUI(t, false, "super", "eng1")
	tui.panes = append(tui.panes, &RemotePane{name: "super", host: "support", alive: true})
	remoteIdx := len(tui.panes) - 1

	tui.agents.searching = true
	tui.agents.searchBuf = []rune("support")

	if !tui.agentsMatched(remoteIdx) {
		t.Error("search for 'support' does not match support:super — search is not reading the displayed name")
	}
	if tui.agentsMatched(0) {
		t.Error("search for 'support' matched local super — over-broad match")
	}
}

// TestAgentsGrid_ArrowsMoveWithinMachineBand pins the band-label unification:
// left/right were dead inside the machine section because navigation derived
// the band from GroupOf (where remote panes deliberately have no entry) while
// the renderer derived the machine label — two derivations of one fact.
func TestAgentsGrid_ArrowsMoveWithinMachineBand(t *testing.T) {
	tui := newTestTUI()
	a := &RemotePane{name: "super", host: "support", alive: true}
	b := &RemotePane{name: "pm", host: "support", alive: true}
	tui.panes = append(tui.panes, a, b)

	tui.agents.selected = 0
	tui.agentsMoveH(1)
	if tui.agents.selected != 1 {
		t.Fatalf("right arrow did not move within the machine band: selected=%d want 1", tui.agents.selected)
	}
	tui.agentsMoveH(-1)
	if tui.agents.selected != 0 {
		t.Fatalf("left arrow did not move back: selected=%d want 0", tui.agents.selected)
	}
}

// TestOverlayOrder_LocalsThenMachinesAlphabetical pins the operator-chosen
// display order (2026-08-15): locals in arrangement order first, then one
// block per remote machine, machines alphabetical, blocks in arrival order.
func TestOverlayOrder_LocalsThenMachinesAlphabetical(t *testing.T) {
	panes := []PaneView{
		&RemotePane{name: "z1", host: "zeta", alive: true},
		&Pane{name: "super"},
		&RemotePane{name: "a1", host: "alpha", alive: true},
		&Pane{name: "pm"},
		&RemotePane{name: "z2", host: "zeta", alive: true},
	}
	got := orderPanesForDisplay(panes)
	want := []int{1, 3, 2, 0, 4} // super, pm, alpha:a1, zeta:z1, zeta:z2
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("display order = %v, want %v (locals first, machines alphabetical, arrival within)", got, want)
		}
	}
}
