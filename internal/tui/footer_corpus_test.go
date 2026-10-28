package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// The footer corpus (ini-2tec): every live footer shape captured, read in
// one table. A new footer report is turned into a test by adding its rows to
// testdata/footer_corpus (or naming an existing fixture) and a line here.
//
// Corpus files hold the input box and the rows under it; the loader puts one
// blank row above them for the transcript a real pane has there, and takes
// the pane's width from the box's bottom rule, which spans the pane.
//
// Sources: Claude Code 2.1.310 screens on Haiku (claude --model haiku), this
// rig's live Opus, Sonnet and Fable panes read with initech peek, and
// theguy's pa pane (rows relayed on the bead). Account names are replaced by
// a placeholder of the same length.

func corpusFooter(t *testing.T, name string) ([]string, int) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "footer_corpus", name))
	if err != nil {
		t.Fatal(err)
	}
	rows := append([]string{""}, strings.Split(strings.TrimRight(string(b), "\n"), "\n")...)
	width := 0
	for _, r := range rows {
		if t := strings.TrimSpace(r); t != "" && strings.Trim(t, "─") == "" {
			width = max(width, utf8.RuneCountInString(t))
		}
	}
	return rows, width
}

func TestFooterCorpus_EveryCapturedShapeReadsWhatItShows(t *testing.T) {
	i, s := l6omInt, l6omStr
	zero := i(0)
	none := func(ctx *int, model, user *string) Footer { return Footer{ctx, zero, zero, zero, model, user} }
	cases := []struct {
		name string
		want Footer
	}{
		// The input box's top rule carries the session name (ini-2tec).
		{"theguy_pa_w94.txt", none(i(73), s("Opus 5.5"), s("Second"))},
		{"haiku310_label_pa_w94.txt", none(i(0), s("Haiku 5.5"), s("Second"))},
		{"haiku310_label_accountant_w94.txt", none(i(0), s("Haiku 5.5"), s("Second"))},
		{"haiku310_label_pa_w40.txt", none(i(0), s("Haiku 5.5"), s("Second"))},
		// hover's and nayutal's supers (rows relayed on the bead): a model
		// segment with a suffix, and background work on the mode line.
		{"hover_1m_3shells_w118.txt", Footer{i(46), i(3), zero, zero, s("Opus 5.5 (1M context)"), s("Second")}},
		{"nayutal_1m_2monitors_w109.txt", Footer{i(46), zero, i(2), zero, s("Opus 5.5 (1M context)"), s("Second")}},
		// nayutal (54 columns) and hover's other panes: no branch segment,
		// "0%", a two-word label, the 1M-context model cut and uncut.
		{"nayutal_opus_1shell_w54.txt", Footer{i(39), i(1), zero, zero, s("Opus 5.5"), s("Second")}},
		{"nayutal_fable_idle_w54.txt", none(i(46), s("Fable 5.1"), s("Second"))},
		{"nayutal_sonnet_nobranch_w54.txt", none(i(0), s("Sonnet 5.5"), s("Second"))},
		{"hover_super_label_w118.txt", Footer{i(46), i(3), zero, zero, s("Opus 5.5 (1M context)"), s("Second")}},
		{"hover_video_1m_cut_w89.txt", none(i(32), s("Opus 5.5 (1M context)"), s("Second"))},
		{"hover_qa4_sonnet_nobranch_w90.txt", Footer{i(43), i(1), zero, zero, s("Sonnet 5.5"), s("Second")}},
		// This rig's live panes, about 58 and 75 columns, bypass mode.
		{"fleet_qa1.txt", none(i(42), s("Sonnet 5.5"), s("Second"))},
		{"fleet_super.txt", Footer{i(22), zero, i(1), zero, s("Opus 5.5"), s("Second")}},
		{"fleet_pm.txt", none(i(60), s("Fable 5.1"), s("Second"))},
		{"fleet_shipper.txt", none(i(36), s("Opus 5.5"), s("Second"))},
		{"fleet_eng1.txt", none(i(0), s("Opus 5.5"), s("Second"))},
		{"fleet_eng2.txt", none(i(54), s("Opus 5.5"), s("Second"))},
		{"fleet_eng4.txt", none(i(0), s("Opus 5.5"), s("Second"))},
		// nayutal eng6, a 32-column pane held "modal open" by a dialog latch
		// that never cleared (ini-gav); the mode line is cut after "· 1".
		{"nayutal_eng6_latched_w32.txt", Footer{i(60), nil, nil, zero, s("Opus 5.5"), s("Second2")}},
		// Claude Code 2.1.312 on Haiku, 36 columns (ini-gav): a background
		// shell, then two shells and a background subagent listed below.
		{"haiku312_bgshell_w36.txt", Footer{i(6), i(1), zero, zero, s("Haiku 5.5"), s("Other")}},
		{"haiku312_subagent_w36.txt", Footer{i(6), i(2), zero, i(1), s("Haiku 5.5"), s("Other")}},
	}
	for _, c := range cases {
		rows, width := corpusFooter(t, c.name)
		l6omSame(t, c.name, parseFooter(rows, width), c.want)
	}
}

