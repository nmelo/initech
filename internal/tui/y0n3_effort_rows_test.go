package tui

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/progress"
)

// ini-y0n.3: the Agents overlay (Alt+s) carries one row per effort in flight,
// reading exactly as a pane header on that effort, from the bead poll and the
// header's once-a-minute refresh; no bd or git call of its own.

// effortFixture is the header's fixture plus: ini-abc, one bead in progress
// (the short form); ini-r0q, one closed and one in QA owned by an agent with
// no pane here; ini-old, all closed, and ini-new, only open (neither in
// flight, so no row).
func effortFixture(now time.Time) []bdBead {
	return append(paceFixture(now),
		bdBead{ID: "ini-abc", Status: "in_progress", Assignee: "p02", UpdatedAt: now},
		bdBead{ID: "ini-r0q.1", Status: "closed", ClosedAt: now.Add(-3 * time.Hour)},
		bdBead{ID: "ini-r0q.2", Status: "in_qa", Assignee: "qa2", UpdatedAt: now.Add(-5 * time.Minute)},
		bdBead{ID: "ini-old.1", Status: "closed", ClosedAt: now.Add(-time.Hour)},
		bdBead{ID: "ini-old.2", Status: "closed", ClosedAt: now.Add(-30 * time.Minute)},
		bdBead{ID: "ini-new.1", Status: "open"},
	)
}

// effortTUI is four panes, two working, p03 idle and p04 waiting, so the
// overlay's columns are two rows tall; p03, idle, owns the quiet ini-wra.4
// (the header's stuck bead), which keeps it listed (ini-7w4).
func effortTUI(t *testing.T) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	tui, s := newTestTUIWithScreen("p01", "p02", "p03", "p04")
	for name, st := range map[string]ActivityState{"p01": StateRunning, "p02": StateRunning, "p03": StateIdle, "p04": StateWaitingInput} {
		p := ysr2Pane(tui, name)
		p.mu.Lock()
		p.activity = st
		p.mu.Unlock()
	}
	return tui, s
}

// effortBlock reads the rule and the rows under the agent columns.
func effortBlock(tui *TUI, rows [][]rune) (rule string, lines []string) {
	y := tui.overlayBounds.y + 1 + tui.overlayBounds.agentCount
	x0 := tui.overlayBounds.x
	inner := func(r []rune) string {
		end := x0 + 1
		for end < len(r) && r[end] != '│' {
			end++
		}
		return string(r[x0+1 : end])
	}
	rule = inner(rows[y])
	for _, r := range rows[y+1:] {
		if r[x0] != '│' {
			break
		}
		lines = append(lines, strings.TrimSpace(inner(r)))
	}
	return rule, lines
}

// AC1: one row per effort in flight, each equal to the header line of a pane
// on that effort, in effort id order, one rule between them and the columns.
func TestEffortRows_OneRowPerEffortReadingAsTheHeader(t *testing.T) {
	tui, s := effortTUI(t)
	now := time.Now()
	ysr2Pane(tui, "p01").SetBead("ini-wra.2", "")
	ysr2Pane(tui, "p02").SetBead("ini-abc", "")
	ysr2Pane(tui, "p03").SetBead("ini-wra.4", "")
	ysr2Pane(tui, "p04").SetBead("ini-r0q.2", "")
	tui.notePaceAnswer(effortFixture(now), true)
	tui.refreshPaceLines(now)
	want := []string{
		ysr2Pane(tui, "p02").PaceLine(), // ini-abc
		ysr2Pane(tui, "p04").PaceLine(), // ini-r0q
		ysr2Pane(tui, "p01").PaceLine(), // ini-wra
	}
	if want[0] != "ini-abc 0/1 · 0/h" || want[1] != "ini-r0q 1/2 · 0.2/h · 0 stuck" || want[2] != "ini-wra 2/4 · 0.2/h · 1 stuck" {
		t.Fatalf("fixture: the headers read %q", want)
	}
	rule, lines := effortBlock(tui, overlayFrameRows(tui, s))
	if strings.Trim(rule, "─ ") != "" || !strings.Contains(rule, "──") {
		t.Errorf("the line under the columns reads %q, want one rule", rule)
	}
	if len(lines) != len(want) {
		t.Fatalf("effort rows %q, want %q", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("row %d reads %q, want the header's %q", i, lines[i], want[i])
		}
	}
}

