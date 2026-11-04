package progress

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeWorld answers tags and runs, counting the questions.
type fakeWorld struct {
	tags     map[string]bool
	runs     map[string]bool // id -> completed; absent means no such run
	tagErr   error
	tagCalls int
	runCalls int
}

func (f *fakeWorld) TagExists(name string) (bool, error) {
	f.tagCalls++
	return f.tags[name], f.tagErr
}

func (f *fakeWorld) RunCompleted(id string) (bool, bool, error) {
	f.runCalls++
	done, ok := f.runs[id]
	return done, ok, nil
}

func waitingBead(id string, meta string, deps ...Dependency) Bead {
	b := Bead{ID: id, Status: "waiting", Assignee: "eng1", UpdatedAt: paceNow.Add(-5 * time.Minute), Dependencies: deps}
	if meta != "" {
		b.Metadata = json.RawMessage(meta)
	}
	return b
}

// AC1: each target reads over the moment its thing arrives, and not before.
func TestJudge_EachTargetIsOverWhenItsThingArrives(t *testing.T) {
	world := &fakeWorld{tags: map[string]bool{}, runs: map[string]bool{"41858021524": false}}
	dep := waitingBead("w.1", "", Dependency{DependsOn: "x.9", Type: "blocks"})
	tag := waitingBead("w.2", `{"wait_on":"tag:v2.58.0"}`)
	run := waitingBead("w.3", `{"wait_on":"run:41858021524"}`)
	date := waitingBead("w.4", `{"wait_on":"date:2026-11-01"}`)
	status := map[string]string{"x.9": "in_progress"}
	before := paceNow // 2026-11-01 03:00 UTC: the date has not passed
	for _, b := range []Bead{dep, tag, run, date} {
		if w := Judge(b, status, before.In(time.UTC), world); w.Over {
			t.Errorf("%s over before its thing arrived: %+v", b.ID, w)
		}
	}
	status["x.9"] = "closed"
	world.tags["v2.58.0"] = true
	world.runs["41858021524"] = true
	after := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC) // local midnight after the date
	for b, event := range map[*Bead]string{&dep: "x.9 closed", &tag: "tag v2.58.0 exists", &run: "run 41858021524 completed", &date: "2026-11-01 passed"} {
		w := Judge(*b, status, after, world)
		if !w.Over || w.Event != event {
			t.Errorf("%s: %+v, want over with %q", b.ID, w, event)
		}
	}
	// A minute before midnight the date is still open.
	if w := Judge(date, status, after.Add(-time.Minute), world); w.Over {
		t.Errorf("the date read over before midnight: %+v", w)
	}
}

// AC1: a waiting bead with no typed target, or one that does not parse, is
// unknown; a run GitHub does not know is unknown, naming it.
func TestJudge_NoTargetIsUnknown(t *testing.T) {
	world := &fakeWorld{runs: map[string]bool{}}
	for b, want := range map[*Bead]string{
		ptr(waitingBead("u.1", "")):                           "unknown",
		ptr(waitingBead("u.2", `{"wait":"free text only"}`)):  "unknown",
		ptr(waitingBead("u.3", `{"wait_on":"soon"}`)):         "unknown (wait_on soon)",
		ptr(waitingBead("u.4", `{"wait_on":"run:12x"}`)):      "unknown (wait_on run:12x)",
		ptr(waitingBead("u.5", `{"wait_on":"run:999"}`)):      "unknown (no run 999)",
		ptr(waitingBead("u.6", `"{\"wait_on\":\"tag:v1\"}"`)): "tag v1",
	} {
		if got := Judge(*b, nil, paceNow, world).Target.String(); got != want {
			t.Errorf("%s: target %q, want %q", b.ID, got, want)
		}
	}
	// A related dependency is no wait; a blocks one is.
	b := waitingBead("u.7", "", Dependency{DependsOn: "x.1", Type: "related"}, Dependency{DependsOn: "x.2", Type: "blocks"})
	if got := TargetOf(b).String(); got != "x.2" {
		t.Errorf("target %q, want the blocks dependency x.2", got)
	}
}

// AC2 in the model: a bead not in waiting is never judged and asks nothing;
// a tag the repo cannot answer leaves the wait open.
func TestJudge_OnlyWaitingBeadsAskTheWorld(t *testing.T) {
	world := &fakeWorld{tags: map[string]bool{"v1": true}, runs: map[string]bool{"1": true}}
	for _, status := range []string{"in_progress", "blocked", "waiting_on_nelson", "closed", "open"} {
		b := waitingBead("n.1", `{"wait_on":"tag:v1"}`)
		b.Status = status
		if w := Judge(b, nil, paceNow, world); w != (WaitState{}) {
			t.Errorf("%s judged: %+v", status, w)
		}
	}
	if world.tagCalls != 0 || world.runCalls != 0 {
		t.Errorf("asked the world for beads not waiting: tags %d runs %d", world.tagCalls, world.runCalls)
	}
	world.tagErr = errors.New("repo unreachable")
	if w := Judge(waitingBead("n.2", `{"wait_on":"tag:v1"}`), nil, paceNow, world); w.Over {
		t.Errorf("an unreachable repo read over: %+v", w)
	}
	if w := Judge(waitingBead("n.3", `{"wait_on":"tag:v1"}`), nil, paceNow, nil); w.Over || w.Target.String() != "tag v1" {
		t.Errorf("no world: %+v, want open on tag v1", w)
	}
}

