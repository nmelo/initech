//go:build !windows

package tui

// manual_suspend_test.go covers the ini-zffi criteria that need a LIVE PTY
// CHILD: waking a real process, proving a keystroke never reached it, and
// delivering a queued message into it.
//
// '//go:build !windows' because the echo fixture is a Unix shell pipeline
// (`sh -c "printf READY; cat"`). On Windows the child dies at once and
// resumePane correctly reports a dead process, so every wake assertion fails
// for a reason that has nothing to do with the code under test (measured, CI
// run 31804800234). The half that needs no child stays untagged in
// manual_suspend_portable_test.go, so the constraint does not take the
// decision logic off a platform with it (ini-47w).
//
// manual_suspend_test.go covers ini-zffi's acceptance criteria.
//
// The load-bearing one is AC 3/9: a keystroke into a suspended pane WAKES the
// agent and is PROVEN never delivered to it. That is the forged-input class --
// input the agent appears to have received but the operator never aimed at it
// -- so it is asserted against a real child process's echoed output, not
// against a flag the production code sets about itself.

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// echoPaneCfg builds a pane whose child echoes everything typed at it, so a
// delivered keystroke is OBSERVABLE in the pane's own screen and an undelivered
// one is observably absent.
func echoPaneCfg(name string) PaneConfig {
	return PaneConfig{
		Name: name,
		// Prints on startup, THEN echoes stdin verbatim.
		//
		// Both halves are load-bearing. The echo makes "did this byte reach the
		// agent" a question about observable bytes rather than about Claude's
		// behavior (the real-workload rule's carve-out: the fate under test is
		// PTY delivery, not agent semantics). The startup PRINT is what makes
		// the probe able to fail at all: resumePane's waitForInit polls for
		// post-respawn output, so a silent child burns the full 30s window
		// (measured by eng2 in ini-9imx) and the wake would not complete inside
		// the test -- a delivered keystroke could not arrive either way, and
		// the absence of "Q" would prove nothing.
		Command: []string{"sh", "-c", "printf 'READY\n'; cat"},
	}
}

// newEchoPane starts an echo-backed pane wired into a minimal TUI.
func newEchoPane(t *testing.T, name string) (*TUI, *Pane) {
	t.Helper()
	p, err := NewPane(echoPaneCfg(name), 24, 80)
	if err != nil {
		t.Fatalf("start echo pane: %v", err)
	}
	// Start() launches readLoop and responseLoop. Without it nothing drains
	// the emulator's response pipe, and the first SendKey blocks forever
	// writing into it -- the trap recorded in this repo's emulator-test notes.
	p.Start()
	t.Cleanup(func() { p.Close() })

	// A simulation screen with a real size: resumePane calls applyLayout as
	// part of swapping the respawned pane in, and a nil screen reports 0x0
	// (this repo's emulator-test note). Without it the wake never completes
	// and every assertion after it measures a wake that did not happen.
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("init simulation screen: %v", err)
	}
	sim.SetSize(120, 40)
	t.Cleanup(sim.Fini)

	tui := &TUI{
		screen:      sim,
		panes:       []PaneView{p},
		layoutState: LayoutState{Focused: agentKey(p)},
		agentEvents: make(chan AgentEvent, 16),
		quitCh:      make(chan struct{}),
		// ipcCh left nil on purpose: runOnMain then executes inline on the
		// calling goroutine, which is what a test wants -- no main loop to
		// pump, and the same code path production takes.
	}
	return tui, p
}

// livePane returns the pane the TUI currently holds for name. resumePane
// REPLACES the pane object on wake, so a test holding the pre-suspend pointer
// would be inspecting a corpse and would see no delivered keystroke no matter
// what the code did.
func livePane(t *testing.T, tui *TUI, name string) *Pane {
	t.Helper()
	p, ok := tui.findLocalPane(name)
	if !ok {
		t.Fatalf("no live pane named %q after the wake", name)
	}
	return p
}

// paneScreenContains reports whether the pane's emulator screen shows s.
func paneScreenContains(p *Pane, s string) bool {
	for y := 0; y < 24; y++ {
		if strings.Contains(p.emu.RowText(y, 80), s) {
			return true
		}
	}
	return false
}

