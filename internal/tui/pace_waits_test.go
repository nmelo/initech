package tui

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/progress"
)

// waitsRig stands in for bd, git and gh. beads is what each poll lists.
type waitsRig struct {
	beads    []bdBead
	tags     map[string]bool
	runs     map[string]string // id -> status; absent is unknown to GitHub
	tagCalls atomic.Int32
	runCalls atomic.Int32
	lists    atomic.Int32
}

func waitsTUI(t *testing.T, r *waitsRig) (*TUI, *lineRecorder) {
	t.Helper()
	origL, origC, origT, origG, origH, origB := bdListBeads, bdStatusCustom, gitTagExists, ghRunStatus, projectHasBeads, bdBeadComments
	t.Cleanup(func() {
		bdListBeads, bdStatusCustom, gitTagExists, ghRunStatus, projectHasBeads, bdBeadComments = origL, origC, origT, origG, origH, origB
	})
	bdListBeads = func(ctx context.Context, dir string) ([]bdBead, error) { r.lists.Add(1); return r.beads, nil }
	bdStatusCustom = func(ctx context.Context, dir string) (string, error) {
		return "ready_for_qa,in_qa,qa_passed,waiting", nil
	}
	bdBeadComments = func(ctx context.Context, dir, id string) ([]progress.Comment, error) { return nil, nil }
	gitTagExists = func(ctx context.Context, url, name string) (bool, error) { r.tagCalls.Add(1); return r.tags[name], nil }
	ghRunStatus = func(ctx context.Context, slug, id string) (string, bool, error) {
		r.runCalls.Add(1)
		st, ok := r.runs[id]
		return st, ok, nil
	}
	projectHasBeads = func(string) bool { return false } // no crossing reads here
	rec := recordInitechLines(t)
	tui := newTestTUI(testPane("super"), testPane("eng1"))
	tui.project = &config.Project{Repos: []config.Repo{{URL: "git@github.com:o/r.git", Name: "r"}}}
	return tui, rec
}

func waitOn(id, value string) bdBead {
	return bdBead{ID: id, Status: "waiting", UpdatedAt: time.Now(), Metadata: json.RawMessage(`{"wait_on":"` + value + `"}`)}
}

// AC1 and AC4: a dependency wait goes over the poll after the other bead
// closes, and super hears exactly one line for it, not one per tick.
func TestWaits_ADependencyGoesOverAndSuperHearsOnce(t *testing.T) {
	r := &waitsRig{beads: []bdBead{
		{ID: "ini-w.1", Status: "waiting", UpdatedAt: time.Now(), Dependencies: []progress.Dependency{{DependsOn: "ini-w.9", Type: "blocks"}}},
		{ID: "ini-w.9", Status: "in_progress"},
	}}
	tui, rec := waitsTUI(t, r)
	now := time.Now()
	tui.pollBeadsOnce(now)
	if tui.paceWaits["ini-w.1"].Over {
		t.Fatal("over while the other bead is in progress")
	}
	r.beads[1].Status = "closed"
	for i := 0; i < 5; i++ {
		tui.pollBeadsOnce(now.Add(time.Duration(i) * beadPollInterval))
	}
	got := rec.waitFor(1)
	time.Sleep(50 * time.Millisecond)
	if got = rec.all(); len(got) != 1 || got[0] != "super|initech|[from initech] ☾ ini-w.1 waits on ini-w.9: ini-w.9 closed; move it on" {
		t.Fatalf("lines %q, want one", got)
	}
	if w := tui.paceWaits["ini-w.1"]; !w.Over || w.Event != "ini-w.9 closed" {
		t.Errorf("wait %+v", w)
	}
	if n := r.lists.Load(); n != 6 {
		t.Errorf("six polls made %d bd lists, want six (one each, as before)", n)
	}
}

