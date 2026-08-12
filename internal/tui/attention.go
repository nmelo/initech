// attention.go computes the "needs input" list -- the top-left box naming every
// agent currently blocked on the operator, with its question and a ticking wait
// duration (ini-2x8.1).
//
// Everything here is pure: rows plus a clock plus a screen width in, finished
// lines out. The renderer only paints what these functions return. That split is
// deliberate -- the operator approved this layout as FRAMES, so the tests assert
// whole rendered lines against those frames rather than poking at coordinates,
// and a layout regression shows up as a readable diff of the box itself.
//
// The frames are authoritative for LAYOUT ONLY. The question texts and durations
// in the spec are illustrations, not fixtures.
package tui

import (
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// WaitingRow is one agent blocked on the operator.
type WaitingRow struct {
	Name    string    // Agent name, as shown.
	Preview string    // What it is waiting for. May be empty.
	Since   time.Time // When the wait started. Drives both ordering and the duration.
}

const (
	// attentionTitle is the box title. Lowercase per the approved frames.
	attentionTitle = " needs input "

	// attentionNameGap is the run of spaces between the padded name column and
	// the preview, so previews start on one column.
	attentionNameGap = 3

	// attentionDurGap is the MINIMUM run of spaces between a preview and the
	// duration on the same line. Durations are right-aligned, so the real gap is
	// usually wider; this only guarantees they never touch.
	attentionDurGap = 2

	// attentionWrapIndent is how far a continuation line is indented when a
	// question outruns the screen.
	attentionWrapIndent = 2

	// attentionMinPreviewW is the narrowest preview column worth drawing. Below
	// this the box is not informative and is dropped entirely -- better to show
	// nothing than a column of shredded words.
	attentionMinPreviewW = 12
)

// formatWaitDuration renders a wait per the approved rule: seconds under a
// minute, m:ss at and above one minute.
//
// Deliberately has no hours tier. The rule the operator approved says "s under a
// minute, m:ss after" and stops there, so a two-hour wait reads "120m14s" rather
// than inventing an "2h00m" format nobody signed off on. Real waits do get that
// long -- the fleet journals show an AskUserQuestion answered after 12 hours --
// so whether long waits deserve an hours tier is a real question, but it is pm's
// to answer, not this function's to assume.
func formatWaitDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d.Seconds())
	if secs < 60 {
		return itoa(secs) + "s"
	}
	return itoa(secs/60) + "m" + pad2(secs%60) + "s"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

// displayWidth is the terminal cell width of s. Byte length is wrong here and
// visibly so: the operator's own approved frame contains an em-dash, and real
// question text carries dashes, quotes, and occasionally CJK.
func displayWidth(s string) int {
	return runewidth.StringWidth(s)
}

