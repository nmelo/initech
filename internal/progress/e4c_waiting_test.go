package progress

import (
	"strings"
	"testing"
	"time"
)

// ini-e4c AC4: the shapes the hover rig's waiting beads take, by shape and not
// its text (this repo is public): 55 waiting there, 21 with an owner and 34
// with no holder, many titled "Step ...", which the table leaves out. All of
// them land in the calm waiting count and none in stuck.
func hoverWaitingInput() Input {
	old := paceNow.Add(-3 * time.Hour)
	return Input{
		Rig: "demo", Prefix: "hov-w.", Hours: 6, Now: paceNow, Rule: NewRule(20*time.Minute, []string{"in_progress", "ready_for_qa", "in_qa", "qa_passed"}),
		Beads: []Bead{
			// Case 1: status waiting, the owner idle.
			{ID: "hov-w.1", Title: "a pause", Status: "waiting", Assignee: "eng1", UpdatedAt: old},
			// Case 2: the named wait is already over: the bead it names closed.
			{ID: "hov-w.2", Title: "wait over", Status: "waiting", Assignee: "eng2", UpdatedAt: old},
			{ID: "hov-w.20", Title: "what it waited on", Status: "closed", ClosedAt: old},
			// Case 3: an idle owner on a bead whose wait is on another bead.
			{ID: "hov-w.3", Title: "on another bead", Status: "waiting", Assignee: "eng3", UpdatedAt: old},
			{ID: "hov-w.30", Title: "the other bead", Status: "in_progress", Assignee: "eng4", UpdatedAt: paceNow.Add(-time.Minute)},
			// No holder, a Step title.
			{ID: "hov-w.4", Title: "Step 5: later work", Status: "waiting", UpdatedAt: old},
			// Not counted: moved inside the value; an owner at work.
			{ID: "hov-w.5", Title: "fresh", Status: "waiting", Assignee: "eng1", UpdatedAt: paceNow.Add(-5 * time.Minute)},
			{ID: "hov-w.6", Title: "owner working", Status: "waiting", Assignee: "eng4", UpdatedAt: old},
		},
		Comments: map[string][]Comment{
			"hov-w.1": {{Author: "eng1", Text: "parked for the release\nmore detail", CreatedAt: old}},
			"hov-w.2": {{Author: "eng2", Text: "older", CreatedAt: old.Add(-time.Hour)}, {Author: "eng2", Text: "waits on hov-w.20 landing", CreatedAt: old}},
			"hov-w.3": {{Author: "eng3", Text: "waiting on hov-w.30", CreatedAt: old}},
		},
		Panes:      map[string]string{"eng1": "idle", "eng2": "idle", "eng3": "idle", "eng4": "running"},
		PanesKnown: true,
	}
}

func TestWaiting_TheHoverCasesCountWaitingNotStuck(t *testing.T) {
	r := Build(hoverWaitingInput())
	if r.Stuck != 0 || len(r.StuckIDs) != 0 {
		t.Errorf("stuck %d %v: a waiting bead read stuck", r.Stuck, r.StuckIDs)
	}
	if r.Blocked != 0 {
		t.Errorf("blocked %d: waiting is not blocked", r.Blocked)
	}
	want := []WaitingRow{
		{ID: "hov-w.1", Owner: "eng1", Wait: "parked for the release"},
		{ID: "hov-w.2", Owner: "eng2", Wait: "waits on hov-w.20 landing"},
		{ID: "hov-w.3", Owner: "eng3", Wait: "waiting on hov-w.30"},
		{ID: "hov-w.4", Owner: "-", Wait: "no comment"},
	}
	if len(r.Waiting) != len(want) {
		t.Fatalf("waiting = %+v, want %+v", r.Waiting, want)
	}
	for i := range want {
		if r.Waiting[i] != want[i] {
			t.Errorf("waiting[%d] = %+v, want %+v", i, r.Waiting[i], want[i])
		}
	}
	out := RenderReport(r)
	for _, line := range []string{"stuck 0, blocked 0, waiting 4", "waiting 20m or more:", "  hov-w.1 eng1: parked for the release", "  hov-w.4 -: no comment"} {
		if !strings.Contains(out, line) {
			t.Errorf("the table lacks %q:\n%s", line, out)
		}
	}
}

