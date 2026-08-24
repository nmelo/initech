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
	if !strings.Contains(logs.String(), "restart failed") {
		t.Error("a failed restart holding mail left no record at default level")
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