// AC2: a tag is asked at most once a minute, a run the same, none once
// final, none for a bead not waiting; AC1 the tag and run go over.
func TestWaits_TagsAndRunsAreAskedOnceAMinuteAndNeverOnceFinal(t *testing.T) {
	r := &waitsRig{
		beads: []bdBead{waitOn("ini-w.2", "tag:v9"), waitOn("ini-w.3", "run:42"),
			{ID: "ini-w.4", Status: "in_progress", Metadata: json.RawMessage(`{"wait_on":"tag:v9"}`)}},
		tags: map[string]bool{}, runs: map[string]string{"42": "in_progress"},
	}
	tui, rec := waitsTUI(t, r)
	t0 := time.Now()
	for i := 0; i < 6; i++ { // a minute of polls, ten seconds apart
		tui.pollBeadsOnce(t0.Add(time.Duration(i) * beadPollInterval))
	}
	if r.tagCalls.Load() != 1 || r.runCalls.Load() != 1 {
		t.Fatalf("a minute of polls: tag %d run %d calls, want 1 each", r.tagCalls.Load(), r.runCalls.Load())
	}
	r.tags["v9"], r.runs["42"] = true, "completed"
	tui.pollBeadsOnce(t0.Add(61 * time.Second))
	if !tui.paceWaits["ini-w.2"].Over || !tui.paceWaits["ini-w.3"].Over {
		t.Fatalf("after a minute: %+v", tui.paceWaits)
	}
	for i := 0; i < 10; i++ {
		tui.pollBeadsOnce(t0.Add(time.Duration(70+i*60) * time.Second))
	}
	if r.tagCalls.Load() != 2 || r.runCalls.Load() != 2 {
		t.Errorf("once final: tag %d run %d calls, want no more than 2 each", r.tagCalls.Load(), r.runCalls.Load())
	}
	if got := rec.waitFor(2); len(got) != 2 {
		t.Errorf("lines %q, want one per bead", got)
	}
}

// AC1 and feed it nothing: a date goes over at local midnight after it; a run
// GitHub does not know reads unknown, is asked once, and sends no line; no
// typed target is unknown.
func TestWaits_ADateGoesOverAtMidnightAndAnUnknownRunIsAskedOnce(t *testing.T) {
	r := &waitsRig{beads: []bdBead{waitOn("ini-w.5", "date:2026-11-04"), waitOn("ini-w.6", "run:999"),
		{ID: "ini-w.7", Status: "waiting"}}, runs: map[string]string{}}
	tui, rec := waitsTUI(t, r)
	day := time.Date(2026, 11, 4, 23, 59, 0, 0, time.Local)
	tui.pollBeadsOnce(day)
	if tui.paceWaits["ini-w.5"].Over {
		t.Fatal("the date read over before midnight")
	}
	for i := 1; i <= 5; i++ {
		tui.pollBeadsOnce(day.Add(time.Duration(i) * time.Minute))
	}
	if w := tui.paceWaits["ini-w.5"]; !w.Over || w.Event != "2026-11-04 passed" {
		t.Errorf("after midnight: %+v", w)
	}
	if w := tui.paceWaits["ini-w.6"]; w.Over || w.Target.String() != "unknown (no run 999)" || r.runCalls.Load() != 1 {
		t.Errorf("unknown run: %+v, %d calls", w, r.runCalls.Load())
	}
	if w := tui.paceWaits["ini-w.7"]; w.Target.String() != "unknown" {
		t.Errorf("no target: %+v", w)
	}
	if got := rec.waitFor(1); len(got) != 1 || !strings.Contains(got[0], "ini-w.5 waits on 2026-11-04: 2026-11-04 passed") {
		t.Errorf("lines %q, want the date's alone", got)
	}
}

