package tui

import (
	"strings"
	"testing"
	"time"
)

// ini-82c.1: Claude Code's rm safety prompt reads waiting. Captured on Claude
// Code 2.1.312, Haiku (claude --model haiku) in bypass mode, asked to run
// rm -rf "$(mktemp -d ...)": "Dangerous rm operation on statically-
// unresolvable target", "Claude Code will automatically block this request in
// 1:54", "Do you want to proceed? 1. Yes 2. No". Its notice arrives about ten
// seconds in, and until then the countdown's repaint each second read as
// running. testdata/haiku312_rm_guard_prompt_w*.txt is the prompt;
// footer_corpus/haiku312_rm_guard_autodenied_w*.txt is the screen after the
// countdown ran out. Account names replaced by a placeholder of the same
// length.

const rmgPreview = "Dangerous rm operation: do you want to proceed?"

func rmgPrompt(t *testing.T) []string { return lvxwFixture(t, "haiku312_rm_guard_prompt_w80.txt") }

// rmgPane is a live Claude pane at 80 columns, still producing output (the
// countdown repaints), showing rows.
func rmgPane(t *testing.T, rows []string) *Pane { return rmgPaneSized(t, rows, 80) }

func rmgPaneSized(t *testing.T, rows []string, cols int) *Pane {
	t.Helper()
	p, _, _ := proofPaneSized(t, cols, 30)
	p.attn = &attentionSignal{}
	p.agentType = "claude-code"
	p.alive = true
	p.lastOutputTime = time.Now()
	lvxwPaint(p, rows)
	return p
}

// rmgTick runs one maintenance tick and one frame's state refresh.
func rmgTick(p *Pane) {
	var last time.Time
	maintainModal([]PaneView{p}, time.Now(), &last)
	maintainPaneFrame(p)
}

func TestRmGuard_TheCaptureIsTheCountdownPrompt(t *testing.T) {
	joined := strings.Join(rmgPrompt(t), "\n")
	for _, want := range []string{"Dangerous rm operation", "automatically block this request in", "Do you want to proceed?", "❯ 1. Yes"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the capture lacks %q", want)
		}
	}
}

// AC1: within one tick, before any notice, the prompt reads waiting with its
// question as the preview, though the countdown keeps the pane producing
// output; never running, never pending.
func TestRmGuard_ThePromptReadsWaitingWithItsQuestion(t *testing.T) {
	// The 32-column capture, from the same build, reads the same.
	if p := rmgPaneSized(t, lvxwFixture(t, "haiku312_rm_guard_prompt_w32.txt"), 32); true {
		rmgTick(p)
		if w, _, preview := p.WaitingInput(); !w || preview != rmgPreview || p.Activity() != StateWaitingInput {
			t.Errorf("32 columns: waiting %v, preview %q, activity %v", w, preview, p.Activity())
		}
	}
	p := rmgPane(t, rmgPrompt(t))
	rmgTick(p)
	waiting, _, preview := p.WaitingInput()
	if !waiting || preview != rmgPreview {
		t.Fatalf("waiting %v, preview %q; want waiting with %q", waiting, preview, rmgPreview)
	}
	if got := p.Activity(); got != StateWaitingInput {
		t.Errorf("activity %v, want waiting", got)
	}
	if p.WaitingTierOf() != WaitingTierListOnly {
		t.Errorf("tier %v before the notice, want list-only (no chime)", p.WaitingTierOf())
	}
}

// The notice that arrives later keeps the question as the preview and raises
// the tier as any permission notice does.
func TestRmGuard_TheLateNoticeKeepsThePreview(t *testing.T) {
	p := rmgPane(t, rmgPrompt(t))
	rmgTick(p)
	p.attn.noteNotify("Claude needs your permission")
	p.refreshWaitingState()
	_, _, preview := p.WaitingInput()
	if preview != rmgPreview {
		t.Errorf("after the notice the preview is %q, want %q", preview, rmgPreview)
	}
	if p.WaitingTierOf() != WaitingTierChime {
		t.Errorf("tier %v after the notice, want chime", p.WaitingTierOf())
	}
	// The notice alone, with nothing raised from the screen first, gives the
	// same preview: one classifier for both paths.
	q := rmgPane(t, rmgPrompt(t))
	q.attn.noteNotify("Claude needs your permission")
	q.refreshWaitingState()
	if _, _, preview := q.WaitingInput(); preview != rmgPreview {
		t.Errorf("the notice alone gave preview %q, want %q", preview, rmgPreview)
	}
}

