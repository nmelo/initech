package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-x432.2: status prints "dnd <age>" beside the state word, --json carries
// dnd and dnd_age_sec (null when the TUI did not say), patrol prints the age
// in its header, and deliver and assign print the held line and succeed.

const x432bHeld = "held: eng4 is in do-not-disturb (12m, 2 messages ahead)"

// AC2: the agent in do-not-disturb shows it with its age; the others show
// nothing new.
func TestDNDStatus_TheTableShowsDNDWithItsAge(t *testing.T) {
	rows := ghyrRows(t, []map[string]any{
		{"name": "eng1", "activity": "running", "alive": true, "visible": true, "dnd": true, "dnd_age_sec": 720, "queued_count": 2},
		{"name": "eng2", "activity": "idle", "alive": true, "visible": true, "dnd": false},
		{"name": "eng3", "activity": "idle", "alive": true, "visible": true},
	})
	want := map[string]string{
		"eng1": "yes - running dnd 12m",
		"eng2": "yes - idle",
		"eng3": "yes - idle",
	}
	for name, w := range want {
		if got := strings.Join(rows[name][1:], " "); got != w {
			t.Errorf("%s row: %q, want %q", name, got, w)
		}
	}
}

// AC3: dnd and dnd_age_sec while on; false and null after off; null for both
// from a reply without the fields (an older TUI, or a pane it cannot read);
// queued_count is the TUI's count.
func TestDNDStatusJSON_OnOffAndNotSaid(t *testing.T) {
	stdout, _, _ := l6omStatus(t, `[
	 {"name":"a","activity":"running","alive":true,"dnd":true,"dnd_age_sec":95,"queued_count":2},
	 {"name":"b","activity":"idle","alive":true,"dnd":false},
	 {"name":"c","activity":"idle","alive":true}
	]`)
	got := l6omByRole(t, l6omObjects(t, stdout))
	if got["a"]["dnd"] != true || got["a"]["dnd_age_sec"] != 95.0 || got["a"]["queued_count"] != 2.0 {
		t.Errorf("a: %v", got["a"])
	}
	if got["b"]["dnd"] != false || got["b"]["dnd_age_sec"] != nil {
		t.Errorf("b: dnd %v age %v, want false null", got["b"]["dnd"], got["b"]["dnd_age_sec"])
	}
	for _, k := range []string{"dnd", "dnd_age_sec"} {
		v, present := got["c"][k]
		if !present || v != nil {
			t.Errorf("c %s = %v (present %v), want null, printed", k, v, present)
		}
	}
}

// AC2 for patrol: the header carries "dnd <age>" after the state word.
func TestDNDStatus_PatrolHeaderShowsIt(t *testing.T) {
	skipWindows(t)
	restoreColor := disableColor(t)
	defer restoreColor()
	restorePatrolFlags := setPatrolFlags(t, 5, false, nil)
	defer restorePatrolFlags()
	sockPath, _ := startPatrolIPCServer(t, tui.IPCResponse{OK: true,
		Data: `[{"name":"eng1","activity":"running","alive":true,"bead":"ini-a","content":"x","dnd_age_sec":240},
		        {"name":"eng2","activity":"idle","alive":true,"content":"y"}]`})
	t.Setenv("INITECH_SOCKET", sockPath)
	output, err := captureStdout(t, func() error { return runPatrol(&cobra.Command{}, nil) })
	if err != nil {
		t.Fatalf("runPatrol: %v", err)
	}
	for _, w := range []string{"=== eng1 (running, dnd 4m | ini-a) ===", "=== eng2 (idle) ==="} {
		if !strings.Contains(output, w) {
			t.Errorf("patrol output lacks %q:\n%s", w, output)
		}
	}
}

// AC4: a hand-off report held for do-not-disturb is a success: deliver prints
// the held line, exits 0, and the bead moves.
func TestDNDStatus_DeliverPrintsHeldAndMoves(t *testing.T) {
	skipWindows(t)
	sock := i35wActionIPC(t, map[string]tui.IPCResponse{"send": {OK: true, Delivery: tui.DeliveryHeld, Data: x432bHeld}})
	r := x0dDeliver(t, sock, "eng4")
	if r.err != nil {
		t.Fatalf("deliver failed on a held report: %v", r.err)
	}
	if len(r.writes) == 0 || r.writes[0].Status != "ready_for_qa" {
		t.Errorf("bd writes %+v, want the move to ready_for_qa", r.writes)
	}
	if !strings.Contains(r.stderr, x432bHeld) {
		t.Errorf("stderr lacks the held line:\n%s", r.stderr)
	}
	if strings.Contains(r.stderr, "NOT sent") {
		t.Errorf("held read as not sent:\n%s", r.stderr)
	}
}

// AC4 for assign: the dispatch held is a success, the held line printed.
func TestDNDStatus_AssignPrintsHeld(t *testing.T) {
	skipWindows(t)
	stubBdFns(t)
	resetAssignFlags(t)
	assignTitles(func(id string) (string, error) { return "Fix the bug", nil })
	dispatched := 0
	bdDispatchFn = func(id, agent, status string, recordImplementer bool) error {
		dispatched++
		return nil
	}
	sock := i35wActionIPC(t, map[string]tui.IPCResponse{"send": {OK: true, Delivery: tui.DeliveryHeld, Data: x432bHeld}})
	t.Setenv("INITECH_SOCKET", sock)
	errBuf := &bytes.Buffer{}
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"assign", "eng4", "ini-77ys"})
	defer rootCmd.SetArgs(nil)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("assign failed on a held dispatch: %v", err)
	}
	if dispatched != 1 || !strings.Contains(errBuf.String(), x432bHeld) {
		t.Errorf("dispatched %d, stderr:\n%s", dispatched, errBuf.String())
	}
}
