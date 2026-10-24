package tui

import (
	"strings"
	"testing"
	"time"
)

// ini-ytxc: the one-time Enter resend is checked against an open dialog at
// the moment it is pressed, as a withheld submit is (deliverWithheldSubmit).
// The watch's last sample excludes a dialog it can see, but it is up to one
// poll old: a permission prompt opening after it would take the second Enter
// and select its highlighted option.

// ytxcHeld is a pane whose box keeps our body after the Enter, so the watch
// ends NotTaken and the resend is due. dialogAtRetry latches a dialog from the
// first sample of the watch, so it is up at the moment of the resend however
// the samples are paced. It does not change the verdict: the samples prove our
// body by its text, which a latched dialog does not hide, so the watch still
// ends NotTaken and the resend is still due; what the dialog decides is only
// the guard at the moment of pressing. (It was latched only once the window
// had passed by the clock, and a slow sample under the race detector could
// end the watch before that latch: rigs 40096953732.)
func ytxcHeld(t *testing.T, dialogAtRetry bool) (*Pane, func() int32, <-chan AgentEvent) {
	return ytxcHeldPaced(t, dialogAtRetry, false)
}

// ytxcHeldPaced is ytxcHeld with an optional slow sample at the window's edge:
// one sample that starts just before the window ends and finishes after the
// watch's deadline, as a sample can under the race detector on a loaded CI
// runner (rigs 40096953732).
func ytxcHeldPaced(t *testing.T, dialogAtRetry, slowEdge bool) (*Pane, func() int32, <-chan AgentEvent) {
	t.Helper()
	window := 200 * time.Millisecond
	nma5Window(t, window)
	p, submits, events := proofPane(t)
	nma5Paint(p, "❯ "+nma5Body)
	n := nma5Enters(t, submits, func(int) {})
	start := time.Now()
	old := submitSampleMidpoint
	slept := false
	submitSampleMidpoint = func() {
		if slowEdge && !slept && time.Since(start) >= window-60*time.Millisecond {
			slept = true
			time.Sleep(120 * time.Millisecond)
			return
		}
		if dialogAtRetry {
			p.latchDialogOpen("Claude needs your permission")
		}
	}
	t.Cleanup(func() { submitSampleMidpoint = old })
	return p, n.Load, events
}

// Criterion 1: a dialog up at the moment of the resend gets no Enter, and the
// sender is told why.
func TestRetryDialog_NoSecondEnterIntoADialog(t *testing.T) {
	logs := captureLogs(t)
	p, enters, events := ytxcHeld(t, true)
	sendSubmitKey(p.emu, p.submitKey)
	confirmAndRetrySubmit(p, newPendingSubmit(nma5Body, "bracketed", ""))
	time.Sleep(100 * time.Millisecond) // let any Enter reach the counter
	if got := enters(); got != 1 {
		t.Fatalf("%d Enters written, want 1: the resend was pressed into an open dialog", got)
	}
	ev, got := nma5Undelivered(events, time.Second)
	if !got || !strings.HasPrefix(ev.Detail, unconfirmedSubmitPrefix) || !strings.Contains(ev.Detail, "dialog open at the retry") {
		t.Fatalf("reported %q (got=%v), want could not confirm, dialog open at the retry", ev.Detail, got)
	}
	if !strings.Contains(logs.String(), "submit retry withheld: dialog open") {
		t.Error("the withheld resend left no log line")
	}
	// Criterion 3: the inbox entry is derived from the same event and keeps
	// the reason (the toast and the event log show the event as it is).
	if r := inboxEventReason(ev.Detail); !strings.Contains(r, "dialog open at the retry") {
		t.Errorf("inbox reason %q, want it to name the dialog", r)
	}
}

// Criterion 2, the positive twin: the same pane with no dialog gets its one
// resend, exactly as before, and ends NOT delivered.
func TestRetryDialog_WithoutADialogTheResendFiresAsBefore(t *testing.T) {
	p, enters, events := ytxcHeld(t, false)
	sendSubmitKey(p.emu, p.submitKey)
	confirmAndRetrySubmit(p, newPendingSubmit(nma5Body, "bracketed", ""))
	time.Sleep(100 * time.Millisecond)
	if got := enters(); got != 2 {
		t.Fatalf("%d Enters written, want 2: the Enter and its one resend", got)
	}
	ev, got := nma5Undelivered(events, time.Second)
	if !got || !strings.HasPrefix(ev.Detail, undeliveredSubmitPrefix) || !strings.Contains(ev.Detail, "Enter not taken twice") {
		t.Fatalf("reported %q (got=%v), want NOT delivered after the resend", ev.Detail, got)
	}
}

// Feed it nothing: no composer at all. The watch cannot say the Enter was
// not taken, so no resend is due and nothing more is pressed.
func TestRetryDialog_NoComposerPressesNothingMore(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, submits, events := proofPane(t)
	nma5Paint(p, "Loading conversation...")
	n := nma5Enters(t, submits, func(int) {})
	sendSubmitKey(p.emu, p.submitKey)
	confirmAndRetrySubmit(p, newPendingSubmit(nma5Body, "bracketed", ""))
	time.Sleep(100 * time.Millisecond)
	if got := n.Load(); got != 1 {
		t.Fatalf("%d Enters written with no composer, want only the first", got)
	}
	if ev, got := nma5Undelivered(events, time.Second); !got || !strings.HasPrefix(ev.Detail, unconfirmedSubmitPrefix) {
		t.Fatalf("reported %q (got=%v), want could not confirm", ev.Detail, got)
	}
}

// The fixture under the slow sample that made rigs 40096953732 red: a sample
// that starts just before the window ends and finishes after the deadline.
// The dialog must still be up at the resend, and no second Enter written.
func TestRetryDialog_ASlowSampleAtTheWindowEdgeStillGetsNoSecondEnter(t *testing.T) {
	p, enters, events := ytxcHeldPaced(t, true, true)
	sendSubmitKey(p.emu, p.submitKey)
	confirmAndRetrySubmit(p, newPendingSubmit(nma5Body, "bracketed", ""))
	time.Sleep(100 * time.Millisecond)
	if got := enters(); got != 1 {
		t.Fatalf("%d Enters written, want 1: a slow sample left the dialog unlatched at the resend", got)
	}
	if ev, got := nma5Undelivered(events, time.Second); !got || !strings.Contains(ev.Detail, "dialog open at the retry") {
		t.Fatalf("reported %q (got=%v), want could not confirm, dialog open at the retry", ev.Detail, got)
	}
}
