package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ini-zrdt: Claude Code's Write/Edit permission prompt ("Do you want to
// create w.txt?" over "❯ 1. Yes" ... "Esc to cancel · Tab to amend") matched
// none of the dialog phrases. The OSC 777 latch it raises was never
// corroborated, the 90 s heal cleared it, and held mail was pasted into the
// picker; after the answer, the needs-input row never cleared.
//
// The haiku_* screens are real: Claude Code 2.1.306 on Haiku 5.5 in default
// permission mode, captured from a 120x40 tmux pane for this bead (user name
// and temp path replaced).

func zrdtScreen(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// zrdtPromptPane is a held-mail pane showing the real Write prompt, with its
// PTY writes captured, its dialog latch raised, and two messages held.
func zrdtPromptPane(t *testing.T, prompt string) (*TUI, *Pane, chan AgentEvent, func() string) {
	t.Helper()
	tui := newTestTUI()
	p, events, _ := heldMailPane(t, "eng2")
	p.emu = claudeScreen(t, 40, zrdtScreen(t, prompt))
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []byte
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			mu.Lock()
			got = append(got, buf[:n]...)
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { w.Close(); r.Close() })
	p.ptmx = &filePty{w}
	tui.panes = []PaneView{p}
	p.latchDialogOpen("Claude needs your permission")
	p.EnqueueMessage("[from super] one", true)
	p.EnqueueMessage("[from pm] two", true)
	written := func() string { mu.Lock(); defer mu.Unlock(); return string(got) }
	return tui, p, events, written
}

// Criterion 1: with the real prompt on screen and two held messages, 95 s of
// maintenance ticks keep the latch, drain nothing, paste nothing and release
// nothing. At v2.27 the heal cleared the latch at 90 s and the drain pasted.
func TestPermissionPrompt_HeldMailStaysHeldPastTheHeal(t *testing.T) {
	for _, prompt := range []string{"haiku_write_prompt.txt", "haiku_edit_prompt.txt"} {
		tui, p, events, written := zrdtPromptPane(t, prompt)
		raise := time.Now()
		p.mu.Lock()
		p.dialogOpenAt = raise
		p.mu.Unlock()
		for s := 0; s <= 95; s++ {
			tui.lastModalMaint = time.Time{}
			tui.modalMaintenance(raise.Add(time.Duration(s) * time.Second))
		}
		time.Sleep(100 * time.Millisecond)
		if !p.dialogLatched() {
			t.Errorf("%s: the latch cleared under an open prompt", prompt)
		}
		if q := p.QueuedMessageCount(); q != 2 {
			t.Errorf("%s: %d messages still held, want 2", prompt, q)
		}
		if w := written(); strings.Contains(w, "[from super] one") || strings.Contains(w, "[from pm] two") {
			t.Errorf("%s: held mail was pasted into the prompt: %q", prompt, w)
		}
		for len(events) > 0 {
			if ev := <-events; strings.Contains(ev.Detail, "held mail released") {
				t.Errorf("%s: %q while the prompt was open", prompt, ev.Detail)
			}
		}
	}
}

// Criterion 2: the prompt answered, the resting composer back: the
// needs-input row clears within a tick and the pane is no longer waiting.
func TestPermissionPrompt_TheWaitingRowClearsOnceAnswered(t *testing.T) {
	p := testPane("eng2")
	p.attn = &attentionSignal{} // as NewPane wires it; without it nothing is read
	p.emu = claudeScreen(t, 40, zrdtScreen(t, "haiku_write_prompt.txt"))
	p.SetWaitingInputTier("Claude needs your permission", WaitingTierChime)
	p.refreshWaitingState()
	if !p.modalSeen() {
		t.Fatal("the real Write prompt on screen was not seen as the dialog")
	}
	p.emu = claudeScreen(t, 40, zrdtScreen(t, "haiku_after_esc.txt"))
	p.refreshWaitingState()
	if waiting, _, preview := p.WaitingInput(); waiting {
		t.Fatalf("still waiting (%q) one tick after the prompt left the screen", preview)
	}
}

