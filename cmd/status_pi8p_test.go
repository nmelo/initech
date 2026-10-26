package cmd

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-pi8p: background is a state. status and patrol print it with the work
// and its age; --json carries it with background_age_sec; and an initech
// that does not know a state word prints it as it came, which is what lets an
// older status read a newer TUI.

func TestStatus_BackgroundIsAState(t *testing.T) {
	rows := ghyrRows(t, []map[string]any{
		{"name": "eng1", "activity": "background", "alive": true, "visible": true,
			"busy": map[string]any{"shells": 2, "monitors": 1, "tasks": 0, "since_sec": 30}},
		{"name": "eng2", "activity": "running", "alive": true, "visible": true,
			"busy": map[string]any{"shells": 1, "monitors": 0, "tasks": 0, "since_sec": 240}},
		{"name": "eng3", "activity": "idle", "alive": true, "visible": true},
	})
	want := map[string]string{
		"eng1": "yes - background, 2 shells, 1 monitor 30s",
		"eng2": "yes - running, 1 shell 4m",
		"eng3": "yes - idle",
	}
	for name, w := range want {
		if got := strings.Join(rows[name][1:], " "); got != w {
			t.Errorf("%s row: %q, want %q", name, got, w)
		}
	}
}

func TestStatusJSON_BackgroundStateAndAge(t *testing.T) {
	stdout, _, _ := l6omStatus(t, `[
	 {"name":"a","activity":"background","alive":true,"busy":{"shells":1,"monitors":0,"tasks":0,"since_sec":95}},
	 {"name":"b","activity":"running","alive":true,"busy":{"shells":1,"monitors":0,"tasks":0,"since_sec":95}},
	 {"name":"c","activity":"idle","alive":true}
	]`)
	got := l6omByRole(t, l6omObjects(t, stdout))
	if got["a"]["state"] != "background" || got["a"]["background_age_sec"] != 95.0 {
		t.Errorf("a: state %v age %v, want background 95", got["a"]["state"], got["a"]["background_age_sec"])
	}
	// The age is the background state's: running with a shell has none.
	if got["b"]["state"] != "running" || got["b"]["background_age_sec"] != nil {
		t.Errorf("b: state %v age %v, want running null", got["b"]["state"], got["b"]["background_age_sec"])
	}
	if got["c"]["background_age_sec"] != nil {
		t.Errorf("c: age %v, want null", got["c"]["background_age_sec"])
	}
}

// The guard older clients rely on: a state word the client does not know is
// printed as it came, in the table and the patrol header, uncoloured.
func TestStatus_AnUnknownStateWordPrintsAsItCame(t *testing.T) {
	rows := ghyrRows(t, []map[string]any{{"name": "eng1", "activity": "frobbing", "alive": true, "visible": true}})
	if got := strings.Join(rows["eng1"][1:], " "); got != "yes - frobbing" {
		t.Errorf("table row %q, want the word as it came", got)
	}
	if got := statusColor("frobbing"); got != "frobbing" {
		t.Errorf("statusColor = %q, want it uncoloured", got)
	}

	skipWindows(t)
	restoreColor := disableColor(t)
	defer restoreColor()
	restorePatrolFlags := setPatrolFlags(t, 5, false, nil)
	defer restorePatrolFlags()
	sockPath, _ := startPatrolIPCServer(t, tui.IPCResponse{OK: true,
		Data: `[{"name":"eng1","activity":"frobbing","alive":true,"content":"x"}]`})
	t.Setenv("INITECH_SOCKET", sockPath)
	output, err := captureStdout(t, func() error { return runPatrol(&cobra.Command{}, nil) })
	if err != nil {
		t.Fatalf("runPatrol: %v", err)
	}
	if !strings.Contains(output, "=== eng1 (frobbing) ===") {
		t.Errorf("patrol header missing the word:\n%s", output)
	}
}
