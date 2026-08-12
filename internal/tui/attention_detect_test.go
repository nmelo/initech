package tui

// attention_detect_test.go covers tier-1 detection (ini-2x8.2): OSC 777 raises
// the waiting state, and the screen -- and only a screen that has proved it can
// see the dialog -- retires it.
//
// Dialog text in these fixtures is transcribed from the real captures recorded
// on ini-2x8 (Claude Code 2.1.229, driven under a PTY), not invented.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// dialogPane builds a pane with an emulator big enough to hold a real dialog.
func dialogPane(name string) *Pane {
	p := &Pane{
		name:    name,
		emu:     vt.NewSafeEmulator(120, 24),
		alive:   true,
		visible: true,
		cfg:     PaneConfig{BeadsEnabled: true},
		attn:    &attentionSignal{},
	}
	registerAttentionOSC(p)
	return p
}

// paint writes lines into the pane's emulator as if the agent had rendered them,
// BOTTOM-ALIGNED like a real dialog. Alignment is not cosmetic here: the modal
// scanner reads only the last modalScanRows rows, so a top-aligned fixture would
// be invisible to the very code under test and every screen assertion would fail
// for the wrong reason.
func paint(t *testing.T, p *Pane, lines ...string) {
	t.Helper()
	const emuRows = 24
	if _, err := p.emu.Write([]byte("\x1b[2J\x1b[H")); err != nil {
		t.Fatalf("clear: %v", err)
	}
	pad := emuRows - len(lines)
	if pad < 0 {
		pad = 0
	}
	out := strings.Repeat("\r\n", pad) + strings.Join(lines, "\r\n")
	if _, err := p.emu.Write([]byte(out)); err != nil {
		t.Fatalf("paint: %v", err)
	}
}

// Real AskUserQuestion dialog, transcribed from the capture.
var askUserQuestionDialog = []string{
	"────────────────────────────────────────────────────────",
	"☐ Deploy target",
	"Should the deploy target be staging or production?",
	"❯ 1. Staging",
	"    Deploy to the staging environment first — safer.",
	"  2. Production",
	"    Deploy straight to production — changes go live.",
	"  3. Type something.",
	"  4. Chat about this",
	"Enter to select · ↑/↓ to navigate · Esc to cancel",
}

// Real permission prompt, transcribed from the capture.
var permissionPromptDialog = []string{
	"────────────────────────────────────────────────────────",
	"Bash command",
	"date +%s",
	"Print current Unix timestamp",
	"Permission rule",
	"Bash requires confirmation for this command. /permissions to update rules",
	"Do you want to proceed?",
	"❯ 1. Yes",
	"  2. No",
	"Esc to cancel · Tab to amend · ctrl+e to explain",
}

// ── Raising ─────────────────────────────────────────────────────────

func TestRefreshWaitingState_OSCRaisesAtChimeGrade(t *testing.T) {
	p := dialogPane("super")
	paint(t, p, askUserQuestionDialog...)

	if _, err := p.emu.Write([]byte(measuredOSC777)); err != nil {
		t.Fatalf("write osc: %v", err)
	}
	p.refreshWaitingState()

	waiting, _, preview := p.WaitingInput()
	if !waiting {
		t.Fatal("OSC 777 did not raise the waiting state")
	}
	if p.WaitingTierOf() != WaitingTierChime {
		t.Errorf("tier = %v, want chime-grade", p.WaitingTierOf())
	}
	if !strings.Contains(preview, "staging or production") {
		t.Errorf("preview = %q, want the question text off the screen", preview)
	}
}

func TestRefreshWaitingState_DoesNotRaiseWithoutTheNotification(t *testing.T) {
	// A dialog on screen with NO OSC 777 must not raise. This is what keeps the
	// screen out of the chime path: the sound is staked on the app declaring its
	// own state, never on our reading of pixels.
	p := dialogPane("eng1")
	paint(t, p, permissionPromptDialog...)

	p.refreshWaitingState()

	if waiting, _, _ := p.WaitingInput(); waiting {
		t.Error("screen content alone raised the waiting state; only OSC 777 may raise it")
	}
}

func TestRefreshWaitingState_PermissionPromptPreviewNamesToolAndCommand(t *testing.T) {
	p := dialogPane("eng1")
	paint(t, p, permissionPromptDialog...)
	if _, err := p.emu.Write([]byte(measuredOSC777)); err != nil {
		t.Fatalf("write osc: %v", err)
	}

	p.refreshWaitingState()

	_, _, preview := p.WaitingInput()
	if !strings.Contains(preview, "date +%s") {
		t.Errorf("preview = %q, want the command; the approved frame shows 'Bash: <command>'", preview)
	}
	if !strings.HasPrefix(preview, "Bash:") {
		t.Errorf("preview = %q, want it to name the tool", preview)
	}
}

// ── Clearing ────────────────────────────────────────────────────────

func TestRefreshWaitingState_ScreenClearsTheRowOnceItHasSeenTheDialog(t *testing.T) {
	p := dialogPane("pm")
	paint(t, p, askUserQuestionDialog...)
	if _, err := p.emu.Write([]byte(measuredOSC777)); err != nil {
		t.Fatalf("write osc: %v", err)
	}

	p.refreshWaitingState() // raises, and the screen confirms the dialog
	if waiting, _, _ := p.WaitingInput(); !waiting {
		t.Fatal("setup: not waiting")
	}
	if !p.modalSeen() {
		t.Fatal("setup: the screen never confirmed the dialog")
	}

	// The operator answers: the dialog leaves the screen.
	paint(t, p, "❯ ", "the agent got back to work")
	p.refreshWaitingState()

	if waiting, _, _ := p.WaitingInput(); waiting {
		t.Error("row survived the dialog leaving the screen")
	}
}

