package tui

// A restart must not destroy deferred mail (ini-1z6i).
//
// restartPane closed the old Pane and built a fresh one, carrying `protected`
// and nothing else -- so a pane holding messages deferred behind a modal lost
// them all, with no notice to the senders or the recipient. resumePane, the
// sibling respawn path, has carried the queue since ini-g7fl. Reported after
// seven messages sat ~40 minutes behind a stuck latch and vanished on restart.

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

func queuedPane(t *testing.T, n int) *Pane {
	t.Helper()
	p, _ := paneWithPipe(t)
	p.name = "eng2"
	p.alive = true
	for i := 0; i < n; i++ {
		p.EnqueueMessage("message "+string(rune('A'+i)), true)
	}
	if got := p.QueuedMessageCount(); got != n {
		t.Fatalf("setup: queued %d, want %d", got, n)
	}
	return p
}

// TestRestart_CarriesTheDeferredQueue is the bug.
func TestRestart_CarriesTheDeferredQueue(t *testing.T) {
	old := queuedPane(t, 7)
	fresh, _ := paneWithPipe(t)
	fresh.name = "eng2"

	carried := carryQueueForRestart(old, fresh)

	if carried != 7 || fresh.QueuedMessageCount() != 7 {
		t.Fatalf("carried %d, new pane holds %d; want 7 and 7.\n\n"+
			"Every message deferred behind a modal is destroyed by a restart, with no "+
			"notice to the senders or the recipient -- seven were lost this way after "+
			"~40 minutes of waiting.", carried, fresh.QueuedMessageCount())
	}
	if old.QueuedMessageCount() != 0 {
		t.Errorf("old pane still holds %d; the queue must move, not duplicate",
			old.QueuedMessageCount())
	}
}

// TestRestart_CarriedQueueKeepsItsOrder: mail delivered out of order is a
// different defect wearing this one's fix.
func TestRestart_CarriedQueueKeepsItsOrder(t *testing.T) {
	old := queuedPane(t, 5)
	fresh, _ := paneWithPipe(t)

	carryQueueForRestart(old, fresh)

	msgs := fresh.DrainQueue()
	for i, m := range msgs {
		if want := "message " + string(rune('A'+i)); m.Text != want {
			t.Fatalf("position %d holds %q, want %q", i, m.Text, want)
		}
	}
}

// TestRestart_ASpawnFailureKeepsTheMailAndSaysSo is the second discard the fix
// could create: restartPane drains the queue and then returns on a NewPane
// error, so the messages would vanish at exactly that point.
func TestRestart_ASpawnFailureKeepsTheMailAndSaysSo(t *testing.T) {
	old := queuedPane(t, 3)
	logs := captureLogs(t)

	msgs := old.DrainQueue() // as restartPane does, before it learns the spawn failed
	restoreQueueAfterFailedRestart(old, msgs)

	if old.QueuedMessageCount() != 3 {
		t.Fatalf("old pane holds %d after a failed restart, want 3; the mail was drained "+
			"for a pane that never came up and has nowhere else to live",
			old.QueuedMessageCount())
	}
	// Asserts on text ONLY THE WARNING emits. "restart failed" alone is
	// satisfied by EmitEvent's own debug line, which logs the event Detail --
	// so the first version of this cell passed with the warning downgraded to
	// Debug, and a mutant proving it survived.
	if !strings.Contains(logs.String(), "messages kept on the old pane") {
		t.Error("a failed restart holding mail left no WARNING; the operator learns " +
			"nothing at default log level about mail stranded on a dead pane")
	}
}

// TestRestart_MidDrainLosesAtMostTheMessageInFlight is the AC's first edge.
//
// drainModalQueue used to pop the WHOLE queue, and it releases sendMu between
// messages -- so restartPane's lock serialises one send, not the drain, and
// everything already popped was unreachable by the carry. Popping one at a
// time bounds the loss to the single message actually being delivered.
func TestRestart_MidDrainLosesAtMostTheMessageInFlight(t *testing.T) {
	p := queuedPane(t, 6)

	m, ok := p.PopQueuedMessage()
	if !ok || m.Text != "message A" {
		t.Fatalf("pop returned (%q, %v), want the head of the queue", m.Text, ok)
	}
	if left := p.QueuedMessageCount(); left != 5 {
		t.Fatalf("after popping one, %d remain; want 5 -- a drain that pops everything "+
			"puts the whole queue out of the carry's reach", left)
	}

	// Whatever a restart does now, five messages are still where the carry looks.
	fresh, _ := paneWithPipe(t)
	if carryQueueForRestart(p, fresh); fresh.QueuedMessageCount() != 5 {
		t.Errorf("carry recovered %d of the 5 still queued", fresh.QueuedMessageCount())
	}
}

