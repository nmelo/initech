package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// Tests for ini-0g5s: dialog words in the transcript, above Claude's
// composer, are not an open dialog.
//
// The two permission-prompt fixtures are REAL captures of Claude Code 2.1.299
// in a PTY (one in default permission mode at 40 rows, one in bypass mode with
// an ask rule at 50 rows), cut at the moment the prompt was open and again
// after Esc brought the composer back. Paths and names in them are replaced
// with same-length filler; the layout is untouched.

const composerRuleText = "────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────"

// claudeScreen paints rows into a fresh 120 x rows emulator, top-aligned.
func claudeScreen(t *testing.T, rows int, lines []string) *vt.SafeEmulator {
	t.Helper()
	emu := vt.NewSafeEmulator(120, rows)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { emu.Close() })
	emu.Write([]byte("\x1b[2J\x1b[H" + strings.Join(lines, "\r\n")))
	return emu
}

// composerAtRest is Claude's idle bottom: the frame and its status lines.
var composerAtRest = []string{
	composerRuleText,
	"❯ ",
	composerRuleText,
	"  Opus │ x │ 5% │ 32%",
	"  ⏵⏵ bypass permissions on (shift+tab to cycle)",
}

// quotedDialog is the qa2 field shape: an agent's reply quoting a
// permission dialog, options, footer and all.
var quotedDialog = []string{
	"⏺ The stand-in printed this dialog, as Claude renders it:",
	"",
	"  Do you want to proceed?",
	"  ❯ 1. Yes",
	"    2. Yes, and don't ask again for this command",
	"    3. No, and tell Claude what to do differently",
	"  Esc to cancel · Tab to amend",
	"",
	"  It deferred the send as expected.",
	"",
	"✻ Cooked for 3s · done 12:35 PM",
	"",
}

func screenOf(emu *vt.SafeEmulator) []string { return emuRowsBlocking(emu) }

func replayFixture(t *testing.T, name string, rows int) []string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	emu := vt.NewSafeEmulator(120, rows)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { emu.Close() })
	emu.Write(b)
	return emuRowsBlocking(emu)
}

// AC2 on real bytes: an open permission prompt is a dialog, in both modes the
// fleets run; after Esc the composer is back and it is not.
func TestLiveDialogRegion_RealPermissionPromptsStillDefer(t *testing.T) {
	for _, tc := range []struct {
		file string
		rows int
		open bool
	}{
		{"claude_permission_default_40.raw", 40, true},
		{"claude_permission_bypass_ask_50.raw", 50, true},
		{"claude_permission_default_40_after_esc.raw", 40, false},
		{"claude_permission_bypass_ask_50_after_esc.raw", 50, false},
	} {
		rows := replayFixture(t, tc.file, tc.rows)
		if got := screenShowsModal(rows); got != tc.open {
			t.Errorf("%s: screenShowsModal = %v, want %v\n%s", tc.file, got, tc.open, strings.Join(rows, "\n"))
		}
	}
}

// The qa2 field shape (AC1, words only): dialog words and options quoted in
// the transcript, Claude idle at its prompt below. Not a dialog; a held
// message goes out on the first drain.
func TestLiveDialogRegion_QuotedDialogAboveAnIdleComposerDelivers(t *testing.T) {
	p, _, _ := heldMailPane(t, "qa2")
	lines := append(append([]string{}, quotedDialog...), composerAtRest...)
	p.emu = claudeScreen(t, 40, lines)
	if !isModalPrompt(strings.Join(lines, "\n")) || !screenOffersAnswer(strings.Join(lines, "\n")) {
		t.Fatal("fixture: the quoted text no longer matches the dialog matcher, so this cell would discriminate nothing")
	}
	if paneHasModal(p) {
		t.Fatalf("quoted dialog text above an idle composer reads as an open dialog:\n%s", strings.Join(screenOf(p.emu), "\n"))
	}
	if !screenShowsIdleComposer(screenOf(p.emu)) {
		t.Error("the idle composer is not recognised under quoted dialog text, so a latch could never clear here")
	}
	p.enqueueHeld("[from super] broadcast", true)
	start := time.Now()
	p.drainModalQueue()
	if p.QueuedMessageCount() != 0 {
		t.Error("the held message was not delivered")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("delivery took %v, want within 2 s", d)
	}
}