// TestSuspendedPaneKeystrokeIsConsumedNotDelivered is AC 3 and half of AC 9.
//
// WHICH MUTANT THIS KILLS, stated because the two obvious ones are not equal.
// Deleting the `return` so the wake ALSO falls through to SendKey does NOT
// turn this test red -- and that is a property of the system, not a weakness
// here: at keystroke time the pane is suspended, so its PTY is already closed,
// and the write is discarded by the closed descriptor (eng2's ini-9imx PATH 2
// finding -- Close() never nils ptmx, so the primitive's nil guard does not
// fire and the error is dropped). Delivery is impossible on that path
// regardless of the guard, so the trivial mutant is behaviourally equivalent.
//
// The mutant this DOES kill is the realistic forgery: wake, then hand the
// gesture to the RESPAWNED process. Verified -- that mutation turns this test
// red. That is the shape a naive implementation actually takes ("wake it, then
// pass the key through"), and it is the one the spec forbids.
//
// If ini-9imx's proposal to nil ptmx on Close lands, the trivial mutant stops
// being masked and this test would catch both.
func TestSuspendedPaneKeystrokeIsConsumedNotDelivered(t *testing.T) {
	tui, p := newEchoPane(t, "eng1")

	// Sanity FIRST: the probe must be able to see a delivered key at all, or
	// its silence later proves nothing. This is the positive control.
	p.SendKey(tcell.NewEventKey(tcell.KeyRune, 'Z', tcell.ModNone))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !paneScreenContains(p, "Z") {
		time.Sleep(20 * time.Millisecond)
	}
	if !paneScreenContains(p, "Z") {
		t.Fatal("the echo probe cannot observe a DELIVERED keystroke, so its later silence " +
			"would prove nothing; fix the probe before trusting this test")
	}

	// Park it through the shared primitive, exactly as auto-suspend does.
	parkPaneSuspended(p)
	if !p.IsSuspended() {
		t.Fatal("pane did not enter the suspended state")
	}

	// The wake gesture: a printable key that would be unmistakable if echoed.
	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'Q', tcell.ModNone))

	// The keystroke must have triggered a wake, and the wake must COMPLETE --
	// otherwise the delivery check below runs at a moment when a delivered key
	// could not have arrived either, and its silence would prove nothing.
	// Poll the TUI's CURRENT pane, never the pre-suspend pointer: resumePane
	// replaces the pane object, so the old one stays suspended forever and a
	// loop watching it would time out no matter how well the wake worked.
	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		cur, ok := tui.findLocalPane("eng1")
		if ok && !cur.IsSuspended() && !cur.IsWaking() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cur, ok := tui.findLocalPane("eng1"); !ok || cur.IsSuspended() {
		t.Fatal("a keystroke into a suspended pane did not wake it")
	}

	// The respawned child announces itself, which is how we know the new
	// process is live and listening -- i.e. that a forged keystroke WOULD be
	// observable if one had been sent.
	live := livePane(t, tui, p.Name())
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !paneScreenContains(live, "READY") {
		time.Sleep(20 * time.Millisecond)
	}
	if !paneScreenContains(live, "READY") {
		t.Fatal("the respawned child never announced itself, so this test cannot tell a " +
			"consumed keystroke from one that simply had nowhere to land")
	}

	// ...and the gesture must NOT have been delivered to it.
	time.Sleep(300 * time.Millisecond)
	if paneScreenContains(live, "Q") {
		t.Error(`THE WAKE KEYSTROKE WAS DELIVERED TO THE AGENT.

The gesture spends itself on the state change (ini-zffi, same principle as
ini-pzx0's focus-click). Delivering it too forges input the operator never
aimed at the agent: they pressed a key at a parked pane to bring it back, not
to type into whatever prompt the respawned process happens to show.`)
	}
}

// TestRunningPaneKeystrokeIsStillDelivered is the OTHER direction of AC 9: the
// consume must be scoped to suspended panes. A guard that swallowed keys to a
// running pane would break all typing while leaving the test above green.
func TestRunningPaneKeystrokeIsStillDelivered(t *testing.T) {
	tui, p := newEchoPane(t, "eng1")

	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'K', tcell.ModNone))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !paneScreenContains(p, "K") {
		time.Sleep(20 * time.Millisecond)
	}
	if !paneScreenContains(p, "K") {
		t.Error("a keystroke to a RUNNING pane was swallowed; the wake consume is over-broad " +
			"and has broken ordinary typing")
	}
}

