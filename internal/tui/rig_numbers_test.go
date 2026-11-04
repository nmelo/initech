package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
	"gopkg.in/yaml.v3"
)

// ini-3sl: window 1 reads the repo at most once a minute, keeps the numbers
// for status and the board, and sends super one line per threshold crossing.

// fakeRig stands in for gh: canned commits and runs, every call counted.
type fakeRig struct {
	mu    sync.Mutex
	calls int
	red   int // failed push runs to report in the last hour
	down  bool
}

func (f *fakeRig) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.down {
		return nil, errors.New("could not resolve host: api.github.com")
	}
	if name != "gh" {
		return nil, errors.New("rig numbers ran " + name)
	}
	if args[0] == "api" {
		return []byte(`[{"commit":{"message":"fix: x (ini-a)","committer":{"date":"` + time.Now().UTC().Format(time.RFC3339) + `"}}}]`), nil
	}
	var runs []string
	for i := 0; i < f.red; i++ {
		runs = append(runs, `{"conclusion":"failure","createdAt":"`+time.Now().Add(-5*time.Minute).UTC().Format(time.RFC3339)+`","updatedAt":"`+time.Now().Add(-3*time.Minute).UTC().Format(time.RFC3339)+`"}`)
	}
	return []byte("[" + strings.Join(runs, ",") + "]"), nil
}

func (f *fakeRig) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func useFakeRig(t *testing.T, f *fakeRig) {
	t.Helper()
	orig := rigRun
	t.Cleanup(func() { rigRun = orig })
	rigRun = f.run
}

// rigProject is a rig with a GitHub repo and the thresholds in block.
func rigProject(t *testing.T, block string) *config.Project {
	t.Helper()
	var p config.Project
	if err := yaml.Unmarshal([]byte("project: demo\nrepos:\n  - url: https://github.com/nmelo/initech\n    name: src\n"+block), &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

// rigTUI is window 1 with an engineer and super, its bead poll answered.
func rigTUI(t *testing.T, block string) *TUI {
	t.Helper()
	tui := newTestTUI(testPane("eng1"), testPane("super"))
	tui.windowID = WindowOne
	tui.project = rigProject(t, block)
	tui.notePaceAnswer([]bdBead{{ID: "ini-q", Status: "qa_passed", CreatedAt: time.Now().Add(-10 * time.Minute)}}, true)
	return tui
}

// gatherAt runs one tick at now and waits for any gather it started.
func gatherAt(t *testing.T, tui *TUI, now time.Time) {
	t.Helper()
	tui.refreshRigNumbers(now)
	deadline := time.Now().Add(2 * time.Second)
	for tui.rig.busy.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the gather never finished")
		}
		time.Sleep(time.Millisecond)
	}
}

// AC2: one tick every 33ms for three minutes reads the repo three times
// (two gh calls each), never per tick, and makes no bd call of its own.
func TestRigNumbers_ReadsTheRepoAtMostOnceAMinute(t *testing.T) {
	f := &fakeRig{}
	useFakeRig(t, f)
	origBd := bdListBeads
	t.Cleanup(func() { bdListBeads = origBd })
	bdCalls := 0
	bdListBeads = func(context.Context, string) ([]bdBead, error) { bdCalls++; return nil, nil }

	tui := rigTUI(t, "")
	start := time.Now()
	for at := start; at.Before(start.Add(3 * time.Minute)); at = at.Add(33 * time.Millisecond) {
		gatherAt(t, tui, at)
	}
	if got := f.count(); got != 6 {
		t.Errorf("gh ran %d times over three minutes of ticks, want 6 (two reads at 0, 1 and 2 minutes)", got)
	}
	if bdCalls != 0 {
		t.Errorf("the rig numbers ran bd %d times; they read the bead poll", bdCalls)
	}
}