// AC2: an effort no pane here carries still has its row, read from bd.
func TestEffortRows_AnEffortWithNoPaneHereHasItsRow(t *testing.T) {
	tui, s := effortTUI(t)
	now := time.Now()
	beads := effortFixture(now)
	tui.notePaceAnswer(beads, true)
	tui.refreshPaceLines(now)
	pb := make([]progress.Bead, len(beads))
	for i, b := range beads {
		pb[i] = progress.Bead{ID: b.ID, Status: b.Status, Assignee: b.Assignee, UpdatedAt: b.UpdatedAt, ClosedAt: b.ClosedAt}
	}
	panes := map[string]string{"p01": "idle", "p02": "idle", "p03": "idle", "p04": "idle"}
	want := progress.PaceOf(pb, "ini-r0q.2", panes, tui.paceRule(), now, progress.HeaderWindow).Line()
	_, lines := effortBlock(tui, overlayFrameRows(tui, s))
	if len(lines) != 3 || lines[1] != want {
		t.Errorf("rows %q, want ini-r0q's %q second", lines, want)
	}
}

// AC2, AC5: nothing in flight, no answer yet, or every bead closed: the
// overlay is drawn exactly as without the rows.
func TestEffortRows_NothingInFlightDrawsNoRule(t *testing.T) {
	now := time.Now()
	tui0, s0 := effortTUI(t)
	before := framesText(overlayFrameRows(tui0, s0))
	cases := map[string][]bdBead{
		"no in-flight effort": {{ID: "ini-new.1", Status: "open"}, {ID: "ini-old.1", Status: "closed", ClosedAt: now}},
		"every bead closed":   {{ID: "ini-old.1", Status: "closed", ClosedAt: now}, {ID: "ini-old.2", Status: "closed", ClosedAt: now}},
		"bd empty":            {},
	}
	for name, beads := range cases {
		tui, s := effortTUI(t)
		tui.notePaceAnswer(beads, true)
		tui.refreshPaceLines(now)
		if got := framesText(overlayFrameRows(tui, s)); got != before {
			t.Errorf("%s: the overlay changed:\n%s\nwant\n%s", name, got, before)
		}
	}
	// Opened before the first poll answer.
	tui, s := effortTUI(t)
	tui.refreshPaceLines(now)
	if got := framesText(overlayFrameRows(tui, s)); got != before {
		t.Errorf("before any answer: the overlay changed:\n%s", got)
	}
}

// AC5: bd unanswered keeps the last rows, marked stale, as the header does.
func TestEffortRows_StaleWhenBdStopsAnswering(t *testing.T) {
	tui, s := effortTUI(t)
	now := time.Now()
	tui.notePaceAnswer(effortFixture(now), true)
	tui.refreshPaceLines(now)
	tui.notePaceAnswer(nil, false)
	tui.refreshPaceLines(now.Add(time.Minute))
	_, lines := effortBlock(tui, overlayFrameRows(tui, s))
	want := []string{"ini-abc 0/1 · 0/h (stale)", "ini-r0q 1/2 · 0.2/h · 0 stuck (stale)", "ini-wra 2/4 · 0.2/h · 1 stuck (stale)"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("rows %q, want %q", lines, want)
	}
}

