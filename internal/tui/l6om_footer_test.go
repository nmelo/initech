package tui

import (
	"fmt"
	"strings"
	"testing"
)

// ini-l6om: the rows under Claude Code's input box, read into status --json.
// Every fixture is a real Claude Code 2.1.310 screen on Haiku (claude --model
// haiku), the status line being the operator's cla one. A value is either
// what the screen says or nil; never a guess.

func l6omInt(v int) *int       { return &v }
func l6omStr(v string) *string { return &v }

func l6omShow(v any) string {
	switch p := v.(type) {
	case *int:
		if p == nil {
			return "null"
		}
		return fmt.Sprint(*p)
	case *string:
		if p == nil {
			return "null"
		}
		return fmt.Sprintf("%q", *p)
	}
	return "?"
}

func l6omSame(t *testing.T, what string, got, want Footer) {
	t.Helper()
	pairs := []struct {
		name      string
		got, want any
	}{
		{"context_percent", got.ContextPercent, want.ContextPercent},
		{"shells", got.Shells, want.Shells},
		{"monitors", got.Monitors, want.Monitors},
		{"tasks", got.Tasks, want.Tasks},
		{"model", got.Model, want.Model},
		{"user", got.User, want.User},
	}
	for _, p := range pairs {
		if g, w := l6omShow(p.got), l6omShow(p.want); g != w {
			t.Errorf("%s: %s = %s, want %s", what, p.name, g, w)
		}
	}
}

// Each captured footer reads exactly what it shows.
func TestFooter_EachCapturedFooterReadsWhatItShows(t *testing.T) {
	model, user := l6omStr("Haiku 5.5"), l6omStr("Personal")
	cases := []struct {
		file  string
		width int
		want  Footer
	}{
		{"haiku310_footer_idle.txt", 160, Footer{l6omInt(0), l6omInt(0), l6omInt(0), l6omInt(0), model, user}},
		{"haiku310_footer_shell.txt", 160, Footer{l6omInt(8), l6omInt(1), l6omInt(0), l6omInt(0), model, user}},
		{"haiku310_footer_shell_monitor.txt", 160, Footer{l6omInt(8), l6omInt(1), l6omInt(1), l6omInt(0), model, user}},
		{"haiku310_footer_bgagent.txt", 160, Footer{l6omInt(9), l6omInt(1), l6omInt(1), l6omInt(1), model, user}},
		// 40 columns: the status line is cut after the context percent, and
		// the mode line is short enough to be whole.
		{"haiku310_footer_narrow40.txt", 40, Footer{l6omInt(8), l6omInt(1), l6omInt(0), l6omInt(0), model, user}},
		// 40 columns, mode line cut mid-word ("1 monito"): the monitor count
		// is not read; the shell count before it is.
		{"haiku310_footer_bgagent_narrow40.txt", 40, Footer{l6omInt(9), l6omInt(1), nil, l6omInt(1), model, user}},
	}
	for _, c := range cases {
		l6omSame(t, c.file, parseFooter(lvxwFixture(t, c.file), c.width), c.want)
	}
}

// Feed it nothing: no footer, a dialog in its place, a shell, an empty pane,
// a footer quoted in the transcript above the input box -- all nil.
func TestFooter_NoFooterReadsNull(t *testing.T) {
	quoted := []string{
		"  Haiku 5.5 │ Personal │ 42% │ 75% ▓▓▓┃▓▓▓▓░░ → 06:50 PM │ 20% ▓▓┃░░░░░░░",
		"  ⏸ manual mode on · 3 shells, 2 monitors",
		"  ⏺ main",
		"  ◯ general-purpose  quoted",
		strings.Repeat("─", 80),
		"❯ ",
		strings.Repeat("─", 80),
	}
	cases := map[string][]string{
		"empty pane":                       nil,
		"blank rows":                       make([]string, 30),
		"shell pane":                       {"sh-3.2$ ls", "a  b  c", "sh-3.2$ echo 1 shell · 5%", "1 shell · 5%", "sh-3.2$ "},
		"write prompt open":                lvxwFixture(t, "haiku310_write_prompt.txt"),
		"trust prompt":                     lvxwFixture(t, "haiku310_trust_prompt.txt"),
		"login":                            lvxwFixture(t, "haiku310_login.txt"),
		"footer quoted above the composer": quoted,
	}
	for name, rows := range cases {
		l6omSame(t, name, parseFooter(rows, 80), Footer{})
	}
}

// A status line cut before a segment closes leaves that segment unread.
func TestFooter_ACutSegmentIsNotRead(t *testing.T) {
	frame := []string{"", strings.Repeat("─", 40), "❯ ", strings.Repeat("─", 40)}
	cases := []struct {
		line string
		want Footer
	}{
		// "8…" could be 80 or 85: not read. The account name is the segment
		// before the percent (ini-67k7), so with no percent it is not read.
		{"  Haiku 5.5 │ Personal │ 8…", Footer{Model: l6omStr("Haiku 5.5")}},
		{"  Haiku 5.5 │ Pers…", Footer{Model: l6omStr("Haiku 5.5")}},
		{"  Haiku 5.…", Footer{}},
		// A cut segment before a separator: the line is not trusted.
		{"  Haiku 5.… │ Personal │ 8% │ 1%", Footer{}},
		// Not a percent at all.
		{"  Haiku 5.5 │ Personal │ lots │ 1%", Footer{Model: l6omStr("Haiku 5.5")}},
		{"  Haiku 5.5 │ Personal │ 140% │ 1%", Footer{Model: l6omStr("Haiku 5.5")}},
	}
	for _, c := range cases {
		rows := append(append([]string{}, frame...), c.line)
		l6omSame(t, c.line, parseFooter(rows, 40), c.want)
	}
}

