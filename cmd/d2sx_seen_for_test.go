package cmd

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-d2sx: an age the process tree did not give is labelled "seen for" in
// status and patrol; a process age prints as before; --json keeps the number.

func TestBusyText_AnAgeTheTreeDidNotGiveIsSeenFor(t *testing.T) {
	if got := busyText(&tui.PaneBusy{Shells: 1, SinceSec: 240}); got != "1 shell 4m" {
		t.Errorf("process age: %q", got)
	}
	if got := busyText(&tui.PaneBusy{Shells: 1, Tasks: 1, SinceSec: 240, SeenFor: true}); got != "1 shell, 1 background agent, seen for 4m" {
		t.Errorf("seen for: %q", got)
	}
	if got := busyText(&tui.PaneBusy{SeenFor: true}); got != "" {
		t.Errorf("no work, seen for: %q", got)
	}
	rows := ghyrRows(t, []map[string]any{
		{"name": "eng1", "activity": "background", "alive": true, "visible": true,
			"busy": map[string]any{"shells": 1, "monitors": 0, "tasks": 0, "since_sec": 30, "seen_for": true}},
	})
	if got := strings.Join(rows["eng1"][1:], " "); got != "yes - background, 1 shell, seen for 30s" {
		t.Errorf("row %q", got)
	}
	stdout, _, _ := l6omStatus(t, `[{"name":"a","activity":"background","alive":true,"busy":{"shells":1,"monitors":0,"tasks":0,"since_sec":95,"seen_for":true}}]`)
	if got := l6omByRole(t, l6omObjects(t, stdout)); got["a"]["background_age_sec"] != 95.0 {
		t.Errorf("--json background_age_sec %v, want 95", got["a"]["background_age_sec"])
	}
}
