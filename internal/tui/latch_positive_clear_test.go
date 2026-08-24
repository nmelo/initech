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
	var stable time.Duration
	for i := 0; i <= int(idlePromptStableWindow/time.Second); i++ {
		stable = p.observeIdlePrompt(now.Add(time.Duration(i)*time.Second), paneShowsIdleComposer(p))
	}
	if !p.auditCorroboratedLatch(stable) {
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

	var stable time.Duration
	for i := 0; i <= 30; i++ { // far past the window
		stable = p.observeIdlePrompt(now.Add(time.Duration(i)*time.Second), paneShowsIdleComposer(p))
	}
	if p.auditCorroboratedLatch(stable) {
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
	stable := p.observeIdlePrompt(now, true)
	if p.auditCorroboratedLatch(stable) {
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

// An UNCORROBORATED latch belongs to auditDialogLatch's window, not to this
// path, and the distinction is load-bearing. A dialog declared over OSC 777
// has not rendered yet, so the pane still shows the PREVIOUS idle prompt --
// clearing on that would drop the latch for a dialog that is about to appear,
// which is the forged answer this guard exists to prevent. Found by mutant:
// removing the corroboration check left every other cell green.
func TestLatch_UncorroboratedLatchIsNotClearedByThisPath(t *testing.T) {
	p := latchedPane(t)
	p.mu.Lock()
	p.dialogCorroborated = false // declared, not yet seen
	p.dialogOpenAt = time.Now()  // just raised, inside the corroboration window
	p.mu.Unlock()
	showIdlePrompt(p) // the screen the dialog has not yet painted over

	now := time.Now()
	var stable time.Duration
	for i := 0; i <= 30; i++ {
		stable = p.observeIdlePrompt(now.Add(time.Duration(i)*time.Second), paneShowsIdleComposer(p))
	}
	if p.auditCorroboratedLatch(stable) {
		t.Error("cleared a latch sight never corroborated: a dialog still painting would lose its guard")
	}
}
