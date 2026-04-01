package tui

import (
	"os"
	"strings"
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

func TestUpdateActivity_IdleEdgeAutoApprovesCodexPrompt(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("1. Yes (y)\n2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:           "eng1",
		agentType:      config.AgentTypeCodex,
		autoApprove:    true,
		alive:          true,
		activity:       StateRunning,
		lastOutputTime: time.Now().Add(-(ptyIdleTimeout + time.Second)),
		emu:            emu,
		ptmx:           w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	p.updateActivity()

	select {
	case got := <-done:
		if string(got) != "p" {
			t.Fatalf("approval write = %q, want %q", string(got), "p")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for auto-approval write")
	}
}

func TestUpdateActivity_IdleEdgeSkipsClaudePanePrompt(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:           "eng1",
		agentType:      config.AgentTypeClaudeCode,
		autoApprove:    false,
		alive:          true,
		activity:       StateRunning,
		lastOutputTime: time.Now().Add(-(ptyIdleTimeout + time.Second)),
		emu:            emu,
		ptmx:           w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	p.updateActivity()
	_ = w.Close()

	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("unexpected approval write %q for Claude pane", string(got))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("read goroutine did not exit")
	}
}

func TestUpdateActivity_IdleEdgeSkipsWhenPromptMissing(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("normal idle prompt"))

	p := &Pane{
		name:           "eng1",
		agentType:      config.AgentTypeCodex,
		autoApprove:    true,
		alive:          true,
		activity:       StateRunning,
		lastOutputTime: time.Now().Add(-(ptyIdleTimeout + time.Second)),
		emu:            emu,
		ptmx:           w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	p.updateActivity()
	_ = w.Close()

	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("unexpected approval write %q without prompt", string(got))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("read goroutine did not exit")
	}
}

func TestUpdateActivity_IdleToIdleDoesNotReapprovePrompt(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte(strings.Repeat("\n", 7) + "2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:              "eng1",
		agentType:         config.AgentTypeCodex,
		autoApprove:       true,
		alive:             true,
		activity:          StateIdle,
		lastOutputTime:    time.Now().Add(-(ptyIdleTimeout + time.Second)),
		lastCodexPermScan: time.Now(),
		emu:               emu,
		ptmx:              w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	p.updateActivity()
	_ = w.Close()

	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("unexpected approval write %q on idle->idle update", string(got))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("read goroutine did not exit")
	}
}

func TestUpdateActivity_PeriodicScanAutoApprovesRunningCodexPrompt(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:              "eng1",
		agentType:         config.AgentTypeCodex,
		autoApprove:       true,
		alive:             true,
		activity:          StateRunning,
		lastOutputTime:    time.Now(),
		lastCodexPermScan: time.Now().Add(-(codexPermissionScanInterval + time.Second)),
		emu:               emu,
		ptmx:              w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	p.updateActivity()

	select {
	case got := <-done:
		if string(got) != "p" {
			t.Fatalf("approval write = %q, want %q", string(got), "p")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for periodic auto-approval write")
	}
}

func TestUpdateActivity_IdleEdgeAutoApprovesTwoOptionPromptWithEnter(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("1. Yes, proceed (y)\n2. No, and tell Codex what to do differently (esc)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:           "eng1",
		agentType:      config.AgentTypeCodex,
		autoApprove:    true,
		alive:          true,
		activity:       StateRunning,
		lastOutputTime: time.Now().Add(-(ptyIdleTimeout + time.Second)),
		emu:            emu,
		ptmx:           w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	p.updateActivity()

	select {
	case got := <-done:
		if string(got) != "\r" {
			t.Fatalf("approval write = %q, want %q", string(got), "\r")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for two-option auto-approval write")
	}
}

func TestUpdateActivity_PeriodicScanThrottleSkipsRecentCodexScan(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:              "eng1",
		agentType:         config.AgentTypeCodex,
		autoApprove:       true,
		alive:             true,
		activity:          StateRunning,
		lastOutputTime:    time.Now(),
		lastCodexPermScan: time.Now(),
		emu:               emu,
		ptmx:              w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	p.updateActivity()
	_ = w.Close()

	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("unexpected approval write %q with recent scan timestamp", string(got))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("read goroutine did not exit")
	}
}

func TestUpdateActivity_IdleEdgeSkipsGenericTypedInputPanePrompt(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:           "eng1",
		agentType:      config.AgentTypeGeneric,
		autoApprove:    false,
		alive:          true,
		activity:       StateRunning,
		lastOutputTime: time.Now().Add(-(ptyIdleTimeout + time.Second)),
		emu:            emu,
		ptmx:           w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	p.updateActivity()
	_ = w.Close()

	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("unexpected approval write %q for generic pane", string(got))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("read goroutine did not exit")
	}
}

func TestUpdateActivity_IdleEdgeSkipsCodexPromptWhenAutoApproveDisabled(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:           "eng1",
		agentType:      config.AgentTypeCodex,
		autoApprove:    false,
		alive:          true,
		activity:       StateRunning,
		lastOutputTime: time.Now().Add(-(ptyIdleTimeout + time.Second)),
		emu:            emu,
		ptmx:           w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	p.updateActivity()
	_ = w.Close()

	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("unexpected approval write %q with autoApprove disabled", string(got))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("read goroutine did not exit")
	}
}

func TestUpdateActivity_IdleEdgeApprovesClaudePromptWhenAutoApproveEnabled(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := testPane("eng1").emu
	emu.Resize(80, codexPermissionScanRows)
	emu.Write([]byte("2. Yes, and dont ask again (p)\nPress enter to confirm or esc to cancel"))

	p := &Pane{
		name:           "eng1",
		agentType:      config.AgentTypeClaudeCode,
		autoApprove:    true,
		alive:          true,
		activity:       StateRunning,
		lastOutputTime: time.Now().Add(-(ptyIdleTimeout + time.Second)),
		emu:            emu,
		ptmx:           w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	p.updateActivity()

	select {
	case got := <-done:
		if string(got) != "p" {
			t.Fatalf("approval write = %q, want %q", string(got), "p")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for auto-approval write")
	}
}
