package tui

// attention_test.go covers the needs-input list's layout and the WaitingInput
// state it renders from (ini-2x8.1).
//
// Layout is asserted as WHOLE RENDERED LINES, not coordinates. The operator
// approved this box as frames, so a frame is the honest unit of assertion and a
// regression reads as a diff of the box rather than an off-by-one in a number.
//
// Fixture discipline (the spec's fixture corollary): every multi-agent fixture
// uses DISTINCT names, DISTINCT questions, and DISTINCT durations. If they were
// interchangeable, a truncation bug, a cross-wiring bug, and a mis-sort could
// each produce output that still looked right, and one could masquerade as
// another. Distinctness is what makes each of those fail differently.

import (
	"strings"
	"testing"
	"time"
)

// ── Duration formatting ─────────────────────────────────────────────

func TestFormatWaitDuration_SecondsUnderAMinuteThenMSS(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{12 * time.Second, "12s"},
		{45 * time.Second, "45s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m00s"},
		{84 * time.Second, "1m24s"},
		{130 * time.Second, "2m10s"},
		{242 * time.Second, "4m02s"},
		// No hours tier: the approved rule is "s under a minute, m:ss after"
		// and stops there. Inventing "2h00m" would be a layout decision this
		// code is not entitled to make. Recorded as a real case, not an
		// oversight -- the fleet journals show waits this long.
		{2 * time.Hour, "120m00s"},
	}
	for _, c := range cases {
		if got := formatWaitDuration(c.d); got != c.want {
			t.Errorf("formatWaitDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestFormatWaitDuration_NegativeClampsToZero(t *testing.T) {
	// Clock skew between the detector's stamp and the render tick must not
	// produce "-3s" in a box whose whole job is being trusted.
	if got := formatWaitDuration(-3 * time.Second); got != "0s" {
		t.Errorf("formatWaitDuration(-3s) = %q, want %q", got, "0s")
	}
}

// ── Hidden when empty ───────────────────────────────────────────────

func TestAttentionLines_EmptyWhenNobodyWaits(t *testing.T) {
	// The hidden-when-empty rule, expressed where it cannot be forgotten: there
	// is no empty-box value for a caller to draw or reserve space for.
	if got := attentionLines(nil, time.Now(), 120); got != nil {
		t.Errorf("attentionLines(nil) = %q, want nil (nothing rendered when nobody waits)", got)
	}
	if got := attentionLines([]WaitingRow{}, time.Now(), 120); got != nil {
		t.Errorf("attentionLines(empty) = %q, want nil", got)
	}
}

// ── Layout, asserted as whole frames ────────────────────────────────

func TestAttentionLines_SingleAgentFrame(t *testing.T) {
	now := time.Now()
	got := attentionLines([]WaitingRow{
		{Name: "pm", Preview: "stripe or paypal first?", Since: now.Add(-84 * time.Second)},
	}, now, 120)

	// The spec's own single-agent frame is a hand-drawn illustration whose
	// border is a few cells wider than its content row. The binding rules are
	// name column, then 3 spaces, then the question, then the duration flushed
	// right -- which this satisfies, with the border actually matching the rows.
	want := []string{
		"┌─ needs input ───────────────────────┐",
		"│ pm   stripe or paypal first?  1m24s │",
		"└─────────────────────────────────────┘",
	}
	assertLines(t, got, want)
	assertUniformWidth(t, got)
}

func TestAttentionLines_MultiAgentFrameOrdersOldestFirstAndPadsNames(t *testing.T) {
	now := time.Now()
	// Supplied deliberately OUT of wait order, so a missing sort cannot pass by
	// accident: 12s first, 4m02s last.
	got := attentionLines([]WaitingRow{
		{Name: "qa4", Preview: "dialog detected", Since: now.Add(-12 * time.Second)},
		{Name: "eng1", Preview: "Bash: rm -rf build/", Since: now.Add(-45 * time.Second)},
		{Name: "super", Preview: "ship v2.6.0 now, or wait?", Since: now.Add(-242 * time.Second)},
	}, now, 120)

	// Names pad to the longest CURRENTLY-waiting name ("super"), so all three
	// previews start on one column; durations flush right; oldest wait on top
	// even though the rows were handed in newest-first.
	want := []string{
		"┌─ needs input ────────────────────────────┐",
		"│ super   ship v2.6.0 now, or wait?  4m02s │",
		"│ eng1    Bash: rm -rf build/          45s │",
		"│ qa4     dialog detected              12s │",
		"└──────────────────────────────────────────┘",
	}
	assertLines(t, got, want)
	assertUniformWidth(t, got)
}

func TestAttentionLines_OldestOnTop(t *testing.T) {
	now := time.Now()
	got := attentionLines([]WaitingRow{
		{Name: "newest", Preview: "aaa", Since: now.Add(-1 * time.Second)},
		{Name: "oldest", Preview: "bbb", Since: now.Add(-999 * time.Second)},
		{Name: "middle", Preview: "ccc", Since: now.Add(-500 * time.Second)},
	}, now, 120)

	// Rows only, in order.
	var names []string
	for _, l := range got[1 : len(got)-1] {
		names = append(names, strings.Fields(l)[1])
	}
	want := []string{"oldest", "middle", "newest"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("row order = %v, want %v (top row must carry the largest attention debt)", names, want)
		}
	}
}

func TestAttentionLines_TiedWaitsOrderByNameNotInsertion(t *testing.T) {
	// Two agents blocked in the same instant must not swap places between
	// frames. A list that reshuffles for reasons the operator cannot see reads
	// as untrustworthy, which is fatal for a box whose job is being trusted.
	now := time.Now()
	since := now.Add(-30 * time.Second)
	first := attentionLines([]WaitingRow{
		{Name: "zeta", Preview: "z question", Since: since},
		{Name: "alpha", Preview: "a question", Since: since},
	}, now, 120)
	second := attentionLines([]WaitingRow{
		{Name: "alpha", Preview: "a question", Since: since},
		{Name: "zeta", Preview: "z question", Since: since},
	}, now, 120)

	assertLines(t, second, first)
	if !strings.Contains(first[1], "alpha") {
		t.Errorf("tie broken wrong: first row = %q, want alpha before zeta", first[1])
	}
}

func TestAttentionLines_ContentDrivenWidthFitsLongestQuestion(t *testing.T) {
	now := time.Now()
	short := attentionLines([]WaitingRow{
		{Name: "pm", Preview: "short?", Since: now.Add(-5 * time.Second)},
	}, now, 200)
	long := attentionLines([]WaitingRow{
		{Name: "pm", Preview: "a considerably longer question that should widen the box", Since: now.Add(-5 * time.Second)},
	}, now, 200)

	if len(long[0]) <= len(short[0]) {
		t.Errorf("box did not grow with content: short border %d cells, long border %d cells", len(short[0]), len(long[0]))
	}
	// And the long one still fits on ONE line -- wrapping only happens when a
	// question outruns the SCREEN, not merely when it is long.
	if len(long) != 3 {
		t.Errorf("long question wrapped at 200 cols: got %d lines, want 3\n%s", len(long), strings.Join(long, "\n"))
	}
}

func TestAttentionLines_WrapsOnlyWhenQuestionOutrunsScreen(t *testing.T) {
	now := time.Now()
	q := "the sweep found two children encoding the visibility decision differently — resolve by changing .10's store or by re-grooming .8's parity AC?"
	got := attentionLines([]WaitingRow{
		{Name: "pm", Preview: q, Since: now.Add(-130 * time.Second)},
	}, now, 100)

	if len(got) < 4 {
		t.Fatalf("expected a wrapped row (>=4 lines), got %d:\n%s", len(got), strings.Join(got, "\n"))
	}
	// Duration stays on line one and appears exactly once for the agent.
	if !strings.Contains(got[1], "2m10s") {
		t.Errorf("duration not on the first line: %q", got[1])
	}
	for _, l := range got[2 : len(got)-1] {
		if strings.Contains(l, "2m10s") {
			t.Errorf("duration repeated on a continuation line (reads as a second waiting agent): %q", l)
		}
	}
	// Continuation is indented.
	if !strings.HasPrefix(got[2], "│   ") {
		t.Errorf("continuation not indented: %q", got[2])
	}
}

func TestAttentionLines_EveryLineIsTheSameWidth(t *testing.T) {
	// A box whose rows disagree on width is a broken box regardless of content.
	// Uses a wide (double-cell) glyph and an em-dash so byte-length math cannot
	// pass this: the operator's own approved frame contains an em-dash.
	now := time.Now()
	got := attentionLines([]WaitingRow{
		{Name: "super", Preview: "ship — now? 日本語", Since: now.Add(-242 * time.Second)},
		{Name: "qa4", Preview: "dialog detected", Since: now.Add(-12 * time.Second)},
		{Name: "eng1", Preview: "Bash: rm -rf build/", Since: now.Add(-45 * time.Second)},
	}, now, 120)

	assertUniformWidth(t, got)
}

func TestAttentionLines_ScalesFromOneToManyAgents(t *testing.T) {
	now := time.Now()
	for n := 1; n <= 12; n++ {
		var rows []WaitingRow
		for i := 0; i < n; i++ {
			rows = append(rows, WaitingRow{
				Name:    "agent" + itoa(i),
				Preview: "question number " + itoa(i) + "?",
				Since:   now.Add(-time.Duration(i+1) * time.Second),
			})
		}
		got := attentionLines(rows, now, 120)
		if len(got) != n+2 {
			t.Errorf("n=%d: got %d lines, want %d (n rows + 2 borders)", n, len(got), n+2)
		}
		assertUniformWidth(t, got)
	}
}

func TestAttentionLines_DropsBoxWhenScreenTooNarrowToSayAnything(t *testing.T) {
	now := time.Now()
	// Better to render nothing than a shredded box: the agent is still visible
	// in its own pane and the chime still fires.
	if got := attentionLines([]WaitingRow{
		{Name: "supercalifragilistic", Preview: "a real question", Since: now.Add(-5 * time.Second)},
	}, now, 20); got != nil {
		t.Errorf("expected no box at 20 cols with a long name, got:\n%s", strings.Join(got, "\n"))
	}
}

func TestAttentionLines_EmptyPreviewStillRendersTheAgent(t *testing.T) {
	// A detector that knows an agent is blocked but not what it is asking must
	// still surface the agent -- that is the whole point of the list. It must
	// not crash or silently drop the row.
	now := time.Now()
	got := attentionLines([]WaitingRow{
		{Name: "codexpane", Preview: "", Since: now.Add(-7 * time.Second)},
	}, now, 120)
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(got), strings.Join(got, "\n"))
	}
	if !strings.Contains(got[1], "codexpane") || !strings.Contains(got[1], "7s") {
		t.Errorf("row lost its name or duration: %q", got[1])
	}
	assertUniformWidth(t, got)
}

// ── helpers ─────────────────────────────────────────────────────────

func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("line count = %d, want %d\n--- got ---\n%s\n--- want ---\n%s",
			len(got), len(want), strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got: %q\nwant: %q", i, got[i], want[i])
		}
	}
}

func assertUniformWidth(t *testing.T, lines []string) {
	t.Helper()
	if len(lines) == 0 {
		return
	}
	w := displayWidth(lines[0])
	for i, l := range lines {
		if got := displayWidth(l); got != w {
			t.Errorf("line %d width = %d, want %d (box rows disagree)\n%q\nall:\n%s",
				i, got, w, l, strings.Join(lines, "\n"))
		}
	}
}
