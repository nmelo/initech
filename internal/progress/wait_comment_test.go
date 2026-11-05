package progress

import (
	"strings"
	"testing"
	"time"
)

// ini-75pt: the newest comment starting WAIT, an optional parenthetical and
// a colon is the bead's stated wait; edges older than it are not judged;
// a wait with several targets is over only when all are.

var w0 = time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC)

func wc(min int, text string) Comment {
	return Comment{Author: "super", Text: text, CreatedAt: w0.Add(time.Duration(min) * time.Minute)}
}

func edge(to string, min int) Dependency {
	return Dependency{DependsOn: to, Type: "blocks", CreatedAt: w0.Add(time.Duration(min) * time.Minute)}
}

func stated(b Bead, cs ...Comment) Bead {
	b.WaitComments = cs
	return b
}

// AC2: which comments are a stated wait.
func TestStatedWait_ThePrefix(t *testing.T) {
	cases := map[string]bool{
		"WAIT: ini-abc":   true,
		"  WAIT: ini-abc": true,
		"WAIT (operator via theguy): shape moves": true,
		"WAIT(super): x":                    true,
		"WAIT OVER: ini-abc landed":         false,
		"WAIT added: also waits on ini-abc": false,
		"WAITING (super, theguy hold): x":   false,
		"Wait restated: ini-abc":            false,
		"wait: ini-abc":                     false, // lower case is prose
		"waits on ini-abc":                  false,
		"The WAIT: ini-abc":                 false,
	}
	for text, want := range cases {
		_, _, ok := StatedWait("ini-me", []Comment{wc(0, text)})
		if ok != want {
			t.Errorf("%q: stated=%v, want %v", text, ok, want)
		}
	}
}

// AC2: the newest stated wait wins over older ones.
func TestStatedWait_TheNewestWins(t *testing.T) {
	tg, at, ok := StatedWait("ini-me", []Comment{wc(0, "WAIT: ini-old"), wc(5, "a note"), wc(9, "WAIT: ini-new"), wc(12, "WAIT OVER: ini-new")})
	if !ok || tg.Name != "ini-new" || !at.Equal(w0.Add(9*time.Minute)) {
		t.Fatalf("stated = %+v at %v %v, want ini-new at +9m", tg, at, ok)
	}
}

// AC2: the target is the first typed token, left to right, skipping the
// bead's own id; a bare hex word is never a sha.
func TestStatedWait_TheTarget(t *testing.T) {
	cases := []struct {
		text string
		want WaitTarget
	}{
		{"WAIT: ini-abc landing, then ini-def", WaitTarget{WaitOnBead, "ini-abc"}},
		{"WAIT: ini-me first, then ◆ ini-r0q.18 lands", WaitTarget{WaitOnBead, "ini-r0q.18"}},
		{"WAIT: the cut v2.61.0 tagged", WaitTarget{WaitOnTag, "v2.61.0"}},
		{"WAIT: run:41958451385 green", WaitTarget{WaitOnRun, "41958451385"}},
		{"WAIT: https://github.com/nmelo/initech/actions/runs/42083803994 done", WaitTarget{WaitOnRun, "42083803994"}},
		{"WAIT: date:2026-11-09 Monday", WaitTarget{WaitOnDate, "2026-11-09"}},
		{"WAIT: 2026-11-09 Monday", WaitTarget{}},            // a bare date is not typed
		{"WAIT: added coverage deadbeef cafe", WaitTarget{}}, // hex words are words
		{"WAIT (operator via theguy): theguy message is due", WaitTarget{}},
		{"WAIT: v2.61.0 then ini-abc", WaitTarget{WaitOnTag, "v2.61.0"}},
		{"WAIT: hov-abc is not this rig", WaitTarget{}},
	}
	for _, c := range cases {
		got, _, ok := StatedWait("ini-me", []Comment{wc(0, c.text)})
		if !ok {
			t.Errorf("%q: not read as a stated wait", c.text)
			continue
		}
		if got != c.want {
			t.Errorf("%q: target %+v, want %+v", c.text, got, c.want)
		}
	}
}

func judgeAt(b Bead, status map[string]string, closed map[string]time.Time, now time.Time) WaitState {
	return JudgeIndexed(b, Index{Status: status, ClosedAt: closed}, now, nil)
}

