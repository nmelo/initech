package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

const rmReason = "Dangerous rm operation on statically-unresolvable target: command substitution output"

// Every captured screen reads as the prompt it shows, or as none. The corpus
// is real Claude Code screens on Haiku: the rm safety prompt early and late
// in its countdown at three widths, the screen after it blocked itself, and
// dialogs without a countdown, which send nothing.
func TestPromptNotice_EveryCaptureReadsRight(t *testing.T) {
	type want struct {
		ok           bool
		reason, left string
	}
	cases := []struct {
		file string
		want want
	}{
		{"footer_corpus/haiku6if_prompt_rm_early_w100.txt", want{true, rmReason, "1:58"}},
		{"footer_corpus/haiku6if_prompt_rm_early_w80.txt", want{true, rmReason, "1:57"}},
		{"footer_corpus/haiku6if_prompt_rm_early_w32.txt", want{true, rmReason, "1:55"}},
		{"footer_corpus/haiku6if_prompt_rm_late_w100.txt", want{true, rmReason, "0:18"}},
		{"footer_corpus/haiku6if_prompt_rm_late_w80.txt", want{true, rmReason, "0:17"}},
		{"footer_corpus/haiku6if_prompt_rm_late_w32.txt", want{true, rmReason, ""}},
		{"haiku312_rm_guard_prompt_w80.txt", want{true, rmReason, "1:54"}},
		{"haiku312_rm_guard_prompt_w32.txt", want{true, rmReason, ""}},
		// Blocked and gone: nothing on screen to tell.
		{"footer_corpus/haiku6if_prompt_rm_after_w100.txt", want{}},
		{"footer_corpus/haiku312_rm_guard_autodenied_w80.txt", want{}},
		// Dialogs with no auto-block countdown: not a prompt it cannot pass.
		{"haiku316_trust_armed_w100.txt", want{}},
		{"haiku316_trust_armed_w32.txt", want{}},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			bp, ok := parseBlockingPrompt(lvxwFixture(t, c.file))
			if ok != c.want.ok {
				t.Fatalf("read %v (%+v), want %v", ok, bp, c.want.ok)
			}
			if !ok {
				return
			}
			if bp.Question != promptQuestion || bp.Reason != c.want.reason {
				t.Errorf("question %q reason %q, want %q", bp.Question, bp.Reason, c.want.reason)
			}
			if c.want.left != "" && bp.Left != c.want.left {
				t.Errorf("left %q, want %q", bp.Left, c.want.left)
			}
			if c.want.left == "" && !regexpLeft.MatchString(bp.Left) {
				t.Errorf("left %q is not M:SS", bp.Left)
			}
		})
	}
}

var regexpLeft = regexp.MustCompile(`^\d+:\d\d$`)

// A shape the parser does not know sends nothing, never half a notice: the
// same real screen with the countdown's words changed, or the question
// changed, reads as none.
func TestPromptNotice_AnUnknownShapeReadsAsNone(t *testing.T) {
	rows := lvxwFixture(t, "footer_corpus/haiku6if_prompt_rm_early_w80.txt")
	plant := func(from, to string) []string {
		out := make([]string, len(rows))
		hit := false
		for i, r := range rows {
			if strings.Contains(r, from) {
				hit = true
			}
			out[i] = strings.ReplaceAll(r, from, to)
		}
		if !hit {
			t.Fatalf("planted %q matched no row: the plant proves nothing", from)
		}
		return out
	}
	if _, ok := parseBlockingPrompt(rows); !ok {
		t.Fatal("the unplanted screen does not read: the twins below prove nothing")
	}
	for name, planted := range map[string][]string{
		"countdown words":  plant("automatically block this request in", "block this soon, in"),
		"question":         plant("Do you want to proceed?", "Do you want to continue?"),
		"no countdown row": plant("Claude Code will", "Claude Code may"),
	} {
		if bp, ok := parseBlockingPrompt(planted); ok {
			t.Errorf("%s: an unknown shape read as %+v", name, bp)
		}
	}
}

// The prompt quoted above a resting input box is transcript, not an open
// prompt: nothing is sent for it.
func TestPromptNotice_AQuotedPromptAboveTheBoxReadsAsNone(t *testing.T) {
	rows := lvxwFixture(t, "footer_corpus/haiku6if_prompt_rm_early_w80.txt")
	box := []string{strings.Repeat("─", 80), "❯ ", strings.Repeat("─", 80), "  ⏵⏵ bypass permissions on"}
	quoted := append(append([]string{}, rows...), "", "✻ Worked for 3s")
	if _, ok := parseBlockingPrompt(append(quoted, box...)); ok {
		t.Error("a prompt above a resting input box read as open")
	}
}

// observe gives the pane one tick whose screen is the rm prompt, or a resting prompt.
func observe(p *Pane, open bool, at time.Time, t *testing.T) {
	t.Helper()
	rows := []string{"❯ ", ""}
	if open {
		rows = lvxwFixture(t, "footer_corpus/haiku6if_prompt_rm_early_w80.txt")
	}
	p.observeBlockingPrompt(rows, at)
}

func noticeTUI(t *testing.T) (*TUI, *Pane, *lineRecorder) {
	t.Helper()
	r := recordInitechLines(t)
	eng1 := testPane("eng1")
	return newTestTUI(eng1, testPane("super")), eng1, r
}

