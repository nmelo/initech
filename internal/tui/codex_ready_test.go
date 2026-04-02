package tui

import (
	"os"
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
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := vt.NewSafeEmulator(80, codexPermissionScanRows)
	p := &Pane{
		name:             "eng1",
		emu:              emu,
		alive:            true,
		ptmx:             w,
		agentType:        config.AgentTypeCodex,
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
		t.Fatalf("pane not ready after prompt write; footer=%q", emulatorBottomText(emu, codexPermissionScanRows))
	}

	select {
	case got := <-readCh:
		if string(got) != "\x1b[200~go\x1b[201~" {
			t.Fatalf("PTY write = %q, want %q", string(got), "\x1b[200~go\x1b[201~")
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

func TestPaneSendText_OpenCodeWaitsForReadyPrompt(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := vt.NewSafeEmulator(80, codexPermissionScanRows)
	p := &Pane{
		name:             "eng1",
		emu:              emu,
		alive:            true,
		ptmx:             w,
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
		t.Fatalf("pane not ready after prompt write; footer=%q", emulatorBottomText(emu, codexPermissionScanRows))
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

	emu := vt.NewSafeEmulator(80, codexPermissionScanRows)
	_, _ = emu.Write([]byte("Do you trust the contents of this directory?\n> 1. Yes, continue\n2. No, quit\nPress enter to continue\n"))
	if !isCodexTrustPrompt(emulatorBottomText(emu, codexPermissionScanRows)) {
		t.Fatalf("synthetic trust prompt not detected; footer=%q", emulatorBottomText(emu, codexPermissionScanRows))
	}

	p := &Pane{
		name:             "eng1",
		emu:              emu,
		alive:            true,
		ptmx:             w,
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