// TestRefreshWaitingState_UnseenDialogIsNotClearedByTheScreen is the guard that
// makes the clear path safe. Only 2 of the 5 legacy modal patterns appear in a
// real dialog, so "no modal visible" routinely means "our patterns do not match
// this one" rather than "the operator answered". Retiring a row on that basis
// would make it flicker and vanish -- the original bug wearing a new hat.
func TestRefreshWaitingState_UnseenDialogIsNotClearedByTheScreen(t *testing.T) {
	p := dialogPane("qa4")
	// A dialog whose wording matches none of our patterns.
	paint(t, p, "some future dialog kind we have never captured", "[a] accept  [r] reject")
	if _, err := p.emu.Write([]byte(measuredOSC777)); err != nil {
		t.Fatalf("write osc: %v", err)
	}

	p.refreshWaitingState()
	if waiting, _, _ := p.WaitingInput(); !waiting {
		t.Fatal("OSC 777 should still raise a dialog we cannot read")
	}

	// Several more ticks with the screen still unable to see it.
	for i := 0; i < 5; i++ {
		p.refreshWaitingState()
	}

	if waiting, _, _ := p.WaitingInput(); !waiting {
		t.Error("a dialog the screen cannot see was retired anyway; the screen must " +
			"SEE a dialog before it is allowed to retire it")
	}
	if p.modalSeen() {
		t.Error("modalSeen set for a dialog no pattern matched")
	}
}

func TestRefreshWaitingState_DeadPaneClearsTheRow(t *testing.T) {
	// Without this the row outlives the agent with no way to retire it: the
	// screen can no longer change, so the screen can never clear it.
	p := dialogPane("eng2")
	paint(t, p, askUserQuestionDialog...)
	if _, err := p.emu.Write([]byte(measuredOSC777)); err != nil {
		t.Fatalf("write osc: %v", err)
	}
	p.refreshWaitingState()
	if waiting, _, _ := p.WaitingInput(); !waiting {
		t.Fatal("setup: not waiting")
	}

	p.mu.Lock()
	p.alive = false
	p.mu.Unlock()
	p.refreshWaitingState()

	if waiting, _, _ := p.WaitingInput(); waiting {
		t.Error("row survived the agent's death")
	}
}

func TestClearWaitingInput_ResetsTheScreenConfirmation(t *testing.T) {
	// A stale confirmation would let the NEXT wait be retired by a screen that
	// has not seen it -- the clear-too-early bug, one dialog later.
	p := dialogPane("pm")
	paint(t, p, askUserQuestionDialog...)
	if _, err := p.emu.Write([]byte(measuredOSC777)); err != nil {
		t.Fatalf("write osc: %v", err)
	}
	p.refreshWaitingState()
	if !p.modalSeen() {
		t.Fatal("setup: screen never confirmed")
	}

	p.ClearWaitingInput()

	if p.modalSeen() {
		t.Error("screen confirmation survived the clear and would leak into the next wait")
	}
}

// ── The wait clock across a full cycle ──────────────────────────────

func TestRefreshWaitingState_RepeatedTicksDoNotRestartTheClock(t *testing.T) {
	p := dialogPane("super")
	paint(t, p, askUserQuestionDialog...)
	if _, err := p.emu.Write([]byte(measuredOSC777)); err != nil {
		t.Fatalf("write osc: %v", err)
	}

	p.refreshWaitingState()
	_, first, _ := p.WaitingInput()

	for i := 0; i < 10; i++ {
		p.refreshWaitingState()
	}

	_, last, _ := p.WaitingInput()
	if !first.Equal(last) {
		t.Errorf("wait clock moved across ticks: %v then %v -- durations would never advance "+
			"and the 2-minute reminder would never come due", first, last)
	}
}

// ── Preview extraction ──────────────────────────────────────────────

func TestWaitingPreviewText_ReadsTheRealDialogs(t *testing.T) {
	q := dialogPane("a")
	paint(t, q, askUserQuestionDialog...)
	if got := q.waitingPreviewText(); !strings.Contains(got, "staging or production") {
		t.Errorf("AskUserQuestion preview = %q, want the question line", got)
	}

	perm := dialogPane("b")
	paint(t, perm, permissionPromptDialog...)
	if got := perm.waitingPreviewText(); !strings.Contains(got, "date +%s") {
		t.Errorf("permission preview = %q, want the command", got)
	}
}

func TestWaitingPreviewText_EmptyWhenItCannotTell(t *testing.T) {
	// Reporting nothing is correct here. The chime is staked on OSC 777, so a
	// preview miss costs a less informative row -- never a false or missed chime.
	p := dialogPane("c")
	paint(t, p, "❯ ", "just an ordinary prompt")
	if got := p.waitingPreviewText(); got != "" {
		t.Errorf("preview = %q, want empty when no dialog is recognisable", got)
	}
}

func TestNumberedOption_RecognisesDialogChoices(t *testing.T) {
	yes := []string{"1. Yes", "  2. No", "❯ 1. Staging", "3. Type something.", "10. Tenth"}
	for _, s := range yes {
		if !numberedOption(s) {
			t.Errorf("numberedOption(%q) = false, want true", s)
		}
	}
	no := []string{"Should the deploy target be staging or production?", "Bash command", "", "date +%s"}
	for _, s := range no {
		if numberedOption(s) {
			t.Errorf("numberedOption(%q) = true, want false", s)
		}
	}
}
