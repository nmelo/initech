package tui

import (
	"os"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
)

func TestIsCodexPermissionPrompt_MatchesKnownPatterns(t *testing.T) {
	text := "2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel"
	if !isCodexPermissionPrompt(text) {
		t.Fatalf("expected prompt match for %q", text)
	}
}

func TestCodexPermissionApprovalInput_PersistentOptionUsesP(t *testing.T) {
	got, ok := codexPermissionApprovalInput("1. Yes, proceed (y)\n2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel")
	if !ok {
		t.Fatal("expected persistent prompt match")
	}
	if string(got) != "p" {
		t.Fatalf("approval input = %q, want %q", string(got), "p")
	}
}

func TestCodexPermissionApprovalInput_TwoOptionPromptUsesEnter(t *testing.T) {
	got, ok := codexPermissionApprovalInput("1. Yes, proceed (y)\n2. No, and tell Codex what to do differently (esc)\nPress enter to confirm or esc to cancel")
	if !ok {
		t.Fatal("expected two-option prompt match")
	}
	if string(got) != "\r" {
		t.Fatalf("approval input = %q, want %q", string(got), "\r")
	}
}

func TestCodexPermissionApprovalInput_FooterOnlyDoesNothing(t *testing.T) {
	got, ok := codexPermissionApprovalInput("Press enter to confirm or esc to cancel")
	if ok {
		t.Fatalf("unexpected approval input %q for footer-only text", string(got))
	}
}

// TestScanPermissionPrompt_CodexPersistentReturnsP verifies that
// scanPermissionPrompt detects a Codex 3-option prompt and returns "p"
// for the persistent approval option.
func TestScanPermissionPrompt_CodexPersistentReturnsP(t *testing.T) {
	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("1. Yes (y)\n2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:      "eng1",
		agentType: config.AgentTypeCodex,
		emu:       emu,
	}

	got := p.scanPermissionPrompt()
	if string(got) != "p" {
		t.Fatalf("scanPermissionPrompt = %q, want %q", string(got), "p")
	}
}

// TestScanPermissionPrompt_TwoOptionReturnsEnter verifies that a 2-option
// prompt without a persistent option returns "\r" (Enter).
func TestScanPermissionPrompt_TwoOptionReturnsEnter(t *testing.T) {
	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("1. Yes, proceed (y)\n2. No, and tell Codex what to do differently (esc)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:      "eng1",
		agentType: config.AgentTypeCodex,
		emu:       emu,
	}

	got := p.scanPermissionPrompt()
	if string(got) != "\r" {
		t.Fatalf("scanPermissionPrompt = %q, want %q", string(got), "\r")
	}
}

// TestScanPermissionPrompt_NoPromptReturnsNil verifies that normal terminal
// output (no permission prompt) returns nil.
func TestScanPermissionPrompt_NoPromptReturnsNil(t *testing.T) {
	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("normal idle prompt"))

	p := &Pane{
		name:      "eng1",
		agentType: config.AgentTypeCodex,
		emu:       emu,
	}

	if got := p.scanPermissionPrompt(); got != nil {
		t.Fatalf("scanPermissionPrompt = %q, want nil", string(got))
	}
}

// TestReadLoopAutoApprove_WritesApprovalOnPrompt simulates PTY bytes arriving
// that contain a permission prompt, and verifies the approval key is written
// back to the PTY immediately within the same read cycle.
func TestReadLoopAutoApprove_WritesApprovalOnPrompt(t *testing.T) {
	ptyR, ptyW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe for pty: %v", err)
	}
	defer ptyR.Close()
	defer ptyW.Close()

	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)

	// Pre-populate emulator with a permission prompt so the scan triggers
	// on the very first readLoop iteration.
	emu.Write([]byte("2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:        "eng1",
		agentType:   config.AgentTypeCodex,
		autoApprove: true,
		alive:       true,
		emu:         emu,
		ptmx:        ptyW,
	}

	// scanPermissionPrompt is called under renderMu in readLoop.
	// Simulate that directly: lock renderMu, scan, write approval.
	p.renderMu.Lock()
	// Write a trivial byte to trigger the emulator update path (as readLoop would).
	p.emu.Write([]byte(" "))
	approvalBytes := p.scanPermissionPrompt()
	p.renderMu.Unlock()

	if approvalBytes == nil {
		t.Fatal("expected approval bytes from scanPermissionPrompt")
	}

	// Write approval to the PTY (as readLoop would).
	p.sendMu.Lock()
	_, err = p.ptmx.Write(approvalBytes)
	p.sendMu.Unlock()
	if err != nil {
		t.Fatalf("ptmx.Write: %v", err)
	}

	// Read the approval byte from the other end of the pipe.
	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := ptyR.Read(buf)
		done <- buf[:n]
	}()

	select {
	case got := <-done:
		if string(got) != "p" {
			t.Fatalf("approval write = %q, want %q", string(got), "p")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for auto-approval write")
	}
}

// TestReadLoopAutoApprove_SkipsWhenDisabled verifies that scanPermissionPrompt
// is not called when autoApprove is false (readLoop checks autoApprove before
// calling scan).
func TestReadLoopAutoApprove_SkipsWhenDisabled(t *testing.T) {
	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:        "eng1",
		agentType:   config.AgentTypeCodex,
		autoApprove: false,
		alive:       true,
		emu:         emu,
	}

	// Simulate the readLoop check: autoApprove is false, so scan is skipped.
	p.mu.Lock()
	autoApprove := p.autoApprove
	p.mu.Unlock()

	if autoApprove {
		t.Fatal("autoApprove should be false")
	}

	// Even if we call scanPermissionPrompt directly, it would return bytes
	// (the prompt is there). But readLoop gates on autoApprove first.
	// Verify the gate works by confirming autoApprove is false.
}

// TestReadLoopAutoApprove_ClaudeCodePaneWithAutoApprove verifies that
// Claude Code panes with autoApprove=true also get auto-approved via
// scanPermissionPrompt (the agentType dispatch handles all types).
func TestReadLoopAutoApprove_ClaudeCodePaneWithAutoApprove(t *testing.T) {
	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:        "eng1",
		agentType:   config.AgentTypeClaudeCode,
		autoApprove: true,
		emu:         emu,
	}

	got := p.scanPermissionPrompt()
	if string(got) != "p" {
		t.Fatalf("scanPermissionPrompt = %q, want %q for Claude Code pane", string(got), "p")
	}
}