// TestRestart_AnEmptyQueueCarriesNothingAndLogsNothing keeps the common path
// quiet: a restart with no deferred mail must not invent an event.
func TestRestart_AnEmptyQueueCarriesNothingAndLogsNothing(t *testing.T) {
	old, _ := paneWithPipe(t)
	fresh, _ := paneWithPipe(t)
	logs := captureLogs(t)

	if n := carryQueueForRestart(old, fresh); n != 0 {
		t.Errorf("carried %d from an empty queue", n)
	}
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(logs.String(), "carried") {
		t.Errorf("an ordinary restart logged about mail it did not have: %q", logs.String())
	}
}

// TestRestart_TheRealRestartPathCarriesTheMail drives restartPane itself.
//
// Written because a mutant deleting restartPane's call to the carry SURVIVED
// every cell above: they drove the helper, and nothing drove the wiring. A
// tested helper the product forgets to call is the same outage with better
// test output.
func TestRestart_TheRealRestartPathCarriesTheMail(t *testing.T) {
	fp, err := NewPane(PaneConfig{Name: "eng2", Command: []string{"/bin/cat"}}, 24, 80)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	fp.Start()
	t.Cleanup(fp.Close)

	for i := 0; i < 4; i++ {
		fp.EnqueueMessage("deferred "+string(rune('A'+i)), true)
	}

	tui := &TUI{panes: []PaneView{fp}, agentEvents: make(chan AgentEvent, 16), quitCh: make(chan struct{})}
	if err := tui.restartPane(fp); err != nil {
		t.Fatalf("restartPane: %v", err)
	}

	np, ok := tui.panes[0].(*Pane)
	if !ok {
		t.Fatal("pane slot does not hold a local pane after restart")
	}
	t.Cleanup(np.Close)
	if np == fp {
		t.Fatal("restart did not replace the pane; this cell would pass trivially")
	}
	if got := np.QueuedMessageCount(); got != 4 {
		t.Fatalf("the restarted pane holds %d of 4 deferred messages.\n\n"+
			"This is the reported outage: mail deferred behind a modal is destroyed by "+
			"a restart, with no notice to senders or recipient.", got)
	}
}

