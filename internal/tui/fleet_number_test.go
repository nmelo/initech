package tui

// Agent number N means the same agent on every monitor (ini-6m4, repaired by
// ini-hbfp).
//
// The defect these pin: a respawned pane is a NEW *Pane, and the hand-written
// carry-over list in resumePane moved region, protected, beads and thresholds
// but not the fleet stamp. Unstamped panes fall into agentsGridNumber's
// fallback and are renumbered after the stamped range in THIS window's local
// order, so window 1 renumbered every staggered agent it respawned while
// window 2 -- which re-derives from the ordered hello_ok list -- kept the
// canonical numbers. Measured live: w1 had eng2=5,eng1=6,qa1=7,pm=8 where w2
// had pm=5,qa1=6,eng1=7,eng2=8.

import (
	"testing"
)

// numbersFromModal reads the number the modal would show for each pane.
func numbersFromModal(t *testing.T, tui *TUI) map[string]int {
	t.Helper()
	out := map[string]int{}
	for i, p := range tui.panes {
		out[agentKey(p)] = tui.agentsGridNumber(i) + 1
	}
	return out
}

// TestFleetNumbering_AgreesAcrossWindowsDespiteLocalOrder is the bead's ask:
// one derivation of number per agent, identical in every window.
//
// The two windows are given DIFFERENT pane orders on purpose -- each window
// applies its own saved arrangement, and numbering by position is the original
// ini-6m4 bug. Only the stamp may decide the number.
func TestFleetNumbering_AgreesAcrossWindowsDespiteLocalOrder(t *testing.T) {
	creation := []string{"super", "pmm", "shipper", "growth", "pm", "qa1", "eng1", "eng2"}

	// Window 1: local panes, stamped in creation order, then arranged
	// differently (its saved layout puts the engineers first).
	w1 := newTestTUI()
	stamped := map[string]*Pane{}
	for i, name := range creation {
		p := testPane(name)
		p.SetFleetIdx(i)
		stamped[name] = p
	}
	for _, name := range []string{"eng2", "eng1", "qa1", "pm", "growth", "shipper", "pmm", "super"} {
		w1.panes = append(w1.panes, stamped[name])
	}

	// Window 2: the same fleet as a viewer sees it, numbered from the ordered
	// hello_ok list, in yet another local arrangement.
	w2 := newTestTUI()
	w2.windowID = "window-2"
	remotes := map[string]*RemotePane{}
	for i, name := range creation {
		rp := &RemotePane{name: name, host: WindowOnePeerName, alive: true}
		rp.fleetNum = i + 1
		remotes[name] = rp
	}
	for _, name := range []string{"qa1", "super", "eng1", "pm", "eng2", "growth", "pmm", "shipper"} {
		w2.panes = append(w2.panes, remotes[name])
	}

	n1, n2 := numbersFromModal(t, w1), numbersFromModal(t, w2)
	for i, name := range creation {
		want := i + 1
		if n1[name] != want {
			t.Errorf("window 1 numbers %s as %d, want %d (creation order is canonical)",
				name, n1[name], want)
		}
		if n2[name] != want {
			t.Errorf("window 2 numbers %s as %d, want %d", name, n2[name], want)
		}
		if n1[name] != n2[name] {
			t.Errorf("agent %s is number %d in window 1 and %d in window 2 -- grab-by-number "+
				"acts on a different agent depending on which monitor the operator is "+
				"looking at", name, n1[name], n2[name])
		}
	}
}

// TestCarryFleetIdentity_RespawnKeepsTheNumber is the regression cell for the
// exact cause: the number must survive the pane object being replaced.
func TestCarryFleetIdentity_RespawnKeepsTheNumber(t *testing.T) {
	old := testPane("eng1")
	old.SetFleetIdx(6) // 7th agent in creation order

	respawned := testPane("eng1")
	if respawned.FleetIdx() >= 0 {
		t.Fatal("precondition: a fresh pane is expected to be unstamped, so this cell " +
			"measures the carry rather than an accident of construction")
	}

	carryFleetIdentity(respawned, old)

	if got := respawned.FleetIdx(); got != 6 {
		t.Errorf("a respawned pane came back with fleet index %d, want 6.\n\nUnstamped panes "+
			"are renumbered after the stamped range in this window's LOCAL order, so every "+
			"agent this window respawns drifts out of the fleet's numbering while other "+
			"windows keep it.", got)
	}
}

// TestFleetNumbering_RespawnedPaneDoesNotRenumberTheFleet is the same defect at
// the surface the operator sees, so a future edit that drops the carry cannot
// pass by keeping the helper and bypassing it.
func TestFleetNumbering_RespawnedPaneDoesNotRenumberTheFleet(t *testing.T) {
	creation := []string{"super", "pmm", "eng1", "eng2"}
	tui := newTestTUI()
	for i, name := range creation {
		p := testPane(name)
		p.SetFleetIdx(i)
		tui.panes = append(tui.panes, p)
	}
	before := numbersFromModal(t, tui)

	// The stagger walker respawns eng1: a NEW pane object takes its slot.
	respawned := testPane("eng1")
	carryFleetIdentity(respawned, tui.panes[2].(*Pane))
	tui.panes[2] = respawned

	after := numbersFromModal(t, tui)
	for name, want := range before {
		if after[name] != want {
			t.Errorf("respawning eng1 renumbered %s from %d to %d -- one agent restarting "+
				"must not move any agent's number, in this window or any other",
				name, want, after[name])
		}
	}
}
