package tui

// modal_send_repro_test.go — PHASE-0 REPRO for ini-2jpo.
//
// Bug: `initech send <agent> "msg"` into a Claude Code pane that currently has
// an AskUserQuestion (or other option-picker) modal open is a double-whammy:
//   1. The message body is silently SWALLOWED — bracketed-pasted into a picker
//      that has no text field to receive it.
//   2. The modal is AUTO-ANSWERED — the trailing submit key (Enter) is read by
//      the picker as "confirm the highlighted option".
//
// Root cause (confirmed by code read): sendPaneTextLocked (ipc.go:282) drives a
// fixed Ctrl+S -> bracketed-paste -> submit sequence with ZERO inspection of the
// target's emulator screen. It assumes the target is at a normal text prompt.
//
// These tests reproduce the MECHANISM at the initech boundary (the swallow and
// the auto-answer happen inside Claude Code, but their triggers — the body paste
// reaching the PTY and the submit Enter reaching the emulator — are observable
// here). They are TEST-ONLY: no production code is changed in this bead. The
// fix (modal detection + suppress/defer submit) is a separate follow-up.

import (
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/nmelo/initech/internal/config"
	"golang.org/x/term"
)

// renderAskUserQuestionModal paints a representative Claude Code AskUserQuestion
// modal into the bottom rows of emu, so that any future modal detector built on
// emulatorBottomText() has a realistic signature to match. The exact glyphs do
// not affect current behavior — sendPaneTextLocked never reads the screen — but
// they document what the send path is blindly driving over.
func renderAskUserQuestionModal(emu *vt.SafeEmulator) {
	rows := []string{
		"\x1b[16;1H╭────────────────────────────────────────────────────────────╮",
		"\x1b[17;1H│ Do you want to delete the production database?               │",
		"\x1b[18;1H│                                                              │",
		"\x1b[19;1H│ ❯ 1. Yes, delete it                                          │",
		"\x1b[20;1H│   2. Cancel (keep the database)                              │",
		"\x1b[21;1H│   3. Type something                                          │",
		"\x1b[22;1H│ Enter to select · up/down to navigate · Esc to cancel        │",
		"\x1b[23;1H╰────────────────────────────────────────────────────────────╯",
	}
	for _, r := range rows {
		_, _ = emu.Write([]byte(r))
	}
}

// modalSendCapture is the observable result of an inject into a pane that has a
// modal on screen.
type modalSendCapture struct {
	ptyBody    string // bytes that reached the PTY slave (the agent's stdin).
	enterCount int    // count of submit Enter (0x0d) emitted through the emulator.
	ctrlS      bool   // whether Ctrl+S (0x13) stash was emitted through the emulator.
	logText    string // captured initech.log content (debug level).
}

// runSendIntoModal stands up a default (bracketed-paste / Claude Code) pane whose
// emulator shows an AskUserQuestion modal, captures the inject debug log, then
// drives Pane.SendText — the exact path used by `initech send` (handleIPCSend ->
// pv.SendText). It returns what the send actually pushed at the target. enter
// mirrors the IPCRequest.Enter flag (true = append a submit key, the default).
func runSendIntoModal(t *testing.T, msg string, enter bool) modalSendCapture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix pipes / raw PTY mode")
	}

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	// Raw mode: no line discipline, so the bracketed-paste body arrives on the
	// slave unmodified (matches Claude Code's PTY).
	oldState, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	defer term.Restore(int(tty.Fd()), oldState)

	// Capture the inject debug lines that are invisible in production: they are
	// LogDebug, and the live TUI runs at INFO. A debug logger over a temp dir
	// gives us the reproducible send-start/body-written/submit evidence.
	logDir := t.TempDir()
	cleanup := InitLogger(logDir, slog.LevelDebug)

	emu := vt.NewSafeEmulator(80, 24)
	renderAskUserQuestionModal(emu)

	// Drain the emulator output: Ctrl+S (0x13) stash and the submit Enter (0x0d)
	// flow through the emulator (not the direct PTY write), exactly as in
	// TestInjectText_StashSkipsRetry.
	var emuMu sync.Mutex
	var enterCount int
	var sawCtrlS bool
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				emuMu.Lock()
				for _, b := range buf[:n] {
					switch b {
					case '\r':
						enterCount++
					case 0x13:
						sawCtrlS = true
					}
				}
				emuMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	// Drain the PTY slave: the bracketed-paste body is written directly to the
	// master, so it surfaces here.
	var ptyMu sync.Mutex
	var ptyBytes []byte
	go func() {
		buf := make([]byte, 512)
		for {
			tty.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := tty.Read(buf)
			if n > 0 {
				ptyMu.Lock()
				ptyBytes = append(ptyBytes, buf[:n]...)
				ptyMu.Unlock()
			}
			if err != nil && !os.IsTimeout(err) {
				return
			}
		}
	}()

	p := &Pane{
		name:           "growth",
		emu:            emu,
		alive:          true,
		ptmx:           &filePty{ptmx},
		lastOutputTime: time.Now().Add(-(ptyIdleTimeout + time.Second)),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.SendText(msg, enter)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SendText did not return within 5s")
	}

	// Let the final emulator/PTY bytes flush, then close the log so we can read it.
	time.Sleep(200 * time.Millisecond)
	cleanup()

	logBytes, _ := os.ReadFile(logDir + "/.initech/" + logFileName)

	emuMu.Lock()
	ptyMu.Lock()
	res := modalSendCapture{
		ptyBody:    string(ptyBytes),
		enterCount: enterCount,
		ctrlS:      sawCtrlS,
		logText:    string(logBytes),
	}
	ptyMu.Unlock()
	emuMu.Unlock()
	return res
}

