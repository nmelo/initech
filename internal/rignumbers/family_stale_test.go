package rignumbers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ini-ylx9: the rig numbers' "no move" count judges a parent by its family's
// newest move, read from bd's list (its parent field).
func TestFamilyStale_GatheredNumbersDoNotCountAMovingFamily(t *testing.T) {
	at := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339) }
	raw, _ := json.Marshal([]map[string]any{
		{"id": "ini-e", "status": "in_progress", "updated_at": at(30 * time.Minute)},
		{"id": "ini-e.1", "status": "in_progress", "parent": "ini-e", "updated_at": at(5 * time.Minute)},
		{"id": "ini-q", "status": "in_progress", "updated_at": at(30 * time.Minute)}, // the twin: quiet, no family
	})
	run := func(context.Context, string, ...string) ([]byte, error) { return raw, nil }
	in := Input{Now: now}
	if err := GatherBeads(context.Background(), run, &in); err != nil {
		t.Fatal(err)
	}
	intIs(t, "no move", Compute(in).NoMove20m, 1)
}

// The rig's beads.stuck_after is the window: 2h counts only the 3-hour bead,
// and the line names it; zero keeps NoMoveAfter (20m) and its line.
func TestFamilyStale_TheWindowIsTheRigsStuckAfter(t *testing.T) {
	in := Input{Now: now, BeadsKnown: true, StaleAfter: 2 * time.Hour, Beads: []Bead{
		{ID: "ini-a", Status: "in_progress", LastMoved: now.Add(-30 * time.Minute)},
		{ID: "ini-b", Status: "in_progress", LastMoved: now.Add(-3 * time.Hour)},
	}}
	n := Compute(in)
	intIs(t, "no move", n.NoMove20m, 1)
	if !strings.Contains(n.Line(), "no move 2h 1") {
		t.Errorf("line %q, want no move 2h 1", n.Line())
	}
	in.StaleAfter = 0
	n = Compute(in)
	intIs(t, "no move", n.NoMove20m, 2)
	if !strings.Contains(n.Line(), "no move 20m 2") {
		t.Errorf("line %q, want no move 20m 2", n.Line())
	}
}