// The mode line: counts only as whole words; a row that runs to the pane's
// edge may hide more, so a kind it does not show is unread, not zero.
// Background agents are listed on rows of their own, so a cut mode line
// does not hide them.
func TestFooter_ModeLineCounts(t *testing.T) {
	frame := []string{"", strings.Repeat("─", 60), "❯ ", strings.Repeat("─", 60)}
	n := func(s, m, k *int) Footer { return Footer{Shells: s, Monitors: m, Tasks: k} }
	cases := []struct {
		line  string
		width int
		want  Footer
	}{
		{"  ⏸ manual mode on", 60, n(l6omInt(0), l6omInt(0), l6omInt(0))},
		{"  ⏵⏵ accept edits on · 2 shells, 3 monitors", 60, n(l6omInt(2), l6omInt(3), l6omInt(0))},
		// At the footer's edge (two columns short of the pane's): the shells
		// word is whole, monitors may be cut.
		{"  ⏸ manual mode on · 1 shell", 30, n(l6omInt(1), nil, l6omInt(0))},
		{"  ⏸ manual mode on · 1 she", 28, n(nil, nil, l6omInt(0))},
		// Not a mode line.
		{"  1 shell, 1 monitor", 60, Footer{}},
	}
	for _, c := range cases {
		rows := append(append([]string{}, frame...), c.line)
		l6omSame(t, c.line, parseFooter(rows, c.width), c.want)
	}
}

// Background agents are only the rows of the list under "⏺ main".
func TestFooter_TasksAreTheAgentList(t *testing.T) {
	rows := []string{"", strings.Repeat("─", 60), "❯ ", strings.Repeat("─", 60),
		"  ⏸ manual mode on", "  ◯ not in an agent list"}
	l6omSame(t, "◯ without the list", parseFooter(rows, 60), Footer{Shells: l6omInt(0), Monitors: l6omInt(0), Tasks: l6omInt(0)})
	rows = append(rows, "  ⏺ main", "  ◯ one", "  ◯ two")
	l6omSame(t, "two agents", parseFooter(rows, 60), Footer{Shells: l6omInt(0), Monitors: l6omInt(0), Tasks: l6omInt(2)})
}

// Each dialog kind on its captured specimen; unknown only when the latch is
// up and nothing is recognised; none otherwise.
func TestBanner_EachSpecimenNamesItsKind(t *testing.T) {
	cases := []struct {
		file    string
		latched bool
		want    string
	}{
		{"haiku310_login.txt", false, "login"},
		{"haiku310_trust_prompt.txt", false, "trust_prompt"},
		{"haiku310_trust_prompt.txt", true, "trust_prompt"},
		{"haiku_trust_prompt.txt", false, "trust_prompt"},
		{"haiku310_write_prompt.txt", true, "permission_prompt"},
		{"haiku310_bash_prompt_bgagent.txt", true, "permission_prompt"},
		{"haiku_write_prompt.txt", true, "permission_prompt"},
		{"haiku_edit_prompt.txt", true, "permission_prompt"},
		{"haiku310_picker.txt", true, "picker"},
		{"haiku310_footer_idle.txt", false, "none"},
		{"haiku310_footer_bgagent.txt", false, "none"},
		{"haiku310_footer_idle.txt", true, "unknown"},
	}
	for _, c := range cases {
		if got := bannerKind(lvxwFixture(t, c.file), c.latched); got != c.want {
			t.Errorf("%s latched=%v: %q, want %q", c.file, c.latched, got, c.want)
		}
	}
}

// Near misses: dialog words in the transcript above a resting input box are
// not a dialog, and a shell or empty pane is none.
func TestBanner_NearMissesAreNone(t *testing.T) {
	quoted := []string{
		" Select login method:",
		" ❯ 1. Claude account with subscription · Pro, Max, Team, or Enterprise",
		"   Yes, I trust this folder",
		" Do you want to create notes.txt?",
		" ❯ 1. Yes",
		" Esc to cancel · Tab to amend",
		"Enter to select · ↑/↓ to navigate · Esc to cancel",
		"✻ Churned for 6s",
		strings.Repeat("─", 80),
		"❯ ",
		strings.Repeat("─", 80),
	}
	cases := map[string][]string{
		"quoted above the composer": quoted,
		"shell":                     {"sh-3.2$ echo Select login method:", "Select login method:", "sh-3.2$ "},
		"empty":                     nil,
		// The trust prompt's options without its "Enter to confirm" footer:
		// quoted, not open (qa1's survivor on ini-l6om).
		"trust options without the footer": {" ❯ No, exit", "   Yes, I trust this folder"},
	}
	for name, rows := range cases {
		if got := bannerKind(rows, false); got != "none" {
			t.Errorf("%s: %q, want none", name, got)
		}
	}
}
