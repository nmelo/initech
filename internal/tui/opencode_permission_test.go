package tui

import (
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/nmelo/initech/internal/config"
)

func renderOpenCodePermissionDialog(selected int, allow, persistent, reject string) string {
	style := func(index int, label string) string {
		if index == selected {
			return "\x1b[30;47m" + label + "\x1b[0m"
		}
		return "\x1b[34m" + label + "\x1b[0m"
	}

	return "Permission Required\n\n" +
		style(0, allow) + "  " +
		style(1, persistent) + "  " +
		style(2, reject)
}

func TestIsOpenCodePermissionPrompt_MatchesKnownDialog(t *testing.T) {
	text := "Permission Required\nAllow once  Allow always  Reject\n"
	if !isOpenCodePermissionPrompt(text) {
		t.Fatalf("expected OpenCode prompt match for %q", text)
	}
}

func TestIsOpenCodePermissionPrompt_RejectsUnrelatedFooter(t *testing.T) {
	text := "permission required later\nallow session caching is enabled\n"
	if isOpenCodePermissionPrompt(text) {
		t.Fatalf("expected unrelated footer to fail detection: %q", text)
	}
}

func TestDetectOpenCodePermissionSelection_FirstOptionSelected(t *testing.T) {
	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(0, "Allow (a)", "Allow for session (s)", "Deny (d)")))

	selected, ok := detectOpenCodePermissionSelection(emu, codexPermissionScanRows)
	if !ok {
		t.Fatal("expected to detect OpenCode selection")
	}
	if selected != 0 {
		t.Fatalf("selected = %d, want 0", selected)
	}
}

func TestDetectOpenCodePermissionSelection_RejectSelectedIsUnsafe(t *testing.T) {
	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(2, "Allow once", "Allow always", "Reject")))

	selected, ok := detectOpenCodePermissionSelection(emu, codexPermissionScanRows)
	if !ok {
		t.Fatal("expected to detect OpenCode selection")
	}
	if selected != 2 {
		t.Fatalf("selected = %d, want 2", selected)
	}
}

// TestScanPermissionPrompt_OpenCodeAllowReturnsRightEnter verifies that
// scanPermissionPrompt detects an OpenCode dialog with "Allow" selected
// and returns the right-arrow + enter sequence.
func TestScanPermissionPrompt_OpenCodeAllowReturnsRightEnter(t *testing.T) {
	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(0, "Allow (a)", "Allow for session (s)", "Deny (d)")))

	p := &Pane{
		name:             "eng2",
		agentType:        config.AgentTypeOpenCode,
		noBracketedPaste: true,
		emu:              emu,
	}

	got := p.scanPermissionPrompt()
	if string(got) != "\x1b[C\r" {
		t.Fatalf("scanPermissionPrompt = %q, want %q", string(got), "\x1b[C\r")
	}
}

// TestScanPermissionPrompt_OpenCodePersistentReturnsEnter verifies that
// when the persistent option is selected, scanPermissionPrompt returns Enter.
func TestScanPermissionPrompt_OpenCodePersistentReturnsEnter(t *testing.T) {
	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(1, "Allow (a)", "Allow for session (s)", "Deny (d)")))

	p := &Pane{
		name:             "eng2",
		agentType:        config.AgentTypeOpenCode,
		noBracketedPaste: true,
		emu:              emu,
	}

	got := p.scanPermissionPrompt()
	if string(got) != "\r" {
		t.Fatalf("scanPermissionPrompt = %q, want %q", string(got), "\r")
	}
}

// TestScanPermissionPrompt_OpenCodeRejectReturnsNil verifies that
// scanPermissionPrompt returns nil when the reject option is selected.
func TestScanPermissionPrompt_OpenCodeRejectReturnsNil(t *testing.T) {
	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(2, "Allow once", "Allow always", "Reject")))

	p := &Pane{
		name:             "eng2",
		agentType:        config.AgentTypeOpenCode,
		noBracketedPaste: true,
		emu:              emu,
	}

	if got := p.scanPermissionPrompt(); got != nil {
		t.Fatalf("scanPermissionPrompt = %q, want nil for reject-selected", string(got))
	}
}

// TestReadLoopAutoApprove_OpenCodeWritesApproval simulates the readLoop
// path for an OpenCode pane and verifies the approval bytes are written.
func TestReadLoopAutoApprove_OpenCodeWritesApproval(t *testing.T) {
	ptyR, ptyW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer ptyR.Close()
	defer ptyW.Close()

	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(0, "Allow (a)", "Allow for session (s)", "Deny (d)")))

	p := &Pane{
		name:             "eng2",
		agentType:        config.AgentTypeOpenCode,
		autoApprove:      true,
		noBracketedPaste: true,
		alive:            true,
		emu:              emu,
		ptmx:             ptyW,
	}

	// Simulate readLoop: acquire renderMu, write to emu, scan, release.
	p.renderMu.Lock()
	p.emu.Write([]byte(" "))
	approvalBytes := p.scanPermissionPrompt()
	p.renderMu.Unlock()

	if approvalBytes == nil {
		t.Fatal("expected approval bytes from scanPermissionPrompt")
	}

	p.sendMu.Lock()
	_, err = p.ptmx.Write(approvalBytes)
	p.sendMu.Unlock()
	if err != nil {
		t.Fatalf("ptmx.Write: %v", err)
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 8)
		n, _ := ptyR.Read(buf)
		done <- buf[:n]
	}()

	select {
	case got := <-done:
		if string(got) != "\x1b[C\r" {
			t.Fatalf("approval write = %q, want %q", string(got), "\x1b[C\r")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for OpenCode approval write")
	}
}

// TestScanPermissionPrompt_CodexPaneIgnoresOpenCodeDialog verifies that a
// Codex-typed pane does not match an OpenCode permission dialog.
func TestScanPermissionPrompt_CodexPaneIgnoresOpenCodeDialog(t *testing.T) {
	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(0, "Allow (a)", "Allow for session (s)", "Deny (d)")))

	p := &Pane{
		name:      "eng1",
		agentType: config.AgentTypeCodex,
		emu:       emu,
	}

	if got := p.scanPermissionPrompt(); got != nil {
		t.Fatalf("scanPermissionPrompt = %q, want nil for codex pane on OpenCode dialog", string(got))
	}
}