// AC3: a minute of frames with the overlay open, toggled shut and open again,
// makes no bd call; the rows change only at the minute. The package names no
// git command anywhere.
func TestEffortRows_NoBdOrGitCallAndOnceAMinute(t *testing.T) {
	var calls atomic.Int32
	old := bdListBeads
	bdListBeads = func(ctx context.Context, dir string) ([]bdBead, error) {
		calls.Add(1)
		return nil, nil
	}
	t.Cleanup(func() { bdListBeads = old })

	tui, s := effortTUI(t)
	t0 := time.Now()
	tui.notePaceAnswer(effortFixture(t0), true)
	tui.refreshPaceLines(t0)
	_, first := effortBlock(tui, overlayFrameRows(tui, s))

	beads := effortFixture(t0)
	beads[2].Status, beads[2].ClosedAt = "closed", t0 // ini-wra.2 closes
	tui.notePaceAnswer(beads, true)
	for f := 0; f < 60*30; f += 30 {
		now := t0.Add(time.Duration(f/30) * time.Second)
		tui.layoutState.Overlay = f%300 != 0 // shut and reopened every ten seconds
		tui.refreshPaceLines(now)
		tui.render()
		if tui.layoutState.Overlay {
			if _, got := effortBlock(tui, overlayFrameRows(tui, s)); strings.Join(got, "|") != strings.Join(first, "|") {
				t.Fatalf("the rows changed %v into the minute: %q", now.Sub(t0), got)
			}
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("a minute of frames, refreshes and overlay toggles called bd %d times, want 0", n)
	}
	tui.refreshPaceLines(t0.Add(time.Minute))
	if _, got := effortBlock(tui, overlayFrameRows(tui, s)); len(got) != 3 || got[2] != "ini-wra 3/4 · 0.3/h · 1 stuck" {
		t.Errorf("a minute on, the rows read %q, want ini-wra's closed bead counted", got)
	}

	files, err := filepath.Glob("*.go")
	if err != nil || len(files) < 50 {
		t.Fatalf("the census read %d files (%v)", len(files), err)
	}
	gitCall := regexp.MustCompile(`exec\.Command(Context)?\([^)]*"git"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if gitCall.Match(b) {
			t.Errorf("%s runs git", f)
		}
	}
	if !gitCall.MatchString(`exec.Command("git", "log")`) || !gitCall.MatchString(`exec.CommandContext(ctx, "git")`) {
		t.Error("the census pattern does not match a git call")
	}
}

// AC4: a click on the rule or an effort row does nothing: no agent hidden,
// focus unmoved; a dot still toggles.
func TestEffortRows_AClickOnARowDoesNothing(t *testing.T) {
	tui, s := effortTUI(t)
	now := time.Now()
	tui.notePaceAnswer(effortFixture(now), true)
	tui.refreshPaceLines(now)
	rows := overlayFrameRows(tui, s)
	focused := tui.layoutState.Focused
	ruleY := tui.overlayBounds.y + 1 + tui.overlayBounds.agentCount
	for _, y := range []int{ruleY, ruleY + 1, ruleY + 3} {
		for _, x := range []int{tui.overlayBounds.x + 1, tui.overlayBounds.x + 2, tui.overlayBounds.x + 6} {
			tui.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, 0))
			tui.handleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, 0))
			for _, n := range []string{"p01", "p02", "p03", "p04"} {
				if tui.layoutState.Hidden[n] {
					t.Errorf("a click at %d,%d hid %s", x, y, n)
				}
			}
			if tui.layoutState.Focused != focused {
				t.Errorf("a click at %d,%d on an effort row moved the focus to %q", x, y, tui.layoutState.Focused)
				tui.layoutState.Focused = focused
			}
		}
	}
	stepOverlayClock(t)
	x, y, ok := overlayDotOf(rows, "p03")
	if !ok {
		t.Fatal("p03 is not drawn")
	}
	tui.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, 0))
	if !tui.layoutState.Hidden["p03"] {
		t.Error("with effort rows drawn, p03's dot no longer toggles")
	}
}

// AC4: a screen too short for every row cuts the effort rows at the bottom
// edge; the agent columns and the footer stay whole.
func TestEffortRows_ATooShortScreenCutsTheRowsFirst(t *testing.T) {
	tui, s := effortTUI(t)
	now := time.Now()
	tui.layoutState.Hidden["p04"] = true // a footer line
	tui.notePaceAnswer(effortFixture(now), true)
	tui.refreshPaceLines(now)
	// 1 top border + 2 list rows + rule + 1 effort row + footer + bottom border,
	// from py 1: the screen holds one effort row of three.
	s.SetSize(120, 1+1+2+1+1+1+1)
	rows := overlayFrameRows(tui, s)
	for _, n := range []string{"p01", "p02", "p03", "p04"} {
		if _, _, ok := overlayDotOf(rows, n); !ok {
			t.Errorf("%s's row was cut\n%s", n, framesText(rows))
		}
	}
	text := framesText(rows)
	if !strings.Contains(text, "3 visible, 1 hidden") {
		t.Errorf("the footer was cut\n%s", text)
	}
	if !strings.Contains(text, "ini-abc 0/1") || strings.Contains(text, "ini-r0q 1/") || strings.Contains(text, "ini-wra 2/") {
		t.Errorf("want the first effort row only\n%s", text)
	}
	if !strings.ContainsRune(string(rows[len(rows)-1]), '╰') {
		t.Errorf("no bottom border on the last line\n%s", text)
	}
	// No room for any row: no rule either.
	s.SetSize(120, 1+1+2+1+1+1)
	text = framesText(overlayFrameRows(tui, s))
	if strings.Contains(text, "ini-abc 0/") || !strings.Contains(text, "3 visible, 1 hidden") {
		t.Errorf("with no room for a row\n%s", text)
	}
	tui0, s0 := effortTUI(t)
	tui0.layoutState.Hidden["p04"] = true
	tui0.notePaceAnswer(effortFixture(now), true) // the same listing: p03 stuck
	tui0.refreshPaceLines(now)
	tui0.paceRows = nil
	s0.SetSize(120, 1+1+2+1+1+1)
	if base := framesText(overlayFrameRows(tui0, s0)); base != text {
		t.Errorf("with no room the overlay differs from one with no rows:\n%s\nwant\n%s", text, base)
	}
}

// AC5: a 20-column overlay cuts each row at its border.
func TestEffortRows_ANarrowOverlayCutsEachRowAtItsEdge(t *testing.T) {
	tui, s := effortTUI(t)
	now := time.Now()
	tui.notePaceAnswer(effortFixture(now), true)
	tui.refreshPaceLines(now)
	s.SetSize(21, 40)
	rows := overlayFrameRows(tui, s)
	y := tui.overlayBounds.y + 1 + tui.overlayBounds.agentCount
	for i, want := range []string{"ini-abc 0/1 · 0/h", "ini-r0q 1/2 · 0.2/h", "ini-wra 2/4 · 0.2/h"} {
		r := string(rows[y+1+i])
		if !strings.HasPrefix(strings.TrimLeft(r[strings.IndexRune(r, '│')+len("│"):], " "), want[:12]) {
			t.Errorf("row %d reads %q, want it to start %q", i, r, want[:12])
		}
		if !strings.HasSuffix(strings.TrimRight(r, " "), "│") {
			t.Errorf("row %d reads %q, want it cut at the right border", i, r)
		}
	}
}

// AC6: no row holds a time, an estimate or an actual: each is exactly the
// pace's shape.
func TestEffortRows_NoTimeOrEstimateInARow(t *testing.T) {
	shape := regexp.MustCompile(`^ini-[a-z0-9]+ \d+/\d+ · \d+(\.\d)?/h( · \d+ stuck)?( \(stale\))?$`)
	tui, s := effortTUI(t)
	now := time.Now()
	tui.notePaceAnswer(effortFixture(now), true)
	tui.refreshPaceLines(now)
	_, lines := effortBlock(tui, overlayFrameRows(tui, s))
	tui.notePaceAnswer(nil, false)
	tui.refreshPaceLines(now.Add(time.Minute))
	_, stale := effortBlock(tui, overlayFrameRows(tui, s))
	all := append(lines, stale...)
	if len(all) != 6 {
		t.Fatalf("rows %q", all)
	}
	for _, l := range all {
		if !shape.MatchString(l) {
			t.Errorf("row %q is not only closed/total, per hour and stuck", l)
		}
	}
	if shape.MatchString("ini-wra 2/4 · 1/h · 0 stuck · eta 14:05") || shape.MatchString("ini-wra 2/4 · 1/h · est 30m") {
		t.Error("the shape lets a time or an estimate through")
	}
}

// Boundary (Nelson, via super): only the Alt+s overlay changes. With the
// overlay off, the frame (the pane grid, headers included) is the same with
// effort rows held as without; the agents panel (the role-group picker) is
// the same too.
func TestEffortRows_TheGridAndThePickerAreUnchanged(t *testing.T) {
	now := time.Now()
	frame := func(withRows, picker bool) string {
		tui, s := effortTUI(t)
		ysr2Pane(tui, "p01").SetBead("ini-wra.2", "")
		tui.notePaceAnswer(effortFixture(now), true)
		tui.refreshPaceLines(now)
		if !withRows {
			if len(tui.paceRows) == 0 {
				t.Fatal("fixture: no effort rows to take away")
			}
			tui.paceRows = nil
		}
		tui.layoutState.Overlay = false
		if picker {
			tui.agents.active = true
		}
		s.Clear()
		tui.render()
		if picker {
			tui.renderAgentsGrid()
		}
		return framesText(overlayScreenRows(s))
	}
	for _, picker := range []bool{false, true} {
		if with, without := frame(true, picker), frame(false, picker); with != without {
			t.Errorf("picker open %v: the frame differs with effort rows held:\n%s\nwant\n%s", picker, with, without)
		}
	}
	if !strings.Contains(frame(true, false), "ini-wra 2/4") {
		t.Error("the frame lost the pane header: the comparison read no grid")
	}
}
