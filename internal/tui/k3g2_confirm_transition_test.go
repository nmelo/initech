package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-k3g2: "delivered" is a transition. The send path confirms a submit only
// after it has SEEN our body in the agent's box and then seen it leave. A box
// it cannot recognise as ours is UNKNOWN and is reported, never counted.
//
// Measured on a 52-agent fleet before this: 253 of 1,613 sends confirmed at
// 0 ms, because the confirmation fired the first time it could not prove our
// text was in the box -- including when it never had.

// Feed it nothing: an empty box from the very first sample never showed our
// text, so nothing was seen leaving it.
func TestK3g2_AnEmptyBoxFromTheStartIsUnknownNotTaken(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	nma5Paint(p, "❯ ")
	if got := confirmSubmit(p, newPendingSubmit(nma5Body, "bracketed", ""), time.Now()); got != submitUnknown {
		t.Fatalf("an empty box that never held our text: outcome %d, want unknown", got)
	}
}

// Someone else's text the whole time is not ours leaving.
func TestK3g2_ABoxThatNeverShowedOurTextIsUnknown(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	nma5Paint(p, "❯ half-typed note from the operator about lunch")
	if got := confirmSubmit(p, newPendingSubmit(nma5Body, "bracketed", ""), time.Now()); got != submitUnknown {
		t.Fatalf("a box holding only someone else's text: outcome %d, want unknown", got)
	}
}

// A collapsed placeholder whose line count is not ours is not our paste, so
// it is not evidence of ours, either there or gone.
func TestK3g2_APlaceholderWithAMismatchedCountIsUnknown(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	body := longBody(40) // ours would read +39 lines
	nma5Paint(p, "❯ [Pasted text #1 +12 lines]")
	if got := confirmSubmit(p, newPendingSubmit(body, "bracketed", ""), time.Now()); got != submitUnknown {
		t.Fatalf("a placeholder with someone else's line count: outcome %d, want unknown", got)
	}
}

// The positive twin of the three above: seen, then gone, is taken.
func TestK3g2_SeenThenGoneIsTaken(t *testing.T) {
	nma5Window(t, time.Second)
	p, _, _ := proofPane(t)
	body := longBody(40)
	nma5Paint(p, "❯ [Pasted text #1 +39 lines]")
	go func() { time.Sleep(150 * time.Millisecond); nma5Paint(p, "❯ ") }()
	if got := confirmSubmit(p, newPendingSubmit(body, "bracketed", ""), time.Now()); got != submitTaken {
		t.Fatalf("our placeholder, then an empty box: outcome %d, want taken", got)
	}
}

// AC4: Claude cannot take an Enter and repaint in under 50 ms, so a
// confirmation that fast observed nothing. Even when the box clears at the
// very next sample after the sighting, the confirmation waits for a sample
// at or past the floor.
//
// The clear is tied to sample order, not a timer. A 5 ms timer raced the
// first sample, and on a loaded race runner it cleared the box mid-sample,
// before the sighting: outcome unknown. The first sample is slowed here to
// be that runner every time.
func TestK3g2_NoConfirmationBeforeTheFiftyMillisecondFloor(t *testing.T) {
	nma5Window(t, time.Second)
	p, _, _ := proofPane(t)
	nma5Paint(p, "❯ "+nma5Body)
	samples := 0
	submitSampleMidpoint = func() {
		switch samples++; samples {
		case 1:
			time.Sleep(10 * time.Millisecond)
		case 2:
			nma5Paint(p, "❯ ")
		}
	}
	t.Cleanup(func() { submitSampleMidpoint = nil })
	start := time.Now()
	if got := confirmSubmit(p, newPendingSubmit(nma5Body, "bracketed", ""), start); got != submitTaken {
		t.Fatalf("seen, then cleared: outcome %d, want taken", got)
	}
	if el := time.Since(start); el < 50*time.Millisecond {
		t.Fatalf("confirmed %s after the Enter; nothing real is faster than %s", el, 50*time.Millisecond)
	}
}

