//go:build !windows

package tui

// gbqc_rig_test.go — the real-Claude measurement leg for ini-gbqc.
//
// eng2's own DONE comment named the gap: "my cells paint the screen
// directly and do not prove Claude's actual resting render satisfies
// composerTail." Reuses zjhg_rig_test.go's already-validated real-Claude
// helpers (zjhgClaudePane, the trust-dialog wait/answer sequence) rather
// than re-deriving them -- the trust prompt is a REAL, GUARANTEED Claude
// dialog on a fresh workspace, which answers the harder half of eng2's
// concern (a real dialog, not a fixture) without needing the fragile
// permission-prompt trigger zjhg's own rig notes can fail to appear.
//
// THE DIALOG IS ANSWERED VIA A RAW PTMX WRITE, not SendKey/handleKey, and
// that is the point, not a shortcut: noteOperatorInput only fires from the
// TUI's own input-handling paths. A raw write reaches the child exactly
// the way the bead's own listed causes do (the harness resolving its own
// dialog, a timeout, a /compact redraw) -- something that is NOT an
// operator keystroke into the pane. If the latch clears here, it cleared
// on paneShowsIdleComposer's positive evidence, not on the pre-existing
// noteOperatorInput path this bead did not touch.
//
// Run: INITECH_GBQC=1 go test ./internal/tui/ -run GBQCRig -v -timeout 180s

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestGBQCRig_CorroboratedLatchClearsOnARealDialogClosing(t *testing.T) {
	if !gbqcClaudeAvailable(t) {
		return
	}

	p := zjhgClaudePane(t)
	trustUp := func() bool {
		return strings.Contains(zjhgScreen(p), "Yes, I trust this folder")
	}

	if _, ok := zjhgWait(trustUp, 60*time.Second); !ok {
		t.Fatalf("no trust dialog appeared; rig cannot proceed.\n%s", zjhgScreen(p))
	}
	t.Logf("real trust dialog open; prompt-glyph row = %q", zjhgComposerRow(p))

	// WHILE THE REAL DIALOG IS UP: paneShowsModalOnScreen must see it, and
	// paneShowsIdleComposer -- which requires BOTH no dialog AND a prompt --
	// must therefore be false. This is the ini-2jpo asymmetry's other half,
	// measured against real Claude rendering rather than a painted fixture.
	if !paneShowsModalOnScreen(p) {
		t.Errorf("paneShowsModalOnScreen = false while a real Claude dialog is open.\n%s",
			zjhgScreen(p))
	}
	if paneShowsIdleComposer(p) {
		t.Errorf("paneShowsIdleComposer = true while a real Claude dialog is open -- this "+
			"would clear a latch on a dialog that is genuinely still up.\n%s", zjhgScreen(p))
	}

	// CORROBORATE A LATCH ON THIS REAL PANE, matching what an OSC 777 declare
	// plus a sighting would have left behind. Done directly rather than via
	// the OSC path because this rig measures the CLEAR, not the raise (the
	// raise is zjhg's own territory).
	p.mu.Lock()
	p.dialogOpen = true
	p.dialogCorroborated = true
	p.dialogOpenAt = time.Now()
	p.mu.Unlock()

	tui := &TUI{panes: []PaneView{p}, agentEvents: make(chan AgentEvent, 16), quitCh: make(chan struct{})}
	// Drive maintenance once while the dialog is still up: the latch must
	// NOT clear yet, and the age-only audit must not touch a corroborated
	// latch either (ini-9gvn, unaffected by this bead).
	tui.modalMaintenance(time.Now())
	if !p.dialogLatched() {
		t.Fatal("the latch cleared while a real dialog was still on screen -- forges an " +
			"operator answer into a dialog that is genuinely open")
	}

	// ANSWER THE DIALOG VIA A RAW WRITE (bypasses noteOperatorInput; see the
	// file header for why this is the faithful case, not a shortcut).
	if _, err := p.ptmx.Write([]byte("\r")); err != nil {
		t.Fatalf("answer trust dialog: %v", err)
	}
	if _, ok := zjhgWait(func() bool {
		return !trustUp() && strings.Contains(zjhgScreen(p), "❯")
	}, 90*time.Second); !ok {
		t.Fatalf("no composer after answering the trust dialog; rig cannot proceed.\n%s",
			zjhgScreen(p))
	}
	zjhgClearComposer(p)
	t.Logf("real composer up; row = %q", zjhgComposerRow(p))

	// THE CRUX: does REAL Claude's actual resting render satisfy
	// paneShowsIdleComposer -- eng2's own named gap, not asserted by any
	// fixture cell. Measured directly first, before driving the tick loop,
	// so a false negative here is diagnosable on its own.
	if !paneShowsIdleComposer(p) {
		t.Fatalf("paneShowsIdleComposer = false against Claude's real resting composer -- "+
			"composerTail/paneShowsModalOnScreen do not recognize real Claude's actual idle "+
			"screen the way the emulator-painted fixture cells assumed. This is exactly the "+
			"gap eng2's DONE comment named as unmeasured.\n%s", zjhgScreen(p))
	}
	t.Log("paneShowsIdleComposer = true against real Claude's resting composer")

	// NOW DRIVE THE REAL PRODUCTION TICK LOOP (not the audit function
	// directly) across the real idlePromptStableWindow, and confirm the
	// latch actually clears -- the end-to-end claim, not just the predicate.
	cleared, waited := false, time.Duration(0)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		tui.modalMaintenance(time.Now())
		if !p.dialogLatched() {
			cleared = true
			waited = 15*time.Second - time.Until(deadline)
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !cleared {
		t.Fatalf("the latch never cleared within 15s of a real, genuinely idle Claude "+
			"composer -- the self-heal this bead adds does not fire against real rendering, "+
			"even though it fires against the fixture cells.\n%s", zjhgScreen(p))
	}
	t.Logf("latch cleared after ~%v of real idle composer (window is %v)", waited, idlePromptStableWindow)

	if got := p.QueuedMessageCount(); got != 0 {
		t.Errorf("QueuedMessageCount = %d after the clear; nothing was enqueued in this rig, "+
			"so a nonzero count would mean modalMaintenance's drain step misbehaved", got)
	}
}

// gbqcClaudeAvailable is the two skip conditions this rig shares with
// zjhg's own, checked once so the failure mode reads the same way.
func gbqcClaudeAvailable(t *testing.T) bool {
	t.Helper()
	if os.Getenv("INITECH_GBQC") != "1" {
		t.Skip("composed rig: set INITECH_GBQC=1 (real Claude, ~60-120s)")
		return false
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
		return false
	}
	return true
}
