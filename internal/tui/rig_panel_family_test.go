package tui

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
	"gopkg.in/yaml.v3"
)

// ini-ylx9: the rig panel's stale list judges a parent by its family's newest
// move, never lists a side state, and counts by the rig's beads.stuck_after.

var famPanelNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// panelStale is the panel's stale alarm, label and names, from beads as bd's
// list JSON gives them (parent included).
func panelStale(t *testing.T, tu *TUI, beads ...map[string]any) (string, []string) {
	t.Helper()
	raw, err := json.Marshal(beads)
	if err != nil {
		t.Fatal(err)
	}
	var bd []bdBead
	if err := json.Unmarshal(raw, &bd); err != nil {
		t.Fatal(err)
	}
	tu.paceBeads, tu.paceAnswered = bd, true
	agents, pb := tu.panelSnapshot(famPanelNow)
	for _, a := range panelTitleOf(pb, agents, famPanelNow, tu.staleWindow()).Alarms {
		if len(a.Label) >= 5 && a.Label[:5] == "stale" {
			return a.Label, a.Names
		}
	}
	t.Fatal("no stale alarm on the panel")
	return "", nil
}

func famPanelBead(id, status, parent string, ago time.Duration) map[string]any {
	b := map[string]any{"id": id, "status": status, "updated_at": famPanelNow.Add(-ago).Format(time.RFC3339)}
	if parent != "" {
		b["parent"] = parent
	}
	return b
}

// AC1, AC2, AC4 and AC3 on the panel: a moving child or grandchild keeps its
// epic off; a quiet family is listed; side states never are.
func TestPanelFamily_StaleListsOnlyQuietFamilies(t *testing.T) {
	label, names := panelStale(t, newTestTUI(),
		famPanelBead("ini-e", "in_progress", "", 30*time.Minute),
		famPanelBead("ini-e.1", "in_progress", "ini-e", 5*time.Minute),
		famPanelBead("ini-g", "in_progress", "", 30*time.Minute),
		famPanelBead("ini-g.1", "open", "ini-g", 30*time.Minute),
		famPanelBead("ini-g.1.1", "in_progress", "ini-g.1", 5*time.Minute),
		famPanelBead("ini-q", "in_progress", "", 30*time.Minute),
		famPanelBead("ini-q.1", "closed", "ini-q", 40*time.Minute),
		famPanelBead("ini-w", "waiting_for_release", "", 3*time.Hour),
		famPanelBead("ini-x", "waiting", "", 3*time.Hour),
		famPanelBead("ini-d", "deferred", "", 3*time.Hour),
	)
	slices.Sort(names)
	if label != "stale 20m" || !slices.Equal(names, []string{"ini-q"}) {
		t.Errorf("%s %v, want stale 20m [ini-q]", label, names)
	}
}

// AC5: with beads.stuck_after: 2h the panel's label reads "stale 2h" and
// lists by 2 hours; unset, it reads and counts as today (stale 20m, above).
func TestPanelFamily_TheWindowIsTheRigsStuckAfter(t *testing.T) {
	tu := newTestTUI()
	tu.project = &config.Project{}
	if err := yaml.Unmarshal([]byte("stuck_after: 2h\n"), &tu.project.Beads); err != nil {
		t.Fatal(err)
	}
	label, names := panelStale(t, tu,
		famPanelBead("ini-a", "in_progress", "", 30*time.Minute),
		famPanelBead("ini-b", "in_progress", "", 3*time.Hour),
	)
	if label != "stale 2h" || !slices.Equal(names, []string{"ini-b"}) {
		t.Errorf("%s %v, want stale 2h [ini-b]", label, names)
	}
}
