package tui

import (
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
	if !confirmSubmit(p, newPendingSubmit(nma5Body, "bracketed", ""), time.Now()) {
		t.Fatal("a composer that emptied after the Enter was reported as not taken")
	}
}

func TestConfirmSubmit_AComposerStillHoldingOurBodyIsNotTaken(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	nma5Paint(p, "❯ "+nma5Body)
	start := time.Now()
	if confirmSubmit(p, newPendingSubmit(nma5Body, "bracketed", ""), start) {
		t.Fatal("a composer still holding our body was reported as taken")
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
	if !confirmSubmit(p, newPendingSubmit(nma5Body, "bracketed", ""), time.Now()) {
		t.Fatal("the operator's restored draft was mistaken for our unsubmitted body")
	}
}

// A collapsed large paste of OUR size counts as ours.
func TestConfirmSubmit_ACollapsedPasteOfOurSizeIsStillOurs(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	body := longBody(60)
	nma5Paint(p, "❯ [Pasted text #1 +59 lines]")
	if confirmSubmit(p, newPendingSubmit(body, "bracketed", ""), time.Now()) {
		t.Fatal("a collapsed paste of our size, still in the composer, was reported as taken")
	}
}

// Under the 12-rune proof floor, a short message is still recognised.
func TestConfirmSubmit_AShortBodyStillInTheComposerIsNotTaken(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	nma5Paint(p, "❯ ok?")
	if confirmSubmit(p, newPendingSubmit("ok?", "bracketed", ""), time.Now()) {
		t.Fatal("a short body still in the composer was reported as taken")
	}
}

// No composer on screen is UNKNOWN, never a delivery.
func TestConfirmSubmit_NoComposerOnScreenIsNeverTaken(t *testing.T) {
	nma5Window(t, 200*time.Millisecond)
	p, _, _ := proofPane(t)
	nma5Paint(p, "Loading conversation...")
	if confirmSubmit(p, newPendingSubmit(nma5Body, "bracketed", ""), time.Now()) {
		t.Fatal("a screen with no composer was reported as a taken submit")
	}
}
