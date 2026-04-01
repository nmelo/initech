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

func TestMaybeApproveOpenCodePermissionPrompt_SendsRightThenEnter(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(0, "Allow (a)", "Allow for session (s)", "Deny (d)")))

	p := &Pane{
		name:             "eng2",
		agentType:        config.AgentTypeOpenCode,
		autoApprove:      true,
		noBracketedPaste: true,
		alive:            true,
		emu:              emu,
		ptmx:             w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 8)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	if !p.maybeApproveOpenCodePermissionPrompt() {
		t.Fatal("expected OpenCode prompt approval")
	}

	select {
	case got := <-done:
		if string(got) != "\x1b[C\r" {
			t.Fatalf("approval write = %q, want %q", string(got), "\x1b[C\r")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for OpenCode approval write")
	}
}

func TestMaybeApproveOpenCodePermissionPrompt_EnterOnlyWhenPersistentSelected(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(1, "Allow (a)", "Allow for session (s)", "Deny (d)")))

	p := &Pane{
		name:             "eng2",
		agentType:        config.AgentTypeOpenCode,
		autoApprove:      true,
		noBracketedPaste: true,
		alive:            true,
		emu:              emu,
		ptmx:             w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 8)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	if !p.maybeApproveOpenCodePermissionPrompt() {
		t.Fatal("expected OpenCode prompt approval")
	}

	select {
	case got := <-done:
		if string(got) != "\r" {
			t.Fatalf("approval write = %q, want %q", string(got), "\r")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for OpenCode approval write")
	}
}

func TestMaybeApproveOpenCodePermissionPrompt_SkipsWhenRejectSelected(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(2, "Allow once", "Allow always", "Reject")))

	p := &Pane{
		name:             "eng2",
		agentType:        config.AgentTypeOpenCode,
		autoApprove:      true,
		noBracketedPaste: true,
		alive:            true,
		emu:              emu,
		ptmx:             w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 8)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	if p.maybeApproveOpenCodePermissionPrompt() {
		t.Fatal("expected reject-selected dialog to fail safe")
	}
	_ = w.Close()

	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("unexpected approval write %q", string(got))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("read goroutine did not exit")
	}
}

func TestUpdateActivity_IdleEdgeAutoApprovesOpenCodePrompt(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(0, "Allow (a)", "Allow for session (s)", "Deny (d)")))

	p := &Pane{
		name:             "eng2",
		agentType:        config.AgentTypeOpenCode,
		autoApprove:      true,
		noBracketedPaste: true,
		alive:            true,
		activity:         StateRunning,
		lastOutputTime:   time.Now().Add(-(ptyIdleTimeout + time.Second)),
		emu:              emu,
		ptmx:             w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 8)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	p.updateActivity()

	select {
	case got := <-done:
		if string(got) != "\x1b[C\r" {
			t.Fatalf("approval write = %q, want %q", string(got), "\x1b[C\r")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for OpenCode auto-approval write")
	}
}

func TestUpdateActivity_PeriodicScanThrottleSkipsRecentOpenCodeScan(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(0, "Allow (a)", "Allow for session (s)", "Deny (d)")))

	p := &Pane{
		name:              "eng2",
		agentType:         config.AgentTypeOpenCode,
		autoApprove:       true,
		noBracketedPaste:  true,
		alive:             true,
		activity:          StateIdle,
		lastOutputTime:    time.Now().Add(-(ptyIdleTimeout + time.Second)),
		lastCodexPermScan: time.Now(),
		emu:               emu,
		ptmx:              w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 8)
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

func TestUpdateActivity_IdleEdgeSkipsOpenCodeWhenAutoApproveDisabled(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(0, "Allow (a)", "Allow for session (s)", "Deny (d)")))

	p := &Pane{
		name:           "eng2",
		agentType:      config.AgentTypeOpenCode,
		autoApprove:    false,
		alive:          true,
		activity:       StateRunning,
		lastOutputTime: time.Now().Add(-(ptyIdleTimeout + time.Second)),
		emu:            emu,
		ptmx:           w,
	}

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 8)
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

func TestUpdateActivity_IdleEdgeSkipsCodexWhenOpenCodeDialogVisible(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	emu := vt.NewSafeEmulator(120, codexPermissionScanRows)
	_, _ = emu.Write([]byte(renderOpenCodePermissionDialog(0, "Allow (a)", "Allow for session (s)", "Deny (d)")))

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
		buf := make([]byte, 8)
		n, _ := r.Read(buf)
		done <- buf[:n]
	}()

	p.updateActivity()
	_ = w.Close()

	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("unexpected approval write %q for codex pane on OpenCode dialog", string(got))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("read goroutine did not exit")
	}
}
