package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-r1u2: status --json against a TUI too old to read screens says so on
// stderr, once; the JSON is unchanged and the exit is 0. The signal is
// banner: since v2.35.0 a TUI asked for detail sets it on every live pane it
// reads. The project block is no signal: it has been sent since v2.31.3.

const r1u2Line = "older initech running; restart it for status detail"

func r1u2Status(t *testing.T, panesJSON string, asJSON bool) (string, string) {
	t.Helper()
	restoreColor := disableColor(t)
	defer restoreColor()
	projectDir := shortProjectDir(t)
	writeBasicConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()
	binDir := filepath.Join(projectDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(binDir, "bd"), "#!/bin/sh\necho '[]'\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Every reply here carries the project block, as v2.31.3 and later do.
	respBytes, err := json.Marshal(tui.IPCResponse{OK: true, Data: panesJSON,
		Project: &tui.IPCProject{Name: "demo", BeadsEnabled: true, Root: projectDir}})
	if err != nil {
		t.Fatal(err)
	}
	_, cleanup := startATIPCServer(t, tui.SocketPath(projectDir, "demo"), responseMode{raw: string(respBytes) + "\n"})
	defer cleanup()
	statusJSON = asJSON
	defer func() { statusJSON = false }()
	var out, errOut bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	return out.String(), errOut.String()
}

// A v2.34.x reply: project block, beads, no detail keys at all.
const r1u2Old = `[{"name":"eng1","activity":"idle","alive":true,"visible":true,"beads":[]},
 {"name":"eng2","activity":"running","alive":true,"visible":true,"beads":["ini-a"]}]`

func TestStatusJSON_AnOlderTUISaysSoOnStderr(t *testing.T) {
	stdout, stderr := r1u2Status(t, r1u2Old, true)
	if strings.Count(stderr, r1u2Line) != 1 || strings.TrimSpace(stderr) != r1u2Line {
		t.Errorf("stderr = %q, want exactly the line once", stderr)
	}
	objs := l6omObjects(t, stdout)
	if len(objs) != 2 || objs[0]["context_percent"] != nil || objs[0]["banner"] != nil {
		t.Errorf("stdout changed: %v", objs)
	}
}

func TestStatusJSON_ACurrentTUIGivesNoLine(t *testing.T) {
	cases := map[string]string{
		"claude panes": `[{"name":"eng1","activity":"idle","alive":true,"beads":[],"banner":"none","context_percent":12,
		  "background":{"shells":0,"monitors":0,"tasks":0},"account":{"user":"W","model":"Haiku 5.5"}}]`,
		// The positive twin: every detail null but banner, as a generic
		// agent reads -- an honest page of nulls from a current TUI.
		"generic panes only": `[{"name":"sh1","activity":"idle","alive":true,"beads":[],"banner":"none"},
		  {"name":"sh2","activity":"running","alive":true,"beads":[],"banner":"none"}]`,
		// Nothing live to read: no way to tell, so no line.
		"only stopped panes": `[{"name":"eng1","activity":"dead","alive":false,"beads":[]}]`,
		"no panes":           `[]`,
	}
	for name, panes := range cases {
		if _, stderr := r1u2Status(t, panes, true); stderr != "" {
			t.Errorf("%s: stderr %q, want nothing", name, stderr)
		}
	}
}

func TestStatus_TheTableNeverSaysIt(t *testing.T) {
	stdout, stderr := r1u2Status(t, r1u2Old, false)
	if strings.Contains(stdout+stderr, r1u2Line) {
		t.Errorf("the table printed the line:\n%s%s", stdout, stderr)
	}
}
