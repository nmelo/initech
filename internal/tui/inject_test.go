package tui

import (
	"bytes"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/nmelo/initech/internal/config"
	"golang.org/x/term"
)

// TestInjectText_BracketedPaste verifies that injectText wraps the text in
// bracketed paste markers (ESC[200~ / ESC[201~) and writes directly to the PTY.
func TestInjectText_BracketedPaste(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix pipes")
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	// Raw mode: no line discipline buffering (matches Claude Code's PTY).
	oldState, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	defer term.Restore(int(tty.Fd()), oldState)

	emu := vt.NewSafeEmulator(80, 24)
	// Drain emulator responses (Ctrl+S produces output).
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()

	p := &Pane{name: "eng1", emu: emu, alive: true, ptmx: &filePty{ptmx}}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	// Inject without Enter so we only see the paste markers + text.
	go tui.injectText(p, "hello", false)

	// Read from the slave side (what Claude Code's stdin sees).
	// Ctrl+S goes through emulator (not to PTY), so we only see paste bytes.
	// Wait for Ctrl+S sleep (75ms) + write.
	time.Sleep(150 * time.Millisecond)
	buf := make([]byte, 512)
	tty.SetReadDeadline(time.Now().Add(time.Second))
	n, err := tty.Read(buf)
	if err != nil {
		t.Fatalf("tty.Read: %v", err)
	}

	got := string(buf[:n])
	want := "\x1b[200~hello\x1b[201~"
	if got != want {
		t.Errorf("PTY received %q, want %q", got, want)
	}
}