// AC1, the fed0 shape: an edge from an earlier wait, then a stated wait
// with no typed target. The old edge closing sends nothing; the stated wait
// is never over.
func TestWaits_TheFed0Shape(t *testing.T) {
	b := stated(waitingBead("ini-fed0", "", edge("ini-nxyf", 0)),
		wc(3, "WAIT (operator via theguy): shape is changing to a five-box rig board"))
	w := judgeAt(b, map[string]string{"ini-nxyf": "closed"}, nil, w0.Add(time.Hour))
	if w.Over {
		t.Fatalf("the fed0 shape read over: %+v", w)
	}
	// Positive twin: the stated wait names a bead; its close ends the wait.
	twin := stated(waitingBead("ini-fed0", "", edge("ini-nxyf", 0)), wc(3, "WAIT: ini-xyz for the board shape"))
	if w := judgeAt(twin, map[string]string{"ini-nxyf": "closed", "ini-xyz": "in_progress"}, nil, w0.Add(time.Hour)); w.Over {
		t.Fatalf("twin over before ini-xyz closed: %+v", w)
	}
	w = judgeAt(twin, map[string]string{"ini-nxyf": "closed", "ini-xyz": "closed"}, nil, w0.Add(time.Hour))
	if !w.Over || w.Event != "ini-xyz closed" {
		t.Fatalf("twin = %+v, want over on ini-xyz", w)
	}
}

// AC1, the pt3j shape: two edges, the first closed, the second open: not
// over. Twin: both closed, the notice names the last to close.
func TestWaits_ThePt3jShape(t *testing.T) {
	b := waitingBead("ini-pt3j", "", edge("ini-t0y1", 0), edge("ini-8rkg", 10))
	if w := judgeAt(b, map[string]string{"ini-t0y1": "closed", "ini-8rkg": "in_progress"}, nil, w0.Add(time.Hour)); w.Over {
		t.Fatalf("over with ini-8rkg open: %+v", w)
	}
	closed := map[string]time.Time{"ini-t0y1": w0.Add(20 * time.Minute), "ini-8rkg": w0.Add(50 * time.Minute)}
	w := judgeAt(b, map[string]string{"ini-t0y1": "closed", "ini-8rkg": "closed"}, closed, w0.Add(time.Hour))
	if !w.Over || w.Event != "ini-8rkg closed" || w.Target.Name != "ini-8rkg" {
		t.Fatalf("both closed: %+v, want over naming ini-8rkg (the last to close)", w)
	}
	// The order of the edges does not decide which is named.
	closed = map[string]time.Time{"ini-t0y1": w0.Add(55 * time.Minute), "ini-8rkg": w0.Add(50 * time.Minute)}
	if w := judgeAt(b, map[string]string{"ini-t0y1": "closed", "ini-8rkg": "closed"}, closed, w0.Add(time.Hour)); w.Target.Name != "ini-t0y1" {
		t.Fatalf("named %q, want ini-t0y1 (closed last)", w.Target.Name)
	}
}

// AC1, the l1r shape: an edge from an earlier wait, then a stated wait on
// another bead. The old edge closing sends nothing; the stated bead's close
// does (the twin).
func TestWaits_TheL1rShape(t *testing.T) {
	b := stated(waitingBead("ini-l1r", "", edge("ini-r0q.18", 0)), wc(30, "WAIT: ini-zz9 Nelson's word, recorded there"))
	if w := judgeAt(b, map[string]string{"ini-r0q.18": "closed", "ini-zz9": "waiting_on_nelson"}, nil, w0.Add(time.Hour)); w.Over {
		t.Fatalf("the old edge closing ended the wait: %+v", w)
	}
	if w := judgeAt(b, map[string]string{"ini-r0q.18": "closed", "ini-zz9": "closed"}, nil, w0.Add(time.Hour)); !w.Over || w.Target.Name != "ini-zz9" {
		t.Fatalf("twin: %+v, want over on ini-zz9", w)
	}
}

// AC2: an edge added after the stated wait is judged with it: over only
// when both are.
func TestWaits_AnEdgeAfterTheStatedWaitJoinsIt(t *testing.T) {
	b := stated(waitingBead("ini-x", "", edge("ini-late", 20)), wc(10, "WAIT: v2.61.0 tag"))
	world := &fakeWorld{tags: map[string]bool{"v2.61.0": true}}
	w := JudgeIndexed(b, Index{Status: map[string]string{"ini-late": "in_progress"}}, w0.Add(time.Hour), world)
	if w.Over {
		t.Fatalf("over with the newer edge open: %+v", w)
	}
	w = JudgeIndexed(b, Index{Status: map[string]string{"ini-late": "closed"}}, w0.Add(time.Hour), world)
	if !w.Over {
		t.Fatalf("not over with both ended: %+v", w)
	}
}

