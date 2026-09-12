// resume_race_test.go reproduces qa3's two-path resume race (ini-x23q): the
// IPC handler and the SendText callback both call resumePane for one
// suspended pane; the loser used to re-check a STALE object's suspended
// flag, spawn a second process, fail "pane not found in list", and strand
// whatever it had queued. Real sh panes: the claim is about a process swap.
package tui

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// raceFixture is a suspended sh pane inside a TUI whose config builder counts
// spawns, with the resume callback wired to RECORD its error (the production
// wiring only logs it).
type raceFixture struct {
	tui    *TUI
	old    *Pane
	spawns int
	mu     sync.Mutex
	cbErrs []error
}

func newResumeRaceFixture(t *testing.T) *raceFixture {
	t.Helper()
	old := newEmuPane("shipper", 80, 24)
	old.cfg = PaneConfig{Name: "shipper", Command: []string{"sh"}, NoBracketedPaste: true}
	old.suspended = true
	old.activity = StateSuspended
	f := &raceFixture{old: old}
	f.tui = &TUI{
		panes:       toPaneViews([]*Pane{old}),
		quitCh:      make(chan struct{}),
		agentEvents: make(chan AgentEvent, 64),
	}
	f.tui.paneConfigBuilder = func(name string) (PaneConfig, error) {
		f.mu.Lock()
		f.spawns++
		f.mu.Unlock()
		return old.cfg, nil
	}
	// The production callback shape (wireSuspendResume), but capturing the
	// error instead of only logging it.
	old.SetOnSuspendedMessage(func(p *Pane) {
		err := f.tui.resumePane(p, "queued message")
		f.mu.Lock()
		f.cbErrs = append(f.cbErrs, err)
		f.mu.Unlock()
	})
	t.Cleanup(func() {
		for _, pv := range f.tui.panes {
			if lp, ok := pv.(*Pane); ok && lp.IsAlive() {
				lp.Close()
			}
		}
	})
	return f
}

func (f *raceFixture) livePane(t *testing.T) *Pane {
	t.Helper()
	np, ok := f.tui.panes[0].(*Pane)
	if !ok {
		t.Fatal("pane slot does not hold a local pane")
	}
	return np
}

func waitForScreen(t *testing.T, p *Pane, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(p.emu.Render(), want) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// qa3's race, reproduced: A is the IPC shape, B arrives on the same stale
// object while A's resume is in flight.
func TestResumePane_TwoPathsForOnePane_OneResumeBothDelivered(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-process test in short mode")
	}
	f := newResumeRaceFixture(t)

	var errA error
	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		f.old.EnqueueMessage("echo A_MARKER_71", true)
		errA = f.tui.resumePane(f.old, "incoming message")
	}()
	time.Sleep(50 * time.Millisecond)        // A is inside its respawn; the old object is about to be stale.
	f.old.SendText("echo B_MARKER_72", true) // queues on the STALE object and fires the callback
	<-doneA

	if errA != nil {
		t.Fatalf("winner failed: %v", errA)
	}
	f.mu.Lock()
	cbErrs := append([]error(nil), f.cbErrs...)
	spawns := f.spawns
	f.mu.Unlock()
	if len(cbErrs) != 1 {
		t.Fatalf("callback fired %d times, want 1", len(cbErrs))
	}
	if cbErrs[0] != nil {
		t.Errorf("loser failed instead of handing over: %v", cbErrs[0])
	}
	if spawns != 1 {
		t.Errorf("spawns = %d, want exactly 1 resume for one pane", spawns)
	}
	np := f.livePane(t)
	if np == f.old || !np.IsAlive() {
		t.Fatalf("t.panes does not hold a live successor (same=%v alive=%v)", np == f.old, np.IsAlive())
	}
	if !waitForScreen(t, np, "A_MARKER_71", 5*time.Second) {
		t.Errorf("winner's message never reached the live pane:\n%s", np.emu.Render())
	}
	if !waitForScreen(t, np, "B_MARKER_72", 5*time.Second) {
		t.Errorf("loser's message stranded on the stale object (queued there: %d):\n%s", f.old.QueuedMessageCount(), np.emu.Render())
	}
}

// A trigger that shows up AFTER the swap completed, still holding the old
// object (the window pump, forward-send, a late IPC lookup) must deliver into
// the live pane, not respawn or fail.
func TestResumePane_StalePointerAfterSwap_ForwardsIntoLivePane(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-process test in short mode")
	}
	f := newResumeRaceFixture(t)
	if err := f.tui.resumePane(f.old, "first"); err != nil {
		t.Fatalf("first resume: %v", err)
	}
	np := f.livePane(t)

	f.old.EnqueueMessage("echo LATE_MARKER_73", true) // mail landing on the dead object
	err := f.tui.resumePane(f.old, "late trigger")
	if err != nil {
		t.Fatalf("late trigger on a stale object failed: %v (want a hand-over, not %q)", err, "pane not found in list")
	}
	f.mu.Lock()
	spawns := f.spawns
	f.mu.Unlock()
	if spawns != 1 {
		t.Errorf("spawns = %d after a stale-pointer trigger, want 1 (no second process)", spawns)
	}
	if f.tui.panes[0].(*Pane) != np {
		t.Error("the live pane was replaced by the stale trigger")
	}
	if !waitForScreen(t, np, "LATE_MARKER_73", 5*time.Second) {
		t.Errorf("late message never reached the live pane:\n%s", np.emu.Render())
	}
	if f.old.QueuedMessageCount() != 0 {
		t.Errorf("%d message(s) still queued on the dead object", f.old.QueuedMessageCount())
	}
}

// The genuine failure keeps its name and keeps the mail: a pane that was
// REMOVED from the fleet cannot be resumed, and what was queued on it is not
// dropped by the attempt.
func TestResumePane_RemovedPane_KeepsQueueAndFailsHonestly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-process test in short mode")
	}
	f := newResumeRaceFixture(t)
	f.tui.panes = nil // removed from the fleet
	f.old.EnqueueMessage("echo GONE_74", true)
	err := f.tui.resumePane(f.old, "orphan")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want a not-found failure", err)
	}
	if errors.Is(err, nil) || f.old.QueuedMessageCount() != 1 {
		t.Errorf("queued = %d after a failed resume, want 1 (the attempt must not drop mail)", f.old.QueuedMessageCount())
	}
}
