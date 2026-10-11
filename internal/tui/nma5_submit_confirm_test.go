package tui

import (
	"strings"
	"testing"
	"time"
)

// ini-nma5 step 1: after the Enter, the send path watches the composer and
// reports one outcome. These cells drive confirmSubmit over a painted screen.

func nma5Paint(p *Pane, composer string) {
	p.emu.Write([]byte("\x1b[2J\x1b[H" + composer))
}

func nma5Window(t *testing.T, d time.Duration) {
	t.Helper()
	old := submitConfirmWindow
	submitConfirmWindow = d
	t.Cleanup(func() { submitConfirmWindow = old })
}

const nma5Body = "please review the retry path in ipc.go and report back"

func TestConfirmSubmit_AComposerThatClearsIsTaken(t *testing.T) {
	nma5Window(t, time.Second)
	p, _, _ := proofPane(t)
	nma5Paint(p, "❯ "+nma5Body)
	go func() { time.Sleep(150 * time.Millisecond); nma5Paint(p, "❯ ") }()
	if got := confirmSubmit(p, newPendingSubmit(nma5Body, "bracketed", ""), time.Now()); got != submitTaken {
		t.Fatalf("a composer that emptied after the Enter: outcome %d, want taken", got)
	}
}

func TestConfirmSubmit_AComposerStillHoldingOurBodyIsNotTaken(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	nma5Paint(p, "❯ "+nma5Body)
	start := time.Now()
	if got := confirmSubmit(p, newPendingSubmit(nma5Body, "bracketed", ""), start); got != submitNotTaken {
		t.Fatalf("a composer still holding our body: outcome %d, want not taken", got)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("took %s to decide; the window is 200ms", el)
	}
}

// The safety property of any retry: Claude restores the operator's stashed
// draft after a submit. That is NOT our text, so the submit was taken.
func TestConfirmSubmit_TheOperatorsRestoredDraftIsNotOurBody(t *testing.T) {
	nma5Window(t, time.Second)
	p, _, _ := proofPane(t)
	nma5Paint(p, "❯ "+nma5Body)
	go func() {
		time.Sleep(100 * time.Millisecond)
		nma5Paint(p, "❯ half-typed note from the operator about lunch")
	}()
	if got := confirmSubmit(p, newPendingSubmit(nma5Body, "bracketed", ""), time.Now()); got != submitTaken {
		t.Fatalf("the operator's restored draft: outcome %d, want taken (not our text)", got)
	}
}

// A collapsed large paste of OUR size counts as ours.
func TestConfirmSubmit_ACollapsedPasteOfOurSizeIsStillOurs(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	body := longBody(60)
	nma5Paint(p, "❯ [Pasted text #1 +59 lines]")
	if got := confirmSubmit(p, newPendingSubmit(body, "bracketed", ""), time.Now()); got != submitNotTaken {
		t.Fatalf("a collapsed paste of our size, still in the composer: outcome %d, want not taken", got)
	}
}

// Under the 12-rune proof floor, a short message is still recognised.
func TestConfirmSubmit_AShortBodyStillInTheComposerIsNotTaken(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	nma5Paint(p, "❯ ok?")
	if got := confirmSubmit(p, newPendingSubmit("ok?", "bracketed", ""), time.Now()); got != submitNotTaken {
		t.Fatalf("a short body still in the composer: outcome %d, want not taken", got)
	}
}

// No composer on screen is UNKNOWN, never a delivery.
func TestConfirmSubmit_NoComposerOnScreenIsNeverTaken(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	nma5Paint(p, "Loading conversation...")
	if got := confirmSubmit(p, newPendingSubmit(nma5Body, "bracketed", ""), time.Now()); got != submitUnknown {
		t.Fatalf("a screen with no composer: outcome %d, want unknown", got)
	}
}

// ── ini-nma5 AC 2: the retry ─────────────────────────────────────────

// nma5Enters counts the Enters the pane sends; fake Claude reacts to them.
func nma5Enters(t *testing.T, submits <-chan []byte, onEnter func(n int)) *int {
	t.Helper()
	n := 0
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case <-done:
				return
			case b := <-submits:
				if len(b) > 0 && (b[len(b)-1] == '\r' || b[len(b)-1] == '\n') {
					n++
					onEnter(n)
				}
			}
		}
	}()
	return &n
}

