package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
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

// TestInjectText_NoBracketedPasteEnterUsesEmulatorSubmit verifies that the
// no-bracketed-paste path writes raw text directly to the PTY, but routes the
// final Enter through the emulator submit path.
func TestInjectText_NoBracketedPasteEnterUsesEmulatorSubmit(t *testing.T) {
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
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				_, _ = ptmx.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	p := &Pane{name: "eng1", emu: emu, alive: true, ptmx: ptmx, noBracketedPaste: true}
	tui := &TUI{agentEvents: make(chan AgentEvent, 8)}

	go tui.injectText(p, "hello", true)

	time.Sleep(250 * time.Millisecond)
	buf := make([]byte, 512)
	tty.SetReadDeadline(time.Now().Add(time.Second))
	n, err := tty.Read(buf)
	if err != nil {
		t.Fatalf("tty.Read: %v", err)
	}

	got := string(buf[:n])
	if !strings.Contains(got, "hello") {
		t.Fatalf("PTY received %q, want raw text payload", got)
	}
	if !strings.ContainsRune(got, '\r') {
		t.Fatalf("PTY received %q, want emulator Enter encoding", got)
	}
	if strings.ContainsRune(got, '\n') {
		t.Errorf("PTY received %q, want no raw newline byte", got)
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
