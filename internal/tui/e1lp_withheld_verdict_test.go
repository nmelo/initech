package tui

import (
	"io"
	"strings"
	"testing"
	"time"
)

// ini-e1lp: after a withhold, a box that empties may have been submitted by
// another key (theguy's stopgap pressed Enter at 12:00:47 and the sender was
// told NOT DELIVERED). The withheld verdict is DELIVERED only on the
// two-sided proof or the queued line, NOT DELIVERED only on sight, UNKNOWN
// otherwise. Deadlines are set by hand; every wait is on an event.

const e1Body = "how did the key reach the Mac kind, and by whose hand?"

var e1Rule = strings.Repeat("─", 31)

// The withhold: a named session's scrolled box holding the end of our body
// (theguy's 49-column shape, here at 31 columns from hover's capture).
var e1Before = []string{
	"✻ Baked for 1m 7s · done 10:26",
	"",
	"─── ↑ 1 more ──── Probe super ─",
	"  Mac kind, and by whose",
	"  hand?",
	e1Rule,
	"  ⎇ main │ Opus 5.5 │ Fifth …",
}

// After another key submitted it: our message in the transcript above an
// empty box.
var e1Delivered = []string{
	"✻ Baked for 1m 7s · done 10:26",
	"",
	"> how did the key reach the Mac",
	"  kind, and by whose hand?",
	"",
	"⏺ It came by the deploy key.",
	e1Rule,
	"❯ ",
	e1Rule,
	"  ⎇ main │ Opus 5.5 │ Fifth …",
}

// theguy's case as the pane can show it: the box emptied, our message not
// (yet) drawn above it.
var e1Emptied = []string{
	"✻ Baked for 1m 7s · done 10:26",
	"",
	e1Rule,
	"❯ ",
	e1Rule,
	"  ⎇ main │ Opus 5.5 │ Fifth …",
}

type e1Rig struct {
	p       *Pane
	ps      *pendingSubmit
	events  <-chan AgentEvent
	submits <-chan []byte
}

func e1Withhold(t *testing.T, before []string) *e1Rig {
	t.Helper()
	xl9LogDir(t)
	p, submits, events := proofPaneSized(t, 31, 15)
	paintRows(t, p, before)
	p.setSendFrom("eng5")
	withholdSubmit(p, e1Body, "bracketed", "", "")
	p.setSendFrom("")
	<-events // the withhold's own event
	return &e1Rig{p: p, ps: xl9Pending(p), events: events, submits: submits}
}

func (r *e1Rig) show(t *testing.T, rows []string) { paintRows(t, r.p, rows) }

func (r *e1Rig) pastDeadline() { r.ps.deadline = time.Now().Add(-time.Second) }

// pending reports whether the message still waits (no verdict yet).
func (r *e1Rig) pending() bool { return xl9Pending(r.p) == r.ps }

// keys is every key the pane was sent, read up to a sentinel written through
// the same pipe after the verdict: the pipe keeps order.
func (r *e1Rig) keys(t *testing.T) string {
	t.Helper()
	w, ok := r.p.emu.InputPipe().(*io.PipeWriter)
	if !ok {
		t.Fatal("no input pipe")
	}
	go w.Write([]byte("<<end>>"))
	var all strings.Builder
	for !strings.Contains(all.String(), "<<end>>") {
		all.Write(<-r.submits)
	}
	return strings.TrimSuffix(all.String(), "<<end>>")
}

// AC1, theguy's case: a box emptied by another key is never NOT DELIVERED.
// Before the deadline it waits; at it, UNKNOWN, a capture, and the sender
// told it may or may not have arrived. No key is pressed.
func TestWithheldE1lp_AnEmptiedBoxIsNeverNotDelivered(t *testing.T) {
	r := e1Withhold(t, e1Before)
	r.show(t, e1Emptied)
	r.p.judgeWithheld(r.ps, time.Now())
	if !r.pending() {
		t.Fatal("an emptied box ended the message before its deadline")
	}
	r.pastDeadline()
	r.p.judgeWithheld(r.ps, time.Now())
	ev := <-r.events
	if strings.HasPrefix(ev.Detail, undeliveredSubmitPrefix) {
		t.Fatalf("emptied box read as NOT DELIVERED: %q", ev.Detail)
	}
	if !strings.HasPrefix(ev.Detail, unconfirmedSubmitPrefix) || !strings.Contains(ev.Detail, withheldEmptiedWhy) {
		t.Fatalf("event %q, want UNKNOWN naming the emptied box", ev.Detail)
	}
	if ev.From != "eng5" || !strings.HasPrefix(ev.ToSender, "the message to eng2 sent at ") || !strings.HasSuffix(ev.ToSender, " may or may not have arrived") {
		t.Fatalf("sender line %+v", ev)
	}
	if k := r.keys(t); strings.ContainsAny(k, "\r\n") {
		t.Fatalf("a key was pressed on UNKNOWN: %q", k)
	}
}

