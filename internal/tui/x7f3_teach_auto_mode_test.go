package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ini-x7f3: Claude Code's "Teach auto mode about your environment?" dialog
// sends no notification, and a Claude pane reads waiting only from its own
// notification, so a pane stopped on it read idle while the send guard (which
// reads the screen) held its mail. The captures are qa1's two sightings,
// byte-exact from initech peek: a wide pane and a 28-column one, Claude Code
// 2.1.311 or 2.1.312 on Haiku 5.5 (the exact version was not read at the
// sighting).

const x7f3Preview = "Teach auto mode about your environment?"

// x7f3Pane is a Claude pane with the attention mailbox, sized as given.
func x7f3Pane(cols, rows int) (*TUI, *Pane) {
	p := newEmuPane("eng1", cols, rows)
	p.attn = &attentionSignal{}
	return &TUI{panes: toPaneViews([]*Pane{p})}, p
}

// x7f3Tick paints rows, runs the once-a-second maintenance and one frame.
func x7f3Tick(tu *TUI, p *Pane, rows []string, now time.Time) {
	lvxwPaint(p, rows)
	tu.modalMaintenance(now)
	maintainPaneFrame(p)
}

func x7f3Waiting(p *Pane) (bool, string) {
	w, _, preview := p.WaitingInput()
	return w, preview
}

// AC1: both captures read waiting, with the dialog's first line as the
// preview, and the activity word every reader shows is waiting.
func TestTeachAutoMode_BothCapturesReadWaiting(t *testing.T) {
	for _, c := range []struct {
		fixture    string
		cols, rows int
	}{
		{"haiku311_teach_auto_mode_wide.txt", 92, 10},
		{"haiku311_teach_auto_mode_narrow.txt", 30, 14},
	} {
		tu, p := x7f3Pane(c.cols, c.rows)
		x7f3Tick(tu, p, lvxwFixture(t, c.fixture), time.Now())
		if w, preview := x7f3Waiting(p); !w || preview != x7f3Preview {
			t.Errorf("%s: waiting %v preview %q, want waiting with %q", c.fixture, w, preview, x7f3Preview)
		}
		if got := p.Activity(); got != StateWaitingInput {
			t.Errorf("%s: activity %v, want waiting", c.fixture, got)
		}
		if tier := p.WaitingTierOf(); tier != WaitingTierListOnly {
			t.Errorf("%s: tier %v, want list-only: an inferred dialog never chimes", c.fixture, tier)
		}
	}
}

// The raise holds across frames while the dialog stays up (the per-frame
// clear must recognise it: the narrow capture carries none of the older
// dialog words, so only the new entry keeps it), and AC2: it clears when the
// dialog closes.
func TestTeachAutoMode_StaysWhileUpAndClearsWhenGone(t *testing.T) {
	for _, c := range []struct {
		fixture    string
		cols, rows int
	}{
		{"haiku311_teach_auto_mode_wide.txt", 92, 10},
		{"haiku311_teach_auto_mode_narrow.txt", 30, 14},
	} {
		tu, p := x7f3Pane(c.cols, c.rows)
		t0 := time.Now()
		x7f3Tick(tu, p, lvxwFixture(t, c.fixture), t0)
		for i := 0; i < 5; i++ {
			maintainPaneFrame(p)
		}
		if w, _ := x7f3Waiting(p); !w {
			t.Fatalf("%s: the dialog is still up but the row cleared after a few frames", c.fixture)
		}
		rule := strings.Repeat("─", c.cols-2)
		prompt := []string{"⏺ ok", "", rule, "❯ ", rule, "  Haiku 5.5 │ <user>"}
		x7f3Tick(tu, p, prompt, t0.Add(2*time.Second))
		if w, preview := x7f3Waiting(p); w {
			t.Errorf("%s: the dialog closed but the pane still reads waiting (%q)", c.fixture, preview)
		}
	}
}

// A pane that is not Claude Code is not raised by this list: its screen tier
// owns its dialogs.
func TestTeachAutoMode_OnlyClaudePanes(t *testing.T) {
	tu, p := x7f3Pane(30, 14)
	p.agentType = "generic"
	lvxwPaint(p, lvxwFixture(t, "haiku311_teach_auto_mode_narrow.txt"))
	// Read straight after the once-a-second pass: the generic pane's own
	// screen tier runs per frame and would hide a wrong raise by clearing it.
	tu.modalMaintenance(time.Now())
	if w, preview := x7f3Waiting(p); w {
		t.Errorf("a generic pane was raised by the Claude silent-dialog list (%q)", preview)
	}
}

