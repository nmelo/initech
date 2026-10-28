package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-e6al: the status header counted every agent that is not alive as
// stopped, and its first number counted only the live ones, so seven parked
// agents read "0 agents, 7 stopped" above seven rows that read suspended. The
// header now reads the label the rows read, and counts the rows.

// e6alHeader runs initech status against a fake socket listing panes and
// returns the header line.
func e6alHeader(t *testing.T, panes []map[string]any) string {
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

	if panes == nil {
		panes = []map[string]any{}
	}
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
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "Session: ") {
			return line
		}
	}
	t.Fatalf("no header in:\n%s", out.String())
	return ""
}

func e6alAgents(labels ...string) []map[string]any {
	var panes []map[string]any
	for i, l := range labels {
		panes = append(panes, map[string]any{
			"name": fmt.Sprintf("eng%d", i+1), "activity": l, "alive": l != "suspended" && l != "dead", "visible": true,
		})
	}
	return panes
}

// AC1 and AC4: seven suspended agents read suspended, never stopped, and the
// first number is the seven rows.
func TestStatusHeader_SevenSuspendedReadSuspended(t *testing.T) {
	got := e6alHeader(t, e6alAgents("suspended", "suspended", "suspended", "suspended", "suspended", "suspended", "suspended"))
	if want := "Session: demo (7 agents, 7 suspended)"; got != want {
		t.Errorf("header %q, want %q", got, want)
	}
	if strings.Contains(got, "stopped") {
		t.Errorf("header %q says stopped for parked agents", got)
	}
}

// AC2 and AC4: two suspended, one exited, four idle.
func TestStatusHeader_AMixCountsEachAsItsRowReads(t *testing.T) {
	got := e6alHeader(t, e6alAgents("suspended", "dead", "idle", "suspended", "idle", "idle", "idle"))
	if want := "Session: demo (7 agents, 2 suspended, 1 stopped)"; got != want {
		t.Errorf("header %q, want %q", got, want)
	}
}

// AC3 and AC4: an exited agent alone reads stopped, with no suspended word.
func TestStatusHeader_AnExitedAgentReadsStopped(t *testing.T) {
	got := e6alHeader(t, e6alAgents("dead", "running"))
	if want := "Session: demo (2 agents, 1 stopped)"; got != want {
		t.Errorf("header %q, want %q", got, want)
	}
}

// All alive: no count after the agents, as before.
func TestStatusHeader_AllAliveHasNoCounts(t *testing.T) {
	got := e6alHeader(t, e6alAgents("running", "idle", "waiting"))
	if want := "Session: demo (3 agents)"; got != want {
		t.Errorf("header %q, want %q", got, want)
	}
}

// AC5 feed it nothing: a reply with no agents.
func TestStatusHeader_NoAgents(t *testing.T) {
	got := e6alHeader(t, nil)
	if want := "Session: demo (0 agents)"; got != want {
		t.Errorf("header %q, want %q", got, want)
	}
}
