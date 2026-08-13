package tui

// attention_geometry_test.go pins that dialog detection does not depend on WHERE
// in the pane the dialog happens to sit (ini-t6n).
//
// THE BUG THIS ENCODES. Detection scanned a fixed 14 bottom rows. In a composed
// 50-row TUI, a permission prompt's "Do you want to proceed?" anchor sat ~16
// rows above the pane bottom -- the options, the esc/tab/ctrl+e hints, a spacer,
// the input box, the bypass footer and padding all sit below it -- so the scan
// never saw a dialog that was plainly on screen. paneHasModal stayed false,
// markModalSeen never set, and ini-2x8.2's earned-clear gate refused (correctly,
// by its own design) to retire a dialog it had never seen. The row ticked
// forever. The gate was not the bug; the window it looked through was.
//
// The original signature captures were REAL but taken in a raw 40-row PTY, where
// the same dialog sits much lower. That is the wrong-mechanism trap in its
// purest form: a faithful measurement of the wrong configuration. The dialog
// text below is still the real captured text -- only the geometry is varied.
//
// SO THESE TESTS SWEEP THE ANCHOR rather than fixing it at one offset. Any
// constant -- 14, 20, 40 -- passes a single-offset test and just relocates the
// cliff. Sweeping is what makes "not geometry-coupled" the thing being asserted.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// composedPaneChrome is what sits BELOW a dialog in a composed pane, and is why
// the anchor ends up far from the bottom.
//
// BE CLEAR ABOUT WHAT THIS IS: the dialog text in these tests is a real capture,
// but this chrome is RECONSTRUCTED from qa1's rig measurement (composed TUI at
// f41ad2e, anchor ~16 rows above the pane bottom) rather than captured from a
// running pane. That is a real limitation, and it is why these tests SWEEP the
// anchor instead of trusting any one layout: a reconstruction can be wrong about
// the exact chrome, but it cannot be wrong about "detection must not depend on
// depth", which is the property under test. A true composed capture -- driving
// the real TUI and reading a pane's rendered rows -- would still be worth having;
// scripts/sigcapture captures raw PTY only and would need a composed mode.
var composedPaneChrome = []string{
	"",
	"────────────────────────────────────────────────────────",
	"❯                                                       ",
	"────────────────────────────────────────────────────────",
	"  ⏵⏵ bypass permissions on (shift+tab to cycle)",
	"",
	"",
}

// paneAtGeometry renders dialog into an emulator of the given size with the
// dialog's last line placed anchorFromBottom rows above the bottom.
func paneAtGeometry(t *testing.T, rows int, dialog []string, anchorFromBottom int) *Pane {
	t.Helper()
	p := &Pane{
		name:    "eng1",
		emu:     vt.NewSafeEmulator(120, rows),
		alive:   true,
		visible: true,
		cfg:     PaneConfig{BeadsEnabled: true},
		attn:    &attentionSignal{},
	}
	registerAttentionOSC(p)

	below := make([]string, anchorFromBottom)
	copy(below, composedPaneChrome)

	body := append(append([]string{}, dialog...), below...)
	pad := rows - len(body)
	if pad < 0 {
		pad = 0
	}
	out := strings.Repeat("\r\n", pad) + strings.Join(body, "\r\n")
	if _, err := p.emu.Write([]byte("\x1b[2J\x1b[H" + out)); err != nil {
		t.Fatalf("paint: %v", err)
	}
	return p
}

// TestPaneHasModal_DetectsADialogAtAnyDepthInThePane is the regression proper.
// At anchorFromBottom=16 it fails against the old fixed 14-row scan, which is
// exactly the composed geometry qa1 measured.
func TestPaneHasModal_DetectsADialogAtAnyDepthInThePane(t *testing.T) {
	for _, dialog := range []struct {
		name  string
		lines []string
	}{
		{"permission prompt", permissionPromptDialog},
		{"AskUserQuestion", askUserQuestionDialog},
	} {
		for _, anchor := range []int{0, 4, 8, 12, 14, 16, 20, 26} {
			p := paneAtGeometry(t, 50, dialog.lines, anchor)
			if !paneHasModal(p) {
				t.Errorf("%s at %d rows above the pane bottom: not detected -- "+
					"detection is coupled to where the dialog sits, which is the ini-t6n bug",
					dialog.name, anchor)
			}
		}
	}
}

