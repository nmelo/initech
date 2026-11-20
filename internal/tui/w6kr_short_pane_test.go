package tui

import (
	"io"
	"strings"
	"testing"
	"time"
)

// ini-w6kr: on a short pane (north's qa2, 55x6) Claude Code 2.1.326's
// scrolled box runs off the bottom edge, its closing rule and footer below
// the last row, and the reader found no box. At 5 rows and fewer no row of
// the box is drawn at all (measured live at 5, 4 and 3), so a message too
// tall for one box row is refused before typing there. A restart carries a
// withheld message to the replacement pane.

func w6Rows(t *testing.T, file string) []string {
	t.Helper()
	rows, _ := hkxnRows(t, file)
	return rows
}

// AC2: a box the bottom edge cuts off is still a box: the 6-row and 10-row
// shapes (north qa2's and two live captures) are read, and the belt lets the
// Enter go.
func TestShortPaneW6kr_ABoxCutByTheBottomEdgeIsRead(t *testing.T) {
	for _, c := range []struct{ file, first string }{
		{"opus326_scrolled_open_w55h6.txt", "and 390 (lines whole), a quiet sample link page"},
		{"haiku326_scrolled_open_w55h6.txt", "will review when the store lands. Put the question"},
		{"haiku326_scrolled_open_w55h10.txt", "will review when the store lands. Put the question"},
	} {
		rows := w6Rows(t, c.file)
		tail, _, found := composerTailFromRows(rows)
		if !found || tail != c.first {
			t.Errorf("%s: composer %q (found %v), want %q", c.file, tail, found, c.first)
			continue
		}
		p, _, _ := proofPaneSized(t, 55, len(rows))
		paintRows(t, p, rows)
		if accepted, last := composerAcceptedBody(p, "", newPendingSubmit("a long body whose end is in the box", "bracketed", "")); !accepted {
			t.Errorf("%s: the belt withheld a body in a box cut by the bottom edge (last %q)", c.file, last)
		}
	}
}

// At 5, 4 and 3 rows no row of the box is drawn: nothing reads as a box.
func TestShortPaneW6kr_NoRowOfTheBoxDrawnIsNoBox(t *testing.T) {
	for _, f := range []string{"haiku326_scrolled_rule_only_w55h5.txt", "haiku326_scrolled_rule_only_w55h4.txt",
		"haiku326_nothing_drawn_w55h3.txt", "opus326_scrolled_cut_w54h5.txt"} {
		if tail, row, found := composerTailFromRows(w6Rows(t, f)); found {
			t.Errorf("%s: a composer %q at row %d where no row of the box is drawn", f, tail, row)
		}
	}
}

// w6Send sends text into a cols x rows pane and returns the verdict and the
// keys the pane was sent, read up to a sentinel through the same pipe.
func w6Send(t *testing.T, cols, rows int, text string, waited bool, from string) (SendVerdict, string, <-chan AgentEvent) {
	t.Helper()
	nma5Window(t, 0)
	hkxnNoEchoWindow(t)
	shortSubmitDelays(t)
	p, submits, events := proofPaneSized(t, cols, rows)
	p.setSendFrom(from)
	p.setSendWaited(waited)
	p.sendMu.Lock()
	v := sendPaneTextLocked(p, text, true)
	p.sendMu.Unlock()
	w, ok := p.emu.InputPipe().(*io.PipeWriter)
	if !ok {
		t.Fatal("no input pipe")
	}
	go w.Write([]byte("<<end>>"))
	var all strings.Builder
	for !strings.Contains(all.String(), "<<end>>") {
		all.Write(<-submits)
	}
	return v, strings.TrimSuffix(all.String(), "<<end>>"), events
}

const w6Long = "Due rule reviewed: the cycle count must come from the store, never from the raw export, and reply with the single word ok."

// Ruling 2: a long message into a 5-row pane is refused before anything is
// typed (no stash key, no Enter), exit 1, naming the height and what to send.
func TestShortPaneW6kr_ALongMessageIntoAShortPaneIsRefusedBeforeTyping(t *testing.T) {
	for _, rows := range []int{5, 4, 3, 1} {
		v, keys, _ := w6Send(t, 55, rows, w6Long, true, "eng5")
		want := "eng2 is " + itoa(rows) + " rows tall, too short to confirm a long message; widen or raise it, or send at most 51 characters on one line (a longer text: write it to a file and send its path)"
		if v.Outcome != DeliveryNotDelivered || v.Reason != want {
			t.Errorf("%d rows: verdict %+v\nwant not delivered: %q", rows, v, want)
		}
		if keys != "" {
			t.Errorf("%d rows: keys were sent (%q) for a refused message", rows, keys)
		}
	}
	// A newline makes any message too tall, however short.
	if v, _, _ := w6Send(t, 55, 5, "two\nlines", true, ""); v.Outcome != DeliveryNotDelivered {
		t.Errorf("a two-line message into 5 rows: %+v, want refused", v)
	}
}

// Its twins: a message that fits one box row types into a short pane as
// today, and a 6-row pane takes a long message (proven by the open-bottom read).
func TestShortPaneW6kr_WhatFitsTypesAndSixRowsTakeAnyLength(t *testing.T) {
	if v, keys, _ := w6Send(t, 55, 3, "please rebase and run make check", true, ""); v.Outcome == DeliveryNotDelivered || !strings.ContainsAny(keys, "\r\n") {
		t.Errorf("a short message into 3 rows: %+v keys %q, want typed with its Enter", v, keys)
	}
	if v, _, _ := w6Send(t, 55, 6, w6Long, true, ""); v.Outcome == DeliveryNotDelivered {
		t.Errorf("a long message into 6 rows refused: %+v", v)
	}
}

