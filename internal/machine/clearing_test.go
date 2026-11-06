package machine

import (
	"strings"
	"testing"
	"time"
)

// ini-c6ii: during the one-minute hold, with every figure already under, a
// refused start says the ceiling is clearing, since when, and when starts
// resume, instead of blaming figures that are under. Times come from the
// readings' clock (Step's now), never a real one.

var c6T0 = time.Date(2026, 11, 5, 14, 1, 30, 0, time.UTC)

func c6Over() Reading {
	r := under()
	r.Awake = 30
	return r
}

func c6Under() Reading {
	return Reading{Awake: 28, AwakeOK: true, FreeGB: 11, FreeOK: true, Load: 12, LoadOK: true}
}

// AC1/AC3: engaged at 14:01:30, every figure under from 14:01:50: the
// refusal says clearing, under since 14:01, starts again at 14:02 (14:01:50
// plus ClearAfter), with the figures against the ceiling.
func TestGate_AHoldWithEveryFigureUnderSaysTheCeilingIsClearing(t *testing.T) {
	var g Gate
	g.Step(c6Over(), testCeiling, c6T0)
	g.Step(c6Under(), testCeiling, c6T0.Add(20*time.Second))
	ok, words := g.Admit()
	want := "not started: the ceiling is clearing: every figure under since 14:01 " +
		"(28/30 sessions, 11.0 GB free over the 8 GB floor, load 12/30); starts again after one minute under, at 14:02"
	if ok || words != want {
		t.Fatalf("Admit in the hold = %v\n%q\nwant\n%q", ok, words, want)
	}
	// A later reading under (14:02:45, still inside the minute) keeps the
	// first under-since, not its own time.
	g.Step(c6Under(), testCeiling, c6T0.Add(75*time.Second))
	if _, words := g.Admit(); words != want {
		t.Fatalf("a later reading under moved the times:\n%q", words)
	}
}

// AC2, the twin: engaged with a figure still at the ceiling keeps today's
// refusal, and so does a hold that a reading over broke.
func TestGate_AFigureStillAtTheCeilingKeepsTheRefusal(t *testing.T) {
	var g Gate
	g.Step(c6Over(), testCeiling, c6T0)
	if _, words := g.Admit(); strings.Contains(words, "clearing") || !strings.Contains(words, "at its ceiling") {
		t.Fatalf("at the ceiling: %q", words)
	}
	g.Step(c6Under(), testCeiling, c6T0.Add(10*time.Second))
	g.Step(c6Over(), testCeiling, c6T0.Add(20*time.Second))
	if _, words := g.Admit(); strings.Contains(words, "clearing") {
		t.Fatalf("over again after a dip: %q", words)
	}
}

// Starts let through since the last reading count: when they bring the awake
// count to the ceiling, the refusal is today's, not clearing.
func TestGate_StartsAdmittedThatReachTheCeilingAreNotClearing(t *testing.T) {
	var g Gate
	g.Step(c6Under(), testCeiling, c6T0)
	g.Admit()
	g.Admit()
	ok, words := g.Admit()
	if ok || strings.Contains(words, "clearing") || !strings.Contains(words, "30 awake sessions") {
		t.Fatalf("third start at the ceiling = %v %q", ok, words)
	}
}

// An unread figure shows "?", as the refusal does.
func TestGate_ClearingWordsMarkAnUnreadFigure(t *testing.T) {
	var g Gate
	g.Step(c6Over(), testCeiling, c6T0)
	r := c6Under()
	r.LoadOK = false
	g.Step(r, testCeiling, c6T0.Add(40*time.Second))
	if _, words := g.Admit(); !strings.Contains(words, "load ?/30") {
		t.Fatalf("unread load: %q", words)
	}
}

// The clearing words say "one minute" because ClearAfter is one minute.
func TestGate_ClearingSaysOneMinuteBecauseClearAfterIsOne(t *testing.T) {
	if ClearAfter != time.Minute {
		t.Fatalf("ClearAfter = %v: change Clearing's words with it", ClearAfter)
	}
}