func nma5Undelivered(events <-chan AgentEvent, within time.Duration) (AgentEvent, bool) {
	select {
	case ev := <-events:
		return ev, true
	case <-time.After(within):
		return AgentEvent{}, false
	}
}

// A composer that takes the first Enter: one Enter, no retry, no report.
func TestSubmitRetry_ATakenEnterIsNotRepeated(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	p, submits, events := proofPane(t)
	nma5Paint(p, "❯ "+nma5Body)
	n := nma5Enters(t, submits, func(int) { nma5Paint(p, "❯ ") })
	sendSubmitKey(p.emu, p.submitKey)
	confirmAndRetrySubmit(p, newPendingSubmit(nma5Body, "bracketed", ""))
	time.Sleep(50 * time.Millisecond)
	if *n != 1 {
		t.Fatalf("sent %d Enters for a submit taken first time, want 1", *n)
	}
	if ev, got := nma5Undelivered(events, 100*time.Millisecond); got {
		t.Fatalf("reported %q for a delivered submit", ev.Detail)
	}
}

// AC 2: a composer that ignores the FIRST Enter is retried once and delivered.
func TestSubmitRetry_ASwallowedEnterIsResentOnceAndDelivered(t *testing.T) {
	nma5Window(t, 300*time.Millisecond)
	p, submits, events := proofPane(t)
	nma5Paint(p, "❯ "+nma5Body)
	n := nma5Enters(t, submits, func(k int) {
		if k >= 2 {
			nma5Paint(p, "❯ ")
		}
	})
	sendSubmitKey(p.emu, p.submitKey)
	confirmAndRetrySubmit(p, newPendingSubmit(nma5Body, "bracketed", ""))
	time.Sleep(50 * time.Millisecond)
	if *n != 2 {
		t.Fatalf("sent %d Enters, want 2 (the Enter and one resend)", *n)
	}
	if ev, got := nma5Undelivered(events, 100*time.Millisecond); got {
		t.Fatalf("reported %q although the resend was taken", ev.Detail)
	}
}

// AC 2: a second miss is surfaced as NOT delivered, within 2s, not 5 min.
func TestSubmitRetry_ASecondMissIsSurfacedWithinTwoSeconds(t *testing.T) {
	nma5Window(t, time.Second) // the production window
	p, submits, events := proofPane(t)
	nma5Paint(p, "❯ "+nma5Body)
	n := nma5Enters(t, submits, func(int) {}) // ignores every Enter
	start := time.Now()
	sendSubmitKey(p.emu, p.submitKey)
	confirmAndRetrySubmit(p, newPendingSubmit(nma5Body, "bracketed", ""))
	ev, got := nma5Undelivered(events, 3*time.Second)
	if !got {
		t.Fatal("a twice-ignored Enter was never surfaced")
	}
	if el := time.Since(start); el > 2500*time.Millisecond {
		t.Fatalf("surfaced after %s, want about 2s", el)
	}
	if !strings.HasPrefix(ev.Detail, undeliveredSubmitPrefix) || !strings.Contains(ev.Detail, "Enter not taken twice") {
		t.Fatalf("surfaced %q, want the NOT delivered report naming the cause", ev.Detail)
	}
	if *n != 2 {
		t.Fatalf("sent %d Enters, want exactly 2 (never a third)", *n)
	}
}

// No composer visible: nothing is pressed blind, and nothing is reported.
func TestSubmitRetry_NoComposerVisiblePressesNothing(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, submits, events := proofPane(t)
	nma5Paint(p, "Loading conversation...")
	n := nma5Enters(t, submits, func(int) {})
	sendSubmitKey(p.emu, p.submitKey)
	confirmAndRetrySubmit(p, newPendingSubmit(nma5Body, "bracketed", ""))
	time.Sleep(50 * time.Millisecond)
	if *n != 1 {
		t.Fatalf("sent %d Enters with no composer visible, want only the original 1", *n)
	}
	if ev, got := nma5Undelivered(events, 100*time.Millisecond); got {
		t.Fatalf("reported %q for an unknown outcome", ev.Detail)
	}
}
