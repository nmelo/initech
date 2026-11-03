package progress

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ini-s3b: a bead in blocked or waiting_on_nelson is counted blocked and
// never stuck; a deferred bead is left out of everything; one counting
// function for the pane header, the overlay row and initech progress.

// s3bFixture is AC1's effort: two closed in the window, one in progress and
// fresh, one in progress quiet an hour with an idle owner (stuck), one
// blocked quiet an hour with an idle owner (blocked, not stuck), one
// deferred; and one waiting on Nelson, quiet an hour.
func s3bFixture() []Bead {
	q := paceNow.Add(-90 * time.Minute)
	return []Bead{
		{ID: "ini-x.1", Status: "closed", ClosedAt: paceNow.Add(-time.Hour)},
		{ID: "ini-x.2", Status: "closed", ClosedAt: paceNow.Add(-2 * time.Hour)},
		{ID: "ini-x.3", Status: "in_progress", Assignee: "eng1", UpdatedAt: paceNow.Add(-5 * time.Minute)},
		{ID: "ini-x.4", Status: "in_progress", Assignee: "eng2", UpdatedAt: q},
		{ID: "ini-x.5", Status: "blocked", Assignee: "eng3", UpdatedAt: q},
		{ID: "ini-x.6", Status: "deferred", Assignee: "eng4", UpdatedAt: q, ClosedAt: paceNow.Add(-time.Hour)},
	}
}

var s3bPanes = map[string]string{"eng1": "running", "eng2": "idle", "eng3": "idle", "eng4": "idle", "eng5": "idle"}

func withNelson(b []Bead) []Bead {
	return append(b, Bead{ID: "ini-x.7", Status: "waiting_on_nelson", Assignee: "eng5", UpdatedAt: paceNow.Add(-2 * time.Hour)})
}

// AC1: closed 2, total 5, stuck 1, blocked 1; a bead waiting on Nelson adds
// to the total and to blocked, never to stuck.
func TestBlocked_CountedApartFromStuck(t *testing.T) {
	p := PaceOf(s3bFixture(), "ini-x.3", s3bPanes, testRule, paceNow, HeaderWindow)
	if p.Closed != 2 || p.Total != 5 || p.Stuck != 1 || p.Blocked != 1 {
		t.Errorf("pace %+v, want closed 2 total 5 stuck 1 blocked 1", p)
	}
	if got, want := p.Line(), "ini-x 2/5 · 0.3/h · 1 stuck · 1 blocked"; got != want {
		t.Errorf("line %q, want %q", got, want)
	}
	n := PaceOf(withNelson(s3bFixture()), "ini-x.3", s3bPanes, testRule, paceNow, HeaderWindow)
	if n.Total != 6 || n.Blocked != 2 || n.Stuck != 1 {
		t.Errorf("with a bead waiting on Nelson: %+v, want total 6 blocked 2 stuck 1", n)
	}
	for _, b := range withNelson(s3bFixture())[4:] {
		if Stuck(b, "idle", testRule, paceNow) {
			t.Errorf("%s (%s) reads stuck", b.ID, b.Status)
		}
	}
}

// AC2: "· N blocked" only above zero; today's line otherwise; a blocked
// single bead.
func TestBlocked_TheLine(t *testing.T) {
	if got := PaceOf(paceFleet(), "ini-wra.2", map[string]string{"eng4": "running", "eng1": "running"}, testRule, paceNow, HeaderWindow).Line(); got != "ini-wra 2/4 · 0.2/h · 0 stuck" {
		t.Errorf("no blocked bead: %q, want today's line", got)
	}
	alone := []Bead{{ID: "ini-abc", Status: "in_progress"}}
	if got := PaceOf(alone, "ini-abc", nil, testRule, paceNow, HeaderWindow).Line(); got != "ini-abc 0/1 · 0/h" {
		t.Errorf("a lone bead: %q", got)
	}
	alone[0].Status = "blocked"
	if got := PaceOf(alone, "ini-abc", nil, testRule, paceNow, HeaderWindow).Line(); got != "ini-abc 0/1 · 0/h · 1 blocked" {
		t.Errorf("a lone blocked bead: %q", got)
	}
}

