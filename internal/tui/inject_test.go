package tui

import (
	"bytes"
	"os"
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

	p := &Pane{name: "eng1", emu: emu, alive: true, ptmx: ptmx}
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
func TestInjectText_CtrlS_StillSent(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	emu := vt.NewSafeEmulator(80, 24)
	// Collect emulator output to verify Ctrl+S was sent.
	ctrlSCh := make(chan bool, 1)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				for _, b := range buf[:n] {
					if b == 0x13 { // Ctrl+S
						ctrlSCh <- true
						return
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	p := &Pane{name: "eng1", emu: emu, alive: true, ptmx: ptmx}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	go tui.injectText(p, "hi", false)

	select {
	case <-ctrlSCh:
		// Good: Ctrl+S was sent through emulator.
	case <-time.After(time.Second):
		t.Error("Ctrl+S (0x13) was not sent through emulator before paste")
	}

	_ = tty // Keep slave open for PTY to work.
}

// TestInjectText_NoBracketedPasteCodexWritesBodyAndSubmitToPTY verifies that
// the Codex raw-inject path writes both the body and submit byte directly to
// the PTY and skips the emulator entirely.
func TestInjectText_NoBracketedPasteCodexWritesBodyToPTY(t *testing.T) {
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
		ptmx:             ptmx,
		noBracketedPaste: true,
		agentType:        config.AgentTypeCodex,
		submitKey:        "enter",
		lastOutputTime:   time.Now().Add(-(ptyIdleTimeout + time.Second)),
	}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	go tui.injectText(p, "hello", true)

	got := readPTYUntil(t, tty, []byte("hello\r"), time.Second)
	if got != "hello\r" {
		t.Fatalf("PTY received %q, want %q", got, "hello\r")
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

// TestPaneSendText_NoBracketedPasteCodexUsesLocalRawPath verifies the actual
// local Pane.SendText path used by IPC sends: raw PTY body and submit byte,
// with no Ctrl+S stash and no emulator traffic.
func TestPaneSendText_NoBracketedPasteCodexUsesLocalRawPath(t *testing.T) {
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
		ptmx:             ptmx,
		noBracketedPaste: true,
		agentType:        config.AgentTypeCodex,
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
		t.Fatalf("emulator output %q, want no emulator traffic for Codex local raw send", string(emuOutput))
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
