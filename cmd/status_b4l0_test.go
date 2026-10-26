package cmd

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-b4l0: an agent whose footer shows background work reads busy after its
// state word, in the status table and the patrol header. The state word
// itself does not change.

func TestBusyText_CountsKindsAndAge(t *testing.T) {
	cases := []struct {
		b    tui.PaneBusy
		want string
	}{
		{tui.PaneBusy{Shells: 1, SinceSec: 90}, "1 background shell 1m"},
		{tui.PaneBusy{Shells: 2, Monitors: 1, SinceSec: 30}, "2 background shells, 1 monitor 30s"},
		{tui.PaneBusy{Tasks: 1, SinceSec: 3700}, "1 background agent 1h"},
		{tui.PaneBusy{Shells: 3, Monitors: 2, Tasks: 2, SinceSec: 300}, "3 background shells, 2 monitors, 2 background agents 5m"},
	}
	for _, c := range cases {
		if got := busyText(&c.b); got != c.want {
			t.Errorf("%+v: %q, want %q", c.b, got, c.want)
		}
	}
	if got := busyText(nil); got != "" {
		t.Errorf("nil: %q, want empty", got)
	}
}

// Criterion: the Status column reads "idle, 1 background shell 4m"; an agent
// that is not alive, or has no work, reads as before.
func TestStatus_ABusyAgentReadsItsBackgroundWork(t *testing.T) {
	rows := ghyrRows(t, []map[string]any{
		{"name": "eng1", "activity": "idle", "alive": true, "visible": true,
			"busy": map[string]any{"shells": 1, "monitors": 0, "tasks": 0, "since_sec": 240}},
		{"name": "eng2", "activity": "running", "alive": true, "visible": true,
			"busy": map[string]any{"shells": 0, "monitors": 1, "tasks": 1, "since_sec": 12}},
		{"name": "eng3", "activity": "suspended", "alive": false, "visible": true,
			"busy": map[string]any{"shells": 1, "monitors": 0, "tasks": 0, "since_sec": 5}},
		{"name": "eng4", "activity": "idle", "alive": true, "visible": true},
	})
	want := map[string]string{
		"eng1": "yes - idle, 1 background shell 4m",
		"eng2": "yes - running, 1 monitor, 1 background agent 12s",
		"eng3": "no - suspended",
		"eng4": "yes - idle",
	}
	for name, w := range want {
		if got := strings.Join(rows[name][1:], " "); got != w {
			t.Errorf("%s row: %q, want %q", name, got, w)
		}
	}
}

// The patrol header says it too, and --active keeps a busy agent whose
// screen is quiet.
func TestPatrol_ABusyAgentReadsItsBackgroundWork(t *testing.T) {
	skipWindows(t)
	restoreColor := disableColor(t)
	defer restoreColor()
	restorePatrolFlags := setPatrolFlags(t, 5, true, nil)
	defer restorePatrolFlags()

	resp := tui.IPCResponse{
		OK: true,
		Data: `[{"name":"eng1","activity":"idle","alive":true,"content":"","busy":{"shells":1,"monitors":0,"tasks":0,"since_sec":240}},` +
			`{"name":"eng2","activity":"idle","alive":true,"content":""}]`,
	}
	sockPath, _ := startPatrolIPCServer(t, resp)
	t.Setenv("INITECH_SOCKET", sockPath)
	output, err := captureStdout(t, func() error { return runPatrol(&cobra.Command{}, nil) })
	if err != nil {
		t.Fatalf("runPatrol: %v", err)
	}
	if !strings.Contains(output, "=== eng1 (idle, 1 background shell 4m) ===") {
		t.Errorf("patrol header missing the work:\n%s", output)
	}
	if strings.Contains(output, "eng2") {
		t.Errorf("--active kept a quiet idle agent with no work:\n%s", output)
	}
}
