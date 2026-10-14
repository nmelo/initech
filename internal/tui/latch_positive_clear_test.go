package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// latchedPane returns a pane with a CORROBORATED dialog latch: the state that
// before ini-gbqc had exactly one clear, an operator keystroke into that pane.
func latchedPane(t *testing.T) *Pane {
	t.Helper()
	p := &Pane{name: "super", emu: vt.NewSafeEmulator(80, 24), eventCh: make(chan AgentEvent, 8)}
	p.dialogOpen = true
	p.dialogCorroborated = true
	p.dialogOpenAt = time.Now().Add(-2 * time.Hour) // the live specimen's age
	return p
}

// showIdlePrompt paints the pane's normal resting composer.
func showIdlePrompt(p *Pane) {
	_, _ = p.emu.Write([]byte("\x1b[2J\x1b[24;1H❯ "))
}

// showNothing paints a screen with neither a dialog nor a prompt: the
// SCROLLED-AWAY case.
func showNothing(p *Pane) {
	_, _ = p.emu.Write([]byte("\x1b[2J\x1b[10;1Hbuild output line with no prompt and no dialog"))
}

// A dialog that closed WITHOUT operator input must release the mail.
func TestLatch_IdlePromptClearsACorroboratedLatch(t *testing.T) {
	p := latchedPane(t)
	showIdlePrompt(p)
	now := time.Now()

	if !paneHasModal(p) {
		t.Fatal("fixture failed: the pane is not latched to begin with")
	}
	// Stable across the window, one observation per maintenance tick.
	p.mu.Lock()
	p.dialogOpenAt = now // raised at the start of the observations
	p.mu.Unlock()
	var stable time.Duration
	last := now
	for i := 0; i <= int(idlePromptStableWindow/time.Second); i++ {
		last = now.Add(time.Duration(i) * time.Second)
		stable = p.observeIdlePrompt(last, paneShowsIdleComposer(p))
	}
	if !p.auditIdleLatch(last) {
		t.Fatalf("a corroborated latch survived %s of idle prompt; mail stays held forever", stable)
	}
	if paneHasModal(p) {
		t.Error("latch cleared but paneHasModal still defers")
	}
}

// THE LINE NOT TO CROSS (ini-2jpo). A dialog that merely scrolled out of view
// shows neither dialog nor prompt, and must NOT clear: saying "closed" there
// forges an operator answer into a dialog that is genuinely open.
func TestLatch_ScrolledAwayDialogDoesNotClear(t *testing.T) {
	p := latchedPane(t)
	showNothing(p)
	now := time.Now()

	last := now
	for i := 0; i <= 30; i++ { // far past the window
		last = now.Add(time.Duration(i) * time.Second)
		p.observeIdlePrompt(last, paneShowsIdleComposer(p))
	}
	if p.auditIdleLatch(last) {
		t.Error("FORGED: a scrolled-away dialog was declared closed on the absence of a dialog")
	}
	if !paneHasModal(p) {
		t.Error("the latch dropped for a pane showing neither prompt nor dialog")
	}
}

// A rendered dialog is not an idle prompt, however long it sits there.
func TestLatch_VisibleDialogNeverCountsAsIdle(t *testing.T) {
	p := latchedPane(t)
	_, _ = p.emu.Write([]byte("\x1b[2J\x1b[20;1HDo you want to proceed?\r\n❯ 1. Yes\r\n  2. No"))
	if paneShowsIdleComposer(p) {
		t.Error("a screen rendering a dialog was read as the idle composer")
	}
}

// Age alone must never clear it: that is the ini-9gvn reasoning this bead
// explicitly preserves.
func TestLatch_AgeAloneDoesNotClearACorroboratedLatch(t *testing.T) {
	p := latchedPane(t)
	showNothing(p)
	if p.auditDialogLatch(time.Now()) {
		t.Error("the age audit downgraded a CORROBORATED latch; that forges an operator answer")
	}
}

// A brief repaint between two dialogs is not a return to rest.
func TestLatch_MomentaryPromptDoesNotClear(t *testing.T) {
	p := latchedPane(t)
	showIdlePrompt(p)
	now := time.Now()
	p.observeIdlePrompt(now, true)
	if p.auditIdleLatch(now) {
		t.Error("one observation of a prompt cleared the latch; a repaint between dialogs would do it")
	}
}

// The release must be legible: a silent clear is the invisibility class this
// fleet keeps paying for.
func TestLatch_ReleaseIsAnnounced(t *testing.T) {
	logs := captureLogs(t)
	tui := &TUI{}
	p := latchedPane(t)
	showIdlePrompt(p)
	tui.panes = toPaneViews([]*Pane{p})

	base := time.Now()
	for i := 0; i <= int(idlePromptStableWindow/time.Second)+1; i++ {
		tui.lastModalMaint = time.Time{}
		tui.modalMaintenance(base.Add(time.Duration(i) * time.Second))
	}
	if paneHasModal(p) {
		t.Fatal("modalMaintenance never released the latch")
	}
	if out := logs.String(); !strings.Contains(out, "returned to its idle prompt") {
		t.Errorf("the release left no record at default level: %q", out)
	}
}

// An UNCORROBORATED latch is cleared by the idle prompt too (ini-0g5s), but
// only by idle time AFTER the raise. A dialog declared over OSC 777 may not
// have painted yet, so the pane can still show the previous idle prompt: that
// screen must never count. Inside the window after the raise the latch stands;
// past it, with the prompt idle and no dialog painted, the raise was not a
// dialog and the mail goes.
func TestLatch_UncorroboratedLatchClearsOnlyOnIdleTimeAfterTheRaise(t *testing.T) {
	p := latchedPane(t)
	showIdlePrompt(p) // the screen the dialog has not (yet) painted over
	base := time.Now()
	// Idle for a long time BEFORE the raise.
	for i := 0; i <= 30; i++ {
		p.observeIdlePrompt(base.Add(time.Duration(i)*time.Second), paneShowsIdleComposer(p))
	}
	raise := base.Add(30 * time.Second)
	p.mu.Lock()
	p.dialogCorroborated = false // declared, not seen
	p.dialogOpenAt = raise
	p.mu.Unlock()

	for i := 1; i < int(idlePromptStableWindow/time.Second); i++ {
		at := raise.Add(time.Duration(i) * time.Second)
		p.observeIdlePrompt(at, paneShowsIdleComposer(p))
		if p.auditIdleLatch(at) {
			t.Fatalf("cleared %ds after the raise, counting idle time from before it: a dialog still painting would lose its guard", i)
		}
	}
	at := raise.Add(idlePromptStableWindow)
	p.observeIdlePrompt(at, paneShowsIdleComposer(p))
	if !p.auditIdleLatch(at) {
		t.Error("an unconfirmed latch survived a full window of idle prompt after the raise; quoted dialog text holds mail")
	}
}