// TestRestart_DrainModalQueueItselfPopsOneAtATime closes a reachability gap
// found while sampling this bead's own mutations, not asserted by anything
// else here: TestRestart_MidDrainLosesAtMostTheMessageInFlight drives
// PopQueuedMessage directly, never drainModalQueue -- so reverting
// drainModalQueue to its pre-fix shape (msgs := p.DrainQueue(); for _, m
// := range msgs {...}) survives the ENTIRE existing suite, including that
// cell. Verified live: applied that exact revert, every other cell in this
// file still passed. PopQueuedMessage has no other production caller, so a
// regression there is invisible to every test that only drives the
// primitive and not the function that is supposed to call it one message
// at a time.
//
// This drives drainModalQueue itself and takes the SAME sendMu restartPane
// takes, at the point a real concurrent restart would: sendMu can only be
// acquired here once drainModalQueue's first SendText call has released
// it, which is while the drain is sleeping between message 1 and message
// 2. At that instant a correct (pop-one-at-a-time) drain has exactly 2
// messages left; the reverted (pop-everything-up-front) shape has 0.
func TestRestart_DrainModalQueueItselfPopsOneAtATime(t *testing.T) {
	p := queuedPane(t, 3)
	// SendText's stash-on-submit path reaches into p.emu (ini-gd0); the
	// bare queuedPane fixture has none, since most cells here never send
	// through SendText at all -- this one does, so it needs a real target.
	p.emu = vt.NewSafeEmulator(80, 24)
	// The emulator's internal response pipe (DA/DSR queries) needs a
	// reader or a Write into it blocks forever -- the same requirement
	// RemotePane's responseLoop exists for. Without this the drain
	// goroutine below never returns and the test hangs (found live).
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := p.emu.Read(buf); err != nil {
				return
			}
		}
	}()

	go p.drainModalQueue()

	// Wait for evidence the first pop happened (this read does not need
	// sendMu -- QueuedMessageCount only touches p.mu), so the sendMu
	// acquisition below lands after message 1's send, not before it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && p.QueuedMessageCount() == 3 {
		time.Sleep(time.Millisecond)
	}
	if p.QueuedMessageCount() == 3 {
		t.Fatal("drainModalQueue never popped anything within the deadline")
	}

	p.sendMu.Lock()
	remaining := p.QueuedMessageCount()
	p.sendMu.Unlock()

	if remaining == 0 {
		t.Fatal("drainModalQueue had already emptied the queue by the time the first send " +
			"released sendMu -- it popped everything up front instead of one message at a " +
			"time, which is exactly the regression that destroyed mail mid-restart " +
			"(restartPane's own carry takes this same lock at this same point)")
	}
	if remaining != 2 {
		t.Errorf("remaining = %d right after the first send, want 2 (one popped and sent, "+
			"two still queued where a concurrent restart's carry would find them)", remaining)
	}

	// Let the drain finish so nothing leaks past the test.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && p.QueuedMessageCount() > 0 {
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRestart_ARealFreshlySpawnedPaneActuallyDeliversTheCarriedQueue closes
// eng1's own named gap: every cell above proves the queue LANDS on the new
// pane object; none proves the readLoop-triggered drain actually POPS AND
// SENDS it once the new process is genuinely running.
// TestRestart_TheRealRestartPathCarriesTheMail spawns /bin/cat, which
// produces NO output of its own -- so maybeDrainModalQueue's only call site
// (pane.go:510, fired from readLoop on PTY output) can never trigger for
// it, and that cell can only prove the carry landed, never that delivery
// happens. This spawns `sh`, which prints its own prompt unprompted, so
// the drain fires from the real process's real output with nothing here
// calling maybeDrainModalQueue directly -- the claim eng1 flagged as
// "argued from ini-9gvn's drain, not driven in a test."
func TestRestart_ARealFreshlySpawnedPaneActuallyDeliversTheCarriedQueue(t *testing.T) {
	// NoBracketedPaste: true, or SendText's stash-on-submit (ini-gd0/ini-a9d8)
	// sends a real Ctrl+S to `sh` before the message -- which a plain shell's
	// tty sees as XOFF flow control, not a stash gesture, and freezes ALL
	// further output permanently. Found live: the first version of this test
	// used a bare PaneConfig and every assertion below failed against a
	// process that never printed anything past its own startup prompt again.
	fp, err := NewPane(PaneConfig{
		Name: "eng2", Command: []string{"sh"}, NoBracketedPaste: true,
	}, 24, 80)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	fp.Start()
	t.Cleanup(fp.Close)

	const marker = "DELIVERED_MARKER_9X2"
	fp.EnqueueMessage("echo "+marker, true)

	tui := &TUI{panes: []PaneView{fp}, agentEvents: make(chan AgentEvent, 16), quitCh: make(chan struct{})}
	if err := tui.restartPane(fp); err != nil {
		t.Fatalf("restartPane: %v", err)
	}

	np, ok := tui.panes[0].(*Pane)
	if !ok {
		t.Fatal("pane slot does not hold a local pane after restart")
	}
	t.Cleanup(np.Close)

	// Poll rather than sleep-once: this is waiting on a REAL process's own
	// startup timing, not a fixed delay this test controls.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && np.QueuedMessageCount() > 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if got := np.QueuedMessageCount(); got != 0 {
		t.Fatalf("the carried message is still queued after waiting for the restarted "+
			"process's own output to trigger delivery (%d remain) -- the queue reaches the "+
			"new pane but the tick that is supposed to drain it never fires", got)
	}

	// Draining and delivering are different claims -- SendText could pop and
	// discard. Confirm the text actually reached the restarted process.
	if screen := np.emu.Render(); !strings.Contains(screen, marker) {
		t.Fatalf("the carried message drained from the queue but never reached the "+
			"restarted process's screen: %q", screen)
	}
}
