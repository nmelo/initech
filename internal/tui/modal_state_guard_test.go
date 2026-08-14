package tui

// modal_state_guard_test.go — regressions for ini-zjhg.
//
// THE BUG, in one sentence: paneHasModal used to be a predicate about what was
// RENDERED, so a dialog that was open and waiting but had scrolled out of the
// pane's rows answered "no modal", the drain fired, and the queued message's
// trailing submit key answered the operator's question for them (ini-543b
// captured the bytes: ESC[200~payload ESC[201~ 0x0a).
//
// The fix is two layers that fail under DIFFERENT conditions, and these tests
// exist to prove that independence rather than to assert it:
//
//	LAYER 1  a state latch raised by the application's own OSC 777 declaration
//	         and cleared only by operator input, unioned with the screen scan.
//	         Catches the open-but-scrolled-out dialog.
//	LAYER 2  a never-submit belt: the submit key goes out only once our own body
//	         has visibly landed in a composer. Catches a dialog layer 1 never
//	         heard about -- which is a REAL product state, not a hypothetical:
//	         a permission prompt with custom message text emits no allowlisted
//	         OSC 777 (see dialogNotifyTexts).
//
// Every assertion here is on BYTES THAT REACHED THE PTY, never on a predicate's
// own opinion of itself. ini-543b's whole lesson was that the instrument lies
// before the product does.

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/gdamore/tcell/v2"
	"golang.org/x/term"
)

// zjhgRig is a pane on a real PTY with a real emulator, plus a tap on
// everything the pane writes toward the agent.
type zjhgRig struct {
	p       *Pane
	emu     *vt.SafeEmulator
	mu      sync.Mutex
	ptyRaw  []byte
	enters  int
	echoing bool
}

// newZJHGRig builds the fixture. echoBody models a child that RENDERS what it
// is pasted; without it the fixture would describe a state no real agent
// reaches (input accepted, nothing painted), which is the fidelity trap that
// made the original drain test assert a submit into a composer that never
// showed the text.
func newZJHGRig(t *testing.T, echoBody bool) *zjhgRig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix pipes / raw PTY mode")
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	t.Cleanup(func() { ptmx.Close(); tty.Close() })
	oldState, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	t.Cleanup(func() { term.Restore(int(tty.Fd()), oldState) })

	emu := vt.NewSafeEmulator(80, 24)
	r := &zjhgRig{emu: emu, echoing: echoBody}
	r.p = &Pane{
		name:           "eng1",
		emu:            emu,
		alive:          true,
		ptmx:           &filePty{ptmx},
		attn:           &attentionSignal{},
		lastOutputTime: time.Now().Add(-(ptyIdleTimeout + time.Second)),
	}
	registerAttentionOSC(r.p)

	// Submit keys travel the emulator path; the body travels the PTY. Tap both.
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				r.mu.Lock()
				for _, b := range buf[:n] {
					if b == '\r' || b == '\n' {
						r.enters++
					}
				}
				r.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 512)
		for {
			tty.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := tty.Read(buf)
			if n > 0 {
				r.mu.Lock()
				r.ptyRaw = append(r.ptyRaw, buf[:n]...)
				echo := r.echoing
				r.mu.Unlock()
				if echo {
					if body, ok := bracketedPasteBody(buf[:n]); ok {
						_, _ = emu.Write([]byte(body))
					}
				}
			}
			if err != nil && !os.IsTimeout(err) {
				return
			}
		}
	}()
	return r
}

func (r *zjhgRig) bytes() (string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.ptyRaw), r.enters
}

// declareDialogViaOSC drives the REAL tier-1 path: the emulator parses an
// OSC 777 notify, the registered handler records it, and the render tick
// applies it. No test-only shortcut into the latch -- a hand-set flag would
// prove the latch works and nothing about whether anything raises it.
func (r *zjhgRig) declareDialogViaOSC(t *testing.T) {
	t.Helper()
	_, _ = r.emu.Write([]byte("\x1b]777;notify;Claude Code;Claude needs your permission\x07"))
	r.p.refreshWaitingState()
	if !r.p.dialogLatched() {
		t.Fatal("OSC 777 did not raise the dialog latch; the rest of this test would prove nothing")
	}
}