// TestInjectText_CtrlS_StillSent verifies that Ctrl+S stash is still sent
// through the emulator before the bracketed paste.
//
// enter=true since ini-a9d8, and the guard is UNCHANGED in intent. The stash
// is now conditional on enter, because Claude restores stashed text only after
// a SUBMIT -- so stashing on a send that never submits strands what the
// operator was composing (a paste into a viewer window is the first caller
// that never submits). This cell was written by ini-6l9j to prove the stash
// survived the switch to bracketed paste; that guarantee now lives on the
// submitting path, so the cell asks for it there rather than being deleted.
func TestInjectText_CtrlS_StillSent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix pipes")
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	emu := vt.NewSafeEmulator(80, 24)
	// Collect emulator output to verify Ctrl+S was sent.
	ctrlSCh := make(chan bool, 1)
	// KEEPS DRAINING after the hit. Returning here stops the only reader of
	// the emulator, and a submitting send writes more to it afterwards (the
	// submit key) -- with nobody reading, that write blocks and the whole
	// package hangs rather than failing. Measured: this test wedged for 600s
	// when ini-a9d8 pointed it at enter=true.
	go func() {
		buf := make([]byte, 256)
		seen := false
		for {
			n, err := emu.Read(buf)
			if n > 0 && !seen {
				for _, b := range buf[:n] {
					if b == 0x13 { // Ctrl+S
						seen = true
						ctrlSCh <- true
						break
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	p := &Pane{name: "eng1", emu: emu, alive: true, ptmx: &filePty{ptmx}}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	go tui.injectText(p, "hi", true)

	select {
	case <-ctrlSCh:
		// Good: Ctrl+S was sent through emulator.
	case <-time.After(time.Second):
		t.Error("Ctrl+S (0x13) was not sent through emulator before paste")
	}

	_ = tty // Keep slave open for PTY to work.
}

// TestInjectText_NoBracketedPasteCodexUsesBracketedPaste verifies that Codex
// keeps its direct PTY submit path but wraps the body in bracketed paste so
// Codex does not classify the burst as non-bracketed pasted typing.
func TestInjectText_NoBracketedPasteCodexUsesBracketedPaste(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
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

	emu := vt.NewSafeEmulator(80, 24)
	_, _ = emu.Write([]byte(">\n"))
	var emuMu sync.Mutex
	var emuOutput []byte
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				emuMu.Lock()
				emuOutput = append(emuOutput, buf[:n]...)
				emuMu.Unlock()
				_, _ = ptmx.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	p := &Pane{
		name:             "eng1",
		emu:              emu,
		alive:            true,
		ptmx:             &filePty{ptmx},
		noBracketedPaste: true,
		agentType:        config.AgentTypeCodex,
		activity:         StateIdle,
		submitKey:        "enter",
		lastOutputTime:   time.Now().Add(-(ptyIdleTimeout + time.Second)),
	}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	go tui.injectText(p, "hello", true)

	got := readPTYUntil(t, tty, []byte("\x1b[200~hello\x1b[201~\r"), 2*time.Second)
	if got != "\x1b[200~hello\x1b[201~\r" {
		t.Fatalf("PTY received %q, want %q", got, "\x1b[200~hello\x1b[201~\r")
	}

	emuMu.Lock()
	defer emuMu.Unlock()
	if len(emuOutput) != 0 {
		t.Fatalf("emulator output %q, want no emulator traffic for Codex raw inject", string(emuOutput))
	}
}

// TestInjectText_DeadPane verifies that injectText returns quickly for dead panes.
func TestInjectText_DeadPane(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()

	// Dead pane with nil ptmx: should return without panic.
	p := &Pane{name: "eng1", emu: emu, alive: false}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		tui.injectText(p, "hi", true)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("injectText(dead pane) did not return promptly")
	}
}

// TestPaneSendText_NoBracketedPasteCodexUsesBracketedPaste verifies the actual
// local Pane.SendText path used by IPC sends: Codex gets direct PTY bracketed
// paste for the body plus a direct submit byte, with no emulator traffic.
func TestPaneSendText_NoBracketedPasteCodexUsesBracketedPaste(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
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

	emu := vt.NewSafeEmulator(80, 24)
	_, _ = emu.Write([]byte(">\n"))
	var emuMu sync.Mutex
	var emuOutput []byte
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				emuMu.Lock()
				emuOutput = append(emuOutput, buf[:n]...)
				emuMu.Unlock()
				_, _ = ptmx.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	p := &Pane{
		name:             "eng1",
		emu:              emu,
		alive:            true,
		ptmx:             &filePty{ptmx},
		noBracketedPaste: true,
		agentType:        config.AgentTypeCodex,
		activity:         StateIdle,
		submitKey:        "enter",
		lastOutputTime:   time.Now().Add(-(ptyIdleTimeout + time.Second)),
	}

	go p.SendText("hello", true)

	got := readPTYUntil(t, tty, []byte("\x1b[200~hello\x1b[201~\r"), 2*time.Second)
	if got != "\x1b[200~hello\x1b[201~\r" {
		t.Fatalf("PTY received %q, want %q", got, "\x1b[200~hello\x1b[201~\r")
	}

	emuMu.Lock()
	defer emuMu.Unlock()
	if len(emuOutput) != 0 {
		t.Fatalf("emulator output %q, want no emulator traffic for Codex local raw send", string(emuOutput))
	}
}

func TestPromptHasContent_CodexPromptGlyph(t *testing.T) {
	emu := vt.NewSafeEmulator(40, 5)
	_, _ = emu.Write([]byte("› hello"))

	p := &Pane{emu: emu}
	if !promptHasContent(p) {
		t.Fatal("promptHasContent = false, want true for Codex prompt glyph")
	}
}

func TestPaneSendText_CodexQueuesWithTabWhileRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix pipes")
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

	emu := vt.NewSafeEmulator(80, 24)
	_, _ = emu.Write([]byte("›\n"))
	var emuMu sync.Mutex
	var emuOutput []byte
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				emuMu.Lock()
				emuOutput = append(emuOutput, buf[:n]...)
				emuMu.Unlock()
				_, _ = ptmx.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	p := &Pane{
		name:             "eng1",
		emu:              emu,
		alive:            true,
		ptmx:             &filePty{ptmx},
		noBracketedPaste: true,
		agentType:        config.AgentTypeCodex,
		submitKey:        "enter",
		activity:         StateRunning,
		lastOutputTime:   time.Now(),
	}

	go p.SendText("hello", true)

	got := readPTYUntil(t, tty, []byte("\x1b[200~hello\x1b[201~\t"), 2*time.Second)
	if got != "\x1b[200~hello\x1b[201~\t" {
		t.Fatalf("PTY received %q, want %q", got, "\x1b[200~hello\x1b[201~\t")
	}

	emuMu.Lock()
	defer emuMu.Unlock()
	if len(emuOutput) != 0 {
		t.Fatalf("emulator output %q, want no emulator traffic for Codex queued send", string(emuOutput))
	}
}

func TestPaneSendText_NoBracketedPasteOpenCodeUsesLocalRawPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
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

	emu := vt.NewSafeEmulator(80, 24)
	_, _ = emu.Write([]byte(">\n"))
	var emuMu sync.Mutex
	var emuOutput []byte
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				emuMu.Lock()
				emuOutput = append(emuOutput, buf[:n]...)
				emuMu.Unlock()
				_, _ = ptmx.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	p := &Pane{
		name:             "eng1",
		emu:              emu,
		alive:            true,
		ptmx:             &filePty{ptmx},
		noBracketedPaste: true,
		agentType:        config.AgentTypeOpenCode,
		submitKey:        "enter",
		lastOutputTime:   time.Now().Add(-(ptyIdleTimeout + time.Second)),
	}

	go p.SendText("hello", true)

	got := readPTYUntil(t, tty, []byte("hello\r"), time.Second)
	if got != "hello\r" {
		t.Fatalf("PTY received %q, want %q", got, "hello\r")
	}

	emuMu.Lock()
	defer emuMu.Unlock()
	if len(emuOutput) != 0 {
		t.Fatalf("emulator output %q, want no emulator traffic for OpenCode local raw send", string(emuOutput))
	}
}

// TestInjectText_StashSkipsRetry verifies that when Ctrl+S stash fires
// (noBracketedPaste=false), the submit retry is skipped even if the prompt
// has content (which would be the restored stashed text, not a failed paste).
// This prevents the bug where the operator's half-written text gets submitted
// by the retry Enter (ini-vxw).
func TestInjectText_StashSkipsRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
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

	emu := vt.NewSafeEmulator(80, 24)

	// Put a prompt with content on the bottom row so promptHasContent returns
	// true. This simulates the restored stashed text after submit.
	_, _ = emu.Write([]byte("\x1b[24;1H\u276f some typed text"))

	var emuMu sync.Mutex
	var enterCount int
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				emuMu.Lock()
				for _, b := range buf[:n] {
					if b == '\r' {
						enterCount++
					}
				}
				emuMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	// Model the child RENDERING the body it is pasted (ini-4bf2). Without this
	// the fixture describes a child that accepts input and paints nothing --
	// a state no real agent reaches, and the one state in which the ini-zjhg
	// belt correctly withholds a submit, because "my text never appeared
	// anywhere" is exactly what it is built to detect.
	//
	// MEASURED before changing this, on real Claude 2.1.232, twice: with the
	// operator's own half-typed text in the composer, a message arriving on
	// this same path IS submitted (26ms), and the composer afterwards holds the
	// restored stash. The stash empties the composer before the body is sampled
	// and the restore lands after the submit, so the tail does change. The
	// product is correct on this path; what was wrong was a fixture that put
	// the POST-submit restored text in place BEFORE the send and then never
	// rendered the body.
	go func() {
		buf := make([]byte, 512)
		for {
			tty.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := tty.Read(buf)
			if n > 0 {
				if body, ok := bracketedPasteBody(buf[:n]); ok {
					_, _ = emu.Write([]byte(body))
				}
			}
			if err != nil && !os.IsTimeout(err) {
				return
			}
		}
	}()

	p := &Pane{
		name:           "eng1",
		emu:            emu,
		alive:          true,
		ptmx:           &filePty{ptmx},
		lastOutputTime: time.Now().Add(-(ptyIdleTimeout + time.Second)),
	}

	// Call with enter=true on a non-noBracketedPaste pane (stash fires).
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.SendText("incoming message", true)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SendText did not return within 5s")
	}

	// Allow emulator output to flush.
	time.Sleep(100 * time.Millisecond)

	emuMu.Lock()
	got := enterCount
	emuMu.Unlock()

	// With stash active, retry is skipped: exactly 1 Enter (the initial submit).
	// Before the fix, this would be 2 (initial + retry hitting restored text).
	if got != 1 {
		t.Errorf("Enter count = %d, want 1 (retry should be skipped when stash active)", got)
	}
}

func readPTYUntil(t *testing.T, tty *os.File, want []byte, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var out []byte
	buf := make([]byte, 256)
	for time.Now().Before(deadline) && !bytes.Equal(out, want) {
		tty.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, _ := tty.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
	}
	return string(out)
}
