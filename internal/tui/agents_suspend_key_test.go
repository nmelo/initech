package tui

import (
	"strings"
	"testing"
	"time"
)

// ── ini-ap3i follow-on: suspend/wake from the agents modal ────────────
//
// Operator request (2026-08-15, after persisted suspension shipped): park and
// wake agents from the modal — `s` for the selected agent, `S` for its whole
// band — instead of round-tripping through `initech suspend <name>` per
// agent. Both keys are thin dispatchers over the same one-mechanism pair
// every other path uses: suspendAgent (truthful refusals) and resumePane.

// The ini-162m rule: a capability nobody can find has not shipped. The
// footer is the modal's discovery surface.
func TestAgentsHelpText_NamesSuspendKeys(t *testing.T) {
	if !strings.Contains(agentsHelpText, "s/S suspend") {
		t.Errorf("modal footer does not name the suspend keys: %q", agentsHelpText)
	}
	// Width budget: boxW floors at len+4 and caps at the screen, so a footer
	// past ~114 chars clips its own "Esc close" hint on a 120-col terminal —
	// exactly how the first draft of this feature failed its goldens.
	if len(agentsHelpText) > 114 {
		t.Errorf("modal footer is %d chars; >114 clips on a 120-col terminal", len(agentsHelpText))
	}
}

func testPaneRunning(t *testing.T, name string) *Pane {
	t.Helper()
	p, err := NewPane(PaneConfig{Name: name, Command: []string{"sh", "-c", "sleep 30"}}, 10, 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestAgentsToggleSuspend_ParksAndRecords(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI()
	tui.projectRoot = root
	p := testPaneRunning(t, "seo")
	tui.panes = append(tui.panes, p)
	tui.agents.selected = 0

	tui.agentsToggleSuspend()

	if !p.IsSuspended() {
		t.Fatalf("s did not park the selected agent (modal msg: %q)", tui.agents.error)
	}
	fs, _ := LoadFleetState(root)
	if !fs.IsSuspended("seo") {
		t.Error("modal suspend did not persist — next launch would boot this agent")
	}
}

func TestAgentsToggleSuspend_WakesSuspended(t *testing.T) {
	tui := newTestTUI()
	parked := NewParkedPane(PaneConfig{Name: "seo", Command: []string{"sh", "-c", "echo up; sleep 5"}}, 10, 40)
	tui.panes = append(tui.panes, parked)
	tui.agents.selected = 0

	tui.agentsToggleSuspend()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if np, ok := tui.panes[0].(*Pane); ok && np != parked && !np.IsSuspended() {
			defer np.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("s on a suspended agent did not wake it (modal msg: %q)", tui.agents.error)
}

func TestAgentsToggleSuspend_RemoteIsHonestlyRefused(t *testing.T) {
	tui := newTestTUI()
	tui.panes = append(tui.panes, &RemotePane{name: "super", host: "support", alive: true})
	tui.agents.selected = 0

	tui.agentsToggleSuspend()

	if tui.agents.error == "" || !strings.Contains(tui.agents.error, "machine") {
		t.Errorf("remote agent suspend must refuse and say why, got %q", tui.agents.error)
	}
}

func TestAgentsToggleSuspendGroup_ParksIdleSkipsBusy(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI()
	tui.projectRoot = root
	a := testPaneRunning(t, "eng1")
	b := testPaneRunning(t, "eng2")
	b.mu.Lock()
	b.activity = StateRunning // mid-work: must be skipped, truthfully
	b.mu.Unlock()
	tui.panes = append(tui.panes, a, b)
	tui.layoutState.GroupOf = map[string]string{"eng1": "eng", "eng2": "eng"}
	tui.layoutState.Groups = []string{"eng"}
	tui.agents.selected = 0

	tui.agentsToggleSuspendGroup()

	if !a.IsSuspended() {
		t.Error("group suspend did not park the idle member")
	}
	if b.IsSuspended() {
		t.Error("group suspend parked a RUNNING agent — the truthful-refusal rule exists to prevent exactly this")
	}
	if !strings.Contains(tui.agents.error, "eng2") {
		t.Errorf("skip must be reported by name, got %q", tui.agents.error)
	}
}

func TestAgentsToggleSuspendGroup_WakesAllWhenAllParked(t *testing.T) {
	tui := newTestTUI()
	a := NewParkedPane(PaneConfig{Name: "eng1", Command: []string{"sh", "-c", "echo up; sleep 5"}}, 10, 40)
	b := NewParkedPane(PaneConfig{Name: "eng2", Command: []string{"sh", "-c", "echo up; sleep 5"}}, 10, 40)
	tui.panes = append(tui.panes, a, b)
	tui.layoutState.GroupOf = map[string]string{"eng1": "eng", "eng2": "eng"}
	tui.layoutState.Groups = []string{"eng"}
	tui.agents.selected = 0

	tui.agentsToggleSuspendGroup()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		awake := 0
		for _, pv := range tui.panes {
			if np, ok := pv.(*Pane); ok && !np.IsSuspended() {
				awake++
			}
		}
		if awake == 2 {
			for _, pv := range tui.panes {
				if np, ok := pv.(*Pane); ok {
					defer np.Close()
				}
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("S on an all-parked band did not wake both members (modal msg: %q)", tui.agents.error)
}

// ── suspension must cross the window boundary ─────────────────────────
//
// Window 2 renders window 1's agents from RemotePanes fed by agent_status
// broadcasts. Suspension was not on that wire, so a parked agent kept its
// last screen and read as idle in every other window ("window 2 doesn't
// show it as suspended", operator, 2026-08-15). agent_status is the channel
// BUILT for observed agent state — suspended rides it like beads and
// waiting do.
func TestRemotePane_AppliedSuspensionShowsInActivity(t *testing.T) {
	rp := &RemotePane{name: "eng2", host: "window1", alive: true}

	rp.ApplySuspended(true)
	if rp.Activity() != StateSuspended {
		t.Fatalf("suspended remote pane reports %v, want StateSuspended — the viewer window renders this state", rp.Activity())
	}

	rp.ApplySuspended(false)
	if rp.Activity() == StateSuspended {
		t.Fatal("cleared suspension still reports StateSuspended — a woken agent would look parked in other windows forever")
	}
}

func TestApplyAgentStatus_CarriesSuspended(t *testing.T) {
	tui := newTestTUI()
	rp := &RemotePane{name: "eng2", host: "window1", alive: true}
	tui.panes = append(tui.panes, rp)

	tui.applyAgentStatus("eng2", nil, "", WaitingState{}, true)
	if rp.Activity() != StateSuspended {
		t.Fatal("agent_status with suspended=true did not park the remote pane's displayed state")
	}
	tui.applyAgentStatus("eng2", nil, "", WaitingState{}, false)
	if rp.Activity() == StateSuspended {
		t.Fatal("agent_status with suspended=false did not clear the displayed state")
	}
}