// TestWaitingPreviewText_ReadsTheDialogAtAnyDepth is the other half of the same
// mechanism. The preview regexes scan the same region, so the composed geometry
// emptied the row's text as well as stranding it -- one fix must restore both,
// and this asserts the second half rather than assuming it.
func TestWaitingPreviewText_ReadsTheDialogAtAnyDepth(t *testing.T) {
	for _, anchor := range []int{0, 8, 16, 26} {
		perm := paneAtGeometry(t, 50, permissionPromptDialog, anchor)
		if got := perm.waitingPreviewText(); !strings.Contains(got, "date +%s") {
			t.Errorf("permission preview at anchor %d = %q, want the command", anchor, got)
		}

		q := paneAtGeometry(t, 50, askUserQuestionDialog, anchor)
		if got := q.waitingPreviewText(); !strings.Contains(got, "staging or production") {
			t.Errorf("question preview at anchor %d = %q, want the question", anchor, got)
		}
	}
}

// TestRefreshWaitingState_ComposedPermissionPromptClearsAfterAnswering is the
// bead's acceptance in unit form: the composed cycle end to end. Raise by
// OSC 777, the screen confirms at composed geometry, the operator answers, the
// row retires within one refresh.
func TestRefreshWaitingState_ComposedPermissionPromptClearsAfterAnswering(t *testing.T) {
	p := paneAtGeometry(t, 50, permissionPromptDialog, 16)
	if _, err := p.emu.Write([]byte(measuredOSC777)); err != nil {
		t.Fatalf("write osc: %v", err)
	}

	p.refreshWaitingState()
	waiting, _, preview := p.WaitingInput()
	if !waiting {
		t.Fatal("composed permission prompt did not raise")
	}
	if !p.modalSeen() {
		t.Fatal("the screen never confirmed a dialog that is plainly rendered -- " +
			"the earned-clear gate can now never fire, and the row ticks forever")
	}
	if !strings.Contains(preview, "date +%s") {
		t.Errorf("preview = %q, want the command (the empty-preview half of ini-t6n)", preview)
	}

	// The operator answers: the dialog leaves, the chrome remains.
	answered := paneAtGeometry(t, 50, []string{"⏺ Ran 1 shell command", "  ⎿ 1786... "}, 6)
	p.emu = answered.emu
	p.refreshWaitingState()

	if waiting, _, _ := p.WaitingInput(); waiting {
		t.Error("row survived the operator answering -- the composed cycle still leaks a stale row")
	}
}

// TestRefreshWaitingState_ComposedAskUserQuestionStillClears makes sure the fix
// did not trade one dialog for the other. The question path was the epic's
// motivating case and was measured WORKING composed; it must stay that way.
func TestRefreshWaitingState_ComposedAskUserQuestionStillClears(t *testing.T) {
	p := paneAtGeometry(t, 50, askUserQuestionDialog, 12)
	if _, err := p.emu.Write([]byte(measuredOSC777)); err != nil {
		t.Fatalf("write osc: %v", err)
	}
	p.refreshWaitingState()
	if waiting, _, _ := p.WaitingInput(); !waiting {
		t.Fatal("composed AskUserQuestion did not raise")
	}
	if !p.modalSeen() {
		t.Fatal("screen never confirmed the composed AskUserQuestion")
	}

	answered := paneAtGeometry(t, 50, []string{"⏺ Deploying to staging"}, 6)
	p.emu = answered.emu
	p.refreshWaitingState()

	if waiting, _, _ := p.WaitingInput(); waiting {
		t.Error("AskUserQuestion row survived answering; the fix traded one dialog for the other")
	}
}

