package progress

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// ini-y0n.2: the pane header's pace, from the effort model.

var paceNow = time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)

// testRule is the default value over this rig's working chain.
var testRule = NewRule(time.Hour, []string{"in_progress", "ready_for_qa", "in_qa", "qa_passed"})

// paceFleet is the fixture fleet: the ini-wra effort (epic plus four steps:
// two closed, one inside the six-hour window; two in progress), and a bead of
// another effort.
func paceFleet() []Bead {
	return []Bead{
		{ID: "ini-wra", Status: "open"},
		{ID: "ini-wra.1", Status: "closed", ClosedAt: paceNow.Add(-2 * time.Hour)},
		{ID: "ini-wra.2", Status: "in_progress", Assignee: "eng4", UpdatedAt: paceNow.Add(-10 * time.Minute)},
		{ID: "ini-wra.3", Status: "closed", ClosedAt: paceNow.Add(-8 * time.Hour)},
		{ID: "ini-wra.4", Status: "ready_for_qa", Assignee: "eng1", UpdatedAt: paceNow.Add(-2 * time.Hour)},
		{ID: "ini-zzz.1", Status: "closed", ClosedAt: paceNow.Add(-time.Hour)},
	}
}

// AC1: closed of total, per hour over the window, stuck, for the effort the
// bead belongs to and only that effort.
func TestPace_TheEffortLine(t *testing.T) {
	p := PaceOf(paceFleet(), "ini-wra.2", map[string]string{"eng4": "running", "eng1": "running"}, testRule, paceNow, HeaderWindow)
	if got, want := p.Line(), "ini-wra 2/4 · 0.2/h · 0 stuck"; got != want {
		t.Errorf("line %q, want %q", got, want)
	}
	six := append(paceFleet(),
		Bead{ID: "ini-wra.5", Status: "closed", ClosedAt: paceNow.Add(-time.Hour)},
		Bead{ID: "ini-wra.6", Status: "closed", ClosedAt: paceNow.Add(-3 * time.Hour)},
		Bead{ID: "ini-wra.7", Status: "closed", ClosedAt: paceNow.Add(-4 * time.Hour)},
		Bead{ID: "ini-wra.8", Status: "closed", ClosedAt: paceNow.Add(-5 * time.Hour)},
		Bead{ID: "ini-wra.9", Status: "closed", ClosedAt: paceNow.Add(-5*time.Hour - 59*time.Minute)},
	)
	if got := PaceOf(six, "ini-wra.2", nil, testRule, paceNow, HeaderWindow).Line(); got != "ini-wra 7/9 · 1/h · 0 stuck" {
		t.Errorf("six closed in six hours: %q, want 1/h", got)
	}
}

// AC2: an effort of one bead (no ".", or no siblings) has the short form.
func TestPace_ABeadOnItsOwn(t *testing.T) {
	alone := append(paceFleet(), Bead{ID: "ini-abc", Status: "in_progress"})
	if got := PaceOf(alone, "ini-abc", nil, testRule, paceNow, HeaderWindow).Line(); got != "ini-abc 0/1 · 0/h" {
		t.Errorf("a bead with no siblings: %q", got)
	}
	if got := PaceOf(paceFleet(), "ini-unknown.1", nil, testRule, paceNow, HeaderWindow).Line(); got != "" {
		t.Errorf("a bead bd does not list reads %q, want no line", got)
	}
	beads := []Bead{{ID: "ini-q.1", Status: "in_progress"}, {ID: "ini-qq.1", Status: "closed"}}
	if got := PaceOf(beads, "ini-q.1", nil, testRule, paceNow, HeaderWindow).Line(); got != "ini-q 0/1 · 0/h" {
		t.Errorf("a lone step: %q (ini-qq.1 is another effort)", got)
	}
	if pre, label := Effort("ini-abc"); pre != "ini-abc" || label != "ini-abc" {
		t.Errorf("a bead with no dot is its own effort: %q/%q", pre, label)
	}
	if pre, label := Effort("ini-wra.2.1"); pre != "ini-wra.2." || label != "ini-wra.2" {
		t.Errorf("a nested step's effort is %q/%q, want its parent's", pre, label)
	}
}

// AC3: a bead quiet an hour counts as stuck only when its owner's pane is
// idle, waiting or pending; running, background or no pane never.
func TestPace_StuckReadsTheOwnersPane(t *testing.T) {
	for state, want := range map[string]int{"idle": 1, "waiting": 1, "pending": 1, "running": 0, "background": 0, "": 0, "suspended": 0} {
		p := PaceOf(paceFleet(), "ini-wra.2", map[string]string{"eng1": state, "eng4": "idle"}, testRule, paceNow, HeaderWindow)
		if p.Stuck != want {
			t.Errorf("owner %q: stuck %d, want %d", state, p.Stuck, want)
		}
	}
	notStarted := append(paceFleet(), Bead{ID: "ini-wra.5", Status: "open", Assignee: "eng7", UpdatedAt: paceNow.Add(-5 * time.Hour)})
	if p := PaceOf(notStarted, "ini-wra.2", map[string]string{"eng7": "idle"}, testRule, paceNow, HeaderWindow); p.Stuck != 0 {
		t.Errorf("an open bead, not started, reads stuck")
	}
	beads := paceFleet()
	beads[4].UpdatedAt = paceNow.Add(-59 * time.Minute)
	if p := PaceOf(beads, "ini-wra.2", map[string]string{"eng1": "idle"}, testRule, paceNow, HeaderWindow); p.Stuck != 0 {
		t.Errorf("a bead moved 59 minutes ago reads stuck")
	}
}

// AC6: no line holds a time, an estimate, an actual or a finish.
func TestPace_TheLineHoldsNoTimeOrEstimate(t *testing.T) {
	clock := regexp.MustCompile(`\d:\d|\d{4}-\d\d`)
	for _, id := range []string{"ini-wra.2", "ini-abc", "ini-zzz.1"} {
		l := PaceOf(paceFleet(), id, map[string]string{"eng1": "idle"}, testRule, paceNow, HeaderWindow).Line()
		low := strings.ToLower(l)
		for _, word := range []string{"est", "actual", "finish", "eta", "min", "done by", "left"} {
			if strings.Contains(low, word) {
				t.Errorf("line %q holds %q", l, word)
			}
		}
		if clock.MatchString(l) {
			t.Errorf("line %q holds a time", l)
		}
	}
}