// TestSendIntoAskUserQuestionModal_DoubleWhammyRepro is the reproduction: it
// pins the CURRENT (buggy) behavior so the double-whammy is demonstrated
// deterministically and the inject log evidence is captured. It stays GREEN on
// unpatched code. When the fix lands, this characterization should be inverted
// (see the FixAnchor test below, which encodes the desired contract).
func TestSendIntoAskUserQuestionModal_DoubleWhammyRepro(t *testing.T) {
	const msg = "REPRO MESSAGE 123"
	res := runSendIntoModal(t, msg, true)

	// FAILURE 1 mechanism — swallow: the body is bracketed-pasted blindly into
	// the modal, which has no text field to accept it.
	wantBody := "\x1b[200~" + msg + "\x1b[201~"
	if !strings.Contains(res.ptyBody, wantBody) {
		t.Fatalf("expected body bracketed-pasted into the modal (swallow mechanism); PTY received %q, want substring %q", res.ptyBody, wantBody)
	}

	// FAILURE 2 mechanism — auto-answer: a submit Enter is driven into the modal,
	// confirming the highlighted option. Exactly one (the retry at ipc.go:379 is
	// skipped for a stashed Claude pane — confirms eng2's single-Enter finding).
	if res.enterCount != 1 {
		t.Fatalf("expected exactly 1 submit Enter into the modal (auto-answer; retry skipped due to stash), got %d", res.enterCount)
	}

	// Ctrl+S stash also fires — meaningless/invalid against an option picker.
	if !res.ctrlS {
		t.Fatalf("expected Ctrl+S (0x13) stash to be emitted (invalid against a modal)")
	}

	// Inject log evidence captured deterministically (invisible at prod INFO level).
	for _, want := range []string{"[inject] send start", "[inject] body written", "[inject] submit"} {
		if !strings.Contains(res.logText, want) {
			t.Errorf("inject log missing %q; captured log:\n%s", want, res.logText)
		}
	}
	// And NO "submit retry" for a stashed Claude pane: the second stray submit is
	// a codex/raw-only risk, not present here (eng2's point).
	if strings.Contains(res.logText, "submit retry") {
		t.Errorf("did not expect 'submit retry' for a stashed Claude pane (single Enter only); captured log:\n%s", res.logText)
	}
}

// TestSendIntoAskUserQuestionModal_MustNotAutoSubmit_FixAnchor encodes the
// DESIRED contract for the follow-up fix bead: a send into an open modal must
// neither auto-submit nor blindly paste the body. It is RED on unpatched code.
//
// It is env-gated rather than t.Skip-without-condition so the failure can be
// demonstrated on demand without editing the file:
//
//	INITECH_RUN_RED_ANCHOR=1 go test ./internal/tui/ -run FixAnchor
//
// The fix bead removes this gate (and inverts the characterization test above).
//
// NOTE for the fix bead: suppressing the submit is necessary but NOT sufficient.
// eng2's live enter=false run showed the body is then LOST (swallowed, not
// buffered). The fix must ALSO defer/queue the body and re-deliver on modal-clear
// — the codebase already has this primitive: Pane.EnqueueMessage + the
// resume-drain path (handleIPCSend, ipc.go:212-245) used for suspended panes. A
// third assertion (body re-delivered after the modal clears) belongs here once
// that mechanism is wired in.
func TestSendIntoAskUserQuestionModal_MustNotAutoSubmit_FixAnchor(t *testing.T) {
	if os.Getenv("INITECH_RUN_RED_ANCHOR") == "" {
		t.Skip("RED anchor for the modal-aware-send FIX (follow-up to ini-2jpo). " +
			"Run with INITECH_RUN_RED_ANCHOR=1 to watch it fail on unpatched code. " +
			"The fix (detect modal via an emulatorBottomText/scanPermissionPrompt-style scan, " +
			"then suppress or defer the submit) removes this gate and the test goes green.")
	}

	const msg = "REPRO MESSAGE 123"
	res := runSendIntoModal(t, msg, true)

	// DESIRED: no submit key may be driven into an open modal (no auto-answer).
	if res.enterCount != 0 {
		t.Errorf("DESIRED: 0 submit Enter into an open AskUserQuestion modal; got %d (auto-answer bug)", res.enterCount)
	}
	// DESIRED: the body must not be blindly bracketed-pasted into a picker (no swallow).
	if strings.Contains(res.ptyBody, "\x1b[200~") {
		t.Errorf("DESIRED: body must not be bracketed-pasted into an open modal; PTY received %q (swallow bug)", res.ptyBody)
	}
}

