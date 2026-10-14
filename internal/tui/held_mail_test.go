package tui

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// Tests for ini-1898: mail held for a dialog is deferred once, never spins,
// and is reported -- not retried forever, not dropped -- when it cannot be
// delivered.

// heldMailPane is a pane with a writable PTY (a pipe), an event channel and
// the startup gate already settled, so sends reach the modal guard.
func heldMailPane(t *testing.T, name string) (*Pane, chan AgentEvent, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	go io.Copy(io.Discard, r) //nolint:errcheck // keep the pipe from filling
	events := make(chan AgentEvent, 64)
	p := testPane(name)
	p.ptmx = &filePty{w}
	p.eventCh = events
	p.startupSettled = true
	// Real panes run responseLoop to drain the emulator's replies; without a
	// reader the send path's keystrokes (the stash) block forever.
	// The reader and the cleanup hold THIS emulator, not the field: a test
	// may swap p.emu for a larger screen afterwards.
	emu := p.emu
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { emu.Close() })
	return p, events, r
}

func countLines(buf *safeLogBuffer, needle string) int {
	return strings.Count(buf.String(), needle)
}

func undelivered(events chan AgentEvent) []AgentEvent {
	var out []AgentEvent
	for {
		select {
		case ev := <-events:
			if ev.Type == EventAgentStalled && strings.HasPrefix(ev.Detail, undeliveredSubmitPrefix) {
				out = append(out, ev)
			}
		default:
			return out
		}
	}
}

// The field loop as a cell. A drain starts while no dialog is drawn (Claude
// repainting at boot), and by the time its send runs the dialog is up -- and
// stays up, as a dead pane's does. Before the fix the drain re-queued the
// message at the back, slept 500 ms, popped it again and logged a fresh
// "deferred" line every pass, forever. Now it puts the message back at the
// FRONT with its original time and the drain returns.
func TestHeldMail_DrainStopsWhenTheDialogComesBackInsteadOfSpinning(t *testing.T) {
	logs := captureLogs(t)
	p, _, _ := heldMailPane(t, "eng3")
	// Not settled and freshly started: the send waits for output to go quiet
	// (resumeQuiesceStable), which is the window the dialog opens in.
	p.startupSettled = false
	p.startedAt = time.Now()
	p.lastOutputTime = time.Now()
	held := time.Now().Add(-time.Minute)
	p.enqueue(QueuedMessage{Text: "[from super] merge order", Enter: true, Time: held, HeldForDialog: true})

	done := make(chan struct{})
	go func() { p.drainModalQueue(); close(done) }()
	time.Sleep(100 * time.Millisecond) // the drain has popped and is in the startup wait
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("the drain is still running 5 s after the dialog opened: it is spinning (%d deferral lines)",
			countLines(logs, "deferred: modal open"))
	}
	if n := countLines(logs, "deferred: modal open"); n != 0 {
		t.Errorf("%d new 'deferred' Info lines for a message already held; want 0 (logged once, when first held)", n)
	}
	if q := p.QueuedMessageCount(); q != 1 {
		t.Fatalf("queue holds %d, want the one message back", q)
	}
	p.mu.Lock()
	m := p.messageQueue[0]
	p.mu.Unlock()
	if !m.Time.Equal(held) || !m.HeldForDialog {
		t.Errorf("re-held message = %+v; want its original time %v and the held mark", m, held)
	}
}

// AC1: a pane that dies holding mail reports each message exactly once,
// within one maintenance tick, and then nothing more -- 60 further ticks
// produce no event and no deferral line.
func TestHeldMail_DeadPaneReportsEachMessageOnceThenGoesQuiet(t *testing.T) {
	logs := captureLogs(t)
	tui := newTestTUI()
	p, events, _ := heldMailPane(t, "eng3")
	tui.panes = []PaneView{p}
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()
	p.SendText("[from super] one", true)
	p.SendText("[from super] two", true)
	if n := countLines(logs, "deferred: modal open"); n != 2 {
		t.Fatalf("precondition: %d deferral lines for two sends, want 2", n)
	}
	undelivered(events) // discard the deferral events

	p.mu.Lock()
	p.alive = false // the child exited; liveness has seen EOF
	p.mu.Unlock()
	now := time.Now()
	tui.modalMaintenance(now)

	got := undelivered(events)
	if len(got) != 2 {
		t.Fatalf("%d NOT delivered events after the pane died, want one per held message (2): %+v", len(got), got)
	}
	for _, ev := range got {
		if !strings.Contains(ev.Detail, "process exited") || !strings.Contains(ev.Detail, "re-send it: [from super]") {
			t.Errorf("report does not say why or what: %q", ev.Detail)
		}
	}
	if n := countLines(logs, "held message NOT DELIVERED"); n != 2 {
		t.Errorf("%d NOT DELIVERED Info lines, want 2", n)
	}
	if q := p.QueuedMessageCount(); q != 0 {
		t.Errorf("queue still holds %d after reporting", q)
	}
	before := countLines(logs, "deferred: modal open")
	for i := 1; i <= 60; i++ {
		tui.modalMaintenance(now.Add(time.Duration(i) * time.Second))
	}
	time.Sleep(100 * time.Millisecond)
	if extra := undelivered(events); len(extra) != 0 {
		t.Errorf("%d more reports over 60 ticks; want none", len(extra))
	}
	if n := countLines(logs, "deferred: modal open") - before; n != 0 {
		t.Errorf("%d deferral lines over 60 ticks on a dead pane; want 0", n)
	}
}

