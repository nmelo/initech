package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ── quit confirmation ─────────────────────────────────────────────────

func TestQuitRequiresConfirmation(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true))

	// First Enter: should set up confirmation, not quit.
	quit := tui.execCmd("quit")
	if quit {
		t.Fatal("quit should not exit on first Enter; confirmation required")
	}
	if tui.cmd.pendingConfirm != "quit" {
		t.Errorf("pendingConfirm = %q, want %q", tui.cmd.pendingConfirm, "quit")
	}
	if tui.cmd.confirmMsg == "" {
		t.Error("confirmMsg should be set after quit")
	}
	if !tui.cmd.active {
		t.Error("modal should remain active while confirmation is pending")
	}
}

func TestQuitShorthandRequiresConfirmation(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true))
	quit := tui.execCmd("q")
	if quit {
		t.Fatal("q should not exit on first Enter")
	}
	if tui.cmd.pendingConfirm != "quit" {
		t.Errorf("pendingConfirm = %q, want %q", tui.cmd.pendingConfirm, "quit")
	}
}

func TestQuitConfirmedOnSecondEnter(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true))
	tui.cmd.pendingConfirm = "quit"
	tui.cmd.confirmMsg = "Quit will stop all agents."
	tui.cmd.confirmExpiry = time.Now().Add(3 * time.Second)
	tui.cmd.active = true

	quit := tui.handleCmdKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !quit {
		t.Error("second Enter on quit confirmation should return true (quit)")
	}
	if tui.cmd.pendingConfirm != "" {
		t.Error("pendingConfirm should be cleared after confirm")
	}
}

func TestQuitCancelledWithEsc(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true))
	tui.cmd.pendingConfirm = "quit"
	tui.cmd.confirmExpiry = time.Now().Add(3 * time.Second)
	tui.cmd.active = true

	quit := tui.handleCmdKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if quit {
		t.Error("Esc should cancel quit, not exit")
	}
	if tui.cmd.pendingConfirm != "" {
		t.Error("pendingConfirm should be cleared after Esc")
	}
	if tui.cmd.active {
		t.Error("modal should close after Esc")
	}
}

func TestQuitConfirmationExpires(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true))
	tui.cmd.pendingConfirm = "quit"
	tui.cmd.confirmExpiry = time.Now().Add(-1 * time.Second) // already expired
	tui.cmd.active = true

	quit := tui.handleCmdKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if quit {
		t.Error("expired confirmation should not quit")
	}
	if tui.cmd.pendingConfirm != "" {
		t.Error("pendingConfirm should be cleared after expiry")
	}
	if tui.cmd.active {
		t.Error("modal should close after expiry")
	}
}

func TestQuitConfirmationCancelledByOtherKey(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true))
	tui.cmd.pendingConfirm = "quit"
	tui.cmd.confirmExpiry = time.Now().Add(3 * time.Second)
	tui.cmd.active = true

	quit := tui.handleCmdKey(tcell.NewEventKey(tcell.KeyRune, 'x', 0))
	if quit {
		t.Error("other key should cancel confirmation, not quit")
	}
	if tui.cmd.pendingConfirm != "" {
		t.Error("pendingConfirm should be cleared on any other key")
	}
}

// ── remove confirmation ───────────────────────────────────────────────

func TestRemoveRequiresConfirmation(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
	)

	quit := tui.execCmd("remove eng1")
	if quit {
		t.Fatal("remove should not quit")
	}
	if tui.cmd.pendingConfirm != "remove eng1" {
		t.Errorf("pendingConfirm = %q, want %q", tui.cmd.pendingConfirm, "remove eng1")
	}
	if !tui.cmd.active {
		t.Error("modal should remain active while confirmation is pending")
	}
}

func TestRemoveShorthandRequiresConfirmation(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
	)
	tui.execCmd("rm eng1")
	if tui.cmd.pendingConfirm != "remove eng1" {
		t.Errorf("pendingConfirm = %q, want %q", tui.cmd.pendingConfirm, "remove eng1")
	}
}

func TestRemoveUnknownAgentErrors(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true))
	tui.execCmd("remove nobody")
	if tui.cmd.pendingConfirm != "" {
		t.Error("unknown agent should not set pendingConfirm")
	}
	if tui.cmd.error == "" {
		t.Error("unknown agent should set error message")
	}
}

func TestRemoveNoArgErrors(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true))
	tui.execCmd("remove")
	if tui.cmd.pendingConfirm != "" {
		t.Error("remove with no arg should not set pendingConfirm")
	}
	if tui.cmd.error == "" {
		t.Error("remove with no arg should set error message")
	}
}

// ── restart confirmation ──────────────────────────────────────────────

func TestRestartNamedRequiresConfirmation(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
	)

	tui.execCmd("restart eng1")
	if tui.cmd.pendingConfirm != "restart eng1" {
		t.Errorf("pendingConfirm = %q, want %q", tui.cmd.pendingConfirm, "restart eng1")
	}
	if !tui.cmd.active {
		t.Error("modal should remain active while confirmation is pending")
	}
}

func TestRestartNamedUnknownErrors(t *testing.T) {
	tui := newTestTUI(newTestPane("eng1", true))
	tui.execCmd("restart nobody")
	if tui.cmd.pendingConfirm != "" {
		t.Error("unknown agent should not set pendingConfirm")
	}
	if tui.cmd.error == "" {
		t.Error("unknown agent should set error message")
	}
}

// ── confirmMsg content ────────────────────────────────────────────────

func TestConfirmMsgContainsAgentName(t *testing.T) {
	tui := newTestTUI(
		newTestPane("super", true),
		newTestPane("eng1", true),
	)
	tui.execCmd("remove eng1")
	if tui.cmd.confirmMsg == "" {
		t.Fatal("confirmMsg should be set")
	}
	// Message should mention the agent name.
	if !containsSubstr(tui.cmd.confirmMsg, "eng1") {
		t.Errorf("confirmMsg %q should contain 'eng1'", tui.cmd.confirmMsg)
	}
}

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