// AC1, raw-sequence case: the stand-in's OSC 777 declaration is in the stream
// too, so the latch raises. Nothing on screen confirms it; the idle prompt
// after the raise clears it at the window and the mail goes, within 8 s.
func TestLiveDialogRegion_EchoedDeclarationClearsOnIdlePromptWithinTheWindow(t *testing.T) {
	tui := newTestTUI()
	p, _, _ := heldMailPane(t, "qa2")
	p.attn = &attentionSignal{}
	lines := append(append([]string{}, quotedDialog...), composerAtRest...)
	p.emu = claudeScreen(t, 40, lines)
	registerAttentionOSC(p)
	p.emu.Write([]byte(measuredOSC777))
	p.refreshWaitingState()
	if !p.dialogLatched() {
		t.Fatal("fixture: the echoed declaration did not raise the latch")
	}
	tui.panes = []PaneView{p}
	p.enqueueHeld("[from super] broadcast", true)

	raise := time.Now()
	p.mu.Lock()
	p.dialogOpenAt = raise
	p.mu.Unlock()
	var released time.Duration = -1
	for s := 0; s <= 8; s++ {
		tui.lastModalMaint = time.Time{}
		tui.modalMaintenance(raise.Add(time.Duration(s) * time.Second))
		if !p.dialogLatched() && released < 0 {
			released = time.Duration(s) * time.Second
		}
	}
	if released < 0 {
		t.Fatal("the echoed declaration still holds mail after 8 s")
	}
	if released < idlePromptStableWindow {
		t.Errorf("released %v after the raise, before the %v window: a dialog still painting would lose its guard", released, idlePromptStableWindow)
	}
	if p.dialogCorroborated {
		t.Error("quoted text above the composer corroborated the latch")
	}
	// The release starts a drain on its own goroutine; wait for it, so the
	// message is proven delivered and nothing writes after this test ends.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		busy := p.modalDraining
		p.mu.Unlock()
		if !busy && p.QueuedMessageCount() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("the released message was never delivered")
}

// The older layout ini-t6n recorded (input box UNDER an open dialog): a dialog
// whose answer rows end right above the composer frame still defers.
func TestLiveDialogRegion_DialogDirectlyAboveTheComposerStillDefers(t *testing.T) {
	lines := []string{
		"⏺ Bash(date +%s)",
		"",
		" Do you want to proceed?",
		" ❯ 1. Yes",
		"   2. No",
		"",
	}
	lines = append(lines, composerAtRest...)
	if !screenShowsModal(screenOf(claudeScreen(t, 40, lines))) {
		t.Error("a dialog ending right above the input box was read as transcript")
	}
}

// The LOWEST frame is the live composer. A quoted composer frame higher in
// the transcript must not make the dialog words under it count.
func TestLiveDialogRegion_TheLowestFrameIsTheComposer(t *testing.T) {
	lines := []string{"⏺ This is what the screen looked like:"}
	lines = append(lines, composerAtRest[:3]...) // a quoted frame
	lines = append(lines, quotedDialog...)
	lines = append(lines, composerAtRest...) // the live one
	if screenShowsModal(screenOf(claudeScreen(t, 40, lines))) {
		t.Error("the region was taken below a quoted frame instead of the live composer")
	}
}

// Feed it nothing.
func TestLiveDialogRegion_EmptyFrameOnlyAndWordsOnly(t *testing.T) {
	if screenShowsModal(screenOf(claudeScreen(t, 40, nil))) {
		t.Error("an empty screen reads as a dialog")
	}
	if screenShowsModal(screenOf(claudeScreen(t, 40, composerAtRest))) {
		t.Error("a bare composer frame reads as a dialog")
	}
	if got := liveDialogRows([]string{}); len(got) != 0 {
		t.Errorf("no rows gave %d rows", len(got))
	}
	// Words with no frame: no composer is visible, so the whole pane is read,
	// exactly as before -- a real dialog has no composer under it.
	if !screenShowsModal(screenOf(claudeScreen(t, 40, quotedDialog))) {
		t.Error("dialog words with no composer frame were skipped; that is the forged-answer direction")
	}
}

// The raise line names the notice and never anything else.
func TestDialogLatch_RaiseLineNamesTheNoticeNeverABody(t *testing.T) {
	logs := captureLogs(t)
	a := testPane("eng1")
	a.latchDialogOpen("Claude needs your permission")
	b := testPane("eng2")
	b.latchDialogOpen("[from super] secret plan: rotate the token now")
	out := logs.String()
	if !strings.Contains(out, "dialog latch raised") || !strings.Contains(out, "Claude needs your permission") {
		t.Errorf("raise not logged with its notice:\n%s", out)
	}
	if strings.Contains(out, "secret plan") || strings.Contains(out, "rotate the token") {
		t.Errorf("the raise line carried a body:\n%s", out)
	}
	if strings.Count(out, "dialog latch raised") != 2 {
		t.Errorf("want one raise line per pane:\n%s", out)
	}
	a.latchDialogOpen("Claude needs your permission") // already raised: no second line
	if strings.Count(logs.String(), "dialog latch raised") != 2 {
		t.Error("a repeat declaration logged a second raise")
	}
}

