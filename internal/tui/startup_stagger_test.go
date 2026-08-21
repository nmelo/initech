package tui

import (
	"strings"
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

// ── ini-hbj4: wake-on-message must not deliver into boot ──────────────
//
// Live-measured (operator-requested verification, 2026-08-21): a control
// send to a live pane delivered; the wake path lost the message 2/2 —
// resumePane drained right after waitForInit, which fires on FIRST output,
// while Claude Code with a large --continue transcript is still booting and
// flushes stdin at raw-mode entry. The fixture reproduces that shape
// faithfully (real-workload doctrine): boot chatter long enough to make
// first-output readiness a lie, an explicit stdin flush at the readiness
// boundary, then a child that echoes what it reads.
func TestResumePane_QueuedMessageSurvivesSlowBoot(t *testing.T) {
	tui := newTestTUI()
	// stty -echo first: with tty echo on, injected bytes appear on screen the
	// moment they are WRITTEN, delivered or not — the instrument could not see
	// the loss (caught interrogating this test's own first green). Claude runs
	// raw/no-echo, so no-echo is also the faithful fidelity.
	boot := `stty -echo
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do echo booting-$i; sleep 0.1; done
while read -t 1 -r junk; do :; done
cat`
	parked := NewParkedPane(PaneConfig{Name: "eng9", Command: []string{"bash", "-c", boot}}, 20, 60)
	tui.panes = append(tui.panes, parked)
	parked.EnqueueMessage("PROBE-SURVIVES-BOOT", true)

	if err := tui.resumePane(parked, "test"); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	np := tui.panes[0].(*Pane)
	defer np.Close()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		np.renderMu.Lock()
		text := emulatorBottomText(np.emu, np.emu.Height())
		np.renderMu.Unlock()
		if strings.Contains(text, "PROBE-SURVIVES-BOOT") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("queued message never reached the child — it was delivered into boot and flushed, " +
		"the silent-loss-through-the-recovery-path shape g7fl exists to end")
}

// The twin of the slow-boot cell, and the one that would have caught round 1
// in the FAST suite instead of an env-gated rig CI does not run (shipper's
// 9IMX bisect, ini-hbj4 round 2): a child that produces NO output has no boot
// to settle, so the quiescence gate must not apply to it. Round 1 waited the
// full 15s cap on silent children and pushed the auto-resume path past the
// suspend-guard rig's deadline. Asserted as a DEADLINE, since the defect was
// purely one of elapsed time — delivery always did happen, just too late.
func TestResumePane_SilentChildDeliversPromptly(t *testing.T) {
	// Costs waitForInit's full 30s: a mute child never produces the first byte
	// that ends it. That timeout is PRE-EXISTING and out of this bead's scope
	// — the assertion below is scoped to the gate's own contribution. Excluded
	// from -short so the fast suite stays fast; it runs in make test-full and
	// in CI's full-suite leg, which is already a wider net than the env-gated
	// rig that caught round 1.
	if testing.Short() {
		t.Skip("30s: waitForInit's timeout on a mute child; runs in the full suite")
	}
	tui := newTestTUI()
	// Mute until spoken to, then echo — the rig fixture's shape exactly.
	parked := NewParkedPane(PaneConfig{
		Name:    "eng9",
		Command: []string{"sh", "-c", `stty -echo; while read l; do echo "GOT:$l"; done`},
	}, 20, 60)
	tui.panes = append(tui.panes, parked)
	parked.EnqueueMessage("PROBE-SILENT", true)

	start := time.Now()
	if err := tui.resumePane(parked, "test"); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	np := tui.panes[0].(*Pane)
	defer np.Close()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		np.renderMu.Lock()
		text := emulatorBottomText(np.emu, np.emu.Height())
		np.renderMu.Unlock()
		if strings.Contains(text, "GOT:PROBE-SILENT") {
			// Scoped to the GATE's contribution: waitForInit's 30s is
			// pre-existing and unavoidable for a mute child, so the budget is
			// that plus a small margin. Round 1 (gate applied to silent
			// children) landed at ~45s and reds here; the fix lands at ~30s.
			budget := resumeTimeout + 5*time.Second
			if elapsed := time.Since(start); elapsed > budget {
				t.Fatalf("silent child delivered after %v (budget %v) — it burned the quiescence "+
					"cap it has no boot output to satisfy", elapsed, budget)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("silent child never received its queued message")
}
