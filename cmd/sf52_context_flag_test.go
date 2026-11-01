package cmd

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-sf5.2: status and patrol print "ctx 91%, compact soon" for a pane the
// TUI flags; status --json is unchanged.

// AC1: the row carries the flag after the state for 91 and 85; nothing at
// 84, with none sent, or for a pane that is not running (AC3).
func TestContextFlag_TheStatusRowShowsIt(t *testing.T) {
	rows := ghyrRows(t, []map[string]any{
		{"name": "eng1", "activity": "running", "alive": true, "visible": true, "context_soon": 91},
		{"name": "eng2", "activity": "idle", "alive": true, "visible": true, "context_soon": 85},
		{"name": "eng3", "activity": "idle", "alive": true, "visible": true, "context_soon": 84},
		{"name": "eng4", "activity": "idle", "alive": true, "visible": true},
		{"name": "eng5", "activity": "idle", "alive": false, "visible": true, "context_soon": 95},
	})
	want := map[string]string{
		"eng1": "yes - running ctx 91%, compact soon",
		"eng2": "yes - idle ctx 85%, compact soon",
		"eng3": "yes - idle",
		"eng4": "yes - idle",
		"eng5": "no - stopped",
	}
	for name, w := range want {
		if got := strings.Join(rows[name][1:], " "); got != w {
			t.Errorf("%s row: %q, want %q", name, got, w)
		}
	}
}

// AC1 for patrol: the header carries the flag after the state.
func TestContextFlag_ThePatrolHeaderShowsIt(t *testing.T) {
	skipWindows(t)
	restoreColor := disableColor(t)
	defer restoreColor()
	restorePatrolFlags := setPatrolFlags(t, 5, false, nil)
	defer restorePatrolFlags()
	sockPath, _ := startPatrolIPCServer(t, tui.IPCResponse{OK: true,
		Data: `[{"name":"eng1","activity":"running","alive":true,"bead":"ini-a","content":"x","context_soon":91},
		        {"name":"eng2","activity":"idle","alive":true,"content":"y","context_soon":84},
		        {"name":"eng3","activity":"idle","alive":true,"content":"z"},
		        {"name":"eng4","activity":"idle","alive":false,"content":"w","context_soon":95}]`})
	t.Setenv("INITECH_SOCKET", sockPath)
	output, err := captureStdout(t, func() error { return runPatrol(&cobra.Command{}, nil) })
	if err != nil {
		t.Fatalf("runPatrol: %v", err)
	}
	for _, w := range []string{"=== eng1 (running, ctx 91%, compact soon | ini-a) ===", "=== eng2 (idle) ===", "=== eng3 (idle) ==="} {
		if !strings.Contains(output, w) {
			t.Errorf("patrol output lacks %q:\n%s", w, output)
		}
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "=== eng4") && strings.Contains(line, "ctx") {
			t.Errorf("a stopped agent is flagged: %q", line)
		}
	}
}

// AC4: status --json is unchanged: no new key, context_percent as before.
func TestContextFlag_StatusJSONIsUnchanged(t *testing.T) {
	stdout, _, _ := l6omStatus(t, `[{"name":"a","activity":"running","alive":true,"context_soon":91,"context_percent":91}]`)
	got := l6omByRole(t, l6omObjects(t, stdout))
	if _, present := got["a"]["context_soon"]; present {
		t.Errorf("status --json gained context_soon: %v", got["a"])
	}
	if got["a"]["context_percent"] != 91.0 {
		t.Errorf("context_percent = %v, want 91", got["a"]["context_percent"])
	}
}