// Every transition has one Info line naming its source.
func TestDialogLatch_CorroborationAndClearsAreLogged(t *testing.T) {
	logs := captureLogs(t)
	p := testPane("eng1")
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()
	p.noteDialogSighting()
	p.noteOperatorInput()
	p.noteOperatorInput()
	p.latchDialogOpen("Claude needs your permission")
	p.clearDialogLatch("the pane's process exited")
	out := logs.String()
	for want, n := range map[string]int{
		"dialog latch corroborated":        1,
		"by=\"operator input\"":            1,
		"by=\"the pane's process exited\"": 1,
	} {
		if got := strings.Count(out, want); got != n {
			t.Errorf("%q logged %d times, want %d:\n%s", want, got, n, out)
		}
	}
}

// A frame needs the prompt row. A dialog boxed by two full-width rules, with
// no composer anywhere, is a dialog: two rules alone are not a composer.
func TestLiveDialogRegion_RulesWithoutAPromptAreNotAComposer(t *testing.T) {
	lines := []string{
		"⏺ Bash(date +%s)",
		composerRuleText,
		" Bash command",
		"",
		" Do you want to proceed?",
		" ❯ 1. Yes",
		"   2. No",
		composerRuleText,
		" Esc to cancel · Tab to amend",
	}
	if !screenShowsModal(screenOf(claudeScreen(t, 40, lines))) {
		t.Error("a boxed dialog with no composer was skipped as if a composer were below it")
	}
}

// A frame needs its bottom rule. A dialog whose options start right under
// its own top rule ("❯ 1. ..."), with nothing ruled off below, is a dialog.
func TestLiveDialogRegion_PromptUnderARuleWithNoRuleBelowIsNotAComposer(t *testing.T) {
	lines := []string{
		"⏺ Asking which environment to deploy to.",
		composerRuleText,
		"❯ 1. Staging",
		"  2. Production",
		"",
		" Which environment? Enter to confirm · Esc to cancel",
	}
	if !screenShowsModal(screenOf(claudeScreen(t, 40, lines))) {
		t.Error("a dialog under a rule, with no rule below, was taken for a composer")
	}
}

// A frame needs its top rule. A "❯" line in the transcript (Claude echoes the
// user's prompt that way) more than composerFrameMaxRows above a rule is not
// the top of a composer, so the dialog between them is still read.
func TestLiveDialogRegion_EchoedPromptFarAboveARuleIsNotAComposer(t *testing.T) {
	lines := []string{"filler", "filler", "❯ run the deploy command"}
	for i := 0; len(lines) < 2+1+composerFrameMaxRows-1; i++ {
		lines = append(lines, "  step "+string(rune('a'+i%26)))
	}
	lines = append(lines[:len(lines)-3], " Do you want to proceed?", " ❯ 1. Yes", "   2. No")
	lines = append(lines, composerRuleText, " Esc to cancel · Tab to amend")
	if len(lines) != 2+1+composerFrameMaxRows-1+2 {
		t.Fatalf("fixture geometry: %d lines", len(lines))
	}
	if !screenShowsModal(screenOf(claudeScreen(t, 40, lines))) {
		t.Error("an echoed prompt far above a rule was taken for a composer's first row")
	}
}

// A fake composer frame INSIDE a live dialog (a command preview or question
// text the agent controls, carrying a full-width rule, a ❯ line and another
// rule) must not hide the dialog. Claude draws the question and its answer
// rows at the bottom, below anything the preview can contain, so the region
// below a fake frame still holds them.
func TestLiveDialogRegion_FakeFrameInsideADialogDoesNotHideIt(t *testing.T) {
	lines := []string{
		"⏺ Bash(printf ...)",
		"",
		composerRuleText,
		" Bash command",
		composerRuleText, // preview text the agent chose
		"❯ ",
		composerRuleText,
		" printf a fake input box",
		"",
		" Do you want to proceed?",
		" ❯ 1. Yes",
		"   2. No",
		"",
		" Esc to cancel · Tab to amend",
	}
	if !screenShowsModal(screenOf(claudeScreen(t, 40, lines))) {
		t.Error("a fake composer frame inside the dialog's preview hid an open dialog")
	}
	// And at the very end of the preview, right above the question.
	lines2 := []string{
		composerRuleText,
		" Bash command",
		" printf x",
		composerRuleText,
		"❯ ",
		composerRuleText,
		" Do you want to proceed?",
		" ❯ 1. Yes",
		"   2. No",
	}
	if !screenShowsModal(screenOf(claudeScreen(t, 40, lines2))) {
		t.Error("a fake frame directly above the question hid an open dialog")
	}
}