// Sender told: a refused send nobody waits on (held or queued mail drained
// later) tells its sender through the not-delivered line.
func TestShortPaneW6kr_AnUnwaitedRefusalTellsTheSender(t *testing.T) {
	_, _, events := w6Send(t, 55, 4, w6Long, false, "eng5")
	ev := <-events
	if ev.From != "eng5" || !strings.HasPrefix(ev.ToSender, "not delivered: eng2 is 4 rows tall, too short to confirm a long message") {
		t.Fatalf("event %+v, want the refusal told to eng5", ev)
	}
}

// Ruling 2 (rule 2 of the ruling): a restart carries the withheld message to
// the replacement pane, where the draft is still in the box (qa2's case);
// there it reads DELIVERED once seen submitted, never NOT DELIVERED.
func TestShortPaneW6kr_ARestartCarriesTheMessageToTheNewPane(t *testing.T) {
	r := e1Withhold(t, e1Before)
	np, _, npEvents := proofPaneSized(t, 31, 15)
	// The resumed session kept the draft; its redraw shows the box from a
	// different row of it, mid-message, so the box's first row changed and
	// only the run proof (boxRunIsOurs) says the text is ours.
	paintRows(t, np, []string{
		"✻ Baked for 1m 7s · done 10:26",
		"",
		"─── ↑ 1 more ──── Probe super ─",
		"  key reach the Mac kind, and",
		"  by whose hand?",
		e1Rule,
		"  ⎇ main │ Opus 5.5 │ Fifth …",
	})
	tui := newTestTUI(r.p)
	tui.replacePane(0, np)
	if xl9Pending(np) != r.ps || xl9Pending(r.p) != nil {
		t.Fatalf("the withheld message did not move: new %p old %p, want %p on the new pane", xl9Pending(np), xl9Pending(r.p), r.ps)
	}
	np.judgeWithheld(r.ps, time.Now()) // the draft is ours, cropped: no verdict
	if xl9Pending(np) != r.ps {
		t.Fatal("our own cropped draft in the new box ended the message")
	}
	paintRows(t, np, e1Delivered)
	np.judgeWithheld(r.ps, time.Now())
	np.judgeWithheld(r.ps, time.Now())
	if ev := <-npEvents; !strings.HasPrefix(ev.Detail, deliveredLateSubmitPrefix) {
		t.Fatalf("event %q, want DELIVERED in the new pane", ev.Detail)
	}
}

// An exit with no replacement: nothing before the deadline, then NOT
// DELIVERED. A resize changes nothing.
func TestShortPaneW6kr_AnExitNotReplacedIsNotDeliveredAtTheDeadline(t *testing.T) {
	r := e1Withhold(t, e1Before)
	r.p.Resize(12, 31)
	if !r.pending() {
		t.Fatal("a resize ended the withheld message")
	}
	r.p.mu.Lock()
	r.p.alive = false
	r.p.mu.Unlock()
	old := sweepGo
	sweepGo = func(f func()) { f() } // in line: what the sweep decides is done on return
	t.Cleanup(func() { sweepGo = old })
	sweepWithheldSubmitsIn([]PaneView{r.p}, time.Now())
	if !r.pending() {
		t.Fatal("a dead pane was ended before its deadline: a replacement may still come")
	}
	r.pastDeadline()
	sweepWithheldSubmitsIn([]PaneView{r.p}, time.Now())
	if ev := <-r.events; !strings.HasPrefix(ev.Detail, undeliveredSubmitPrefix) || !strings.Contains(ev.Detail, "the pane exited and was not replaced") {
		t.Fatalf("event %q", ev.Detail)
	}
}

// Live check (6 rows): the Enter went, but the pre-Enter sighting never saw
// our text, so each verdict was "could not confirm". The box-run proof reads
// the scrolled box cut off by the bottom edge, so the one row of our message
// it shows is sighted, and the confirmation can read the box clearing.
func TestShortPaneW6kr_TheSightingReadsAScrolledBoxCutByTheBottomEdge(t *testing.T) {
	rows := w6Rows(t, "haiku326_scrolled_open_w55h6.txt")
	p, _, _ := proofPaneSized(t, 55, len(rows))
	paintRows(t, p, rows)
	body := "The ledger read is right; keep the fields, build the routes against the interface meanwhile and I will review when the store lands. Put the question and your recommendation on the bead."
	ps := newPendingSubmit(body, "bracketed", "")
	if !boxRunIsOurs(p, ps) {
		t.Fatal("the box-run proof does not read our row in a scrolled box cut by the bottom edge")
	}
	tail, found := composerTail(p)
	if !submitSighted(p, tail, found, ps) {
		t.Fatalf("not sighted: tail %q found %v", tail, found)
	}
	// A body that is not ours is not sighted from the same box.
	if boxRunIsOurs(p, newPendingSubmit("an unrelated message about something else entirely, long enough to prove", "bracketed", "")) {
		t.Fatal("someone else's text read as ours")
	}
}