// AC2: an UNKNOWN outcome is surfaced to the operator as "could not
// confirm", and presses nothing.
func TestK3g2_UnknownIsSurfacedAsCouldNotConfirmAndPressesNothing(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, submits, events := proofPane(t)
	nma5Paint(p, "❯ ")
	n := nma5Enters(t, submits, func(int) {})
	sendSubmitKey(p.emu, p.submitKey)
	confirmAndRetrySubmit(p, newPendingSubmit(nma5Body, "bracketed", ""))
	ev, got := nma5Undelivered(events, 2*time.Second)
	if !got {
		t.Fatal("an unconfirmable submit was not reported to the operator")
	}
	if !strings.Contains(ev.Detail, "could not confirm") {
		t.Fatalf("reported %q, want a could-not-confirm report", ev.Detail)
	}
	if strings.HasPrefix(ev.Detail, undeliveredSubmitPrefix) {
		t.Fatalf("reported %q: unknown is not NOT delivered", ev.Detail)
	}
	time.Sleep(50 * time.Millisecond)
	if n.Load() != 1 {
		t.Fatalf("sent %d Enters for an unknown outcome, want only the original 1", n.Load())
	}
}

// The reason names what was not recognised, so a Claude that counts lines
// differently shows up in the operator's report rather than as a delivery.
func TestK3g2_AMismatchedPlaceholderIsNamedInTheReport(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, events := proofPane(t)
	nma5Paint(p, "❯ [Pasted text #1 +12 lines]")
	confirmAndRetrySubmit(p, newPendingSubmit(longBody(40), "bracketed", ""))
	ev, got := nma5Undelivered(events, 2*time.Second)
	if !got {
		t.Fatal("a mismatched placeholder was not reported")
	}
	if !strings.Contains(ev.Detail, "line count") {
		t.Fatalf("reported %q, want the reason to name the line count", ev.Detail)
	}
}

// The could-not-confirm toast gets the same width as NOT delivered: the
// usual 50-column cap would clip the running/measured version pair, which is
// what tells the operator whether a Claude upgrade changed the box.
func TestK3g2_TheCouldNotConfirmToastShowsTheVersionPair(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	defer s.Fini()
	s.SetSize(80, 24)
	u := newTestTUI()
	u.screen = s
	u.notifications = []notification{{event: AgentEvent{
		Type: EventAgentStalled, Pane: "eng2",
		Detail: unconfirmedSubmitPrefix + "running 2.1.233, measured 2.1.240) after 1s (our text was never seen in the box), re-send it if the agent did not get it: hello",
	}}}
	u.renderNotifications()
	visible := readScreenRect(s, 0, 22, 80, 1)
	if !strings.Contains(visible, "2.1.240") {
		t.Fatalf("toast clipped the version pair: %s", visible)
	}
}

// Seen, then the composer vanished (a redraw, a dialog, a crash screen): our
// text is not provably still there, so this is not "not taken" and gets no
// blind resend; it is not "taken" either, because no composer showed it gone.
func TestK3g2_SeenThenNoComposerIsUnknownAndPressesNothing(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	p, submits, events := proofPane(t)
	nma5Paint(p, "❯ "+nma5Body)
	n := nma5Enters(t, submits, func(int) { nma5Repaint(p, "Loading conversation...") })
	sendSubmitKey(p.emu, p.submitKey)
	confirmAndRetrySubmit(p, newPendingSubmit(nma5Body, "bracketed", ""))
	ev, got := nma5Undelivered(events, time.Second)
	if !got || !strings.HasPrefix(ev.Detail, unconfirmedSubmitPrefix) {
		t.Fatalf("reported %q (got=%v), want could not confirm", ev.Detail, got)
	}
	time.Sleep(50 * time.Millisecond)
	if n.Load() != 1 {
		t.Fatalf("sent %d Enters, want only the original 1", n.Load())
	}
}

// The resend's own watch reports too: held through the first window, the
// resend makes the composer vanish, and that is surfaced, not dropped.
func TestK3g2_AnUnknownAfterTheResendIsSurfaced(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	p, submits, events := proofPane(t)
	nma5Paint(p, "❯ "+nma5Body)
	n := nma5Enters(t, submits, func(k int) {
		if k >= 2 {
			nma5Repaint(p, "Loading conversation...")
		}
	})
	sendSubmitKey(p.emu, p.submitKey)
	confirmAndRetrySubmit(p, newPendingSubmit(nma5Body, "bracketed", ""))
	ev, got := nma5Undelivered(events, time.Second)
	if !got || !strings.HasPrefix(ev.Detail, unconfirmedSubmitPrefix) {
		t.Fatalf("reported %q (got=%v), want could not confirm after the resend", ev.Detail, got)
	}
	if n.Load() != 2 {
		t.Fatalf("sent %d Enters, want 2 (the Enter and one resend)", n.Load())
	}
}

