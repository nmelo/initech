// codex_activity_test.go pins the Codex screen-signature predicate over the
// captured 0.154.0 frames (ini-lnnk), the idle-with-bead net it restores,
// the send-path consequence, and the once-per-transition log.
package tui

import (
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
)

func TestCodexScreenSignature_RunningFrame(t *testing.T) {
	state, ok := codexScreenSignature(loadCodexFixture(t, "running-frame.txt"))
	if !ok || state != StateRunning {
		t.Errorf("running frame → (%v, %v), want (StateRunning, true)", state, ok)
	}
}

func TestCodexScreenSignature_IdleFrame(t *testing.T) {
	state, ok := codexScreenSignature(loadCodexFixture(t, "idle-frame.txt"))
	if !ok || state != StateIdle {
		t.Errorf("idle frame → (%v, %v), want (StateIdle, true)", state, ok)
	}
}

// The braille strip is load-bearing HERE and only here: the dot sits inside
// the prompt text, so without the strip the signature never matches.
func TestCodexScreenSignature_IdleFrameWithDotInsidePrompt(t *testing.T) {
	rows := loadCodexFixture(t, "idle-frame-dotted.txt")
	found := false
	for _, r := range rows {
		if r == "›⠁Ask Codex to do anything   ⠈          ⠁" {
			found = true
		}
	}
	if !found {
		t.Fatal("fixture no longer carries the dot-inside-prompt row; the capture is the evidence")
	}
	state, ok := codexScreenSignature(rows)
	if !ok || state != StateIdle {
		t.Errorf("dotted idle frame → (%v, %v), want (StateIdle, true)", state, ok)
	}
}

func TestCodexScreenSignature_UnknownWhenNeither(t *testing.T) {
	if _, ok := codexScreenSignature([]string{"just prose", "› 1. yes, continue", "  2. no, quit"}); ok {
		t.Error("a numbered dialog and prose carry neither the idle nor the running signature")
	}
	if _, ok := codexScreenSignature(nil); ok {
		t.Error("an empty screen has no signature")
	}
}

// The trust prompt and MCP boot are not-ready screens, never idle.
func TestCodexScreenSignature_StartupScreensAreNotIdle(t *testing.T) {
	rows := []string{"Do you trust the contents of this directory?", "", "› 1. Yes, continue", "  2. No, quit", "Press enter to continue"}
	if state, ok := codexScreenSignature(rows); ok && state == StateIdle {
		t.Error("trust prompt read as idle")
	}
	rows = []string{"Booting MCP server: codex_apps", "", "› "}
	if state, ok := codexScreenSignature(rows); ok && state == StateIdle {
		t.Error("booting screen read as idle")
	}
}

// The net is back (AC4): idle by signature past the bead threshold, with the
// animation still resetting lastOutputTime every frame, FIRES.
func TestUpdateActivity_Codex_IdleWithBead_FiresDespiteAnimationBytes(t *testing.T) {
	ch := make(chan AgentEvent, 4)
	p := codexPaneShowing(t, config.AgentTypeCodex, "idle-frame.txt")
	p.beadIDs = []string{"ini-z5uw"}
	p.beadAssignedAt = time.Now().Add(-2 * time.Hour)
	p.eventCh = ch
	p.sigIdleSince = time.Now().Add(-65 * time.Second) // idle by signature for 65s
	p.lastOutputTime = time.Now()                      // ...while bytes still flow
	p.updateActivity()
	select {
	case ev := <-ch:
		if ev.Type != EventAgentIdleWithBead || ev.BeadID != "ini-z5uw" {
			t.Errorf("event = %+v, want EventAgentIdleWithBead for ini-z5uw", ev)
		}
	default:
		t.Fatal("idle-with-bead did not fire for a codex pane idle by signature with animation bytes flowing")
	}
}

// Control: Working on screen never fires the net, however long the clock.
func TestUpdateActivity_Codex_IdleWithBead_NoFireWhileWorking(t *testing.T) {
	ch := make(chan AgentEvent, 4)
	p := codexPaneShowing(t, config.AgentTypeCodex, "running-frame.txt")
	p.beadIDs = []string{"ini-z5uw"}
	p.beadAssignedAt = time.Now().Add(-2 * time.Hour)
	p.eventCh = ch
	p.sigIdleSince = time.Now().Add(-65 * time.Second)
	p.lastOutputTime = time.Now().Add(-65 * time.Second)
	p.updateActivity()
	select {
	case ev := <-ch:
		t.Fatalf("fired %+v while 'Working (' is on screen", ev)
	default:
	}
	if !p.sigIdleSince.IsZero() {
		t.Error("a running signature must clear the idle clock")
	}
}

// AC5: the queued-submit path follows the signature.
func TestCodexShouldQueueSubmit_FollowsSignature(t *testing.T) {
	idle := codexPaneShowing(t, config.AgentTypeCodex, "idle-frame.txt")
	idle.updateActivity()
	if codexShouldQueueSubmit(idle) {
		t.Error("idle-by-signature codex must take the direct path, not the queued submit")
	}
	running := codexPaneShowing(t, config.AgentTypeCodex, "running-frame.txt")
	running.updateActivity()
	if !codexShouldQueueSubmit(running) {
		t.Error("running-by-signature codex must take the queued-submit path")
	}
	claude := codexPaneShowing(t, config.AgentTypeClaudeCode, "running-frame.txt")
	claude.updateActivity()
	if codexShouldQueueSubmit(claude) {
		t.Error("the queued submit is a codex-only path")
	}
}

// AC6: one transition record per state change, none per frame.
func TestUpdateActivity_TransitionRecordedOncePerChange(t *testing.T) {
	p := codexPaneShowing(t, config.AgentTypeCodex, "running-frame.txt")
	p.activity = StateIdle
	p.updateActivity() // idle → running: one transition
	p.updateActivity() // still running: none
	p.updateActivity()
	if p.activityTransitions != 1 {
		t.Errorf("transitions = %d after one change and two same-state ticks, want 1", p.activityTransitions)
	}
	if p.activityDecidedBy != activityByCodexScreen {
		t.Errorf("decided by %q, want %q", p.activityDecidedBy, activityByCodexScreen)
	}
	paintRows(t, p, loadCodexFixture(t, "idle-frame.txt"))
	p.updateActivity() // running → idle: second transition
	if p.activityTransitions != 2 {
		t.Errorf("transitions = %d after a second change, want 2", p.activityTransitions)
	}
	c := codexPaneShowing(t, config.AgentTypeClaudeCode, "idle-frame.txt")
	c.activity = StateIdle
	c.updateActivity()
	if c.activityDecidedBy != activityByByteRecency {
		t.Errorf("claude-code decided by %q, want %q", c.activityDecidedBy, activityByByteRecency)
	}
}