// Rule 1: our message newly above the prompt and the box empty, in two equal
// samples in a row, is DELIVERED; one sample is not enough.
func TestWithheldE1lp_TwoSamplesAboveThePromptAreDelivered(t *testing.T) {
	r := e1Withhold(t, e1Before)
	r.show(t, e1Delivered)
	r.p.judgeWithheld(r.ps, time.Now())
	if !r.pending() {
		t.Fatal("one sample decided it")
	}
	r.p.judgeWithheld(r.ps, time.Now())
	ev := <-r.events
	if !strings.HasPrefix(ev.Detail, deliveredLateSubmitPrefix) || !strings.Contains(ev.Detail, withheldSeenAbove) {
		t.Fatalf("event %q, want DELIVERED naming the evidence", ev.Detail)
	}
	if k := r.keys(t); strings.ContainsAny(k, "\r\n") {
		t.Fatalf("a key was pressed for a message another key submitted: %q", k)
	}
}

// The proof is two-sided: our message already above the prompt before the
// withhold proves nothing, so an empty box then reads UNKNOWN at the deadline.
func TestWithheldE1lp_ABodyAlreadyAboveBeforeIsNoProof(t *testing.T) {
	before := append([]string{"> how did the key reach the Mac", "  kind, and by whose hand?"}, e1Before...)
	r := e1Withhold(t, before)
	r.show(t, e1Delivered)
	r.p.judgeWithheld(r.ps, time.Now())
	r.p.judgeWithheld(r.ps, time.Now())
	if !r.pending() {
		t.Fatal("a body that was above before the withhold read as delivered")
	}
	r.pastDeadline()
	r.p.judgeWithheld(r.ps, time.Now())
	if ev := <-r.events; !strings.HasPrefix(ev.Detail, unconfirmedSubmitPrefix) {
		t.Fatalf("event %q, want UNKNOWN", ev.Detail)
	}
}

// Rule 1: Claude's queued line (the captured 40-column mid-turn screen, our
// message queued above the box, the box reading the queued hint) is
// DELIVERED at once.
func TestWithheldE1lp_TheQueuedLineIsDelivered(t *testing.T) {
	xl9LogDir(t)
	after := xl9Capture(t, "haiku318_queued_midturn_w40.txt", "a queued line typed mid-turn", e1Body)
	rows := strings.Split(after, "\r\n")
	p, _, events := proofPaneSized(t, 40, len(rows)+2) // the queued line wraps once at 40
	paintRows(t, p, []string{strings.Repeat("─", 40), "❯ " + e1Body, strings.Repeat("─", 40)})
	withholdSubmit(p, e1Body, "bracketed", "", "")
	<-events
	ps := xl9Pending(p)
	paintRows(t, p, rows)
	p.judgeWithheld(ps, time.Now())
	if ev := <-events; !strings.HasPrefix(ev.Detail, deliveredLateSubmitPrefix) || !strings.Contains(ev.Detail, withheldSeenQueued) {
		t.Fatalf("event %q, want DELIVERED by the queued line", ev.Detail)
	}
}

// Rule 2: the box holds other text and our message is nowhere above it:
// NOT DELIVERED, at once, with the sender line.
func TestWithheldE1lp_OtherTextAndOursNowhereIsNotDelivered(t *testing.T) {
	r := e1Withhold(t, e1Before)
	draft := append([]string(nil), e1Emptied...)
	draft[3] = "❯ the operator types a draft"
	r.show(t, draft)
	r.p.judgeWithheld(r.ps, time.Now())
	ev := <-r.events
	if !strings.HasPrefix(ev.Detail, undeliveredSubmitPrefix) || !strings.Contains(ev.Detail, withheldOtherText) {
		t.Fatalf("event %q, want NOT DELIVERED naming the other text", ev.Detail)
	}
	if ev.From != "eng5" || !strings.Contains(ev.ToSender, "eng2 did not get your message") {
		t.Fatalf("sender line %+v", ev)
	}
}

// Rule 2: the pane exited (the sweep) or was restarted (replacePane):
// NOT DELIVERED.
func TestWithheldE1lp_AnExitedOrRestartedPaneIsNotDelivered(t *testing.T) {
	r := e1Withhold(t, e1Before)
	r.p.mu.Lock()
	r.p.alive = false
	r.p.mu.Unlock()
	sweepWithheldSubmitsIn([]PaneView{r.p}, time.Now())
	if ev := <-r.events; !strings.HasPrefix(ev.Detail, undeliveredSubmitPrefix) || !strings.Contains(ev.Detail, "the pane exited") {
		t.Fatalf("exit: event %q", ev.Detail)
	}

	q := e1Withhold(t, e1Before)
	tui := newTestTUI(q.p)
	tui.replacePane(0, testPane("eng2"))
	if ev := <-q.events; !strings.HasPrefix(ev.Detail, undeliveredSubmitPrefix) || !strings.Contains(ev.Detail, "the pane was restarted") {
		t.Fatalf("restart: event %q", ev.Detail)
	}
}

// Feed it nothing: an empty screen, and no screen kept at the withhold,
// reads UNKNOWN at the deadline, never DELIVERED or NOT DELIVERED.
func TestWithheldE1lp_NothingToReadIsUnknown(t *testing.T) {
	r := e1Withhold(t, nil)
	r.ps.screenAtWithhold = nil
	r.show(t, nil)
	r.p.judgeWithheld(r.ps, time.Now())
	r.p.judgeWithheld(r.ps, time.Now())
	if !r.pending() {
		t.Fatal("an empty screen decided it before the deadline")
	}
	r.pastDeadline()
	r.p.judgeWithheld(r.ps, time.Now())
	if ev := <-r.events; !strings.HasPrefix(ev.Detail, unconfirmedSubmitPrefix) {
		t.Fatalf("event %q, want UNKNOWN", ev.Detail)
	}
}
