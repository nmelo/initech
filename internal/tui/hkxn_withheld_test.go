package tui

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// ini-hkxn, rules 2, 4 and 5: a WITHHELD writes a capture; a withheld
// message that ends NOT DELIVERED tells its sender once; a screen the belt
// does not recognise after typing still gets no Enter. No real clock: the
// belt's echo window is set to zero, so it reads the screen once.

func hkxnNoEchoWindow(t *testing.T) {
	t.Helper()
	old := composerEchoWindow
	composerEchoWindow = 0
	t.Cleanup(func() { composerEchoWindow = old })
}

const hkxnBody = "a field-length message that never reached a box the reader could see"

// Rule 5 and feed-it-nothing: after typing, a screen with no box holding our
// text (an empty screen, a 1-row pane, a garbled box) is never given the
// Enter.
func TestWithheld326_AnUnrecognisedScreenAfterTypingGetsNoEnter(t *testing.T) {
	hkxnNoEchoWindow(t)
	cases := []struct {
		name       string
		cols, rows int
		screen     []string
	}{
		{"empty screen", 49, 16, nil},
		{"one row", 49, 1, []string{"garbled"}},
		{"garbled box", 49, 16, []string{"────────────────────", "  %$# 12 ~~", "────────────────────"}},
	}
	for _, c := range cases {
		p, _, _ := proofPaneSized(t, c.cols, c.rows)
		paintRows(t, p, c.screen)
		if accepted, _ := composerAcceptedBody(p, "", newPendingSubmit(hkxnBody, "bracketed", "")); accepted {
			t.Errorf("%s: the belt accepted a screen with no box holding our text", c.name)
		}
	}
}

// Rule 2: a WITHHELD keeps the screen the belt read, beside the log, named
// for the pane, with its size.
func TestWithheld326_AWithheldSendWritesACapture(t *testing.T) {
	dir := xl9LogDir(t)
	p, _, _ := proofPaneSized(t, 49, 16)
	paintRows(t, p, []string{"some transcript", "no box at all"})
	withholdSubmit(p, hkxnBody, "bracketed", "", "")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), withheldCapturePref+"eng2-") {
		t.Fatalf("captures = %v (%v), want one withheld-eng2- file", entries, err)
	}
	b, _ := os.ReadFile(dir + "/" + entries[0].Name())
	if !strings.HasPrefix(string(b), "pane=eng2 cols=49 rows=16 ") || !strings.Contains(string(b), "no box at all") {
		t.Fatalf("capture:\n%s", b)
	}
}

// Rule 4: the sender of a withheld message is told once, through the send
// path, when it ends not delivered; the log and the report stay.
func TestWithheld326_ANotDeliveredEndTellsTheSenderOnce(t *testing.T) {
	xl9LogDir(t)
	p, _, events := proofPaneSized(t, 49, 16)
	p.setSendFrom("eng5")
	withholdSubmit(p, hkxnBody, "bracketed", "", "")
	p.setSendFrom("")
	ps := xl9Pending(p)
	if ps == nil || ps.from != "eng5" {
		t.Fatalf("pending submit = %+v, want it to carry the sender eng5", ps)
	}
	<-events // the withhold's own "submit withheld" event
	p.surfaceUndeliveredSubmit(ps, "the composer no longer holds our text")
	ev := <-events
	if ev.From != "eng5" || !strings.HasPrefix(ev.ToSender, "eng2 did not get your message (not delivered after ") ||
		!strings.Contains(ev.ToSender, "the composer no longer holds our text); send it again: ") {
		t.Fatalf("report event = %+v", ev)
	}
	// A second resolver finds it gone: no second report.
	p.surfaceUndeliveredSubmit(ps, "again")
	select {
	case ev2 := <-events:
		t.Fatalf("a second report: %+v", ev2)
	default:
	}
	r := recordInitechLines(t)
	tui := newTestTUI(testPane("eng2"), testPane("eng5"), testPane("super"))
	tui.handleAgentEvent(ev)
	<-r.seen
	got := r.all()
	if len(got) != 1 || !strings.HasPrefix(got[0], "eng5|initech|[from initech] eng2 did not get your message") {
		t.Fatalf("lines = %q, want one line to eng5", got)
	}
}

// A report with no sender (the confirm path's, or a send with none recorded)
// tells no one: only a withheld message carries one.
func TestWithheld326_AReportWithNoSenderTellsNoOne(t *testing.T) {
	r := recordInitechLines(t)
	tui := newTestTUI(testPane("eng2"), testPane("super"))
	tui.handleAgentEvent(AgentEvent{Type: EventAgentStalled, Pane: "eng2", Detail: undeliveredSubmitPrefix + "x) after 1s"})
	if got := r.all(); len(got) != 0 {
		t.Fatalf("lines = %q, want none", got)
	}
}