// TestSendIntoAskUserQuestionModal_EnterFalse_SuppressesAutoAnswerButStillSwallows
// covers vary item #4 (enter=false): with enter=false, sendPaneTextLocked returns
// after the body write (ipc.go:334) and sends NO submit — so the auto-answer half
// is avoided, but the body is STILL bracketed-pasted into the picker (still
// swallowed). I.e. `--no-enter` is a partial mitigation, not a fix.
func TestSendIntoAskUserQuestionModal_EnterFalse_SuppressesAutoAnswerButStillSwallows(t *testing.T) {
	const msg = "REPRO MESSAGE 123"
	res := runSendIntoModal(t, msg, false)

	// No submit key fired: the modal is NOT auto-answered when enter=false.
	if res.enterCount != 0 {
		t.Errorf("enter=false should drive 0 submit Enter into the modal, got %d", res.enterCount)
	}
	// But the body was still pasted into the modal (the swallow half persists).
	wantBody := "\x1b[200~" + msg + "\x1b[201~"
	if !strings.Contains(res.ptyBody, wantBody) {
		t.Errorf("enter=false still bracketed-pastes the body into the modal (swallow persists); PTY received %q, want substring %q", res.ptyBody, wantBody)
	}
}

// TestSendIntoCodexModal_RetryFiresSecondStraySubmit covers vary item #4 (codex /
// !stashed) and eng2's hand-off. The submit retry at ipc.go:347/379 is guarded by
// `!stashed`. A standard Claude pane stashes (stashed=true) so the retry is
// skipped — only ONE Enter. A codex pane does NOT stash (noBracketedPaste skips
// the Ctrl+S), so `!stashed` is true and the retry fires a SECOND blind submit
// whenever promptHasContent is true. An open modal's rendered option text ("❯ 1.
// ...") trips promptHasContent, so the codex path drives TWO blind submits into
// the modal. (Idle codex would be two Enters; a running codex queues, so it is two
// Tabs — captured here. Either way it is a second stray submit the operator never
// authorized.) Contrast TestPaneSendText_CodexQueuesWithTabWhileRunning, whose
// clean prompt makes promptHasContent false → exactly one Tab.
func TestSendIntoCodexModal_RetryFiresSecondStraySubmit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix pipes / raw PTY mode")
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	oldState, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	defer term.Restore(int(tty.Fd()), oldState)

	logDir := t.TempDir()
	cleanup := InitLogger(logDir, slog.LevelDebug)

	emu := vt.NewSafeEmulator(80, 24)
	renderAskUserQuestionModal(emu)

	// Codex submits go straight to the PTY, so capture everything on the slave.
	var ptyMu sync.Mutex
	var ptyBytes []byte
	go func() {
		buf := make([]byte, 512)
		for {
			tty.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := tty.Read(buf)
			if n > 0 {
				ptyMu.Lock()
				ptyBytes = append(ptyBytes, buf[:n]...)
				ptyMu.Unlock()
			}
			if err != nil && !os.IsTimeout(err) {
				return
			}
		}
	}()

	// Running codex pane: codexQueueSubmit short-circuits the 10s ready-wait and
	// routes submits to the PTY as Tab. noBracketedPaste => no Ctrl+S stash =>
	// stashed=false => the retry branch is reachable.
	p := &Pane{
		name:             "growth",
		emu:              emu,
		alive:            true,
		ptmx:             &filePty{ptmx},
		noBracketedPaste: true,
		agentType:        config.AgentTypeCodex,
		activity:         StateRunning,
		submitKey:        "enter",
		lastOutputTime:   time.Now(),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.SendText("REPRO MESSAGE 123", true)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SendText did not return within 5s")
	}
	time.Sleep(200 * time.Millisecond)
	cleanup()

	ptyMu.Lock()
	got := string(ptyBytes)
	ptyMu.Unlock()
	logBytes, _ := os.ReadFile(logDir + "/.initech/" + logFileName)

	// Two submits driven into the modal: the initial submit plus the retry's
	// second stray submit (promptHasContent tripped by the modal's option text).
	if n := strings.Count(got, "\t"); n != 2 {
		t.Fatalf("expected 2 blind submits into codex modal (initial + retry), got %d Tab(s); PTY=%q", n, got)
	}
	if !strings.Contains(string(logBytes), "submit retry") {
		t.Errorf("expected 'submit retry' in inject log for the !stashed codex path; log:\n%s", string(logBytes))
	}
}
