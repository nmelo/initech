package appserve

import (
	"os"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/progress"
)

func super(path string) PaneState {
	return PaneState{Name: "super", State: "idle", Banner: "none", SessionFile: path}
}

func replyLine(uuid, text string) string {
	return tline("assistant", uuid, t0, []map[string]any{{"type": "text", "text": text}})
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}



// AC11: the beads of the latest progress run reach Blockers: a bead quiet an
// hour with its owner idle is stuck on that owner's pane.
func TestBlockersSnapshot_CarriesTheProgressRunsBeads(t *testing.T) {
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	b := &progressBackend{efforts: map[string][]string{"/r/a": {"ini-x."}}}
	b.rigs = []Rig{{Name: "a", Root: "/r/a"}}
	b.panes = map[string][]PaneState{"/r/a": {{Name: "eng1", State: "idle", Banner: "none"}}}
	s := New(Config{Backend: b, Digest: shaToken(), HasToken: true, Now: clk.Now})
	s.Poll()
	s.mu.Lock()
	s.progress = []progress.Report{{Rig: "a", Prefix: "ini-x.", Beads: []progress.Row{
		{ID: "ini-x.1", Stage: "in_progress", Owner: "eng1", LastMove: clk.Now().Add(-2 * time.Hour)},
		{ID: "ini-x.2", Stage: "in_progress", Owner: "-", LastMove: clk.Now().Add(-2 * time.Hour)},
	}}}
	s.mu.Unlock()
	snap := s.snapshot()
	if len(snap.Rigs) != 1 || len(snap.Rigs[0].Beads) != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if got := snap.Rigs[0].Beads[0]; got.ID != "ini-x.1" || got.Status != "in_progress" || got.Assignee != "eng1" || got.LastMove.IsZero() {
		t.Errorf("bead = %+v", got)
	}
	if got := snap.Rigs[0].Beads[1].Assignee; got != "" {
		t.Errorf("no owner reads %q, want empty", got)
	}
	list := Blockers(snap)
	if len(list[0].Stuck) != 1 || list[0].Stuck[0].Pane != "eng1" {
		t.Errorf("stuck = %+v, want eng1 for the quiet bead", list[0].Stuck)
	}
}