// Criterion 3: a forwarded click on the pane while the prompt is on screen.
// The click clears the latch; the next tick sees the prompt and the send
// guard still defers, so nothing is pasted.
func TestPermissionPrompt_AClickDoesNotOpenTheGuard(t *testing.T) {
	tui, p, _, written := zrdtPromptPane(t, "haiku_write_prompt.txt")
	p.noteOperatorInput() // what a forwarded click does
	tui.lastModalMaint = time.Time{}
	tui.modalMaintenance(time.Now())
	time.Sleep(100 * time.Millisecond)
	if !paneHasModal(p) {
		t.Fatal("after a click the send guard sees no dialog under the open prompt")
	}
	if w := written(); strings.Contains(w, "[from super] one") {
		t.Fatalf("held mail was pasted after a click: %q", w)
	}
}

// Criterion 4: no false positives. Real idle and busy Haiku screens, and the
// existing classifications, are unchanged; the real prompts and the trust
// prompt are dialogs.
func TestPermissionPrompt_ClassifiesRealScreens(t *testing.T) {
	for name, wantDialog := range map[string]bool{
		"haiku_write_prompt.txt":       true,
		"haiku_edit_prompt.txt":        true,
		"haiku_trust_prompt.txt":       true,
		"haiku_idle.txt":               false,
		"haiku_after_esc.txt":          false,
		"haiku_busy_spinner.txt":       false,
		"haiku_busy_streaming.txt":     false,
		"haiku_idle_list_question.txt": false,
		// The closest real shape: a question ending in "?" directly above a
		// visible "1." row, at rest and mid-turn. No cursor, no "Esc to cancel".
		"haiku_idle_question_list.txt": false,
		"haiku_busy_question_list.txt": false,
	} {
		rows := screenOf(claudeScreen(t, 40, zrdtScreen(t, name)))
		if got := screenShowsModal(rows); got != wantDialog {
			t.Errorf("%s: send guard says dialog=%v, want %v", name, got, wantDialog)
		}
		if !wantDialog && screenShowsDialogText(rows) {
			t.Errorf("%s: an ordinary screen reads as dialog text", name)
		}
		if !wantDialog && !strings.Contains(name, "trust") && !screenShowsIdleComposer(rows) && !strings.Contains(name, "busy") {
			t.Errorf("%s: a resting screen no longer reads as idle", name)
		}
	}
}

// Feed it nothing: an empty screen, a question line alone, and "Esc to
// cancel" alone are not dialogs.
func TestPermissionPrompt_FragmentsAreNotDialogs(t *testing.T) {
	for name, lines := range map[string][]string{
		"empty":              {""},
		"question only":      {" Do you want to create w.txt?"},
		"esc to cancel only": {" Esc to cancel · Tab to amend"},
		"numbered answer":    {" Which one?", "  1. Keep a schedule", "  2. Dim the lights", "  3. No screens"},
	} {
		if screenShowsModal(screenOf(claudeScreen(t, 40, lines))) {
			t.Errorf("%s: reads as a dialog", name)
		}
	}
}

// Each signature is sufficient on its own, so each gets a screen only it can
// recognise, built from the real Write prompt.
//   - shape only: the "switch to accept edits" option removed (a prompt
//     whose second option differs): question, cursor and footer remain.
//   - phrase only: the question line gone (scrolled off a short pane): the
//     "Yes, and switch to accept edits" row remains.
func TestPermissionPrompt_EachSignatureIsSufficientAlone(t *testing.T) {
	real := zrdtScreen(t, "haiku_write_prompt.txt")
	var shapeOnly, phraseOnly []string
	for _, l := range real {
		if !strings.Contains(l, "switch to accept edits") {
			shapeOnly = append(shapeOnly, strings.Replace(l, "3. No", "2. No", 1))
		}
		if !strings.Contains(l, "Do you want to create") {
			phraseOnly = append(phraseOnly, l)
		}
	}
	for name, lines := range map[string][]string{"shape only": shapeOnly, "phrase only": phraseOnly} {
		if !screenShowsModal(screenOf(claudeScreen(t, 40, lines))) {
			t.Errorf("%s: the prompt is not seen as a dialog", name)
		}
	}
}