// scrollDialogOutOfView paints over the whole screen with an ordinary prompt.
// The dialog is STILL OPEN -- the agent is still waiting -- it has simply been
// pushed out of the rows the scan can see. This is the bug's exact state.
func (r *zjhgRig) scrollDialogOutOfView(t *testing.T) {
	t.Helper()
	_, _ = r.emu.Write([]byte("\x1b[2J\x1b[24;1H❯ "))
	if paneShowsModalOnScreen(r.p) {
		t.Fatal("the dialog is still visible to the screen scan; this test needs it scrolled out")
	}
}

// TestZJHG_ScrolledOutDialogGetsNothing is the ini-543b repro, red before the
// fix: a dialog declared open, then scrolled out of the scan window, must
// receive neither a body nor a submit from either queue.
func TestZJHG_ScrolledOutDialogGetsNothing(t *testing.T) {
	r := newZJHGRig(t, true)
	r.declareDialogViaOSC(t)
	r.scrollDialogOutOfView(t)

	r.p.SendText("payload-A", true)
	if got := r.p.QueueLen(); got != 1 {
		t.Fatalf("send to a scrolled-out open dialog should DEFER, queue=1; got %d", got)
	}

	// Then the drain trigger fires, exactly as readLoop fires it on new output.
	r.p.maybeDrainModalQueue()
	time.Sleep(bracketedPasteSubmitDelay + 300*time.Millisecond)

	raw, enters := r.bytes()
	if strings.Contains(raw, "payload-A") {
		t.Errorf("the message body reached an open dialog. Claude's picker swallows the body and "+
			"reads the next submit as 'confirm the highlighted option'. PTY got %q", raw)
	}
	if enters != 0 {
		t.Errorf("a submit key reached an open dialog: that keystroke ANSWERS the operator's "+
			"question for them, which is the ini-543b P1. Enter count %d, PTY %q", enters, raw)
	}
	if got := r.p.QueueLen(); got != 1 {
		t.Errorf("the deferred message must survive for re-delivery after the dialog closes; queue=%d", got)
	}
}

// TestZJHG_OperatorInputReleasesTheQueue is the other half of the contract, and
// the reason the latch clears on input rather than on a timer: once the
// operator has acted, the queue must drain PROMPTLY. A guard that never
// releases is not a fix, it is delivery deferred to never (the g7fl lesson).
func TestZJHG_OperatorInputReleasesTheQueue(t *testing.T) {
	r := newZJHGRig(t, true)
	r.declareDialogViaOSC(t)
	r.scrollDialogOutOfView(t)

	r.p.SendText("payload-B", true)
	if got := r.p.QueueLen(); got != 1 {
		t.Fatalf("precondition: message should be deferred, queue=1; got %d", got)
	}

	// The operator answers the dialog. This is the ONLY thing that retires the
	// latch, because it is the only positive evidence an answer happened.
	r.p.SendKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if r.p.dialogLatched() {
		t.Fatal("operator input did not retire the dialog latch; queued messages would never drain")
	}

	r.p.maybeDrainModalQueue()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if raw, _ := r.bytes(); strings.Contains(raw, "payload-B") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	raw, enters := r.bytes()
	if !strings.Contains(raw, "\x1b[200~payload-B\x1b[201~") {
		t.Errorf("after the operator answered, the deferred body must be delivered; PTY got %q", raw)
	}
	if enters == 0 {
		t.Errorf("after the operator answered, the deferred message must also be SUBMITTED -- text "+
			"sitting unsubmitted in a composer is delayed delivery, not delivery. PTY %q", raw)
	}
	if got := r.p.QueueLen(); got != 0 {
		t.Errorf("queue should be empty after a successful drain, got %d", got)
	}
}