// A child window never reads the repo and has no numbers.
func TestRigNumbers_OnlyWindowOneReads(t *testing.T) {
	f := &fakeRig{}
	useFakeRig(t, f)
	tui := rigTUI(t, "")
	tui.windowID = "w2"
	gatherAt(t, tui, time.Now())
	if f.count() != 0 || tui.RigNumbersSnapshot() != nil {
		t.Fatalf("a child window read gh %d times", f.count())
	}
}

// AC3: one line per crossing, nothing while crossed, re-armed on return.
func TestRigNumbers_AThresholdCrossingSendsOneLineToSuper(t *testing.T) {
	r := recordInitechLines(t)
	f := &fakeRig{}
	useFakeRig(t, f)
	tui := rigTUI(t, "numbers:\n  gate_red_max: 0\n")
	at := time.Now()
	step := func(red int) {
		f.mu.Lock()
		f.red = red
		f.mu.Unlock()
		gatherAt(t, tui, at)
		at = at.Add(rigNumbersEvery)
	}
	step(0)
	step(1)
	got := r.waitFor(1)
	want := "super|initech|[from initech] rig numbers: gate red is 1, over its max of 0"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("after the crossing: %q, want %q", got, want)
	}
	step(2)
	step(1)
	if got := r.waitFor(2); len(got) != 1 {
		t.Fatalf("while crossed: %q, want nothing more", got)
	}
	step(0)
	step(1)
	if got := r.waitFor(2); len(got) != 2 || got[1] != want {
		t.Fatalf("after re-arming: %q, want a second line", got)
	}
}

// AC4: gh unreachable prints the repo numbers as unknown and sends nothing,
// even with a threshold on them.
func TestRigNumbers_GHUnreachableIsUnknownAndSilent(t *testing.T) {
	r := recordInitechLines(t)
	f := &fakeRig{down: true}
	useFakeRig(t, f)
	tui := rigTUI(t, "numbers:\n  gate_red_max: 0\n  merges_per_hour_min: 5\n")
	gatherAt(t, tui, time.Now())
	n := tui.RigNumbersSnapshot()
	if n == nil || !strings.HasPrefix(n.Line(), "merges/h ? · gate ? · landing ?") {
		t.Fatalf("numbers %v", n)
	}
	if got := r.waitFor(1); len(got) != 0 {
		t.Fatalf("unknown numbers sent %q", got)
	}
}

// AC4: a rig with no repo reads nothing and still has its line.
func TestRigNumbers_ARigWithNoRepo(t *testing.T) {
	f := &fakeRig{}
	useFakeRig(t, f)
	tui := rigTUI(t, "")
	tui.project.Repos = nil
	gatherAt(t, tui, time.Now())
	n := tui.RigNumbersSnapshot()
	if f.count() != 0 || n == nil || !strings.HasPrefix(n.Line(), "merges/h - · gate - · landing -") {
		t.Fatalf("gh calls %d, numbers %v", f.count(), n)
	}
}

// AC1: the board's last row is the rig line, under the effort rows.
func TestRigNumbers_TheBoardCarriesTheLineAsItsLastRow(t *testing.T) {
	f := &fakeRig{red: 1}
	useFakeRig(t, f)
	tui, s := effortTUI(t)
	tui.windowID = WindowOne
	tui.project = rigProject(t, "")
	now := time.Now()
	ysr2Pane(tui, "p02").SetBead("ini-abc", "")
	tui.notePaceAnswer(effortFixture(now), true)
	tui.refreshPaceLines(now)
	gatherAt(t, tui, now)
	_, lines := effortBlock(tui, overlayFrameRows(tui, s))
	if len(lines) < 2 {
		t.Fatalf("board rows %q, want the effort rows and the rig line", lines)
	}
	want := rigRowPrefix + tui.RigNumbersSnapshot().Line()
	if got := lines[len(lines)-1]; got != want {
		t.Errorf("last row %q, want %q", got, want)
	}
	if !strings.Contains(want, "gate 0g/1r") {
		t.Errorf("rig line %q does not read the fake gate", want)
	}
}
