package tui

import (
	"strings"
	"testing"
)

// ini-gdym: Claude cuts the operator's status line right after the context
// percent ("0% │…", "0% …", "0%…"); the % sign is on screen, so the percent
// is whole and is read. Cut inside it ("0…") it is not. Fixtures: real Claude
// Code 2.1.310 Haiku screens in a git folder at 39, 38, 37 and 36 columns.

func TestFooter_APercentCutRightAfterIsRead(t *testing.T) {
	model, user := l6omStr("Haiku 5.5"), l6omStr("Second")
	zero := l6omInt(0)
	for _, w := range []int{39, 38, 37} {
		file := "haiku310_branch_idle_w" + string(rune('0'+w/10)) + string(rune('0'+w%10)) + ".txt"
		got := parseFooter(lvxwFixture(t, file), w)
		l6omSame(t, file, Footer{ContextPercent: got.ContextPercent, Model: got.Model, User: got.User},
			Footer{ContextPercent: zero, Model: model, User: user})
	}
	got := parseFooter(lvxwFixture(t, "haiku310_branch_idle_w36.txt"), 36)
	// No percent read, so the name before it is not read either (ini-67k7).
	l6omSame(t, "w36, cut inside the percent", Footer{ContextPercent: got.ContextPercent, Model: got.Model, User: got.User},
		Footer{Model: model})
}

func TestFooter_TheLastSegmentNeedsItsPercentSign(t *testing.T) {
	frame := []string{"", strings.Repeat("─", 60), "❯ ", strings.Repeat("─", 60)}
	cases := []struct {
		line string
		want *int
	}{
		{"  ⎇ main │ Opus 5.5 │ Work │ 10%", l6omInt(10)},
		{"  ⎇ main │ Opus 5.5 │ Work │ 10% │…", l6omInt(10)},
		{"  ⎇ main │ Opus 5.5 │ Work │ 8…", nil},
		{"  ⎇ main │ Opus 5.5 │ Work │ 8%x", nil},
		{"  ⎇ main │ Opus 5.5 │ Work │ 180%…", nil},
		// An earlier whole percent is the context; a later cut one is not.
		{"  ⎇ main │ Opus 5.5 │ Work │ 3% │ 60%…", l6omInt(3)},
	}
	for _, c := range cases {
		got := parseFooter(append(append([]string{}, frame...), c.line), 60)
		if l6omShow(got.ContextPercent) != l6omShow(c.want) {
			t.Errorf("%s: context %s, want %s", c.line, l6omShow(got.ContextPercent), l6omShow(c.want))
		}
	}
}
