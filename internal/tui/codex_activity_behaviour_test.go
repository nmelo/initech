// codex_activity_behaviour_test.go drives updateActivity and the send-path
// predicates through the EXISTING API only, so these tests compile against
// the shipped byte-recency rule and were confirmed RED there first: a Codex
// pane whose idle prompt animates read as running forever (ini-lnnk).
package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"

	"github.com/nmelo/initech/internal/config"
)

// codexFixtureVersion pins the Codex CLI the frames were captured from.
const codexFixtureVersion = "0.154.0"

// loadCodexFixture returns the rendered rows of a captured Codex frame.
func loadCodexFixture(t *testing.T, name string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "codex", codexFixtureVersion, name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

// codexPaneShowing builds an alive pane of the given agent type whose screen
// shows the captured frame, with output bytes flowing RIGHT NOW -- the
// animation's signature.
func codexPaneShowing(t *testing.T, agentType, fixture string) *Pane {
	t.Helper()
	p := &Pane{
		name:                  "eng3",
		emu:                   vt.NewSafeEmulator(100, 50),
		alive:                 true,
		visible:               true,
		agentType:             agentType,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now(),
	}
	paintRows(t, p, loadCodexFixture(t, fixture))
	return p
}

// paintRows writes rows into the pane's emulator bottom-aligned, the way the
// real frame sits above the composer.
func paintRows(t *testing.T, p *Pane, rows []string) {
	t.Helper()
	if _, err := p.emu.Write([]byte("\x1b[2J\x1b[H")); err != nil {
		t.Fatalf("clear: %v", err)
	}
	pad := p.emu.Height() - len(rows)
	if pad < 0 {
		pad = 0
	}
	if _, err := p.emu.Write([]byte(strings.Repeat("\r\n", pad) + strings.Join(rows, "\r\n"))); err != nil {
		t.Fatalf("paint: %v", err)
	}
}

// The bug: idle prompt, bytes flowing (animation), byte recency says running.
func TestUpdateActivity_Codex_IdleAtAnimatedPrompt(t *testing.T) {
	p := codexPaneShowing(t, config.AgentTypeCodex, "idle-frame.txt")
	p.updateActivity()
	if p.Activity() != StateIdle {
		t.Errorf("codex at its idle prompt with animation bytes flowing: activity = %v, want StateIdle", p.Activity())
	}
}

// The dot INSIDE the prompt text must not defeat the idle signature.
func TestUpdateActivity_Codex_IdleWithDotInsidePrompt(t *testing.T) {
	p := codexPaneShowing(t, config.AgentTypeCodex, "idle-frame-dotted.txt")
	p.updateActivity()
	if p.Activity() != StateIdle {
		t.Errorf("codex idle frame with a braille dot inside the prompt text: activity = %v, want StateIdle", p.Activity())
	}
}

// Working on screen is running even through a long byte silence: the
// discriminator is the status line, not byte flow in either direction.
func TestUpdateActivity_Codex_RunningWithWorkingLine_DespiteSilence(t *testing.T) {
	p := codexPaneShowing(t, config.AgentTypeCodex, "running-frame.txt")
	p.lastOutputTime = time.Now().Add(-20 * time.Second) // past the old 15s codex threshold
	p.updateActivity()
	if p.Activity() != StateRunning {
		t.Errorf("codex with 'Working (' on screen after 20s of silence: activity = %v, want StateRunning", p.Activity())
	}
}

// Neither signature on screen: hold the last state (pm's table, row 4).
func TestUpdateActivity_Codex_HoldsLastStateWithoutASignature(t *testing.T) {
	p := codexPaneShowing(t, config.AgentTypeCodex, "idle-frame.txt")
	p.updateActivity()
	if p.Activity() != StateIdle {
		t.Fatalf("precondition: idle frame → %v", p.Activity())
	}
	paintRows(t, p, []string{"some prose the agent printed", "with no prompt and no status line"})
	p.lastOutputTime = time.Now()
	p.updateActivity()
	if p.Activity() != StateIdle {
		t.Errorf("no signature on screen: activity = %v, want the held StateIdle", p.Activity())
	}
	// Held state is running too -- until the bytes stop for the byte
	// threshold, when silence alone is enough for idle (a Codex whose prompt
	// the signature does not know must not read as running forever).
	q := codexPaneShowing(t, config.AgentTypeCodex, "running-frame.txt")
	q.updateActivity()
	paintRows(t, q, []string{"some prose", "no prompt, no status line"})
	q.lastOutputTime = time.Now()
	q.updateActivity()
	if q.Activity() != StateRunning {
		t.Errorf("no signature, bytes flowing: activity = %v, want the held StateRunning", q.Activity())
	}
	q.lastOutputTime = time.Now().Add(-(ptyIdleTimeoutCodex + time.Second))
	q.updateActivity()
	if q.Activity() != StateIdle {
		t.Errorf("no signature, %v of silence: activity = %v, want StateIdle (silence stays sufficient)", ptyIdleTimeoutCodex+time.Second, q.Activity())
	}
}

// claude-code keeps byte recency: the same animated frame reads running
// because bytes are flowing, exactly as today (AC1 regression guard).
func TestUpdateActivity_ClaudeCode_KeepsByteRecency(t *testing.T) {
	p := codexPaneShowing(t, config.AgentTypeClaudeCode, "idle-frame.txt")
	p.updateActivity()
	if p.Activity() != StateRunning {
		t.Errorf("claude-code with bytes flowing: activity = %v, want StateRunning (byte recency unchanged)", p.Activity())
	}
	p.lastOutputTime = time.Now().Add(-(ptyIdleTimeout + time.Second))
	p.updateActivity()
	if p.Activity() != StateIdle {
		t.Errorf("claude-code after %v of silence: activity = %v, want StateIdle", ptyIdleTimeout+time.Second, p.Activity())
	}
}

// opencode: no real pane in this fleet to capture from (AC8), so it keeps the
// byte-recency rule until measured.
func TestUpdateActivity_OpenCode_KeepsByteRecency(t *testing.T) {
	p := codexPaneShowing(t, config.AgentTypeOpenCode, "idle-frame.txt")
	p.updateActivity()
	if p.Activity() != StateRunning {
		t.Errorf("opencode with bytes flowing: activity = %v, want StateRunning (byte recency kept, AC8)", p.Activity())
	}
}

// Send path: an idle-by-signature codex takes the DIRECT path, so its ready
// check must not wait for byte silence the animation never provides.
func TestIsCodexReadyForSend_IdleFrameWithAnimationBytes(t *testing.T) {
	p := codexPaneShowing(t, config.AgentTypeCodex, "idle-frame.txt")
	if !p.isCodexReadyForSend() {
		t.Error("codex at its idle prompt with bytes flowing should be ready for send")
	}
	running := codexPaneShowing(t, config.AgentTypeCodex, "running-frame.txt")
	if running.isCodexReadyForSend() {
		t.Error("codex with 'Working (' on screen must not be ready for send")
	}
}

// The existing composer-only ready frame (no footer, no animation) stays ready.
func TestIsCodexReadyForSend_BareComposerStillReady(t *testing.T) {
	p := codexPaneShowing(t, config.AgentTypeCodex, "idle-frame.txt")
	paintRows(t, p, []string{"done.", "", "› "})
	p.lastOutputTime = time.Now().Add(-3 * time.Second)
	if !p.isCodexReadyForSend() {
		t.Error("bare '› ' composer should still read ready")
	}
}
