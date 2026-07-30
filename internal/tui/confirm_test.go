package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ── quit confirmation ─────────────────────────────────────────────────

func TestQuitRequiresConfirmation(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))

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
	tui := newTestTUI(testPane("eng1"))
	quit := tui.execCmd("q")
	if quit {
		t.Fatal("q should not exit on first Enter")
	}
	if tui.cmd.pendingConfirm != "quit" {
		t.Errorf("pendingConfirm = %q, want %q", tui.cmd.pendingConfirm, "quit")
	}
}

func TestQuitConfirmedOnSecondEnter(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
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
	tui := newTestTUI(testPane("eng1"))
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

func TestQuitConfirmationAutoCancelsViaPrune(t *testing.T) {
	// ini-a1e.31: expiry is handled by pruneConfirmation() on each render
	// tick, not inside handleCmdKey. This prevents auto-cancel racing with
	// Enter pressed at exactly the deadline.
	tui := newTestTUI(testPane("eng1"))
	tui.cmd.pendingConfirm = "quit"
	tui.cmd.confirmExpiry = time.Now().Add(-1 * time.Second) // already expired
	tui.cmd.active = true

	// pruneConfirmation() (called by the ticker) auto-cancels expired confirmations.
	tui.pruneConfirmation()
	if tui.cmd.pendingConfirm != "" {
		t.Error("pruneConfirmation should clear expired pendingConfirm")
	}
	if tui.cmd.active {
		t.Error("pruneConfirmation should deactivate modal on expiry")
	}
}

func TestQuitConfirmationEnterAtDeadlineStillConfirms(t *testing.T) {
	// ini-a1e.31: if the operator presses Enter at exactly the deadline,
	// the key handler fires before the next prune tick, so Enter confirms.
	tui := newTestTUI(testPane("eng1"))
	tui.cmd.pendingConfirm = "quit"
	tui.cmd.confirmExpiry = time.Now().Add(-1 * time.Millisecond) // just expired
	tui.cmd.active = true

	// Key arrives before tick fires pruneConfirmation — pendingConfirm still set.
	quit := tui.handleCmdKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !quit {
		t.Error("Enter at exactly the deadline should confirm (not race-cancel)")
	}
}

func TestQuitConfirmationCancelledByOtherKey(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
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
		testPane("super"),
		testPane("eng1"),
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
		testPane("super"),
		testPane("eng1"),
	)
	tui.execCmd("rm eng1")
	if tui.cmd.pendingConfirm != "remove eng1" {
		t.Errorf("pendingConfirm = %q, want %q", tui.cmd.pendingConfirm, "remove eng1")
	}
}

func TestRemoveUnknownAgentErrors(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.execCmd("remove nobody")
	if tui.cmd.pendingConfirm != "" {
		t.Error("unknown agent should not set pendingConfirm")
	}
	if tui.cmd.error == "" {
		t.Error("unknown agent should set error message")
	}
}

func TestRemoveNoArgErrors(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
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
		testPane("super"),
		testPane("eng1"),
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
	tui := newTestTUI(testPane("eng1"))
	tui.execCmd("restart nobody")
	if tui.cmd.pendingConfirm != "" {
		t.Error("unknown agent should not set pendingConfirm")
	}
	if tui.cmd.error == "" {
		t.Error("unknown agent should set error message")
	}
}

// ── remote-stop confirmation (ini-z61) ────────────────────────────────
//
// remote-stop was offered by the command bar's autocomplete (commandNames)
// and appeared implemented (executeConfirmed had a full case for it) since
// commit 78885e40, but execCmd's dispatcher had no route to it, so nothing
// ever set pendingConfirm and typing "remote-stop <peer>" always returned
// "unknown command" -- half-wired since birth. These tests drive the exact
// user-facing path (execCmd, then Enter via handleCmdKey) rather than
// calling executeConfirmed directly, so they fail the same way a real user
// hitting Enter would have failed before this fix.