// A sample is one frame. Here a repaint lands inside the second sample,
// after its ❯ row was read and before the box is: the sample is stitched from
// our body's frame and a frame with no composer at all. Neither frame shows a
// composer without our text, so this must never read as taken. Measured
// before the stable-sample check, with the repaint left to timing: about 1
// run in 5 confirmed "taken". The hook makes the tear land every time.
func TestK3g2_ATornSampleDuringARepaintIsNotCountedAsTaken(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	p, _, _ := proofPane(t)
	nma5Paint(p, "❯ "+nma5Body)
	samples := 0
	submitSampleMidpoint = func() {
		if samples++; samples == 2 {
			nma5Paint(p, "Loading conversation...")
		}
	}
	t.Cleanup(func() { submitSampleMidpoint = nil })
	if got := confirmSubmit(p, newPendingSubmit(nma5Body, "bracketed", ""), time.Now()); got != submitUnknown {
		t.Fatalf("a sample torn across a repaint: outcome %d, want unknown (never taken)", got)
	}
	if samples < 3 {
		t.Fatalf("only %d samples ran; the torn one must be followed by a whole one", samples)
	}
}

// The sighting reads the same places the belt does: when Claude's ❯ row has
// scrolled off a short pane under a long wrapped body, our text above the
// composer's bottom rule is still a sighting, and the box clearing after the
// Enter confirms it.
func TestK3g2_OurBodyAboveTheRuleWithThePromptScrolledOffIsASighting(t *testing.T) {
	nma5Window(t, time.Second)
	p, _, _ := proofPane(t)
	p.emu.Write([]byte(cg8yScrolledOffScreen(true)))
	if _, found := composerTail(p); found {
		t.Fatal("fixture invalid: a prompt glyph is on screen")
	}
	go func() { time.Sleep(150 * time.Millisecond); nma5Paint(p, "❯ ") }()
	if got := confirmSubmit(p, newPendingSubmit(cg8yBody, "bracketed", ""), time.Now()); got != submitTaken {
		t.Fatalf("our wrapped body above the rule, then a cleared box: outcome %d, want taken", got)
	}
}

// MEASURED on Claude 2.1.302 (eng2, this bead): the placeholder's +M is the
// number of newline characters in the paste. A 40-line body ending in a
// newline read "+40 lines", the same body without it "+39", and with two
// "+41". A message sent from a file usually ends in a newline, and "lines
// minus one" called every such placeholder someone else's.
func TestK3g2_APlaceholderForABodyEndingInANewlineIsOurs(t *testing.T) {
	nma5Window(t, time.Second)
	p, _, _ := proofPane(t)
	body := longBody(40) + "\n"
	nma5Paint(p, "❯ [Pasted text #1 +40 lines]")
	go func() { time.Sleep(150 * time.Millisecond); nma5Paint(p, "❯ ") }()
	if got := confirmSubmit(p, newPendingSubmit(body, "bracketed", ""), time.Now()); got != submitTaken {
		t.Fatalf("our placeholder (+40 for 40 newlines), then cleared: outcome %d, want taken", got)
	}
}

// The reason comes from what was seen during the window, not only the last
// sample: a mismatched placeholder that was submitted and cleared is still
// the explanation the operator needs.
func TestK3g2_AMismatchSeenEarlierIsTheReasonEvenAfterTheBoxClears(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	p, _, events := proofPane(t)
	nma5Paint(p, "❯ [Pasted text #1 +12 lines]")
	go func() { time.Sleep(100 * time.Millisecond); nma5Paint(p, "❯ ") }()
	confirmAndRetrySubmit(p, newPendingSubmit(longBody(40), "bracketed", ""))
	ev, got := nma5Undelivered(events, time.Second)
	if !got || !strings.Contains(ev.Detail, "+12") || !strings.Contains(ev.Detail, "+39") {
		t.Fatalf("reported %q (got=%v), want the reason naming +12 shown and +39 expected", ev.Detail, got)
	}
}

// Exact, not near: a placeholder one off ours either way is someone else's
// paste. A tolerance of one would also have hidden the trailing-newline
// miscount, which is exactly one off.
func TestK3g2_APlaceholderOneOffOurCountIsNotOurs(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	body := longBody(40) // 39 newlines
	for _, shown := range []string{"+38", "+40"} {
		p, _, _ := proofPane(t)
		nma5Paint(p, "❯ [Pasted text #1 "+shown+" lines]")
		if got := confirmSubmit(p, newPendingSubmit(body, "bracketed", ""), time.Now()); got != submitUnknown {
			t.Fatalf("placeholder %s for a body with 39 newlines: outcome %d, want unknown", shown, got)
		}
	}
}