// AC4: every bead blocked; every bead deferred; a status the model does not
// know counts as today.
func TestBlocked_NothingButBlockedOrDeferred(t *testing.T) {
	q := paceNow.Add(-2 * time.Hour)
	all := []Bead{{ID: "ini-y.1", Status: "blocked", Assignee: "eng1", UpdatedAt: q}, {ID: "ini-y.2", Status: "waiting_on_nelson", Assignee: "eng1", UpdatedAt: q}}
	if got := PaceOf(all, "ini-y.1", map[string]string{"eng1": "idle"}, testRule, paceNow, HeaderWindow).Line(); got != "ini-y 0/2 · 0/h · 0 stuck · 2 blocked" {
		t.Errorf("every bead blocked: %q", got)
	}
	parked := []Bead{{ID: "ini-z.1", Status: "deferred"}, {ID: "ini-z.2", Status: "deferred"}}
	if got := PaceOf(parked, "ini-z.1", nil, testRule, paceNow, HeaderWindow).Line(); got != "" {
		t.Errorf("every bead deferred: %q, want no line", got)
	}
	odd := []Bead{{ID: "ini-u.1", Status: "hooked", Assignee: "eng1", UpdatedAt: q}, {ID: "ini-u.2", Status: "closed", ClosedAt: q}}
	if got := PaceOf(odd, "ini-u.1", map[string]string{"eng1": "idle"}, testRule, paceNow, HeaderWindow).Line(); got != "ini-u 1/2 · 0.2/h · 0 stuck" {
		t.Errorf("a status outside the working chain: %q, want it never stuck", got)
	}
}

func s3bInput(beads []Bead) Input {
	return Input{Rig: "r", Prefix: "ini-x.", Hours: 6, Now: paceNow, Rule: testRule, Beads: beads, Panes: s3bPanes, PanesKnown: true}
}

// AC3: the report counts stuck and blocked beside closed of total, from the
// same function as the pace; blocked rows read blocked and blocked (Nelson);
// the deferred bead is nowhere.
func TestBlocked_TheProgressReport(t *testing.T) {
	in := s3bInput(withNelson(s3bFixture()))
	r := Build(in)
	p := PaceOver(in.Beads, "ini-x", s3bPanes, testRule, paceNow, 6*time.Hour)
	if r.Closed != p.Closed || r.Total != p.Total || r.Stuck != p.Stuck || r.Blocked != p.Blocked {
		t.Errorf("report closed %d total %d stuck %d blocked %d, want the pace's %+v", r.Closed, r.Total, r.Stuck, r.Blocked, p)
	}
	if r.Stuck != 1 || r.Blocked != 2 || r.Total != 6 {
		t.Errorf("report stuck %d blocked %d total %d, want 1, 2, 6", r.Stuck, r.Blocked, r.Total)
	}
	out := RenderReport(r)
	if !strings.Contains(out, "closed 2 of 6: worked by engineers 0, writing/review only 2; stuck 1, blocked 2") {
		t.Errorf("the closed line:\n%s", out)
	}
	rows := map[string]string{}
	for _, ln := range strings.Split(out, "\n") {
		// The table's row, not the quiet section's line for the same bead.
		if f := strings.Fields(ln); len(f) > 1 && strings.HasPrefix(f[0], "ini-x.") && rows[f[0]] == "" {
			rows[f[0]] = ln
		}
	}
	if !strings.Contains(rows["ini-x.5"], " blocked ") || !strings.Contains(rows["ini-x.7"], " blocked (Nelson) ") {
		t.Errorf("blocked rows: %q / %q", rows["ini-x.5"], rows["ini-x.7"])
	}
	if strings.Contains(out, "ini-x.6") || strings.Contains(out, "deferred") {
		t.Errorf("the deferred bead is in the report:\n%s", out)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)
	for _, want := range []string{`"stuck":1`, `"blocked":2`, `"stage":"waiting_on_nelson"`, `"stage":"blocked"`} {
		if !strings.Contains(js, want) {
			t.Errorf("--json has no %s:\n%s", want, js)
		}
	}
	if strings.Contains(js, "ini-x.6") {
		t.Errorf("--json carries the deferred bead")
	}
}

// AC4: every bead deferred leaves no bead in flight.
func TestBlocked_EveryBeadDeferredIsNoBeadInFlight(t *testing.T) {
	r := Build(s3bInput([]Bead{{ID: "ini-x.1", Status: "deferred"}, {ID: "ini-x.2", Status: "deferred"}}))
	if r.Total != 0 || len(r.Beads) != 0 || r.Stages["deferred"] != 0 {
		t.Errorf("every bead deferred: %+v", r)
	}
	if out := RenderReport(r); !strings.Contains(out, "no bead in flight") {
		t.Errorf("want no bead in flight:\n%s", out)
	}
}