// sortWaitingRows orders rows oldest-wait-first: the top row carries the largest
// attention debt. Ties break on name so the list never shuffles between frames
// for reasons the operator cannot see -- two agents that started waiting in the
// same millisecond would otherwise swap places at random and make the whole box
// look untrustworthy.
func sortWaitingRows(rows []WaitingRow) []WaitingRow {
	out := make([]WaitingRow, len(rows))
	copy(out, rows)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Since.Equal(out[j].Since) {
			return out[i].Since.Before(out[j].Since)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// attentionLines renders the whole box -- border, title, one entry per waiting
// agent -- as finished lines, or nil when nobody is waiting.
//
// Returning nil for the empty case is the hidden-when-empty rule expressed
// structurally rather than as a caller's if-statement: there is no "empty box"
// value to accidentally draw, so a caller cannot reserve space for one.
//
// screenW is the full terminal width. The box grows to fit the longest question
// on one line and is capped only by the screen edge; a question that still does
// not fit wraps, and only then.
func attentionLines(rows []WaitingRow, now time.Time, screenW int) []string {
	if len(rows) == 0 {
		return nil
	}
	rows = sortWaitingRows(rows)

	nameW := 0
	for _, r := range rows {
		if w := runewidth.StringWidth(r.Name); w > nameW {
			nameW = w
		}
	}

	durs := make([]string, len(rows))
	durW := 0
	for i, r := range rows {
		durs[i] = formatWaitDuration(now.Sub(r.Since))
		if w := runewidth.StringWidth(durs[i]); w > durW {
			durW = w
		}
	}

	// Content width wanted if every question fits on its own line.
	fixed := nameW + attentionNameGap + attentionDurGap + durW
	wantPreviewW := 0
	for _, r := range rows {
		if w := runewidth.StringWidth(r.Preview); w > wantPreviewW {
			wantPreviewW = w
		}
	}

	// Cap at the screen edge: 2 cells of border plus 1 of padding on each side.
	maxContentW := screenW - 4
	if maxContentW < 1 {
		return nil
	}
	previewW := wantPreviewW
	if previewW > maxContentW-fixed {
		previewW = maxContentW - fixed
	}
	if previewW < attentionMinPreviewW {
		// Too narrow to say anything useful. Drawing a shredded box here would
		// be worse than drawing none: the chime still fires and the agent is
		// still visible in its own pane.
		if maxContentW-fixed < attentionMinPreviewW {
			return nil
		}
		previewW = attentionMinPreviewW
	}
	contentW := fixed + previewW

	var out []string
	out = append(out, attentionBorder('┌', '┐', attentionTitle, contentW))
	for i, r := range rows {
		out = append(out, attentionRowLines(r, durs[i], nameW, previewW, durW, contentW)...)
	}
	out = append(out, attentionBorder('└', '┘', "", contentW))
	return out
}

// attentionBorder builds a top or bottom border, embedding title after a single
// leading dash when one is given.
func attentionBorder(left, right rune, title string, contentW int) string {
	var b strings.Builder
	b.WriteRune(left)
	// +2 for the one-cell padding either side of the content.
	span := contentW + 2
	written := 0
	b.WriteRune('─')
	written++
	if title != "" && runewidth.StringWidth(title)+1 <= span {
		b.WriteString(title)
		written += runewidth.StringWidth(title)
	}
	for ; written < span; written++ {
		b.WriteRune('─')
	}
	b.WriteRune(right)
	return b.String()
}

// attentionRowLines renders one agent: a first line carrying name, the start of
// the question, and the right-aligned duration, plus continuation lines if the
// question outran the width.
func attentionRowLines(r WaitingRow, dur string, nameW, previewW, durW, contentW int) []string {
	name := r.Name + strings.Repeat(" ", nameW-runewidth.StringWidth(r.Name))

	segs := wrapToWidth(r.Preview, previewW)
	if len(segs) == 0 {
		segs = []string{""}
	}

	lines := make([]string, 0, len(segs))

	// Line one: name, first segment, duration flushed right.
	head := name + strings.Repeat(" ", attentionNameGap) + segs[0]
	padTo := contentW - durW
	if w := runewidth.StringWidth(head); w < padTo {
		head += strings.Repeat(" ", padTo-w)
	}
	head += strings.Repeat(" ", durW-runewidth.StringWidth(dur)) + dur
	lines = append(lines, attentionContentLine(head, contentW))

	// Continuations: indented under the preview column, no duration -- the
	// duration belongs to the agent, not to the line, and repeating it would
	// read as a second waiting agent.
	for _, seg := range segs[1:] {
		cont := strings.Repeat(" ", attentionWrapIndent) + seg
		lines = append(lines, attentionContentLine(cont, contentW))
	}
	return lines
}

// attentionContentLine wraps one line of content in the box's borders and
// padding, padding or truncating it to exactly contentW cells.
func attentionContentLine(content string, contentW int) string {
	w := runewidth.StringWidth(content)
	if w > contentW {
		content = runewidth.Truncate(content, contentW, "")
		w = runewidth.StringWidth(content)
	}
	return "│ " + content + strings.Repeat(" ", contentW-w) + " │"
}

// wrapToWidth splits s into display-width-bounded segments, breaking on spaces
// where it can and mid-token only when a single token is itself wider than the
// budget. Returns nil for an empty string.
func wrapToWidth(s string, width int) []string {
	if s == "" {
		return nil
	}
	if width <= 0 {
		return []string{""}
	}
	if runewidth.StringWidth(s) <= width {
		return []string{s}
	}

	var segs []string
	var cur string
	flush := func() {
		if cur != "" {
			segs = append(segs, cur)
			cur = ""
		}
	}
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = word
		case runewidth.StringWidth(cur)+1+runewidth.StringWidth(word) <= width:
			cur += " " + word
		default:
			flush()
			cur = word
		}
		// A single token wider than the budget must still be broken, or the
		// content line would silently truncate it and the operator would read a
		// half-word as the whole question.
		for runewidth.StringWidth(cur) > width {
			head := runewidth.Truncate(cur, width, "")
			segs = append(segs, head)
			cur = strings.TrimPrefix(cur, head)
		}
	}
	flush()
	if len(segs) == 0 {
		return []string{""}
	}
	return segs
}