// One line to super, with the agent, the question, the reason, the time
// left and the two things super can do, once the prompt has stayed on
// screen for the grace.
func TestPromptNotice_SuperGetsOneLine(t *testing.T) {
	tui, eng1, r := noticeTUI(t)
	t0 := time.Unix(1_000_000, 0)
	observe(eng1, true, t0, t)
	tui.sendPromptNotices()
	observe(eng1, true, t0.Add(promptNoticeGrace), t)
	tui.sendPromptNotices()
	got := r.waitFor(1)
	want := "super|initech|[from initech] eng1 is at a prompt it cannot pass alone: Do you want to proceed? Reason: " + rmReason +
		". Claude Code blocks it in 1:57. Answer it: initech keystroke eng1 y (or n), or tell eng1 to rewrite the command."
	if len(got) != 1 || got[0] != want {
		t.Fatalf("lines %q\nwant %q", got, want)
	}
}

// The prompt staying open for its whole countdown sends nothing more.
func TestPromptNotice_AnOpenPromptIsToldOnce(t *testing.T) {
	tui, eng1, r := noticeTUI(t)
	t0 := time.Unix(1_000_000, 0)
	for s := 0; s <= 120; s++ {
		observe(eng1, true, t0.Add(time.Duration(s)*time.Second), t)
		tui.sendPromptNotices()
	}
	if got := r.waitFor(2); len(got) != 1 {
		t.Fatalf("an open prompt sent %d lines, want 1: %q", len(got), got)
	}
}

// Answered, then a new prompt: super is told again.
func TestPromptNotice_ANewPromptIsToldAgain(t *testing.T) {
	tui, eng1, r := noticeTUI(t)
	t0 := time.Unix(1_000_000, 0)
	observe(eng1, true, t0, t)
	observe(eng1, true, t0.Add(promptNoticeGrace), t)
	tui.sendPromptNotices()
	observe(eng1, false, t0.Add(10*time.Second), t)
	observe(eng1, true, t0.Add(20*time.Second), t)
	observe(eng1, true, t0.Add(20*time.Second+promptNoticeGrace), t)
	tui.sendPromptNotices()
	if got := r.waitFor(2); len(got) != 2 {
		t.Fatalf("two prompts sent %d lines, want 2: %q", len(got), got)
	}
}

// A prompt answered within the grace sends nothing: the agent or the
// operator was already on it.
func TestPromptNotice_APromptAnsweredWithinTheGraceSendsNothing(t *testing.T) {
	tui, eng1, r := noticeTUI(t)
	t0 := time.Unix(1_000_000, 0)
	observe(eng1, true, t0, t)
	observe(eng1, true, t0.Add(promptNoticeGrace-time.Second), t)
	tui.sendPromptNotices()
	observe(eng1, false, t0.Add(promptNoticeGrace), t)
	tui.sendPromptNotices()
	if got := r.waitFor(1); len(got) != 0 {
		t.Fatalf("a prompt answered within the grace sent %q", got)
	}
}

// The line goes to super and nowhere else: never into the agent's own pane.
// Positive twin: the same tick does send, to super.
func TestPromptNotice_NothingGoesIntoTheAgentsPane(t *testing.T) {
	tui, eng1, r := noticeTUI(t)
	t0 := time.Unix(1_000_000, 0)
	observe(eng1, true, t0, t)
	observe(eng1, true, t0.Add(promptNoticeGrace), t)
	tui.sendPromptNotices()
	got := r.waitFor(1)
	if len(got) != 1 {
		t.Fatalf("no line sent: the check below proves nothing (%q)", got)
	}
	for _, l := range got {
		if !strings.HasPrefix(l, "super|") {
			t.Errorf("a line went to %q", l)
		}
	}
}

// super's own prompt is not sent to super: it would wait behind that prompt.
func TestPromptNotice_SupersOwnPromptSendsNothing(t *testing.T) {
	r := recordInitechLines(t)
	super := testPane("super")
	tui := newTestTUI(testPane("eng1"), super)
	t0 := time.Unix(1_000_000, 0)
	observe(super, true, t0, t)
	observe(super, true, t0.Add(promptNoticeGrace), t)
	tui.sendPromptNotices()
	if got := r.waitFor(1); len(got) != 0 {
		t.Fatalf("super's own prompt sent %q", got)
	}
}

// No pane named super: the prompt is noticed and nothing is sent (the
// helper logs it).
func TestPromptNotice_NoSuperPaneSendsNothing(t *testing.T) {
	r := recordInitechLines(t)
	eng1 := testPane("eng1")
	tui := newTestTUI(eng1, testPane("eng2"))
	t0 := time.Unix(1_000_000, 0)
	observe(eng1, true, t0, t)
	observe(eng1, true, t0.Add(promptNoticeGrace), t)
	tui.sendPromptNotices()
	if got := r.waitFor(1); len(got) != 0 {
		t.Fatalf("with no super pane, sent %q", got)
	}
}

// A pane with no dialog on screen makes nothing due.
func TestPromptNotice_APaneWithNoDialogSendsNothing(t *testing.T) {
	tui, eng1, r := noticeTUI(t)
	t0 := time.Unix(1_000_000, 0)
	for s := 0; s < 10; s++ {
		observe(eng1, false, t0.Add(time.Duration(s)*time.Second), t)
		tui.sendPromptNotices()
	}
	if got := r.waitFor(1); len(got) != 0 {
		t.Fatalf("a pane with no dialog sent %q", got)
	}
}
