package machine

import (
	"testing"
	"time"
)

// ini-o9dy: the coordinator is never refused by the ceiling and still counts
// as awake; a refusal names the figure at or over.

var o9T0 = time.Date(2026, 11, 17, 9, 59, 0, 0, time.UTC)

func o9Reading(awake int, free, load float64) Reading {
	return Reading{Awake: awake, AwakeOK: true, FreeGB: free, FreeOK: true, Load: load, LoadOK: true}
}

var o9Ceiling = Ceiling{Sessions: 40, FreeMemoryGB: 8, Load: 30}

// AC3: the refusal names each figure at or over, then the rest.
func TestRefusal_NamesTheFigureAtOrOver(t *testing.T) {
	cases := []struct {
		name string
		r    Reading
		want string
	}{
		{"load", o9Reading(0, 28.2, 30), "not started: the Mac is at its ceiling: load 30 at or over 30 (0 awake sessions, 28.2 GB free)"},
		{"sessions", o9Reading(41, 28.2, 5), "not started: the Mac is at its ceiling: 41 awake sessions at or over 40 (28.2 GB free, load 5)"},
		{"memory", o9Reading(3, 6.1, 5), "not started: the Mac is at its ceiling: 6.1 GB free at or under 8 GB (3 awake sessions, load 5)"},
		{"two", o9Reading(40, 28.2, 31), "not started: the Mac is at its ceiling: 40 awake sessions at or over 40, load 31 at or over 30 (28.2 GB free)"},
		{"all", o9Reading(40, 8, 30), "not started: the Mac is at its ceiling: 40 awake sessions at or over 40, 8.0 GB free at or under 8 GB, load 30 at or over 30"},
		{"none over (engaged, a reading under)", o9Reading(3, 28.2, 5), "not started: the Mac is at its ceiling: 3 awake sessions, 28.2 GB free, load 5; ceiling 40 / 8 GB / 30"},
	}
	for _, c := range cases {
		if got := Refusal(c.r, o9Ceiling); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	// An unread figure is never over, even at a value that would be.
	r := o9Reading(0, 28.2, 99)
	r.LoadOK = false
	if got := Refusal(r, o9Ceiling); got != "not started: the Mac is at its ceiling: 0 awake sessions, 28.2 GB free, load ?; ceiling 40 / 8 GB / 30" {
		t.Errorf("unread load: %q", got)
	}
}

// The exempt start: let through at the ceiling, with what was over for the
// log; in the hold, "clearing"; under the ceiling, not an exemption at all.
func TestGate_AdmitExemptLetsTheCoordinatorThrough(t *testing.T) {
	var g Gate
	if used, over := g.AdmitExempt(); used || over != "" {
		t.Fatalf("no reading yet: used=%v over=%q", used, over)
	}
	g.Step(o9Reading(0, 28.2, 30), o9Ceiling, o9T0)
	if used, over := g.AdmitExempt(); !used || over != "load 30/30" {
		t.Fatalf("at load 30/30: used=%v over=%q", used, over)
	}
	if ok, _ := g.Admit(); ok {
		t.Fatal("eng1 was let through at the ceiling after the coordinator")
	}
	g.Step(o9Reading(0, 28.2, 5), o9Ceiling, o9T0.Add(10*time.Second))
	if used, over := g.AdmitExempt(); !used || over != "clearing" {
		t.Fatalf("in the hold: used=%v over=%q", used, over)
	}
	g.Step(o9Reading(0, 28.2, 5), o9Ceiling, o9T0.Add(80*time.Second))
	if used, over := g.AdmitExempt(); used || over != "" {
		t.Fatalf("under the ceiling: used=%v over=%q", used, over)
	}
	var h Gate
	h.Step(o9Reading(0, 6.1, 5), o9Ceiling, o9T0)
	if _, over := h.AdmitExempt(); over != "6.1/8 GB free" {
		t.Fatalf("memory: %q", over)
	}
	var k Gate
	k.Step(o9Reading(41, 28.2, 5), o9Ceiling, o9T0)
	if _, over := k.AdmitExempt(); over != "41/40 sessions" {
		t.Fatalf("sessions: %q", over)
	}
}

// The coordinator still counts as an awake session: at 39 of 40, its start
// brings the count to the ceiling, so the next agent is refused.
func TestGate_TheExemptStartCountsAsAwake(t *testing.T) {
	var g Gate
	g.Step(o9Reading(39, 28.2, 5), o9Ceiling, o9T0)
	if used, _ := g.AdmitExempt(); used {
		t.Fatal("under the ceiling the coordinator's start is not an exemption")
	}
	ok, words := g.Admit()
	if ok || words != "not started: the Mac is at its ceiling: 40 awake sessions at or over 40 (28.2 GB free, load 5)" {
		t.Fatalf("the agent after the coordinator = %v %q; want refused at 40 of 40", ok, words)
	}
}