// TestPaneHasModal_QuietPaneAtAnyDepthIsNotADialog is the false-positive control
// for widening the scan. A pane full of ordinary output and its own input chrome
// must not read as a blocking dialog at any size -- otherwise the list fills
// permanently and the send-deferral path defers everything.
func TestPaneHasModal_QuietPaneAtAnyDepthIsNotADialog(t *testing.T) {
	quiet := []string{
		"⏺ Ran 1 shell command",
		"  ⎿ $ date +%s",
		"    1786012345",
		"⏺ That is the current Unix timestamp.",
		"✻ Ruminating… (12s · esc to interrupt)",
	}
	for _, rows := range []int{24, 40, 50, 80} {
		p := paneAtGeometry(t, rows, quiet, 6)
		if paneHasModal(p) {
			t.Errorf("a quiet %d-row pane read as a blocking dialog", rows)
		}
		if got := p.waitingPreviewText(); got != "" {
			t.Errorf("a quiet %d-row pane produced a preview: %q", rows, got)
		}
	}
}

// TestRefreshWaitingState_StaleRowRecovery answers the ini-zfm recovery
// question by measurement rather than by reasoning about the gate: given a row
// that was raised WITHOUT a dialog ever being on screen -- the phantom row
// v2.7.0 put on the operator's display -- what actually retires it?
//
// This models the pre-fix raise deliberately (noteNotify directly, bypassing the
// allowlist that now stops it) because the rows already on screen were created
// by exactly that path, and the fix does not reach back in time to clear them.
func TestRefreshWaitingState_StaleRowRecovery(t *testing.T) {
	// A pane showing ordinary output: no dialog, and none ever seen.
	stale := func(t *testing.T) *Pane {
		t.Helper()
		p := paneAtGeometry(t, 50, []string{"> ", "  ready"}, 16)
		p.attn.noteNotify("Claude needs your permission")
		p.refreshWaitingState()
		if waiting, _, _ := p.WaitingInput(); !waiting {
			t.Fatal("setup failed: no stale row to recover from")
		}
		return p
	}

	t.Run("ticking does not clear it -- this is the stuck state, reproduced", func(t *testing.T) {
		p := stale(t)
		for i := 0; i < 20; i++ {
			p.refreshWaitingState()
		}
		if waiting, _, _ := p.WaitingInput(); !waiting {
			t.Fatal("the stale row cleared on its own; if that were true ini-zfm would " +
				"have healed itself instead of ticking to 4m39s")
		}
		if p.modalSeen() {
			t.Error("modalSeen set with no dialog ever rendered")
		}
	})

	t.Run("a real dialog, answered, clears it", func(t *testing.T) {
		p := stale(t)
		// The dialog arrives on the same pane.
		below := make([]string, 16)
		copy(below, composedPaneChrome)
		body := append(append([]string{}, permissionPromptDialog...), below...)
		if _, err := p.emu.Write([]byte("\x1b[2J\x1b[H" + strings.Join(body, "\r\n"))); err != nil {
			t.Fatalf("paint dialog: %v", err)
		}
		p.refreshWaitingState()
		if !p.modalSeen() {
			t.Fatal("the screen did not confirm a rendered dialog, so the gate can never open")
		}
		// The operator answers it: dialog gone, chrome remains.
		if _, err := p.emu.Write([]byte("\x1b[2J\x1b[H" + strings.Join(composedPaneChrome, "\r\n"))); err != nil {
			t.Fatalf("paint answer: %v", err)
		}
		p.refreshWaitingState()
		if waiting, _, _ := p.WaitingInput(); waiting {
			t.Error("the stale row survived a real dialog being answered; then nothing short " +
				"of restarting the agent would clear it")
		}
	})

	t.Run("the agent exiting clears it", func(t *testing.T) {
		p := stale(t)
		p.mu.Lock()
		p.alive = false
		p.mu.Unlock()
		p.refreshWaitingState()
		if waiting, _, _ := p.WaitingInput(); waiting {
			t.Error("the row outlived the agent")
		}
	})
}
