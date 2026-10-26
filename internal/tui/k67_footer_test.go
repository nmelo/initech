package tui

import (
	"strings"
	"testing"
)

// ini-67k7: the status line is read by shape, not position, and a mode line
// that shows Claude Code's "(shift+tab to cycle)" hint has no background
// work, whole or cut. Fixtures: real Claude Code 2.1.310 screens on Haiku in
// a git folder, where the operator's status line leads with the branch.

func TestFooter_TheBranchSegmentIsSkipped(t *testing.T) {
	model, user := l6omStr("Haiku 5.5"), l6omStr("Second")
	zero := l6omInt(0)
	cases := []struct {
		file  string
		width int
		want  Footer
	}{
		{"haiku310_branch_idle.txt", 160, Footer{zero, zero, zero, zero, model, user}},
		{"haiku310_branch_shell.txt", 160, Footer{l6omInt(5), l6omInt(1), zero, zero, model, user}},
		// 40 columns: the status line is cut after the percent; the idle
		// mode line is exactly at the footer's edge and still whole.
		{"haiku310_branch_idle_w40.txt", 40, Footer{zero, zero, zero, zero, model, user}},
		{"haiku310_branch_shell_w40.txt", 40, Footer{l6omInt(5), l6omInt(1), zero, zero, model, user}},
		// 34 columns: cut before the percent, so neither it nor the name
		// before it is read; the idle mode line is cut inside its hint.
		{"haiku310_branch_idle_w34.txt", 34, Footer{nil, zero, zero, zero, model, nil}},
		{"haiku310_branch_shell_w34.txt", 34, Footer{nil, l6omInt(1), zero, zero, model, nil}},
	}
	for _, c := range cases {
		l6omSame(t, c.file, parseFooter(lvxwFixture(t, c.file), c.width), c.want)
	}
}

// Shapes the fixtures do not show: segments around the percent in other
// orders, other model names, and a percent with no model before it.
func TestFooter_StatusLineByShape(t *testing.T) {
	frame := []string{"", strings.Repeat("─", 100), "❯ ", strings.Repeat("─", 100)}
	cases := []struct {
		line string
		want Footer
	}{
		{"  ⎇ main │ Opus 5.5 │ Camila │ 10% │ 29% ▓▓▓ → 04:00 PM │ 8%", Footer{ContextPercent: l6omInt(10), Model: l6omStr("Opus 5.5"), User: l6omStr("Camila")}},
		{"  ⎇ feature/x │ ✓ │ Claude Sonnet 4.6 │ Work │ 60% │ 1%", Footer{ContextPercent: l6omInt(60), Model: l6omStr("Claude Sonnet 4.6"), User: l6omStr("Work")}},
		// No model-shaped segment: the model is not read; the name still is.
		{"  ⎇ main │ Work │ 12% │ 1%", Footer{ContextPercent: l6omInt(12), User: l6omStr("Work")}},
		// The first whole percent is the context, not a later one.
		{"  Haiku 5.5 │ Work │ 7% │ 75% │ 20%", Footer{ContextPercent: l6omInt(7), Model: l6omStr("Haiku 5.5"), User: l6omStr("Work")}},
		// A model-shaped segment after the percent is not the model.
		{"  ⎇ main │ Work │ 12% │ Haiku 5.5 │ 1%", Footer{ContextPercent: l6omInt(12), User: l6omStr("Work")}},
		// The percent early in the line.
		{"  Work │ 12% │ 1%", Footer{ContextPercent: l6omInt(12), User: l6omStr("Work")}},
		{"  12% │ 1%", Footer{ContextPercent: l6omInt(12)}},
		// Not a status line: no segment has any of the shapes.
		{"  a │ b │ c", Footer{}},
	}
	for _, c := range cases {
		rows := append(append([]string{}, frame...), c.line)
		l6omSame(t, c.line, parseFooter(rows, 100), c.want)
	}
}

// In a wide pane a short rule (a table in the transcript) is not the input
// box, so what follows it is not a footer.
func TestFooter_AShortRuleInAWidePaneIsNotTheFrame(t *testing.T) {
	short := strings.Repeat("─", 12)
	rows := []string{"", short, "❯ quoted", short, "  Haiku 5.5 │ Work │ 42% │ x", "  ⏸ manual mode on · 3 shells"}
	l6omSame(t, "short rule", parseFooter(rows, 100), Footer{})
}

// The hint means no background work, whole, at the edge, or cut.
func TestFooter_TheModeLineHintMeansNoBackground(t *testing.T) {
	frame := []string{"", strings.Repeat("─", 60), "❯ ", strings.Repeat("─", 60)}
	zero := l6omInt(0)
	cases := []struct {
		line  string
		width int
	}{
		{"  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents", 160},
		{"  ⏵⏵ auto mode on (shift+tab to cycle)", 40},
		{"  ⏵⏵ auto mode on (shift+tab to", 34},
		{"  ⏵⏵ auto mode on (shift+tab", 30},
		{"  ⏸ plan mode on (shift+tab to cycle)", 60},
		{"  ⏵⏵ accept edits on (shift+tab to cycle)", 60},
	}
	for _, c := range cases {
		rows := append(append([]string{}, frame...), c.line)
		l6omSame(t, c.line, parseFooter(rows, c.width), Footer{Shells: zero, Monitors: zero, Tasks: zero})
	}
}
