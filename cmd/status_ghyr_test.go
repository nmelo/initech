package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-ghyr: a suspended agent is not alive, and status used to call every
// agent that is not alive "stopped". A suspended one now reads suspended;
// one whose process exited still reads stopped.

// ghyrRows runs initech status against a fake socket listing panes and
// returns each agent's row as its whitespace-separated fields.
func ghyrRows(t *testing.T, panes []map[string]any) map[string][]string {
	t.Helper()
	restoreColor := disableColor(t)
	defer restoreColor()

	projectDir := shortProjectDir(t)
	writeBasicConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	binDir := filepath.Join(projectDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	writeExecutable(t, filepath.Join(binDir, "bd"), "#!/bin/sh\necho '[]'\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	panesJSON, err := json.Marshal(panes)
	if err != nil {
		t.Fatalf("Marshal panes: %v", err)
	}
	respBytes, err := json.Marshal(tui.IPCResponse{OK: true, Data: string(panesJSON)})
	if err != nil {
		t.Fatalf("Marshal IPC response: %v", err)
	}
	_, cleanup := startATIPCServer(t, tui.SocketPath(projectDir, "demo"), responseMode{raw: string(respBytes) + "\n"})
	defer cleanup()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	rows := map[string][]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 {
			for _, p := range panes {
				if f[0] == p["name"] {
					rows[f[0]] = f
				}
			}
		}
	}
	if len(rows) != len(panes) {
		t.Fatalf("found %d agent rows, want %d:\n%s", len(rows), len(panes), out.String())
	}
	return rows
}

// Criteria 1 and 2: suspended reads suspended, every other label on an agent
// that is not alive reads stopped, and Alive is "no" for all of them.
func TestStatus_ASuspendedAgentReadsSuspended(t *testing.T) {
	rows := ghyrRows(t, []map[string]any{
		{"name": "eng1", "activity": "suspended", "alive": false, "visible": true},
		{"name": "eng2", "activity": "dead", "alive": false, "visible": true},
		{"name": "eng3", "activity": "idle", "alive": false, "visible": true},
		{"name": "eng4", "activity": "suspended", "alive": false, "visible": false},
		{"name": "eng5", "activity": "running", "alive": true, "visible": true},
	})
	want := map[string]string{
		"eng1": "no - suspended",
		"eng2": "no - stopped",
		"eng3": "no - stopped",
		"eng4": "no - suspended [hidden]",
		"eng5": "yes - running",
	}
	for name, w := range want {
		if got := strings.Join(rows[name][1:], " "); got != w {
			t.Errorf("%s row: %q, want %q", name, got, w)
		}
	}
}

// The colour switch dims suspended like idle and stopped.
func TestStatus_SuspendedIsDrawnDim(t *testing.T) {
	prev := color.Enabled()
	color.SetEnabled(true)
	defer color.SetEnabled(prev)
	if got, want := statusColor("suspended"), color.Dim("suspended"); got != want {
		t.Errorf("statusColor(suspended) = %q, want dim %q", got, want)
	}
	if color.Dim("suspended") == "suspended" {
		t.Fatal("precondition: dim adds no escape, the check cannot fail")
	}
}
