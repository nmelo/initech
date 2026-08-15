package tui

import (
	"os"
	"path/filepath"
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

// ── ini-ap3i: remote panes never carry a persisted group ─────────────
//
// The operator grab-moved support:pm into a monitor-2 band; the write path
// persisted group_of: {support:pm: core}. Window 1 then skipped it (core is
// window 2's group) and window 2 cannot draw it (no stream — the relay is the
// deferred feature), so the agent rendered NOWHERE (observed 2026-08-15).
// ensureGroups' remote-skip only prevented NEW assignments; existing entries
// survived every frame. The rule has three halves: the healer purges entries
// on sight, the loader refuses them from disk, and the write sites never
// create them.
func TestEnsureGroups_PurgesRemotePaneEntries(t *testing.T) {
	tui := newTestTUI()
	tui.panes = append(tui.panes, &RemotePane{name: "pm", host: "support", alive: true})
	tui.layoutState.GroupOf = map[string]string{"support:pm": "core"}
	tui.layoutState.Groups = []string{"core"}

	tui.ensureGroups(false)

	if g, ok := tui.layoutState.GroupOf["support:pm"]; ok {
		t.Errorf("ensureGroups kept remote pane group entry %q — routes the pane to a window with no stream for it", g)
	}
}

func TestLoadLayout_DropsCrossMachineGroupEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".initech"), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := `grid: 2x1
mode: grid
order:
    - super
    - support:pm
groups:
    - core
group_of:
    super: core
    support:pm: core
`
	if err := os.WriteFile(filepath.Join(dir, ".initech", "layout.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	ls, ok := LoadLayout(dir, []string{"super", "support:pm"})
	if !ok {
		t.Fatal("LoadLayout rejected a valid layout")
	}
	if _, has := ls.GroupOf["support:pm"]; has {
		t.Error("loader kept cross-machine group_of entry — stale placement resurrects the vanishing-pane bug on every restart")
	}
	if ls.GroupOf["super"] != "core" {
		t.Errorf("loader dropped a LOCAL group entry: got %q, want core", ls.GroupOf["super"])
	}
}

func TestSetPaneGroup_RefusesRemotePane(t *testing.T) {
	tui := newTestTUI()
	rp := &RemotePane{name: "pm", host: "support", alive: true}
	tui.panes = append(tui.panes, rp)
	tui.layoutState.GroupOf = map[string]string{}

	tui.setPaneGroup(rp, "core")

	if _, ok := tui.layoutState.GroupOf["support:pm"]; ok {
		t.Error("setPaneGroup wrote a group for a remote-machine pane")
	}
}