// TestSuspendedPaneClickAndWheelDoNotWake is AC 4, asserted rather than
// inferred from the key path's shape. Click focuses (ini-pzx0) and wheel
// scrolls scrollback; neither is the wake gesture, and a mouse path that
// happened to route through the same handler would wake on a stray scroll.
func TestSuspendedPaneClickAndWheelDoNotWake(t *testing.T) {
	tui, p := newEchoPane(t, "eng1")
	parkPaneSuspended(p)

	for _, ev := range []*tcell.EventMouse{
		tcell.NewEventMouse(1, 1, tcell.Button1, tcell.ModNone),   // click
		tcell.NewEventMouse(1, 1, tcell.WheelUp, tcell.ModNone),   // wheel up
		tcell.NewEventMouse(1, 1, tcell.WheelDown, tcell.ModNone), // wheel down
	} {
		tui.handleMouse(ev)
	}
	time.Sleep(200 * time.Millisecond)

	cur, ok := tui.findLocalPane("eng1")
	if !ok {
		t.Fatal("pane vanished")
	}
	if !cur.IsSuspended() || cur.IsWaking() {
		t.Error("a click or wheel event woke a suspended pane; only a KEYSTROKE is the wake " +
			"gesture, or an operator scrolling back through a parked pane's history restarts it")
	}
}

// TestManualAndAutoSuspensionAreIndistinguishable is AC 5. The two origins must
// produce the same state and wake the same way; the pane deliberately carries
// no origin flag, and this pins that there is nothing to tell apart.
func TestManualAndAutoSuspensionAreIndistinguishable(t *testing.T) {
	// Manual origin: the operator's command.
	manualTUI, manualPane := newEchoPane(t, "eng1")
	manualPane.mu.Lock()
	manualPane.activity = StateIdle
	manualPane.mu.Unlock()
	if err := manualTUI.suspendAgent("eng1"); err != nil {
		t.Fatalf("manual suspend: %v", err)
	}

	// Auto origin: the pressure loop's own primitive.
	autoTUI, autoPane := newEchoPane(t, "eng1")
	parkPaneSuspended(autoPane)

	for _, c := range []struct {
		origin string
		pane   *Pane
	}{{"manual", manualPane}, {"auto", autoPane}} {
		if !c.pane.IsSuspended() {
			t.Errorf("%s-suspended pane: suspended flag not set", c.origin)
		}
		if got := c.pane.Activity(); got != StateSuspended {
			t.Errorf("%s-suspended pane: activity = %v, want StateSuspended", c.origin, got)
		}
		if c.pane.IsAlive() {
			t.Errorf("%s-suspended pane: process still alive; the memory was not freed", c.origin)
		}
	}

	// And both wake by the same command path.
	for _, c := range []struct {
		origin string
		tui    *TUI
	}{{"manual", manualTUI}, {"auto", autoTUI}} {
		if _, err := c.tui.resumeAgent("eng1"); err != nil {
			t.Errorf("%s-suspended agent could not be resumed: %v -- the wake paths must not "+
				"distinguish the two origins", c.origin, err)
		}
	}
}

// TestResumeDeliversQueuedMessagesAndNamesTheCount is AC 1's mechanism: the
// operator is told what their action actually delivered, not just "resumed".
func TestResumeDeliversQueuedMessagesAndNamesTheCount(t *testing.T) {
	tui, p := newEchoPane(t, "eng1")
	parkPaneSuspended(p)

	// Two messages arrive while it is parked.
	p.mu.Lock()
	p.messageQueue = append(p.messageQueue,
		QueuedMessage{Text: "first", Enter: true},
		QueuedMessage{Text: "second", Enter: true})
	p.mu.Unlock()

	delivered, err := tui.resumeAgent("eng1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if delivered != 2 {
		t.Errorf("resume reported %d delivered, want 2 -- the count is read BEFORE the wake "+
			"because resumePane drains the queue, and reporting zero would understate what "+
			"the operator just caused", delivered)
	}
}