// Rule 5 through the send path itself: the box was an empty ❯ row before
// the paste and stays one (nothing of ours ever shows), so the send is
// withheld and no Enter is written. Its twin: our text already in the box
// is accepted and the Enter is written, so the negative cell reads a
// channel that would carry one.
func TestWithheld326_TheSendPathWritesNoEnterWithoutOurTextInABox(t *testing.T) {
	hkxnNoEchoWindow(t)
	nma5Window(t, 0)
	shortSubmitDelays(t)
	xl9LogDir(t)
	// keys is everything the send wrote as keys, read up to a sentinel
	// written through the same pipe after it returned: the pipe keeps order,
	// so nothing the send wrote can arrive after the sentinel.
	keys := func(p *Pane, submits <-chan []byte) string {
		w, ok := p.emu.InputPipe().(*io.PipeWriter)
		if !ok {
			t.Fatal("no input pipe")
		}
		go w.Write([]byte("<<end>>"))
		var all strings.Builder
		for !strings.Contains(all.String(), "<<end>>") {
			all.Write(<-submits)
		}
		return strings.TrimSuffix(all.String(), "<<end>>")
	}
	send := func(screen string) (SendVerdict, string) {
		p, submits, _ := proofPaneSized(t, 80, 16)
		p.emu.Write([]byte("\x1b[2J\x1b[H────────────────────\r\n" + screen + "\r\n────────────────────"))
		p.sendMu.Lock()
		v := sendPaneTextLocked(p, hkxnBody, true)
		p.sendMu.Unlock()
		return v, keys(p, submits)
	}
	v, wrote := send("❯ ")
	if v.Outcome != DeliveryUnconfirmed || v.Reason != withheldVerdictReason {
		t.Fatalf("verdict %+v, want withheld", v)
	}
	if strings.ContainsAny(wrote, "\r\n") {
		t.Fatalf("an Enter was written (%q) with nothing of ours in the box", wrote)
	}
	v, wrote = send("❯ " + hkxnBody)
	if v.Outcome == DeliveryUnconfirmed && v.Reason == withheldVerdictReason {
		t.Fatalf("twin: our text in the box was withheld: %+v", v)
	}
	if !strings.ContainsAny(wrote, "\r\n") {
		t.Fatalf("twin: wrote %q, want the Enter", wrote)
	}
}

// Reopen, the loop: a not-delivered notice never goes into the pane that
// failed. initech's own line to a stuck super would tell "initech", which
// routes to super, into the same stuck box. Its twin: the same notice for
// eng2 goes to super once.
func TestWithheld326_ANoticeNeverGoesIntoThePaneThatFailed(t *testing.T) {
	logs := captureLogs(t)
	r := recordInitechLines(t)
	tui := newTestTUI(testPane("eng2"), testPane("eng5"), testPane("super"))
	notice := func(pane, from string) AgentEvent {
		return AgentEvent{Type: EventAgentStalled, Pane: pane, Detail: undeliveredSubmitPrefix + "x) after 1s",
			From: from, ToSender: withheldNotDeliveredLine(pane, time.Second, "the composer no longer holds our text", "start")}
	}
	// The guard's own log line is the positive evidence (synchronous); a
	// line that went out anyway would be sent off this goroutine, so its
	// absence alone proves nothing.
	tui.handleAgentEvent(notice("super", initechSender)) // routes to super: the pane that failed
	tui.handleAgentEvent(notice("eng5", "eng5"))         // an agent's own message to itself
	for _, pane := range []string{"super", "eng5"} {
		if !strings.Contains(logs.String(), "not-delivered notice not sent: it would go to the pane that failed\" pane="+pane) {
			t.Fatalf("no guard line for %s; log:\n%s", pane, logs.String())
		}
	}
	tui.handleAgentEvent(notice("eng2", initechSender))
	<-r.seen
	got := r.all()
	if len(got) != 1 || !strings.HasPrefix(got[0], "super|initech|[from initech] for initech: eng2 did not get your message") {
		t.Fatalf("lines %q, want only the one line to super about eng2", got)
	}
	if strings.Contains(logs.String(), "would go to the pane that failed\" pane=eng2") {
		t.Fatal("the guard held back a notice for a pane that was not its destination")
	}
}

// qa2's probe (F): the operator's message to super that ends not delivered
// tells the operator, as a notice in window 1; the guard holds back only a
// notice that would land in the pane that failed, and the operator's notice
// lands in no pane.
func TestWithheld326_AnOperatorsNoticeAboutSuperStillReachesTheOperator(t *testing.T) {
	tui := newTestTUI(testPane("eng2"), testPane("super"))
	line := withheldNotDeliveredLine("super", time.Second, "the composer no longer holds our text", "start")
	tui.handleAgentEvent(AgentEvent{Type: EventAgentStalled, Pane: "super", Detail: undeliveredSubmitPrefix + "x) after 1s",
		From: operatorSender, ToSender: line})
	for _, n := range tui.notifications {
		if n.event.Detail == line {
			return
		}
	}
	t.Fatalf("no operator notice %q among %d notifications", line, len(tui.notifications))
}
