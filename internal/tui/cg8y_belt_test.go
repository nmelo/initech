package tui

import (
	"strings"
	"testing"
	"time"
)

// ini-cg8y: the belt read Claude's "› stashed" hint row as the composer and
// withheld every send; the retry never delivered a body sitting in the
// composer; a later send left an earlier withhold without a fate.

const cg8yBody = "please rebase the inbox branch and run make check before pushing"

// What run 1 found on a real Claude screen: our body in the ❯ composer, the
// stash hint below it.
func cg8yStashedScreen(composerText string) string {
	return "\x1b[2J\x1b[H────────\r\n❯ " + composerText + "\r\n────────\r\n  status line\r\n               › stashed"
}

func TestComposerTail_ClaudesPromptWinsOverAStashHintBelowIt(t *testing.T) {
	p, _, _ := proofPane(t)
	p.emu.Write([]byte(cg8yStashedScreen(cg8yBody)))
	tail, found := composerTail(p)
	if !found || tail != cg8yBody {
		t.Fatalf("composer tail = %q (found %v); the \"› stashed\" hint must not read as the composer", tail, found)
	}
}

func TestComposerTail_AScreenWithNoClaudePromptStillFallsBackToTheOtherGlyphs(t *testing.T) {
	p, _, _ := proofPane(t)
	p.emu.Write([]byte("\x1b[2J\x1b[H› codex input"))
	if tail, found := composerTail(p); !found || tail != "codex input" {
		t.Fatalf("Codex-style prompt: tail = %q (found %v)", tail, found)
	}
}

// The cascade from run 1: the body is in the real composer, the hint sits
// below. The belt must accept rather than withhold.
func TestBelt_AcceptsOurBodyInTheComposerAboveAStashHint(t *testing.T) {
	p, _, _ := proofPane(t)
	p.emu.Write([]byte(cg8yStashedScreen(cg8yBody)))
	accepted, last := composerAcceptedBody(p, cg8yBody, newPendingSubmit(cg8yBody, "bracketed", ""))
	if !accepted {
		t.Fatalf("belt withheld a body sitting in the composer (last polled %q)", last)
	}
}

// The retry delivered 0 times in a month: with our text visible it must
// deliver even though the tail never changed since the withhold.
func TestWithheldRetry_DeliversWhenOurTextIsVisibleWithoutATransition(t *testing.T) {
	p, submits, events := proofPane(t)
	p.emu.Write([]byte(cg8yStashedScreen(cg8yBody)))
	tail, _ := composerTail(p)
	ps := newPendingSubmit(cg8yBody, "bracketed", tail) // tail at withhold == tail now
	p.pendingSubmit = ps
	p.maybeRetryWithheldSubmit()
	select {
	case <-submits:
	case <-time.After(time.Second):
		t.Fatal("the retry did not send Enter for a body provably in the composer")
	}
	select {
	case ev := <-events:
		if !strings.HasPrefix(ev.Detail, deliveredLateSubmitPrefix) {
			t.Fatalf("event %q, want the late-delivery report", ev.Detail)
		}
	case <-time.After(time.Second):
		t.Fatal("no DELIVERED report")
	}
	if p.pendingSubmit != nil {
		t.Fatal("pending submit still armed after delivery")
	}
}

// AC 2: a send arriving at a pane with a pending submit settles it first.
func TestSettlePendingSubmit_DeliversAStrandedBodyStillInTheComposer(t *testing.T) {
	p, submits, events := proofPane(t)
	p.emu.Write([]byte(cg8yStashedScreen(cg8yBody)))
	p.pendingSubmit = newPendingSubmit(cg8yBody, "bracketed", "something else")
	p.settlePendingSubmit()
	select {
	case <-submits:
	case <-time.After(time.Second):
		t.Fatal("the stranded body was not submitted before the next send")
	}
	ev := <-events
	if !strings.HasPrefix(ev.Detail, deliveredLateSubmitPrefix) {
		t.Fatalf("event %q, want DELIVERED", ev.Detail)
	}
	if p.pendingSubmit != nil {
		t.Fatal("pending submit survived its fate")
	}
}