// TestZJHG_BeltHoldsWhenTheLatchNeverHeardOfTheDialog is the layer-independence
// test, and it is deliberately NOT a mutation: it is a real product state.
//
// A permission prompt whose message text is not in the OSC 777 allowlist raises
// nothing at tier 1 (dialogNotifyTexts documents exactly this tradeoff), and a
// dialog scrolled out of view is invisible to the screen scan. So layer 1 is
// blind here BY CONSTRUCTION, with no source edited. Only the belt is left, and
// the belt must still prevent a submission.
//
// The fixture does NOT echo: a picker swallows a pasted body without painting
// it, which is precisely what the belt detects. That was measured on real
// Claude 2.1.232 -- body into an open dialog left the composer tail byte-for-byte
// unchanged and nothing on screen moved.
// It runs on the PERMISSION PROMPT specifically -- the bug's own fixture, rows
// captured verbatim from real Claude 2.1.232 -- because a belt proven on some
// generic dialog is not proven on the one the operator actually loses questions
// to. The fixture is asserted to be recognisable BEFORE it is scrolled away, so
// a future edit that guts renderPermissionPrompt cannot quietly turn this test
// into a check on an empty screen.
func TestZJHG_BeltHoldsWhenTheLatchNeverHeardOfTheDialog(t *testing.T) {
	r := newZJHGRig(t, false)

	renderPermissionPrompt(r.emu)
	if !paneShowsModalOnScreen(r.p) {
		t.Fatal("fixture check: the captured permission prompt is not recognised as a modal, so " +
			"everything below would be measuring the wrong screen")
	}

	// A permission prompt carrying CUSTOM message text: dialogNotifyTexts
	// allowlists the default wording only, so this one raises nothing at tier 1.
	// Measured, twice, on the stock prompt: the default text DOES arrive
	// ("Claude needs your permission", allowlisted) -- so this is the custom-text
	// case the allowlist deliberately trades away, not the common one.
	_, _ = r.emu.Write([]byte("\x1b]777;notify;Claude Code;Some custom permission text\x07"))
	r.p.refreshWaitingState()
	if r.p.dialogLatched() {
		t.Fatal("this test requires layer 1 to be blind; the latch was raised")
	}
	r.scrollDialogOutOfView(t)
	if paneHasModal(r.p) {
		t.Fatal("this test requires BOTH terms of the guard to answer 'no modal'")
	}

	r.p.SendText("payload-C", true)
	time.Sleep(composerEchoWindow + bracketedPasteSubmitDelay + 300*time.Millisecond)

	raw, enters := r.bytes()
	if !strings.Contains(raw, "payload-C") {
		t.Fatalf("precondition: with both guard terms blind the body IS written -- that is what makes "+
			"this the dangerous state. PTY got %q", raw)
	}
	if enters != 0 {
		t.Errorf("the belt let a submit key out after a body that never reached a composer. With "+
			"layer 1 blind this is the last thing between a queued message and a forged operator "+
			"answer. Enter count %d, PTY %q", enters, raw)
	}
}

// TestZJHG_BeltPassesANormalDrain is the belt's other direction, and it is the
// assertion that keeps the fix honest: ini-zjhg AC item 2 forbids paying for
// forged-answer safety with silently undelivered messages. A correct drain into
// a real composer must still produce a COMPLETE, actionable message -- body and
// submit both.
func TestZJHG_BeltPassesANormalDrain(t *testing.T) {
	r := newZJHGRig(t, true)
	_, _ = r.emu.Write([]byte("\x1b[2J\x1b[24;1H❯ "))

	r.p.SendText("payload-D", true)
	time.Sleep(bracketedPasteSubmitDelay + 500*time.Millisecond)

	raw, enters := r.bytes()
	if !strings.Contains(raw, "\x1b[200~payload-D\x1b[201~") {
		t.Errorf("a normal send must deliver its body; PTY got %q", raw)
	}
	if enters == 0 {
		t.Errorf("a normal send must be SUBMITTED. A belt that withholds here has converted every "+
			"delivery into text sitting in a composer -- the regression AC item 2 names. PTY %q", raw)
	}
	if got := r.p.QueueLen(); got != 0 {
		t.Errorf("nothing should have been deferred; queue=%d", got)
	}
}

// TestZJHG_ResumeQueueCannotSubmitToAScrolledOutDialog covers the SECOND queue.
// ini-g7fl made suspended-agent messages survive and replay, and both queues
// drain through the same primitive -- so the guarantee has to be proven from
// the resume queue's own entry point, not inferred from the modal queue's.
// (The i7fr rule: a guard proven at one site says nothing about another site.)
func TestZJHG_ResumeQueueCannotSubmitToAScrolledOutDialog(t *testing.T) {
	r := newZJHGRig(t, true)
	r.declareDialogViaOSC(t)
	r.scrollDialogOutOfView(t)

	// Queue via the resume path's own API, then drain via its own drain.
	if dropped := r.p.EnqueueMessage("payload-E", true); dropped {
		t.Fatal("queue dropped the message before the test could run")
	}
	r.p.drainModalQueue()
	time.Sleep(bracketedPasteSubmitDelay + 300*time.Millisecond)

	raw, enters := r.bytes()
	if strings.Contains(raw, "payload-E") {
		t.Errorf("the resume queue delivered a body into an open dialog; PTY got %q", raw)
	}
	if enters != 0 {
		t.Errorf("the resume queue fired a submit into an open dialog -- a forged answer through the "+
			"second queue. Enter count %d, PTY %q", enters, raw)
	}
}

