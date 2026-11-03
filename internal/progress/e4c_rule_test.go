package progress

import (
	"strings"
	"testing"
	"time"
)

// ini-e4c: stuck counts by one function with the rig's value and chain as a
// parameter, not a package constant.

func idleBead(id, status string, age time.Duration) Bead {
	return Bead{ID: id, Status: status, Assignee: "eng1", UpdatedAt: paceNow.Add(-age)}
}

// AC1: the value decides. Twenty minutes quiet is stuck at 15m and not at the
// default hour; exactly the value is stuck.
func TestStuck_TheRulesValueDecides(t *testing.T) {
	b := idleBead("ini-a.1", "in_progress", 20*time.Minute)
	chain := []string{"in_progress"}
	if !Stuck(b, "idle", NewRule(15*time.Minute, chain), paceNow) {
		t.Error("20m quiet is not stuck at 15m")
	}
	if Stuck(b, "idle", NewRule(time.Hour, chain), paceNow) {
		t.Error("20m quiet is stuck at 1h")
	}
	if !Stuck(b, "idle", NewRule(20*time.Minute, chain), paceNow) {
		t.Error("exactly the value is not stuck")
	}
}

// AC3: the chain is the rig's own, an allowlist: a status in it can be stuck,
// one outside it never is, whatever its age and owner.
func TestStuck_TheChainIsAnAllowlist(t *testing.T) {
	rule := NewRule(time.Hour, []string{"in_progress", "review"})
	for status, want := range map[string]bool{
		"in_progress": true, "review": true,
		"in_qa": false, "open": false, "closed": false, "blocked": false, "deferred": false,
		"waiting": false, "waiting_on_nelson": false, "a_status_added_later": false,
	} {
		if got := Stuck(idleBead("x", status, 5*time.Hour), "idle", rule, paceNow); got != want {
			t.Errorf("%s: Stuck = %v, want %v", status, got, want)
		}
	}
}

// AC3: the same beads and rule give the same count and ids from PaceOver,
// PaceOf and the report; the report names the value in effect.
func TestRule_SameCountAndIDsFromPaceAndReport(t *testing.T) {
	beads := []Bead{
		idleBead("ini-a.1", "in_progress", 20*time.Minute),
		idleBead("ini-a.2", "review", 30*time.Minute),
		idleBead("ini-a.3", "in_progress", 5*time.Minute),
		idleBead("ini-a.4", "in_qa", 40*time.Minute), // not in this rig's chain
	}
	rule := NewRule(15*time.Minute, []string{"in_progress", "review"})
	panes := map[string]string{"eng1": "idle"}
	over := PaceOver(beads, "ini-a", panes, rule, paceNow, HeaderWindow)
	of := PaceOf(beads, "ini-a.1", panes, rule, paceNow, HeaderWindow)
	r := Build(Input{Rig: "r", Prefix: "ini-a.", Hours: 6, Now: paceNow, Rule: rule, Beads: beads, Panes: panes, PanesKnown: true})
	const want = "ini-a.1 ini-a.2"
	for name, got := range map[string][]string{"PaceOver": over.StuckIDs, "PaceOf": of.StuckIDs, "report": r.StuckIDs} {
		if strings.Join(got, " ") != want {
			t.Errorf("%s stuck ids %v, want %s", name, got, want)
		}
	}
	if over.Stuck != 2 || of.Stuck != 2 || r.Stuck != 2 {
		t.Errorf("stuck: PaceOver %d, PaceOf %d, report %d; want 2", over.Stuck, of.Stuck, r.Stuck)
	}
	if r.StuckAfter != "15m" {
		t.Errorf("StuckAfter = %q", r.StuckAfter)
	}
	out := RenderReport(r)
	for _, line := range []string{"stuck after 15m", "stuck: ini-a.1 ini-a.2", "quiet 15m or more:"} {
		if !strings.Contains(out, line) {
			t.Errorf("the table lacks %q:\n%s", line, out)
		}
	}
}

func TestRule_AfterTextIsShort(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Hour: "1h", 20 * time.Minute: "20m", 90 * time.Minute: "1h30m", 90 * time.Second: "1m30s", 24 * time.Hour: "24h",
	} {
		if got := (Rule{After: d}).AfterText(); got != want {
			t.Errorf("%v reads %q, want %q", d, got, want)
		}
	}
}

// Feed it nothing: no chain, no owner, no comments, a zero rule.
func TestStuck_NothingToCount(t *testing.T) {
	if Stuck(idleBead("x", "in_progress", time.Hour), "idle", Rule{}, paceNow) {
		t.Error("a zero rule counted a bead")
	}
	noOwner := Bead{ID: "x", Status: "in_progress", UpdatedAt: paceNow.Add(-5 * time.Hour)}
	if Stuck(noOwner, "", testRule, paceNow) {
		t.Error("a bead with no owner is stuck")
	}
	if p := PaceOver(nil, "x", nil, testRule, paceNow, HeaderWindow); p.Stuck != 0 || len(p.StuckIDs) != 0 {
		t.Errorf("no beads: %+v", p)
	}
}
