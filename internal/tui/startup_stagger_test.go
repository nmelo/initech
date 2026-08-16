package tui

import (
	"testing"
	"time"
)

// ── ini-ap3i follow-on: launch cost must not scale with fleet size ────
//
// Measured on hover (39 agents, 2026-08-15): one launch booted 39 claude
// processes in a 90ms burst — +17GB RSS, load 3→216, ninety seconds of a
// machine that could not schedule its own TUI. Two features close it:
// persisted suspension (an agent parked at shutdown cold-parks at launch,
// no process) and staggered spawns (everyone else boots in small batches).
// Both ride the same primitive: a pane that exists without a process, woken
// by the pre-existing resumePane machinery.

func TestFleetState_SuspendedRoundTrip(t *testing.T) {
	root := t.TempDir()

	fs, err := LoadFleetState(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.SetSuspended("mktg", true); err != nil {
		t.Fatal(err)
	}
	if err := fs.SetSuspended("seo", true); err != nil {
		t.Fatal(err)
	}
	if err := fs.SetSuspended("seo", false); err != nil {
		t.Fatal(err)
	}

	// A fresh load (next launch) must see exactly what was recorded.
	fs2, err := LoadFleetState(root)
	if err != nil {
		t.Fatal(err)
	}
	if !fs2.IsSuspended("mktg") {
		t.Error("suspension did not survive reload — next launch would boot a parked agent")
	}
	if fs2.IsSuspended("seo") {
		t.Error("cleared suspension survived reload — next launch would park a woken agent")
	}
}

func TestStartupSpawnPlan(t *testing.T) {
	names := []string{"super", "pm", "eng1", "eng2", "eng3", "qa1", "mktg"}
	suspended := map[string]bool{"eng2": true, "mktg": true}

	plan := startupSpawnPlan(names, suspended, 3)

	want := map[string]spawnMode{
		"super": spawnLive, "pm": spawnLive, "eng1": spawnLive, // first 3 non-suspended
		"eng3": spawnStaggered, "qa1": spawnStaggered, // overflow boots later
		"eng2": spawnParked, "mktg": spawnParked, // persisted suspension wins over position
	}
	for i, n := range names {
		if plan[i] != want[n] {
			t.Errorf("%s: mode=%d want %d", n, plan[i], want[n])
		}
	}
}

func TestNewParkedPane_StateAndSafeClose(t *testing.T) {
	p := NewParkedPane(PaneConfig{Name: "mktg"}, 10, 40)

	if !p.IsSuspended() {
		t.Error("parked pane must report suspended — all three wake paths key on it")
	}
	if p.Activity() != StateSuspended {
		t.Errorf("activity=%v want StateSuspended", p.Activity())
	}
	if p.IsAlive() {
		t.Error("parked pane must not report alive — no process exists")
	}
	// Quit-time cleanup closes every pane; a parked one must not panic on
	// its nil process/pty.
	p.Close()
}

func TestResumePane_BootsColdParkedPane(t *testing.T) {
	tui := newTestTUI()
	pane := NewParkedPane(PaneConfig{
		Name:    "mktg",
		Command: []string{"sh", "-c", "echo up; sleep 5"},
	}, 10, 40)
	tui.wireSuspendResume(pane)
	tui.panes = append(tui.panes, pane)

	if err := tui.resumePane(pane, "test"); err != nil {
		t.Fatalf("resume of a cold-parked pane failed: %v", err)
	}

	np, ok := tui.panes[len(tui.panes)-1].(*Pane)
	if !ok || np == pane {
		t.Fatal("resume did not replace the parked pane with a live one")
	}
	defer np.Close()
	if np.IsSuspended() {
		t.Error("resumed pane still reports suspended")
	}
	deadline := time.Now().Add(3 * time.Second)
	for np.LastOutputTime().IsZero() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if np.LastOutputTime().IsZero() {
		t.Error("resumed pane produced no output — process did not boot")
	}
}

func TestResumePane_CopiesIdleWithBeadThreshold(t *testing.T) {
	// The stagger path routes EVERY deferred agent through resumePane, which
	// promoted an existing gap to the main path: the threshold was not copied
	// to the replacement pane, so staggered agents lost idle-with-bead
	// detection.
	tui := newTestTUI()
	pane := NewParkedPane(PaneConfig{
		Name:    "eng1",
		Command: []string{"sh", "-c", "echo up; sleep 5"},
	}, 10, 40)
	pane.idleWithBeadThreshold = 90 * time.Second
	tui.panes = append(tui.panes, pane)

	if err := tui.resumePane(pane, "test"); err != nil {
		t.Fatal(err)
	}
	np := tui.panes[len(tui.panes)-1].(*Pane)
	defer np.Close()
	if np.idleWithBeadThreshold != 90*time.Second {
		t.Errorf("idleWithBeadThreshold not carried across resume: got %v", np.idleWithBeadThreshold)
	}
}

func TestParkPaneRecorded_PersistsSuspension(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI()
	tui.projectRoot = root

	p, err := NewPane(PaneConfig{Name: "seo", Command: []string{"sh", "-c", "sleep 5"}}, 10, 40)
	if err != nil {
		t.Fatal(err)
	}
	tui.panes = append(tui.panes, p)

	tui.parkPaneRecorded(p)

	if !p.IsSuspended() {
		t.Error("pane not parked")
	}
	fs, err := LoadFleetState(root)
	if err != nil {
		t.Fatal(err)
	}
	if !fs.IsSuspended("seo") {
		t.Error("park did not persist — next launch would boot this agent")
	}
}