// TestZJHG_TrustPromptIsDetectedOnScreen pins the measured detection gap that
// ini-zjhg's rig turned up: the fresh-workspace trust prompt is a real blocking
// option picker -- the first thing an agent sees in a new workspace -- and the
// guard did not see it, because modalPromptPatterns carried "press enter to
// confirm" while Claude 2.1.232 renders "Enter to confirm · Esc to cancel".
// Undetected, a queued message would paste a body and fire Enter into the
// highlighted option.
func TestZJHG_TrustPromptIsDetectedOnScreen(t *testing.T) {
	emu := vt.NewSafeEmulator(120, 24)
	for _, row := range []string{
		"\x1b[10;1H Quick safety check: Is this a project you created or one you trust?",
		"\x1b[13;1H ❯ 1. Yes, I trust this folder",
		"\x1b[14;1H   2. No, exit",
		"\x1b[16;1H Enter to confirm · Esc to cancel",
	} {
		_, _ = emu.Write([]byte(row))
	}
	p := &Pane{name: "eng1", emu: emu, alive: true}
	if !paneShowsModalOnScreen(p) {
		t.Errorf("the trust prompt is not recognised as a modal, so a send would paste into its "+
			"option picker and Enter would select the highlighted option.\nscreen row: %q",
			emu.RowText(15, 120))
	}
}

// renderPermissionPrompt paints the permission dialog EXACTLY as real Claude
// Code 2.1.232 renders it, captured from the ini-zjhg measurement rig on a live
// prompt (a Bash call under an explicit ask rule).
//
// Verbatim on purpose. The dialog this fix exists for is this one, and a
// hand-invented approximation is how a guard passes its tests and misses the
// product -- ini-t6n already paid for that once, with a signature capture taken
// in a raw 40-row PTY that did not survive contact with a composed pane.
func renderPermissionPrompt(emu *vt.SafeEmulator) {
	for _, row := range []string{
		"\x1b[14;1H Bash command",
		"\x1b[15;1H   echo zjhg-probe",
		"\x1b[16;1H   Echo probe string",
		"\x1b[18;1H Permission rule Bash requires confirmation for this command.",
		"\x1b[19;1H /permissions to update rules",
		"\x1b[21;1H Do you want to proceed?",
		"\x1b[22;1H ❯ 1. Yes",
		"\x1b[23;1H   2. No",
		"\x1b[24;1H Esc to cancel · Tab to amend · ctrl+e to explain",
	} {
		_, _ = emu.Write([]byte(row))
	}
}

// TestZJHG_BeltPassesWithOperatorTextAlreadyInTheComposer is the ini-4bf2
// regression, and it lives HERE rather than beside the test that caught the bug
// because this file is not skipped by -short.
//
// That placement is the actual lesson of ini-4bf2. The belt's delivery-
// regression direction WAS tested — TestInjectText_StashSkipsRetry went red the
// moment the belt shipped — but that test skips under -short, and both `make
// test` and CI run -short. A red suite existed for two releases and no gate
// could see it. A guard nobody runs is not a guard.
//
// The state under test is the one the belt's original measurement did not
// cover: the operator has half-typed text in the composer when a message
// arrives. Measured on real Claude (INITECH_ZJHG rig, twice): the send path
// stashes, which EMPTIES the composer before the body is sampled, so the tail
// does change and the submit goes out; the stash is restored only afterwards.
// This fixture reproduces that shape -- prior content, then a body that
// actually renders -- and asserts the message is both delivered and SUBMITTED.
func TestZJHG_BeltPassesWithOperatorTextAlreadyInTheComposer(t *testing.T) {
	r := newZJHGRig(t, true)
	// The operator's own half-written text, plus a prompt glyph, exactly as the
	// composer looks mid-sentence.
	_, _ = r.emu.Write([]byte("\x1b[2J\x1b[24;1H❯ half written thought"))

	r.p.SendText("payload-F", true)
	time.Sleep(bracketedPasteSubmitDelay + 700*time.Millisecond)

	raw, enters := r.bytes()
	if !strings.Contains(raw, "\x1b[200~payload-F\x1b[201~") {
		t.Errorf("the body was never delivered with operator text already in the composer; PTY got %q", raw)
	}
	if enters == 0 {
		t.Errorf("the belt WITHHELD a legitimate submit because the composer was not empty when the "+
			"message arrived. That is the delivery-regression direction ini-zjhg AC item 2 forbids, "+
			"and it is the exact shape of ini-4bf2. PTY %q", raw)
	}
}