// AC2: after the countdown auto-denies, the next tick clears the wait and the
// pane reads idle once quiet; the same after an answer, whose screen is the
// input box back.
func TestRmGuard_ItClearsWhenTheCountdownBlocksIt(t *testing.T) {
	p := rmgPane(t, rmgPrompt(t))
	rmgTick(p)
	if w, _, _ := p.WaitingInput(); !w {
		t.Fatal("precondition: waiting")
	}
	lvxwPaint(p, dmiScreen(t, "haiku312_rm_guard_autodenied_w80.txt"))
	p.mu.Lock()
	p.lastOutputTime = time.Now().Add(-time.Minute)
	p.mu.Unlock()
	rmgTick(p)
	if w, _, preview := p.WaitingInput(); w {
		t.Fatalf("still waiting (%q) a tick after the prompt closed", preview)
	}
	if got := p.Activity(); got != StateIdle {
		t.Errorf("activity %v after the prompt closed and the pane went quiet, want idle", got)
	}
}

// AC3: a send while the prompt is up is held for the dialog.
func TestRmGuard_ASendIsHeldWhileThePromptIsUp(t *testing.T) {
	prompt := rmgPrompt(t)
	f := &pkinFake{
		body:  "[from b] a message while the prompt is up",
		takes: func(int) bool { return true },
		screen: func(string) string {
			return "\x1b[2J\x1b[H" + strings.Join(prompt, "\r\n")
		},
	}
	p, _ := pkinSendRigSized(t, f, 80, 30)
	f.paint("")
	if v := pkinSend(p, f.body); v.Outcome != DeliveryDeferred {
		t.Fatalf("a send with the prompt up answered %q (%s), want deferred", v.Outcome, v.Reason)
	}
	if n := f.enters.Load(); n != 0 {
		t.Errorf("%d Enters pressed into the prompt", n)
	}
}

// AC4, feed it nothing: an empty screen; the question line alone; the rm text
// quoted in the transcript above a resting input box; and the twin, the
// countdown at zero with the prompt still drawn, which is still waiting.
func TestRmGuard_WhatIsNotThePrompt(t *testing.T) {
	box := []string{"────────────────────────────────────────────────────────────────────────────────", "❯ ", "────────────────────────────────────────────────────────────────────────────────", "  Haiku 5.5 │ Acct │ 5% │ 40%"}
	for name, rows := range map[string][]string{
		"empty screen":        nil,
		"question line alone": {" Do you want to proceed?"},
		// In the transcript, as an agent quoting it would print it: output
		// between the quotation and the box. (Option rows directly on top of
		// the box are read as a live dialog drawn over it: answerRowJustAbove.)
		"quoted above a box": append([]string{
			" │ Dangerous rm operation on statically-unresolvable target",
			" Do you want to proceed?", " ❯ 1. Yes", "   2. No",
			"⏺ That was the prompt I saw earlier; nothing is waiting now.", ""}, box...),
	} {
		if got := silentClaudeDialogPreview(rows); got != "" {
			t.Errorf("%s: read as the prompt (%q)", name, got)
		}
	}
	zero := append([]string{}, rmgPrompt(t)...)
	for i, r := range zero {
		if strings.Contains(r, "automatically block this request in") {
			zero[i] = " ⚠ Claude Code will automatically block this request in 0:00, to avoid holding"
		}
	}
	p := rmgPane(t, zero)
	rmgTick(p)
	if w, _, _ := p.WaitingInput(); !w {
		t.Error("the prompt still drawn at 0:00 is not waiting")
	}
}