// AC4: the quiet list holds no side state; a working-chain bead past the value
// is in it and, with an idle owner, is stuck with its id.
func TestQuiet_HoldsOnlyWorkingChainBeads(t *testing.T) {
	old := paceNow.Add(-3 * time.Hour)
	in := Input{Rig: "r", Prefix: "x.", Hours: 6, Now: paceNow, Rule: testRule, Panes: map[string]string{"eng1": "idle"}, PanesKnown: true}
	for id, status := range map[string]string{"x.1": "waiting", "x.2": "blocked", "x.3": "waiting_on_nelson", "x.4": "deferred", "x.5": "in_progress", "x.6": "ready_to_ship"} {
		in.Beads = append(in.Beads, Bead{ID: id, Title: "t", Status: status, Assignee: "eng1", UpdatedAt: old})
	}
	r := Build(in)
	if len(r.Quiet) != 1 || r.Quiet[0].ID != "x.5" {
		t.Errorf("quiet = %+v, want only the in_progress bead", r.Quiet)
	}
	if strings.Join(r.StuckIDs, " ") != "x.5" {
		t.Errorf("stuck ids %v, want x.5", r.StuckIDs)
	}
	if r.Blocked != 2 {
		t.Errorf("blocked %d, want blocked and waiting_on_nelson", r.Blocked)
	}
}

// The header's line carries the count; an effort of one does too.
func TestPace_LineCarriesTheWaitingCount(t *testing.T) {
	in := hoverWaitingInput()
	p := PaceOf(in.Beads, "hov-w.1", in.Panes, in.Rule, in.Now, HeaderWindow)
	if p.Waiting != 4 || !strings.HasSuffix(p.Line(), "· 4 waiting") {
		t.Errorf("waiting %d, line %q", p.Waiting, p.Line())
	}
	alone := []Bead{{ID: "hov-a", Status: "waiting", UpdatedAt: paceNow.Add(-time.Hour)}}
	if got := PaceOf(alone, "hov-a", nil, in.Rule, paceNow, HeaderWindow).Line(); got != "hov-a 0/1 · 0/h · 1 waiting" {
		t.Errorf("an effort of one reads %q", got)
	}
}

// Feed it nothing: no rule, no comments, an owner with no pane here.
func TestWaiting_NothingToCount(t *testing.T) {
	b := Bead{ID: "x", Status: "waiting", Assignee: "eng1", UpdatedAt: paceNow.Add(-5 * time.Hour)}
	if Waiting(b, "idle", Rule{}, paceNow) {
		t.Error("a zero rule counted a waiting bead")
	}
	if Waiting(b, "", testRule, paceNow) {
		t.Error("an owner with no pane here is not known to be idle")
	}
	if got := waitText(nil); got != "no comment" {
		t.Errorf("no comments: %q", got)
	}
}

// A comment cannot drive the reader's terminal: control characters leave the
// wait text, including a C1 such as CSI, and the words around them stay. A raw
// invalid byte cannot reach the terminal either: it comes out as U+FFFD.
func TestWaitText_DropsControlCharacters(t *testing.T) {
	got := waitText([]Comment{{Text: "waits on\x1b[2J the \x07release\u009b31m\rnow\nsecond line", CreatedAt: paceNow}})
	if got != "waits on[2J the release31mnow" {
		t.Errorf("wait text %q", got)
	}
	raw := waitText([]Comment{{Text: "a\x9bb", CreatedAt: paceNow}})
	if strings.Contains(raw, "\x9b") {
		t.Errorf("wait text %q holds the raw byte", raw)
	}
}
