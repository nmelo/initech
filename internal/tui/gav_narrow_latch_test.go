package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ini-gav: a send to an agent at its prompt was deferred "modal open" for an
// hour. The capture (nayutal eng6, Claude Code with Opus, a ~32-column pane)
// showed the cause: the dialog guard found the input box only by a rule of 40
// or more characters, so a pane under 40 columns never read "back at its
// prompt" and the latch an answered permission prompt raised never cleared.
// A labelled input box (a named session) lost the frame the same way.

func gavScreen(t *testing.T, name string) []string {
	t.Helper()
	rows, _ := corpusFooter(t, name)
	return rows
}

// The captured eng6 screen is at its prompt: the input box is found, no
// dialog, at rest. Red at base: no frame below 40 columns.
func TestGav_TheNarrowCapturedPaneIsAtItsPrompt(t *testing.T) {
	rows := gavScreen(t, "nayutal_eng6_latched_w32.txt")
	if _, _, ok := lowestComposerFrame(rows); !ok {
		t.Error("the 32-column input box is not found")
	}
	if screenShowsModal(rows) {
		t.Error("the captured screen reads as a dialog")
	}
	if !screenShowsIdleComposer(rows) {
		t.Error("the captured screen does not read at rest; a latch on it can never clear")
	}
}

// Through the real tick: a latch raised by a permission prompt that has
// since been answered clears once the narrow pane has sat at its prompt for
// the idle window, and a send is no longer deferred.
func TestGav_AnAnsweredPromptsLatchClearsOnANarrowPane(t *testing.T) {
	rows := gavScreen(t, "nayutal_eng6_latched_w32.txt")
	p := newEmuPane("eng6", 32, len(rows))
	lvxwPaint(p, rows[1:]) // the loader's blank first row is the pane's
	tu := &TUI{panes: toPaneViews([]*Pane{p})}
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()
	if !paneHasModal(p) {
		t.Fatal("precondition: the latch does not hold the pane")
	}
	t0 := time.Now()
	tu.modalMaintenance(t0)
	tu.modalMaintenance(t0.Add(idlePromptStableWindow + 2*time.Second))
	if p.dialogLatched() || paneHasModal(p) {
		t.Errorf("latch still up after %v at the prompt; every send stays deferred", idlePromptStableWindow+2*time.Second)
	}
}

// A labelled input box (a named session) is found and reads at rest.
func TestGav_ALabelledInputBoxIsAtItsPrompt(t *testing.T) {
	for _, name := range []string{"theguy_pa_w94.txt", "haiku310_label_pa_w94.txt", "haiku310_label_pa_w40.txt", "haiku310_label_accountant_w94.txt", "hover_super_label_w118.txt"} {
		rows := gavScreen(t, name)
		if !screenShowsIdleComposer(rows) {
			t.Errorf("%s: a labelled input box does not read at rest", name)
		}
	}
}

// Every capture in the footer corpus is an agent at its prompt: none reads
// as a dialog, every one reads at rest.
func TestGav_EveryCorpusScreenIsAtItsPrompt(t *testing.T) {
	for _, name := range gavCorpusNames(t) {
		rows := gavScreen(t, name)
		if screenShowsModal(rows) || !screenShowsIdleComposer(rows) {
			t.Errorf("%s: modal %v, at rest %v; want no dialog and at rest", name, screenShowsModal(rows), screenShowsIdleComposer(rows))
		}
	}
}

// Feed it nothing: an empty screen, a bare prompt, a footer only, and a
// narrow screen of text with no frame are not at rest, and none is a dialog.
func TestGav_ScreensWithNothingToRead(t *testing.T) {
	for name, rows := range map[string][]string{
		"empty":       nil,
		"blank rows":  {"", "", ""},
		"prompt only": {"❯ "},
		"footer only": {"  ⏵⏵ bypass permissions on · 1 shell"},
		"narrow text": {"  some words", "  that wrap", "❯ "},
	} {
		if screenShowsModal(rows) {
			t.Errorf("%s reads as a dialog", name)
		}
		if screenShowsIdleComposer(rows) {
			t.Errorf("%s reads at rest", name)
		}
	}
	// A short rule inside a wide pane is not the input box: the width is the
	// widest row, and the rule must span it (or reach 40).
	wide := []string{strings.Repeat("x", 80), "──────────", "❯ ", "──────────"}
	if _, _, ok := lowestComposerFrame(wide); ok {
		t.Error("a 10-character rule in an 80-column pane was taken for the input box")
	}
}

// gavCorpusNames lists the footer corpus captures.
func gavCorpusNames(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "footer_corpus", "*.txt"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus: %v", err)
	}
	var names []string
	for _, f := range files {
		if _, err := os.Stat(f); err == nil {
			names = append(names, filepath.Base(f))
		}
	}
	return names
}

// gavSendPane is a pane of the given size showing screen, with a PTY whose
// writes the test reads.
func gavSendPane(t *testing.T, cols int, screen []string) (*Pane, func() string) {
	t.Helper()
	p, _, typed := dndPane(t, "a")
	p.emu.Resize(cols, len(screen)+2)
	lvxwPaint(p, screen)
	return p, typed
}

// AC1: a send to an agent at its prompt with a background shell, or with a
// background subagent, is typed, not deferred (Claude Code 2.1.312, Haiku,
// a 36-column pane).
func TestGav_ABackgroundShellOrSubagentIsNotADialog(t *testing.T) {
	for _, name := range []string{"haiku312_bgshell_w36.txt", "haiku312_subagent_w36.txt"} {
		rows := gavScreen(t, name)
		if screenShowsModal(rows) || !screenShowsIdleComposer(rows) {
			t.Errorf("%s: modal %v, at rest %v", name, screenShowsModal(rows), screenShowsIdleComposer(rows))
		}
		p, typed := gavSendPane(t, 36, rows[1:])
		if v := p.SendTextVerdict("[from b] hello", true); v.Outcome == DeliveryDeferred {
			t.Errorf("%s: deferred", name)
		}
		if !strings.Contains(typed(), "[from b] hello") {
			t.Errorf("%s: nothing typed: %q", name, typed())
		}
	}
}

// AC3: a permission prompt on a pane that also runs a background shell is
// still a dialog, and a send to it is held.
func TestGav_APermissionPromptWithABackgroundShellStillHolds(t *testing.T) {
	rows := lvxwFixture(t, "haiku312_write_prompt_bgshell_w36.txt")
	if !screenShowsModal(rows) {
		t.Fatal("the permission prompt does not read as a dialog")
	}
	if screenShowsIdleComposer(rows) {
		t.Error("the permission prompt reads at rest; a latch on it would clear")
	}
	p, typed := gavSendPane(t, 36, rows)
	if v := p.SendTextVerdict("[from b] hello", true); v.Outcome != DeliveryDeferred {
		t.Errorf("verdict %+v, want deferred", v)
	}
	if strings.Contains(typed(), "hello") {
		t.Errorf("typed into the dialog: %q", typed())
	}
}

// A caller that does not know the width yet (0) gets no frame from blank
// rows around a prompt: an empty row is never a rule.
func TestGav_ABlankRowIsNeverARule(t *testing.T) {
	rows := []string{"x", "", "❯ ", "", "  footer"}
	if _, _, ok := composerFrame(rows, 0); ok {
		t.Error("blank rows around a prompt were taken for the input box at width 0")
	}
	if _, ok := footerFrameBottom(rows, 0); ok {
		t.Error("the footer parser found a frame of blank rows")
	}
}
