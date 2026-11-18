package tui

import (
	"os"
	"strings"
	"testing"

	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/nmelo/initech/internal/config"
)

func TestIsCodexReadyPrompt_MatchesComposerPrompt(t *testing.T) {
	text := "Tip: something\n\n>\n"
	if !isCodexReadyPrompt(text) {
		t.Fatalf("expected ready prompt match for %q", text)
	}
}

func TestIsCodexReadyPrompt_RejectsTrustPrompt(t *testing.T) {
	text := "Do you trust the contents of this directory?\n› 1. Yes, continue\nPress enter to continue\n"
	if isCodexReadyPrompt(text) {
		t.Fatalf("expected trust prompt to be treated as not ready: %q", text)
	}
}

func TestIsCodexReadyPrompt_RejectsBootingMCPServer(t *testing.T) {
	text := "Booting MCP server: codex_apps\n\n> qa submit test\n"
	if isCodexReadyPrompt(text) {
		t.Fatalf("expected booting MCP server screen to be treated as not ready: %q", text)
	}
}

func TestIsCodexTrustPrompt_MatchesStartupPrompt(t *testing.T) {
	text := "Do you trust the contents of this directory?\n› 1. Yes, continue\n2. No, quit\nPress enter to continue\n"
	if !isCodexTrustPrompt(text) {
		t.Fatalf("expected trust prompt match for %q", text)
	}
}

func TestPaneSendText_CodexWaitsForReadyPrompt(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		const body = "type this draft"
		_, p, f := agentCaptureRig(t, "codex", "")
		p.SendText(body, false)
		if got, _, _ := f.stream(); got != "" || p.QueueLen() != 1 {
			t.Fatalf("starting pane: PTY %q, queued %d", got, p.QueueLen())
		}
		idle := agentCapture(t, "local-codex-0170-idle")
		f.shows = strings.Replace(idle, "› Ask Codex to do anything", "› "+body, 1)
		f.paint(idle)
		done := make(chan struct{})
		p.safeGo = func(fn func()) { go func() { defer close(done); fn() }() }
		p.maybeDrainModalQueue()
		<-done
		if got, _, _ := f.stream(); got != string(bracketedBody(body)) || p.QueueLen() != 0 {
			t.Fatalf("ready pane: PTY %q queue %d", got, p.QueueLen())
		}
	})
}

func TestPaneSendText_OpenCodeWaitsForReadyPrompt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := vt.NewSafeEmulator(80, 10)
	p := &Pane{
		name:             "eng1",
		emu:              emu,
		alive:            true,
		ptmx:             &filePty{w},
		agentType:        config.AgentTypeOpenCode,
		activity:         StateIdle,
		noBracketedPaste: true,
		lastOutputTime:   time.Now(),
	}

	readCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 16)
		n, _ := r.Read(buf)
		readCh <- buf[:n]
	}()

	done := make(chan struct{})
	go func() {
		p.SendText("go", false)
		close(done)
	}()

	select {
	case got := <-readCh:
		t.Fatalf("got PTY write before ready prompt: %q", string(got))
	case <-time.After(150 * time.Millisecond):
	}

	p.mu.Lock()
	p.lastOutputTime = time.Now().Add(-(ptyIdleTimeout + time.Second))
	p.mu.Unlock()
	_, _ = emu.Write([]byte(">"))
	if !p.isCodexReadyForSend() {
		t.Fatalf("pane not ready after prompt write; footer=%q", emulatorBottomTextBlocking(emu, 10))
	}

	select {
	case got := <-readCh:
		if string(got) != "go" {
			t.Fatalf("PTY write = %q, want %q", string(got), "go")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for PTY write after ready prompt")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SendText did not return after ready prompt")
	}
}

func TestWaitForCodexReady_AcceptsTrustPrompt(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := vt.NewSafeEmulator(80, 10)
	_, _ = emu.Write([]byte("Do you trust the contents of this directory?\n> 1. Yes, continue\n2. No, quit\nPress enter to continue\n"))
	if !isCodexTrustPrompt(emulatorBottomTextBlocking(emu, 10)) {
		t.Fatalf("synthetic trust prompt not detected; footer=%q", emulatorBottomTextBlocking(emu, 10))
	}

	p := &Pane{
		name:             "eng1",
		emu:              emu,
		alive:            true,
		ptmx:             &filePty{w},
		agentType:        config.AgentTypeCodex,
		activity:         StateIdle,
		noBracketedPaste: true,
		lastOutputTime:   time.Now().Add(-(ptyIdleTimeout + time.Second)),
	}

	accepted := make(chan string, 1)
	go func() {
		buf := make([]byte, 16)
		n, _ := r.Read(buf)
		accepted <- string(buf[:n])
	}()

	if p.waitForCodexReady(200 * time.Millisecond) {
		t.Fatal("waitForCodexReady unexpectedly returned true while trust prompt remained onscreen")
	}

	select {
	case got := <-accepted:
		if got != "\r" {
			t.Fatalf("trust prompt write = %q, want %q", got, "\r")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for trust prompt acceptance")
	}
}