func TestSettlePendingSubmit_SupersedesOneWhoseBodyIsGone(t *testing.T) {
	p, submits, events := proofPane(t)
	p.emu.Write([]byte("\x1b[2J\x1b[H❯ "))
	p.pendingSubmit = newPendingSubmit(cg8yBody, "bracketed", "")
	p.settlePendingSubmit()
	select {
	case b := <-submits:
		t.Fatalf("pressed %q for a body that is not in the composer", b)
	case <-time.After(150 * time.Millisecond):
	}
	select {
	case ev := <-events:
		if !strings.HasPrefix(ev.Detail, undeliveredSubmitPrefix) || !strings.Contains(ev.Detail, "superseded") {
			t.Fatalf("event %q, want NOT delivered naming superseded", ev.Detail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a superseded withhold left no fate")
	}
	if p.pendingSubmit != nil {
		t.Fatal("pending submit survived its fate")
	}
}

func TestBeltEchoWindow_StretchesWithLoopLagAndCapsAt3x(t *testing.T) {
	old := loopMissedLastMinute.Load()
	t.Cleanup(func() { loopMissedLastMinute.Store(old) })
	for missed, want := range map[uint64]time.Duration{
		0: composerEchoWindow, 30: 2 * composerEchoWindow, 60: 3 * composerEchoWindow, 500: 3 * composerEchoWindow,
	} {
		loopMissedLastMinute.Store(missed)
		if got := beltEchoWindow(); got != want {
			t.Errorf("missed %d: window %s, want %s", missed, got, want)
		}
	}
}

// The wiring: the send path itself settles a pending submit (not only the
// helper in isolation).
func TestSendPath_SettlesAPendingSubmitBeforeTheNextSend(t *testing.T) {
	p, _, events := proofPane(t)
	p.emu.Write([]byte("\x1b[2J\x1b[H❯ "))
	p.pendingSubmit = newPendingSubmit(cg8yBody, "bracketed", "")
	p.sendMu.Lock()
	sendPaneTextLocked(p, "the next message", false)
	p.sendMu.Unlock()
	select {
	case ev := <-events:
		if !strings.Contains(ev.Detail, "superseded") {
			t.Fatalf("first event %q, want the superseded fate of the earlier withhold", ev.Detail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the send path left the earlier withhold without a fate")
	}
}

// Safety: a withheld submit is never delivered while a dialog is on screen,
// even if the dialog shows text matching our body.
func TestWithheldRetry_NeverDeliversIntoADialogShowingOurText(t *testing.T) {
	p, submits, _ := proofPane(t)
	renderAskUserQuestionModal(p.emu)
	p.emu.Write([]byte("\r\n❯ " + cg8yBody))
	if !paneHasModal(p) {
		t.Fatal("fixture: the dialog is not detected")
	}
	ps := newPendingSubmit(cg8yBody, "bracketed", "")
	p.pendingSubmit = ps
	p.maybeRetryWithheldSubmit()
	p.settlePendingSubmit()
	select {
	case b := <-submits:
		t.Fatalf("pressed %q into an open dialog", b)
	case <-time.After(200 * time.Millisecond):
	}
}

// Run 2: in a 10-row pane the wrapped body pushed Claude's ❯ row off the top
// of the screen; the rows above the bottom rule still hold our text.
func cg8yScrolledOffScreen(withRule bool) string {
	words := strings.Fields(cg8yBody)
	s := "\x1b[2J\x1b[H"
	for i := 0; i < len(words); i += 3 {
		end := min(i+3, len(words))
		s += strings.Join(words[i:end], " ") + "\r\n"
	}
	if withRule {
		s += "────────────────────────────────────\r\n"
	}
	return s + "  status line"
}

func TestBelt_AcceptsOurWrappedBodyWhenThePromptRowScrolledOff(t *testing.T) {
	p, _, _ := proofPane(t)
	p.emu.Write([]byte(cg8yScrolledOffScreen(true)))
	if _, found := composerTail(p); found {
		t.Fatal("fixture invalid: a prompt glyph is on screen")
	}
	accepted, _ := composerAcceptedBody(p, "", newPendingSubmit(cg8yBody, "bracketed", ""))
	if !accepted {
		t.Fatal("belt withheld a body sitting above the composer's bottom rule")
	}
}

// Negative control: with no rule there is no composer evidence at all, and
// the belt must still withhold rather than guess.
func TestBelt_WithholdsWhenNeitherPromptNorRuleIsOnScreen(t *testing.T) {
	p, _, _ := proofPane(t)
	p.emu.Write([]byte(cg8yScrolledOffScreen(false)))
	if bodyTextVisible(p, newPendingSubmit(cg8yBody, "bracketed", "")) {
		t.Fatal("text on a screen with no composer frame counted as composer evidence")
	}
}

func TestRuleBoundedRegion_StopsAtThePreviousRule(t *testing.T) {
	rows := []string{"old output", "──────────────", "line one", "line two", "──────────────", "status"}
	got, ok := ruleBoundedRegion(rows)
	if !ok || strings.Join(got, "|") != "line one|line two" {
		t.Fatalf("region = %q (ok %v)", got, ok)
	}
}

// The fallback region is positive evidence only when no dialog is up: a
// dialog that quotes our text above a rule must never read as our composer.
func TestBelt_FallbackRegionIsIgnoredWhileADialogIsOpen(t *testing.T) {
	p, _, _ := proofPane(t)
	p.emu.Write([]byte(cg8yScrolledOffScreen(true)))
	p.mu.Lock()
	p.dialogOpen = true // a latched dialog whose box has scrolled out of view
	p.mu.Unlock()
	if !paneHasModal(p) {
		t.Fatal("fixture: the dialog is not detected")
	}
	if _, found := composerTail(p); found {
		t.Fatal("fixture invalid: a prompt glyph is on screen")
	}
	if accepted, _ := composerAcceptedBody(p, "", newPendingSubmit(cg8yBody, "bracketed", "")); accepted {
		t.Fatal("belt accepted text above a rule while a dialog is open")
	}
}