// The whole-screen fixtures from earlier reports stay in the corpus: wide and
// narrow, with and without the branch, cut after and inside the percent, the
// mode line whole, at the edge, cut, and with work.
func TestFooterCorpus_TheEarlierScreensStillRead(t *testing.T) {
	i, s := l6omInt, l6omStr
	zero := i(0)
	h, p, sec := s("Haiku 5.5"), s("Personal"), s("Second")
	cases := []struct {
		file  string
		width int
		want  Footer
	}{
		{"haiku310_footer_idle.txt", 160, Footer{zero, zero, zero, zero, h, p}},
		{"haiku310_footer_shell.txt", 160, Footer{i(8), i(1), zero, zero, h, p}},
		{"haiku310_footer_shell_monitor.txt", 160, Footer{i(8), i(1), i(1), zero, h, p}},
		{"haiku310_footer_bgagent.txt", 160, Footer{i(9), i(1), i(1), i(1), h, p}},
		{"haiku310_footer_narrow40.txt", 40, Footer{i(8), i(1), zero, zero, h, p}},
		{"haiku310_footer_bgagent_narrow40.txt", 40, Footer{i(9), i(1), nil, i(1), h, p}},
		{"haiku310_branch_idle.txt", 160, Footer{zero, zero, zero, zero, h, sec}},
		{"haiku310_branch_shell.txt", 160, Footer{i(5), i(1), zero, zero, h, sec}},
		{"haiku310_branch_idle_w40.txt", 40, Footer{zero, zero, zero, zero, h, sec}},
		{"haiku310_branch_shell_w40.txt", 40, Footer{i(5), i(1), zero, zero, h, sec}},
		{"haiku310_branch_idle_w39.txt", 39, Footer{zero, zero, zero, zero, h, sec}},
		{"haiku310_branch_idle_w38.txt", 38, Footer{zero, zero, zero, zero, h, sec}},
		{"haiku310_branch_idle_w37.txt", 37, Footer{zero, zero, zero, zero, h, sec}},
		{"haiku310_branch_idle_w36.txt", 36, Footer{nil, zero, zero, zero, h, nil}},
		{"haiku310_branch_idle_w34.txt", 34, Footer{nil, zero, zero, zero, h, nil}},
		{"haiku310_branch_shell_w34.txt", 34, Footer{nil, i(1), zero, zero, h, nil}},
		{"haiku310_landing_shell.txt", 160, Footer{i(6), i(1), zero, zero, h, sec}},
		{"haiku310_compacted.txt", 160, Footer{i(0), zero, zero, zero, h, sec}},
	}
	for _, c := range cases {
		l6omSame(t, c.file, parseFooter(lvxwFixture(t, c.file), c.width), c.want)
	}
}

// A rule with a label is the input box only as its TOP rule, with the label
// short and at the end; a rule with other text, or text across it, is not.
func TestFooterCorpus_OnlyAShortEndLabelMakesARule(t *testing.T) {
	foot := []string{"  ⎇ main │ Opus 5.5 │ Work │ 12% │ 1%", "  ⏸ manual mode on"}
	frame := func(top string) []string {
		return append([]string{"", top, "❯ ", strings.Repeat("─", 60)}, foot...)
	}
	read := frame(strings.Repeat("─", 54) + " pa ─")
	if f := parseFooter(read, 60); l6omShow(f.ContextPercent) != "12" {
		t.Errorf("labelled top rule: context %s, want 12", l6omShow(f.ContextPercent))
	}
	for _, top := range []string{
		strings.Repeat("─", 20) + " some words in the middle " + strings.Repeat("─", 14),
		strings.Repeat("─", 54) + " pa │",
		"── " + strings.Repeat("x", 50) + " ─",
		// A short rule before a long label is text, not a rule.
		strings.Repeat("─", 8) + " " + strings.Repeat("x", 35) + " ─",
	} {
		if f := parseFooter(frame(top), 60); f.ContextPercent != nil {
			t.Errorf("%q read as the box's rule", top)
		}
	}
	// The bottom rule is never labelled.
	rows := []string{"", strings.Repeat("─", 60), "❯ ", strings.Repeat("─", 54) + " pa ─", foot[0], foot[1]}
	if f := parseFooter(rows, 60); f.ContextPercent != nil {
		t.Error("a labelled bottom rule read as the box")
	}
}
