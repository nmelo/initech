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
	queueLen   int    // messages left buffered on the pane after the send (deferred).
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
		queueLen:   p.QueueLen(),
	}
	ptyMu.Unlock()
	emuMu.Unlock()
	return res
}

// TestSendIntoAskUserQuestionModal_DefersInsteadOfPastingOrSubmitting is the
// inverted repro: it pins the FIXED contract. The ini-2jpo repro proved the
// unpatched code pasted the body (swallow) and fired one submit Enter
// (auto-answer of the destructive default). With the modal guard, a send into an
// open modal must paste NOTHING, submit NOTHING, skip the Ctrl+S stash, and
// instead DEFER the message to the queue for re-delivery on modal-close.
func TestSendIntoAskUserQuestionModal_DefersInsteadOfPastingOrSubmitting(t *testing.T) {
	const msg = "REPRO MESSAGE 123"
	res := runSendIntoModal(t, msg, true)

	// No body pasted into the picker (kills the swallow).
	if strings.Contains(res.ptyBody, "\x1b[200~") {
		t.Errorf("body must not be bracketed-pasted into an open modal; PTY received %q", res.ptyBody)
	}
	// No submit fired (kills the destructive auto-answer).
	if res.enterCount != 0 {
		t.Errorf("no submit Enter may be driven into an open modal; got %d", res.enterCount)
	}
	// No Ctrl+S stash either — the whole inject sequence is suppressed.
	if res.ctrlS {
		t.Error("Ctrl+S stash must not fire when the send is deferred for a modal")
	}
	// The message is buffered, not lost.
	if res.queueLen != 1 {
		t.Errorf("deferred message should be queued (queueLen=1), got %d", res.queueLen)
	}
	// The deferral is logged; the inject body/submit lines must be absent.
	if !strings.Contains(res.logText, "deferred: modal open") {
		t.Errorf("expected 'deferred: modal open' in inject log; got:\n%s", res.logText)
	}
	if strings.Contains(res.logText, "[inject] body written") {
		t.Errorf("body must not be written when deferring; log:\n%s", res.logText)
	}
}

// TestSendIntoAskUserQuestionModal_NoAutoSubmit is the regression that was the
// env-gated RED fix-anchor in the repro (ini-2jpo). It is now ungated and must
// stay green: a send into an open AskUserQuestion modal selects NO option (no
// submit) and pastes NO body. If it ever goes red again, the destructive
// auto-answer regressed.
func TestSendIntoAskUserQuestionModal_NoAutoSubmit(t *testing.T) {
	const msg = "REPRO MESSAGE 123"
	res := runSendIntoModal(t, msg, true)

	if res.enterCount != 0 {
		t.Errorf("0 submit Enter into an open AskUserQuestion modal expected; got %d (auto-answer regression)", res.enterCount)
	}
	if strings.Contains(res.ptyBody, "\x1b[200~") {
		t.Errorf("body must not be bracketed-pasted into an open modal; PTY received %q (swallow regression)", res.ptyBody)
	}
}

// TestSendIntoAskUserQuestionModal_EnterFalse_DefersWithoutLoss covers the AC
// edge case: an enter=false / --no-enter send must ALSO detect the modal and
// defer the body, not just drop the submit. The repro proved that suppress-Enter
// alone still pasted-and-lost the body; the guard defers the whole message.
func TestSendIntoAskUserQuestionModal_EnterFalse_DefersWithoutLoss(t *testing.T) {
	const msg = "REPRO MESSAGE 123"
	res := runSendIntoModal(t, msg, false)

	if res.enterCount != 0 {
		t.Errorf("enter=false should drive 0 submit into the modal, got %d", res.enterCount)
	}
	if strings.Contains(res.ptyBody, "\x1b[200~") {
		t.Errorf("enter=false must not paste the body into the modal (no silent loss); PTY received %q", res.ptyBody)
	}
	if res.queueLen != 1 {
		t.Errorf("enter=false send should be deferred (queueLen=1), got %d", res.queueLen)
	}
}

// TestSendIntoCodexModal_DefersInsteadOfStraySubmits covers the AC codex/raw
// edge case. The repro showed the !stashed codex path drove TWO blind submits
// into a modal (initial + a promptHasContent-tripped retry). With the modal
// guard, a send into a codex pane showing the modal must fire ZERO submits and
// defer the message — the second-stray-submit risk is gone for codex/raw too.
func TestSendIntoCodexModal_DefersInsteadOfStraySubmits(t *testing.T) {
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

	// No submit (Tab) and no body reach the modal: the send is deferred.
	if n := strings.Count(got, "\t"); n != 0 {
		t.Fatalf("expected 0 submits into codex modal (deferred), got %d Tab(s); PTY=%q", n, got)
	}
	if strings.Contains(got, "\x1b[200~") {
		t.Errorf("body must not be pasted into the codex modal; PTY=%q", got)
	}
	if p.QueueLen() != 1 {
		t.Errorf("codex modal send should be deferred (queueLen=1), got %d", p.QueueLen())
	}
	if !strings.Contains(string(logBytes), "deferred: modal open") {
		t.Errorf("expected 'deferred: modal open' in inject log; log:\n%s", string(logBytes))
	}
}