// A suspended pane is not dead: its queue is resume mail and stays.
func TestHeldMail_SuspendedPaneKeepsItsQueue(t *testing.T) {
	tui := newTestTUI()
	p, events, _ := heldMailPane(t, "eng3")
	tui.panes = []PaneView{p}
	p.EnqueueMessage("wake up and do this", true)
	p.mu.Lock()
	p.alive, p.suspended = false, true
	p.mu.Unlock()
	tui.modalMaintenance(time.Now())
	if len(undelivered(events)) != 0 || p.QueuedMessageCount() != 1 {
		t.Error("a suspended pane's resume mail was reported as undeliverable")
	}
}

// AC4: on a live pane whose dialog never closes, a held message is reported
// at the deadline, with how long it waited -- not a second before.
func TestHeldMail_DeadlineReportsTheWaitOnALivePane(t *testing.T) {
	tui := newTestTUI()
	p, events, _ := heldMailPane(t, "eng3")
	tui.panes = []PaneView{p}
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()
	start := time.Now()
	p.enqueue(QueuedMessage{Text: "[from pm] notes pinned", Enter: true, Time: start, HeldForDialog: true})
	// Mail queued for suspension reasons is not on the dialog clock.
	p.enqueue(QueuedMessage{Text: "not held", Enter: true, Time: start})

	tui.modalMaintenance(start.Add(heldMessageDeadline - time.Second))
	if got := undelivered(events); len(got) != 0 {
		t.Fatalf("reported %d message(s) a second before the deadline", len(got))
	}
	tui.modalMaintenance(start.Add(heldMessageDeadline))
	got := undelivered(events)
	if len(got) != 1 {
		t.Fatalf("%d reports at the deadline, want 1: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Detail, "held 10m0s") || !strings.Contains(got[0].Detail, "notes pinned") {
		t.Errorf("report does not carry the wait and the message: %q", got[0].Detail)
	}
	if q := p.QueuedMessageCount(); q != 1 {
		t.Errorf("queue holds %d, want only the un-held message left", q)
	}
}

// AC3: a message deferred and then delivered when the dialog closes leaves
// exactly two Info lines, one for each event.
func TestHeldMail_DeferredThenDeliveredIsTwoInfoLines(t *testing.T) {
	logs := captureLogs(t)
	p, _, _ := heldMailPane(t, "eng3")
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()
	p.SendText("[from super] go", true)
	p.mu.Lock()
	p.dialogOpen = false // the operator answered the dialog
	p.mu.Unlock()
	p.drainModalQueue()

	info := 0
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "level=INFO") && strings.Contains(line, "pane=eng3") &&
			(strings.Contains(line, "deferred: modal open") || strings.Contains(line, "held message delivered")) {
			info++
		}
	}
	if info != 2 {
		t.Errorf("%d Info lines for one deferred-then-delivered message, want 2:\n%s", info, logs.String())
	}
	if p.QueuedMessageCount() != 0 {
		t.Error("message still queued after the dialog closed and the drain ran")
	}
}

// AC2, remove: mail a removed pane still holds is reported, not dropped with
// the pane.
func TestHeldMail_RemovingAPaneReportsItsMail(t *testing.T) {
	tui := newTestTUI()
	tui.agentEvents = make(chan AgentEvent, 16)
	p, events, _ := heldMailPane(t, "eng3")
	keep := testPane("super")
	tui.panes = []PaneView{keep, p}
	p.enqueueHeld("[from super] last words", true)
	if err := tui.removePane("eng3"); err != nil {
		t.Fatal(err)
	}
	got := undelivered(events)
	if len(got) != 1 || !strings.Contains(got[0].Detail, "removed") {
		t.Errorf("reports after remove = %+v, want one naming the removal", got)
	}
}

// AC2, restart: the carried queue keeps each message's held mark and time,
// so the deadline keeps counting from when it was first held.
func TestHeldMail_RestartCarryKeepsTheHeldClock(t *testing.T) {
	old, _, _ := heldMailPane(t, "eng3")
	fresh := testPane("eng3")
	held := time.Now().Add(-7 * time.Minute)
	old.enqueue(QueuedMessage{Text: "carried", Enter: true, Time: held, HeldForDialog: true})
	if n := carryQueueForRestart(old, fresh); n != 1 {
		t.Fatalf("carried %d, want 1", n)
	}
	fresh.mu.Lock()
	m := fresh.messageQueue[0]
	fresh.mu.Unlock()
	if !m.HeldForDialog || !m.Time.Equal(held) {
		t.Errorf("carried message = %+v; the held mark and original time must survive a restart", m)
	}
}