// AC4: the line is not sent again while the bead waits on the same target,
// even after it leaves waiting and comes back; a new target sends a new one.
func TestWaits_ANewTargetSendsANewLineTheSameOneDoesNot(t *testing.T) {
	r := &waitsRig{beads: []bdBead{waitOn("ini-w.8", "date:2026-01-01")}}
	tui, rec := waitsTUI(t, r)
	now := time.Now()
	tui.pollBeadsOnce(now)
	r.beads[0].Status = "in_progress"
	tui.pollBeadsOnce(now.Add(time.Minute))
	r.beads[0].Status = "waiting"
	tui.pollBeadsOnce(now.Add(2 * time.Minute))
	time.Sleep(50 * time.Millisecond)
	if got := rec.all(); len(got) != 1 {
		t.Fatalf("lines %q, want one for the same target", got)
	}
	r.beads[0].Metadata = json.RawMessage(`{"wait_on":"date:2026-02-01"}`)
	tui.pollBeadsOnce(now.Add(3 * time.Minute))
	if got := rec.waitFor(2); len(got) != 2 || !strings.Contains(got[1], "2026-02-01 passed") {
		t.Errorf("lines %q, want a second for the new target", got)
	}
}

// AC3: the effort row reads "N waiting · M over" with the over part red, and
// the rest of the row is not.
func TestWaits_TheOverPartOfTheEffortRowIsRed(t *testing.T) {
	r := &waitsRig{beads: []bdBead{waitOn("ini-v.1", "date:2026-01-01"), waitOn("ini-v.2", "tag:v1")}, tags: map[string]bool{}}
	tui, s := ysr3Parked(t)
	origL, origT, origH := bdListBeads, gitTagExists, projectHasBeads
	t.Cleanup(func() { bdListBeads, gitTagExists, projectHasBeads = origL, origT, origH })
	bdListBeads = func(ctx context.Context, dir string) ([]bdBead, error) { return r.beads, nil }
	gitTagExists = func(ctx context.Context, url, name string) (bool, error) { return false, nil }
	projectHasBeads = func(string) bool { return false }
	recordInitechLines(t)
	tui.project = e4cProject(t, "15m")
	tui.layoutState.Overlay = true
	now := time.Now()
	tui.pollBeadsOnce(now)
	tui.paceAt = time.Time{}
	tui.refreshPaceLines(now)
	if len(tui.paceRows) != 1 || !strings.Contains(tui.paceRows[0], "1 waiting · 1 over") {
		t.Fatalf("rows %q", tui.paceRows)
	}
	tui.render()
	w, h := s.Size()
	red, other := 0, 0
	for y := 0; y < h; y++ {
		var row []rune
		for x := 0; x < w; x++ {
			c, _, _ := s.Get(x, y)
			row = append(row, []rune(c + " ")[0])
		}
		i := strings.Index(string(row), "1 over")
		if i < 0 {
			continue
		}
		col := len([]rune(string(row)[:i]))
		for x := col; x < col+len("1 over"); x++ {
			if _, st, _ := s.Get(x, y); colorOf(st) == tcell.ColorRed {
				red++
			}
		}
		if _, st, _ := s.Get(col-4, y); colorOf(st) == tcell.ColorRed {
			other++
		}
	}
	if red != len("1 over") || other != 0 {
		t.Errorf("red cells in the over part %d of %d, red before it %d", red, len("1 over"), other)
	}
}

func colorOf(st tcell.Style) tcell.Color {
	fg, _, _ := st.Decompose()
	return fg
}

// The line is typed into super's pane, so nothing from bd can carry a control
// character into it: the bead id, the target and the event are cleaned.
func TestWaits_TheLineToSuperCarriesNoControlCharacters(t *testing.T) {
	bad := "ini-x\x1b[2J"
	r := &waitsRig{beads: []bdBead{
		{ID: bad, Status: "waiting", UpdatedAt: time.Now(), Dependencies: []progress.Dependency{{DependsOn: "ini-y\x07", Type: "blocks"}}},
		{ID: "ini-y\x07", Status: "closed"},
	}}
	tui, rec := waitsTUI(t, r)
	tui.pollBeadsOnce(time.Now())
	got := rec.waitFor(1)
	if len(got) != 1 {
		t.Fatalf("lines %q", got)
	}
	for _, c := range got[0] {
		if c < 0x20 || (c >= 0x7f && c <= 0x9f) {
			t.Fatalf("the line holds control character %#x: %q", c, got[0])
		}
	}
	if !strings.Contains(got[0], "ini-x[2J waits on ini-y: ini-y closed") {
		t.Errorf("line %q", got[0])
	}
}