// AC5: stuck is unchanged, a waiting bead whose wait is over counts waiting
// and over whatever its age, and never stuck; the stuck setting's three hover
// cases stay where they were.
func TestCounts_OverIsWaitingNeverStuck(t *testing.T) {
	in := hoverWaitingInput()
	fresh := Bead{ID: "hov-w.7", Title: "over, just set", Status: "waiting", Assignee: "eng1", UpdatedAt: paceNow.Add(-time.Minute),
		Metadata: json.RawMessage(`{"wait_on":"date:2026-10-30"}`)}
	in.Beads = append(in.Beads, fresh)
	in.Beads = JudgeAll(in.Beads, paceNow, nil)
	r := Build(in)
	if r.Stuck != 0 {
		t.Errorf("stuck %d %v: a waiting bead read stuck", r.Stuck, r.StuckIDs)
	}
	var row *WaitingRow
	for i := range r.Waiting {
		if r.Waiting[i].ID == "hov-w.7" {
			row = &r.Waiting[i]
		}
	}
	if row == nil || !row.Over || row.Event != "2026-10-30 passed" || r.WaitingOver != 1 {
		t.Fatalf("the over bead a minute old: row %+v, over %d", row, r.WaitingOver)
	}
	if len(r.Waiting) != 5 {
		t.Errorf("waiting %d, want the four hover cases and the over bead", len(r.Waiting))
	}
	p := PaceOver(in.Beads, "hov-w", in.Panes, in.Rule, in.Now, HeaderWindow)
	if p.Over != 1 || !strings.HasSuffix(p.Line(), "· 5 waiting · 1 over") {
		t.Errorf("line %q, over %d", p.Line(), p.Over)
	}
	out := RenderReport(r)
	for _, want := range []string{"waiting 5 (1 over)", "hov-w.7 eng1: no comment [OVER: 2026-10-30 passed]", "hov-w.4 -: no comment [waits on unknown]"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table lacks %q:\n%s", want, out)
		}
	}
}

// Feed it nothing: no waiting beads count nothing and ask nothing.
func TestJudgeAll_NothingWaiting(t *testing.T) {
	world := &fakeWorld{}
	beads := JudgeAll([]Bead{{ID: "a", Status: "in_progress"}}, paceNow, world)
	if beads[0].Wait != (WaitState{}) || world.tagCalls+world.runCalls != 0 {
		t.Errorf("a bead not waiting: %+v, calls %d", beads[0].Wait, world.tagCalls+world.runCalls)
	}
	if got := JudgeAll(nil, paceNow, world); len(got) != 0 {
		t.Errorf("no beads gave %v", got)
	}
}

func ptr(b Bead) *Bead { return &b }

// Text from bd cannot drive the terminal: a tag outside git's ref characters
// is refused, and a target, an event or a note carries no control character.
func TestWaits_NoControlCharactersReachTheTable(t *testing.T) {
	if _, ok := ParseWaitOn("tag:v1\x1b[2J"); ok {
		t.Error("a tag name with an escape parsed")
	}
	if _, ok := ParseWaitOn("tag:v2.58.0"); !ok {
		t.Error("an ordinary tag name was refused")
	}
	dep := waitingBead("e.1", "", Dependency{DependsOn: "x\x1b]0;pwned\x07", Type: "blocks"})
	bad := waitingBead("e.2", `{"wait_on":"date:\u001b[31m"}`)
	in := Input{Rig: "r", Prefix: "e.", Hours: 6, Now: paceNow, Rule: testRule, Panes: map[string]string{"eng1": "idle"}, PanesKnown: true,
		Beads: []Bead{dep, bad, {ID: "x\x1b]0;pwned\x07", Status: "closed"}}, Notes: []string{"bad \x1b[2J note"}}
	in.Beads[0].UpdatedAt = paceNow.Add(-3 * time.Hour)
	in.Beads[1].UpdatedAt = paceNow.Add(-3 * time.Hour)
	out := RenderReport(Build(in))
	for _, r := range out {
		if r != '\n' && (r < 0x20 || (r >= 0x7f && r <= 0x9f)) {
			t.Fatalf("the table holds control character %#x:\n%q", r, out)
		}
	}
	if !strings.Contains(out, "OVER: x]0;pwned closed") {
		t.Errorf("the cleaned event is missing:\n%s", out)
	}
}
