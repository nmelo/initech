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