func TestRemoteStopRequiresConfirmation(t *testing.T) {
	rp := NewRemotePane("agent1", "workbench", nil, nil, 40, 10)
	tui := &TUI{panes: []PaneView{rp}}

	quit := tui.execCmd("remote-stop workbench")
	if quit {
		t.Fatal("remote-stop should not quit")
	}
	if got := fmt.Sprintf("unknown command %q", "remote-stop"); tui.cmd.error == got {
		t.Fatalf("remote-stop rejected as unknown command -- execCmd has no route to it")
	}
	if tui.cmd.pendingConfirm != "remote-stop workbench" {
		t.Errorf("pendingConfirm = %q, want %q", tui.cmd.pendingConfirm, "remote-stop workbench")
	}
	if !tui.cmd.active {
		t.Error("modal should remain active while confirmation is pending")
	}
}

func TestRemoteStopUnknownPeerErrors(t *testing.T) {
	rp := NewRemotePane("agent1", "workbench", nil, nil, 40, 10)
	tui := &TUI{panes: []PaneView{rp}}

	tui.execCmd("remote-stop nosuchpeer")
	if tui.cmd.pendingConfirm != "" {
		t.Error("unknown peer should not set pendingConfirm")
	}
	// Must be cmdRemoteStop's own validation error, not execCmd's generic
	// "unknown command" fallback -- the latter would pass this assertion
	// vacuously whether or not remote-stop is actually routed at all.
	if got := fmt.Sprintf("unknown command %q", "remote-stop"); tui.cmd.error == got {
		t.Fatalf("remote-stop rejected as unknown command -- execCmd has no route to it")
	}
	if tui.cmd.error == "" {
		t.Error("unknown peer should set an error message")
	}
	if !containsSubstr(tui.cmd.error, "nosuchpeer") {
		t.Errorf("error message %q should name the unknown peer", tui.cmd.error)
	}
}

func TestRemoteStopNoArgErrors(t *testing.T) {
	tui := &TUI{}
	tui.execCmd("remote-stop")
	if tui.cmd.pendingConfirm != "" {
		t.Error("remote-stop with no arg should not set pendingConfirm")
	}
	if got := fmt.Sprintf("unknown command %q", "remote-stop"); tui.cmd.error == got {
		t.Fatalf("remote-stop rejected as unknown command -- execCmd has no route to it")
	}
	if tui.cmd.error == "" {
		t.Error("remote-stop with no arg should set an error message")
	}
}

func TestRemoteStopConfirmedCallsRemoteStopPeer(t *testing.T) {
	rp := NewRemotePane("agent1", "workbench", nil, nil, 40, 10)
	tui := &TUI{panes: []PaneView{rp}}

	tui.execCmd("remote-stop workbench")
	if tui.cmd.pendingConfirm != "remote-stop workbench" {
		t.Fatalf("pendingConfirm = %q, want %q -- remote-stop did not reach the confirmation step", tui.cmd.pendingConfirm, "remote-stop workbench")
	}
	tui.cmd.confirmExpiry = time.Now().Add(3 * time.Second)

	// Second Enter: executeConfirmed's existing "remote-stop" case must
	// actually run. mux is nil so remoteStopPeer's stop_agent send is
	// skipped (no real daemon needed to prove the dispatch path), leaving
	// zero of the one target actually stopped -- which ini-om0 made an
	// error rather than a silent "0 agent(s) stopped" success, so the
	// error text differs from the pre-fix message this test originally
	// checked. Reaching ANY result message at all (still routed through
	// cmd.error, still naming the peer) is what proves the command-bar-to-
	// remoteStopPeer path is wired end to end, not just that pendingConfirm
	// gets set.
	quit := tui.handleCmdKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if quit {
		t.Error("remote-stop confirmation should not quit")
	}
	if !containsSubstr(tui.cmd.error, "remote-stop") || !containsSubstr(tui.cmd.error, "workbench") {
		t.Errorf("executeConfirmed should report the remote-stop result, got error=%q", tui.cmd.error)
	}
}

// ── confirmMsg content ────────────────────────────────────────────────

func TestConfirmMsgContainsAgentName(t *testing.T) {
	tui := newTestTUI(
		testPane("super"),
		testPane("eng1"),
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
