package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
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
	origW := bdSetWaitOverSent
	t.Cleanup(func() { bdSetWaitOverSent = origW })
	bdSetWaitOverSent = func(ctx context.Context, dir, id, target string) error { return nil }
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

// writeRecorder stands in for bd's metadata write and keeps each call.
type writeRecorder struct {
	mu    sync.Mutex
	calls []string
}

func recordWaitOverSent(t *testing.T) *writeRecorder {
	t.Helper()
	orig := bdSetWaitOverSent
	t.Cleanup(func() { bdSetWaitOverSent = orig })
	w := &writeRecorder{}
	bdSetWaitOverSent = func(ctx context.Context, dir, id, target string) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.calls = append(w.calls, id+"="+target)
		return nil
	}
	return w
}

// waitWrites is the calls so far once there are n, or at a two-second
// deadline.
func waitWrites(w *writeRecorder, n int) []string {
	deadline := time.Now().Add(2 * time.Second)
	for {
		w.mu.Lock()
		got := append([]string(nil), w.calls...)
		w.mu.Unlock()
		if len(got) >= n || !time.Now().Before(deadline) {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ini-agq AC4 (pm's ruling after qa1's restart finding): the line's record is
// the bead's own metadata, wait_over_sent: <target>, written once when the
// line goes out; a window 1 that restarts and polls the bead with that record
// sends nothing more. Positive twin: the same bead with no record sends.
func TestWaits_ARestartBetweenTwoPollsSendsNoSecondLine(t *testing.T) {
	over := func(meta string) []bdBead {
		return []bdBead{{ID: "ini-r.1", Status: "waiting", UpdatedAt: time.Now(), Dependencies: []progress.Dependency{{DependsOn: "ini-r.9", Type: "blocks"}},
			Metadata: json.RawMessage(meta)}, {ID: "ini-r.9", Status: "closed"}}
	}
	r := &waitsRig{beads: over(`{}`)}
	first, rec := waitsTUI(t, r)
	writes := recordWaitOverSent(t)
	first.pollBeadsOnce(time.Now())
	if got := rec.waitFor(1); len(got) != 1 {
		t.Fatalf("first process: lines %q", got)
	}
	if got := waitWrites(writes, 1); len(got) != 1 || got[0] != "ini-r.1=ini-r.9" {
		t.Fatalf("record writes %q, want one: ini-r.1=ini-r.9", got)
	}
	first.pollBeadsOnce(time.Now().Add(beadPollInterval))
	time.Sleep(50 * time.Millisecond)
	if got := waitWrites(writes, 1); len(got) != 1 {
		t.Errorf("a second poll wrote the record again: %q", got)
	}

	// The restart: a new process, its memory empty, and bd now carrying the record.
	r2 := &waitsRig{beads: over(`{"wait_over_sent":"ini-r.9"}`)}
	second, rec2 := waitsTUI(t, r2) // a new process records its own lines
	for i := 0; i < 3; i++ {
		second.pollBeadsOnce(time.Now().Add(time.Duration(i) * beadPollInterval))
	}
	time.Sleep(50 * time.Millisecond)
	if got := rec2.all(); len(got) != 0 {
		t.Errorf("after a restart the over wait was announced again: %q", got)
	}
	if !second.paceWaits["ini-r.1"].Over {
		t.Error("the restarted process does not read the wait over (the record must not hide it)")
	}
}

// The record names a target: a bead that now waits on another one is sent
// again when that one is over, and a record left by an old target is cleared
// while the new one is still open.
func TestWaits_ARecordForAnOldTargetIsClearedAndANewTargetIsSent(t *testing.T) {
	r := &waitsRig{beads: []bdBead{{ID: "ini-r.2", Status: "waiting", UpdatedAt: time.Now(),
		Dependencies: []progress.Dependency{{DependsOn: "ini-r.8", Type: "blocks"}}, Metadata: json.RawMessage(`{"wait_over_sent":"ini-r.7"}`)},
		{ID: "ini-r.8", Status: "in_progress"}}}
	tui, rec := waitsTUI(t, r)
	writes := recordWaitOverSent(t)
	tui.pollBeadsOnce(time.Now())
	if got := waitWrites(writes, 1); len(got) != 1 || got[0] != "ini-r.2=" {
		t.Fatalf("writes %q, want the old record cleared: ini-r.2=", got)
	}
	r.beads[0].Metadata = json.RawMessage(`{}`)
	r.beads[1].Status = "closed"
	tui.pollBeadsOnce(time.Now().Add(beadPollInterval))
	if got := rec.waitFor(1); len(got) != 1 || !strings.Contains(got[0], "waits on ini-r.8: ini-r.8 closed") {
		t.Fatalf("lines %q", got)
	}
	if got := waitWrites(writes, 2); len(got) != 2 || got[1] != "ini-r.2=ini-r.8" {
		t.Errorf("writes %q, want the new record", got)
	}
}

// The record's bd words, and a test binary that forgot the seam writes no
// real bead.
func TestWaits_TheRecordIsOneBdUpdateAndNeverRealInATest(t *testing.T) {
	if got := strings.Join(waitOverSentArgs("ini-x", "tag v1"), " "); got != "update ini-x --set-metadata wait_over_sent=tag v1" {
		t.Errorf("set: %q", got)
	}
	if got := strings.Join(waitOverSentArgs("ini-x", ""), " "); got != "update ini-x --unset-metadata wait_over_sent" {
		t.Errorf("clear: %q", got)
	}
	if err := bdSetWaitOverSent(context.Background(), t.TempDir(), "ini-x", "tag v1"); !errors.Is(err, errBdWriteInTest) {
		t.Errorf("the real seam ran under a test: %v", err)
	}
}

// A record names one target only: a bead whose record is for an old target and
// whose new target is already over is announced, and the record moves on.
func TestWaits_ARecordForAnotherTargetDoesNotHideAnOverWait(t *testing.T) {
	r := &waitsRig{beads: []bdBead{{ID: "ini-r.3", Status: "waiting", UpdatedAt: time.Now(),
		Dependencies: []progress.Dependency{{DependsOn: "ini-r.6", Type: "blocks"}}, Metadata: json.RawMessage(`{"wait_over_sent":"ini-r.5"}`)},
		{ID: "ini-r.6", Status: "closed"}}}
	tui, rec := waitsTUI(t, r)
	writes := recordWaitOverSent(t)
	tui.pollBeadsOnce(time.Now())
	if got := rec.waitFor(1); len(got) != 1 || !strings.Contains(got[0], "waits on ini-r.6: ini-r.6 closed") {
		t.Fatalf("lines %q, want the over wait announced", got)
	}
	if got := waitWrites(writes, 1); len(got) != 1 || got[0] != "ini-r.3=ini-r.6" {
		t.Errorf("writes %q", got)
	}
}