// AC2: an untyped stated wait is waiting, never over, even with every edge
// closed.
func TestWaits_AnUntypedStatedWaitIsNeverOver(t *testing.T) {
	b := stated(waitingBead("ini-x", "", edge("ini-a", 20)), wc(10, "WAIT: a human answer"))
	if w := judgeAt(b, map[string]string{"ini-a": "closed"}, nil, w0.Add(time.Hour)); w.Over {
		t.Fatalf("untyped stated wait over: %+v", w)
	}
}

// AC2, rule 4: wait_on is judged only with no stated wait and no edge.
func TestWaits_WaitOnOnlyAsTheFallback(t *testing.T) {
	plain := waitingBead("ini-x", `{"wait_on":"date:2026-11-01"}`)
	if w := judgeAt(plain, nil, nil, w0.Add(48*time.Hour)); !w.Over {
		t.Fatalf("wait_on alone not judged: %+v", w)
	}
	outranked := stated(plain, wc(0, "WAIT: ini-abc"))
	if w := judgeAt(outranked, map[string]string{"ini-abc": "open"}, nil, w0.Add(48*time.Hour)); w.Over {
		t.Fatalf("wait_on judged beside a stated wait: %+v", w)
	}
	withEdge := waitingBead("ini-x", `{"wait_on":"date:2026-11-01"}`, edge("ini-abc", 0))
	if w := judgeAt(withEdge, map[string]string{"ini-abc": "open"}, nil, w0.Add(48*time.Hour)); w.Over {
		t.Fatalf("wait_on judged beside an edge in force: %+v", w)
	}
}

// Feed it nothing: no comment, no edge, no wait_on: unknown, never over.
func TestWaits_NothingIsUnknown(t *testing.T) {
	w := judgeAt(waitingBead("ini-x", ""), nil, nil, w0)
	if w.Over || w.Target.Kind != WaitOnUnknown {
		t.Fatalf("%+v, want unknown", w)
	}
}

// AC4's base: a moved wait gives a new Target while not over, so the caller
// clears wait_over_sent.
func TestWaits_AMovedWaitChangesTheTarget(t *testing.T) {
	b := stated(waitingBead("ini-x", ""), wc(0, "WAIT: ini-a"))
	before := judgeAt(b, map[string]string{"ini-a": "open"}, nil, w0.Add(time.Hour))
	moved := stated(waitingBead("ini-x", ""), wc(0, "WAIT: ini-a"), wc(5, "WAIT: ini-b"))
	after := judgeAt(moved, map[string]string{"ini-a": "open", "ini-b": "open"}, nil, w0.Add(time.Hour))
	if before.Target.String() == after.Target.String() {
		t.Fatalf("a moved wait kept target %s", after.Target)
	}
}

// No comment text ever reaches the judgement's words: only ids and typed
// targets.
func TestWaits_NoCommentTextInTheJudgement(t *testing.T) {
	secret := "please-do-not-repeat-me"
	b := stated(waitingBead("ini-x", "", edge("ini-a", 20)), wc(10, "WAIT (super): ini-b "+secret))
	w := judgeAt(b, map[string]string{"ini-a": "closed", "ini-b": "closed"}, nil, w0.Add(time.Hour))
	if strings.Contains(w.Event+w.Target.String(), secret) || strings.Contains(w.Event+w.Target.String(), "super") {
		t.Fatalf("comment text in the judgement: %+v", w)
	}
}

// An edge from an earlier wait that is still open does not hold a stated
// wait that has ended: the stated wait is the wait.
func TestWaits_AnOldOpenEdgeDoesNotHoldAnEndedStatedWait(t *testing.T) {
	b := stated(waitingBead("ini-x", "", edge("ini-old", 0)), wc(10, "WAIT: ini-new"))
	w := judgeAt(b, map[string]string{"ini-old": "in_progress", "ini-new": "closed"}, nil, w0.Add(time.Hour))
	if !w.Over || w.Target.Name != "ini-new" {
		t.Fatalf("%+v, want over on ini-new with the old edge ignored", w)
	}
}
