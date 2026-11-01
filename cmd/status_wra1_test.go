package cmd

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-wra.1: status --json keeps its fields; a pane whose bead the TUI could
// not refresh from bd says so in bead_note, and the no-bead marker still wins.
func TestStatusJSON_BeadNoteCarriesTheStaleMark(t *testing.T) {
	beads := []string{"ini-x"}
	cases := []struct {
		name string
		pi   paneInfo
		want string
	}{
		{"fresh", paneInfo{Name: "eng1", Beads: &beads}, ""},
		{"stale", paneInfo{Name: "eng1", Beads: &beads, BeadStale: true}, tui.BeadStaleNote},
		{"no-bead marker wins", paneInfo{Name: "eng1", NoBead: "fix the flake", BeadStale: true}, "fix the flake"},
	}
	for _, c := range cases {
		if got := toAgentJSON(c.pi, nil).BeadNote; got != c.want {
			t.Errorf("%s: bead_note %q, want %q", c.name, got, c.want)
		}
	}
}

// AC6: the list reply's bead_stale is read, and status --json's key set is
// the same as before (no new key, beads and bead_note in place).
func TestStatusJSON_KeySetUnchangedByTheStaleMark(t *testing.T) {
	var pi paneInfo
	if err := json.Unmarshal([]byte(`{"name":"eng1","beads":["ini-x"],"bead_stale":true}`), &pi); err != nil {
		t.Fatal(err)
	}
	if !pi.BeadStale {
		t.Fatal("bead_stale in the list reply was not read")
	}
	raw, err := json.Marshal(toAgentJSON(pi, nil))
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	json.Unmarshal(raw, &obj)
	var keys []string
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"account", "alive", "background", "background_age_sec", "banner", "banner_age_sec", "bead_note", "beads", "context_percent", "dnd", "dnd_age_sec", "host", "queued_count", "role", "state", "visible"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("status --json keys %v, want %v", keys, want)
	}
}