// A pane with no attention mailbox never runs the per-frame clear, so it is
// never raised: a row there could not be retired.
func TestTeachAutoMode_NoMailboxNoRaise(t *testing.T) {
	tu, p := x7f3Pane(30, 14)
	p.attn = nil
	lvxwPaint(p, lvxwFixture(t, "haiku311_teach_auto_mode_narrow.txt"))
	tu.modalMaintenance(time.Now())
	if w, preview := x7f3Waiting(p); w {
		t.Errorf("a pane with no mailbox was raised (%q); nothing could clear it", preview)
	}
}

// A row the application raised itself is never downgraded to list-only or
// relabelled by the screen list.
func TestTeachAutoMode_ANotifiedRowKeepsItsTierAndText(t *testing.T) {
	tu, p := x7f3Pane(30, 14)
	p.SetWaitingInputTier("Bash: make test", WaitingTierChime)
	x7f3Tick(tu, p, lvxwFixture(t, "haiku311_teach_auto_mode_narrow.txt"), time.Now())
	if w, preview := x7f3Waiting(p); !w || preview != "Bash: make test" || p.WaitingTierOf() != WaitingTierChime {
		t.Errorf("notified row became waiting=%v %q tier %v", w, preview, p.WaitingTierOf())
	}
}

// AC5 feed it nothing, and the quotation rule: none of these is the dialog.
func TestTeachAutoMode_WhatIsNotTheDialog(t *testing.T) {
	rule := strings.Repeat("─", 90)
	for name, screen := range map[string][]string{
		"an empty screen":                  {""},
		"only the prompt":                  {rule, "❯ ", rule, "  Haiku 5.5 │ <user>"},
		"quoted above the composer":        {"⏺ The dialog read \"Teach auto mode about your environment?\"", "  ❯ 1. Yes", "  2. Not now", "", "✻ Sautéed for 3s", "", rule, "❯ ", rule, "  Haiku 5.5 │ <user>"},
		"the question with no answer rows": {rule, "  Teach auto mode about your environment?", "", "  Auto mode works better when it knows your environment."},
	} {
		tu, p := x7f3Pane(92, 10)
		x7f3Tick(tu, p, screen, time.Now())
		if w, preview := x7f3Waiting(p); w {
			t.Errorf("%s: reads waiting (%q)", name, preview)
		}
	}
}

// AC3: an existing permission capture is unchanged by the new entry: it
// still needs the application's notification to raise, and does not raise
// from its screen alone.
func TestTeachAutoMode_APermissionDialogStillWaitsForItsNotification(t *testing.T) {
	tu, p := x7f3Pane(160, 45)
	x7f3Tick(tu, p, lvxwFixture(t, "haiku310_write_prompt.txt"), time.Now())
	if w, preview := x7f3Waiting(p); w {
		t.Errorf("a permission prompt raised from its screen alone (%q); it waits for the notification", preview)
	}
}

// AC4, cited: the send guard holds a send against the wide capture, as qa1
// observed (deferred, never typed). Not changed here.
func TestTeachAutoMode_TheSendGuardHoldsOnTheWideCapture(t *testing.T) {
	rows := lvxwFixture(t, "haiku311_teach_auto_mode_wide.txt")
	if !screenShowsModal(rows) {
		t.Error("the send guard does not see the wide capture as a dialog")
	}
}

// AC3, the corpus: no other captured screen reads as the silent dialog, and
// both captures do. Every haiku*/claude* capture in testdata is checked, so
// a new capture is checked the day it lands.
func TestTeachAutoMode_TheCorpusReadsOnlyItsOwnCaptures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.txt"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no captures found: %v", err)
	}
	seen := 0
	for _, f := range files {
		name := filepath.Base(f)
		got := silentClaudeDialogPreview(lvxwFixture(t, name))
		own := strings.HasPrefix(name, "haiku311_teach_auto_mode_")
		if own {
			seen++
		}
		if own && got != x7f3Preview {
			t.Errorf("%s: preview %q, want %q", name, got, x7f3Preview)
		}
		if !own && got != "" {
			t.Errorf("%s: read as the teach auto mode dialog (%q)", name, got)
		}
	}
	if seen != 2 {
		t.Errorf("found %d teach auto mode captures, want 2", seen)
	}
	t.Logf("%d captures checked", len(files))
}
