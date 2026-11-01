package cmd

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-b5w: a pane whose input box holds unsent text is pending input. The TUI
// reports "pending"; status and patrol print "pending input"; --json keeps
// "pending"; a delivered send that leaves other text in the box says so.

func TestStatus_PendingInputIsAState(t *testing.T) {
	rows := ghyrRows(t, []map[string]any{
		{"name": "eng1", "activity": "pending", "alive": true, "visible": true},
		{"name": "eng2", "activity": "idle", "alive": true, "visible": true},
	})
	for name, w := range map[string]string{"eng1": "yes - pending input", "eng2": "yes - idle"} {
		if got := strings.Join(rows[name][1:], " "); got != w {
			t.Errorf("%s row: %q, want %q", name, got, w)
		}
	}
	// Not dimmed like idle: yellow, like other states that need a look.
	if got, idle := statusColor("pending input"), statusColor("idle"); got == idle || !strings.Contains(got, "pending input") {
		t.Errorf("statusColor(pending input) = %q, idle %q", got, idle)
	}
}

func TestStatusJSON_PendingState(t *testing.T) {
	stdout, _, _ := l6omStatus(t, `[
	 {"name":"a","activity":"pending","alive":true},
	 {"name":"b","activity":"pending","alive":false}
	]`)
	got := l6omByRole(t, l6omObjects(t, stdout))
	if got["a"]["state"] != "pending" {
		t.Errorf("a: state %v, want pending", got["a"]["state"])
	}
	if got["b"]["state"] != "stopped" {
		t.Errorf("b (not alive): state %v, want stopped", got["b"]["state"])
	}
}

func TestPatrol_PendingInputHeader(t *testing.T) {
	skipWindows(t)
	restoreColor := disableColor(t)
	defer restoreColor()
	restorePatrolFlags := setPatrolFlags(t, 5, false, nil)
	defer restorePatrolFlags()
	sockPath, _ := startPatrolIPCServer(t, tui.IPCResponse{OK: true,
		Data: `[{"name":"eng1","activity":"pending","alive":true,"content":"x"}]`})
	t.Setenv("INITECH_SOCKET", sockPath)
	output, err := captureStdout(t, func() error { return runPatrol(&cobra.Command{}, nil) })
	if err != nil {
		t.Fatalf("runPatrol: %v", err)
	}
	if !strings.Contains(output, "=== eng1 (pending input) ===") {
		t.Errorf("patrol header:\n%s", output)
	}
}

// AC4: delivered with text left in the box prints the note, exit 0; the
// woken agent's delivered answer keeps its plain line.
func TestSendVerdict_DeliveredWithTextLeftInTheBox(t *testing.T) {
	note := tui.LeftInBoxPrefix + `"my own half-typed note"`
	stderr, err := v69RunSend(t, tui.IPCResponse{OK: true, Delivery: tui.DeliveryDelivered, Data: note}, "eng1")
	if v69Exit(err) != 0 {
		t.Fatalf("exit %d, want 0", v69Exit(err))
	}
	if got, want := strings.TrimSuffix(stderr, "\n"), "delivered to eng1; "+note; got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
}

// Pending input is drawn yellow in status and patrol, not dimmed like idle.
func TestStatus_PendingInputIsDrawnYellow(t *testing.T) {
	prev := color.Enabled()
	color.SetEnabled(true)
	defer color.SetEnabled(prev)
	if color.Yellow("pending input") == "pending input" {
		t.Fatal("precondition: yellow adds no escape, the check cannot fail")
	}
	if got, want := statusColor("pending input"), color.Yellow("pending input"); got != want {
		t.Errorf("statusColor = %q, want yellow %q", got, want)
	}
	if got, want := patrolStatusColor("pending input"), color.Yellow("pending input"); got != want {
		t.Errorf("patrolStatusColor = %q, want yellow %q", got, want)
	}
}
