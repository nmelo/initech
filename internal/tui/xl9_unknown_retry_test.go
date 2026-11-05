package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ini-xl9 (d): a send whose outcome was UNKNOWN is kept for one bounded
// retry. Our own text provably in the box gets one Enter, never two; a box
// that reads without it ends quietly; a screen with no box waits to the
// bound; a dialog is never answered; the next send settles it the same way.

// xl9Kept is a pane with an UNKNOWN send kept, as the send path leaves it.
func xl9Kept(t *testing.T, screen string) (*Pane, *pendingSubmit, <-chan AgentEvent, func() int32) {
	t.Helper()
	p, submits, events := proofPane(t)
	n := nma5Enters(t, submits, func(int) {})
	nma5Paint(p, screen)
	ps := newPendingSubmit(nma5Body, "bracketed", "")
	p.sendMu.Lock()
	keepUnknownSubmit(p, ps)
	p.sendMu.Unlock()
	return p, ps, events, func() int32 { time.Sleep(60 * time.Millisecond); return n.Load() }
}

// xl9Sweep runs the sweep and waits for the retry it starts.
func xl9Sweep(p *Pane, ps *pendingSubmit) {
	sweepWithheldSubmitsIn([]PaneView{p}, time.Now())
	for i := 0; i < 200 && ps.retrying.Load(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
}

func xl9Pending(p *Pane) *pendingSubmit {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pendingSubmit
}

// AC9: our body still in the box behind a lost Enter: the sweep presses Enter
// once, reports it delivered late, and a second sweep presses nothing.
func TestUnknownRetry_OurBodyInTheBoxGetsOneEnter(t *testing.T) {
	buf := captureLogs(t)
	p, ps, events, enters := xl9Kept(t, "❯ "+nma5Body)
	if xl9Pending(p) != ps || !ps.fromUnknown {
		t.Fatal("the UNKNOWN send was not kept")
	}
	xl9Sweep(p, ps)
	xl9Sweep(p, ps)
	if n := enters(); n != 1 {
		t.Fatalf("%d Enters, want exactly 1", n)
	}
	ev, ok := nma5Undelivered(events, time.Second)
	if !ok || !strings.HasPrefix(ev.Detail, deliveredLateSubmitPrefix) {
		t.Errorf("reported %q (%v), want the delivered-late report", ev.Detail, ok)
	}
	if xl9Pending(p) != nil {
		t.Error("still pending after its one Enter")
	}
	if !strings.Contains(buf.String(), "submit UNKNOWN kept for one retry") {
		t.Error("no log line says the UNKNOWN was kept")
	}
}

// A box that reads and does not hold our text ends it: no Enter, no report.
func TestUnknownRetry_ABoxWithoutOurTextEndsQuietly(t *testing.T) {
	buf := captureLogs(t)
	p, ps, events, enters := xl9Kept(t, "❯ ")
	nma5Paint(p, "❯ lunch order: two of the usual")
	xl9Sweep(p, ps)
	if n := enters(); n != 0 {
		t.Fatalf("%d Enters into a box without our text, want 0", n)
	}
	if ev, ok := nma5Undelivered(events, 100*time.Millisecond); ok {
		t.Errorf("reported %q, want nothing beyond the could-not-confirm already sent", ev.Detail)
	}
	if xl9Pending(p) != nil || !strings.Contains(buf.String(), "kept UNKNOWN submit ended without a retry") {
		t.Error("the kept send did not end with its one line")
	}
}

// Feed it nothing: no box on screen waits, then ends at the bound, quietly.
func TestUnknownRetry_NoBoxWaitsToTheBound(t *testing.T) {
	p, ps, events, enters := xl9Kept(t, "Loading conversation...")
	xl9Sweep(p, ps)
	if xl9Pending(p) != ps {
		t.Fatal("a screen with no box ended the kept send before its bound")
	}
	ps.deadline = time.Now().Add(-time.Second)
	xl9Sweep(p, ps)
	if n := enters(); n != 0 {
		t.Fatalf("%d Enters with no box on screen, want 0", n)
	}
	if xl9Pending(p) != nil {
		t.Error("still pending past its bound")
	}
	if ev, ok := nma5Undelivered(events, 100*time.Millisecond); ok {
		t.Errorf("reported %q at the bound, want nothing more", ev.Detail)
	}
}

// Never into a dialog: our text quoted under an open prompt gets no Enter.
func TestUnknownRetry_ADialogIsNeverAnswered(t *testing.T) {
	p, ps, _, enters := xl9Kept(t, "❯ "+nma5Body)
	p.mu.Lock()
	p.dialogOpen = true // the dialog is latched; our text is still on screen
	p.mu.Unlock()
	xl9Sweep(p, ps)
	if n := enters(); n != 0 {
		t.Fatalf("%d Enters under a dialog, want 0", n)
	}
	if xl9Pending(p) != ps {
		t.Fatal("a dialog ended the kept send; want it still waiting")
	}
	p.mu.Lock()
	p.dialogOpen = false // the twin: the dialog closes, our text still there
	p.mu.Unlock()
	xl9Sweep(p, ps)
	if n := enters(); n != 1 {
		t.Fatalf("%d Enters once the dialog closed, want 1", n)
	}
}

// The next send settles a kept UNKNOWN: our text in the box gets its one
// Enter first; without it, it ends quietly, never "superseded".
func TestUnknownRetry_TheNextSendSettlesIt(t *testing.T) {
	p, ps, _, enters := xl9Kept(t, "❯ "+nma5Body)
	p.sendMu.Lock()
	p.settlePendingSubmit()
	p.sendMu.Unlock()
	if n := enters(); n != 1 || xl9Pending(p) != nil {
		t.Fatalf("settle: %d Enters, pending %v; want 1 and none", n, xl9Pending(p) != nil)
	}
	_ = ps

	buf := captureLogs(t)
	q, _, events, enters2 := xl9Kept(t, "❯ ")
	q.sendMu.Lock()
	q.settlePendingSubmit()
	q.sendMu.Unlock()
	if n := enters2(); n != 0 || xl9Pending(q) != nil {
		t.Fatalf("settle without our text: %d Enters, pending %v; want 0 and none", n, xl9Pending(q) != nil)
	}
	if strings.Contains(buf.String(), "SUPERSEDED") {
		t.Error("a kept UNKNOWN was reported superseded")
	}
	if ev, ok := nma5Undelivered(events, 100*time.Millisecond); ok {
		t.Errorf("reported %q, want nothing", ev.Detail)
	}
}

// xl9Capture is a testdata capture as a screen to paint, with old replaced.
func xl9Capture(t *testing.T, name, old, new string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.TrimRight(string(b), "\n")
	if old != "" {
		if !strings.Contains(s, old) {
			t.Fatalf("%s holds no %q", name, old)
		}
		s = strings.Replace(s, old, new, 1)
	}
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// The 2.1.318 shapes (eng4's captures). Mid-turn, a taken Enter queues our
// line above the box and the box reads Claude's hint: no Enter, it ends
// quietly. A pasted body behind a lost Enter shows only as a placeholder
// ("[Pasted text #1 +5 lines]"), which another paste with the same count
// would show too: with no change seen, it is never proof, so no Enter.
func TestUnknownRetry_TheCapturedShapes(t *testing.T) {
	for _, w := range []string{"100", "40"} {
		t.Run("queued_w"+w, func(t *testing.T) {
			screen := xl9Capture(t, "haiku318_queued_midturn_w"+w+".txt", "a queued line typed mid-turn", nma5Body)
			p, ps, events, enters := xl9Kept(t, "❯ ")
			nma5Paint(p, screen)
			xl9Sweep(p, ps)
			if n := enters(); n != 0 {
				t.Fatalf("%d Enters with our line queued mid-turn, want 0", n)
			}
			if xl9Pending(p) != nil {
				t.Error("the kept send did not end")
			}
			if ev, ok := nma5Undelivered(events, 100*time.Millisecond); ok {
				t.Errorf("reported %q, want nothing", ev.Detail)
			}
		})
	}
	t.Run("pasted_w100", func(t *testing.T) {
		body := "line one of six\nline two\nline three\nline four\nline five\nline six"
		p, submits, _ := proofPane(t)
		n := nma5Enters(t, submits, func(int) {})
		nma5Paint(p, xl9Capture(t, "haiku318_composer_pasted_w100.txt", "", ""))
		ps := newPendingSubmit(body, "bracketed", "")
		p.sendMu.Lock()
		keepUnknownSubmit(p, ps)
		p.sendMu.Unlock()
		xl9Sweep(p, ps)
		time.Sleep(60 * time.Millisecond)
		if got := n.Load(); got != 0 {
			t.Fatalf("%d Enters on a placeholder never seen to change, want 0", got)
		}
		if xl9Pending(p) != ps {
			t.Error("the kept send ended; want it waiting to its bound")
		}
	})
}
