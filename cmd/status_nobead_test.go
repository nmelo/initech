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

// ini-1q3x.2 criterion 3: an agent working without a bead is listed with the
// work's first words where a bead id would go, ahead of any bd assignment.
func TestRunStatus_ShowsWorkWithNoBeadWhereTheBeadGoes(t *testing.T) {
	skipWindows(t)
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
	// eng1 still holds a bead in bd; eng2 holds nothing.
	writeExecutable(t, filepath.Join(binDir, "bd"), `#!/bin/sh
echo '[{"id":"ini-s.1","title":"Older work","status":"in_progress","assignee":"eng1"}]'
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	panes, _ := json.Marshal([]tui.PaneInfo{
		{Name: "eng1", Activity: "running", Alive: true, Visible: true, Beads: []string{}, NoBead: "bump main to 0.28.0 in package.json, tauri.conf.json"},
		{Name: "eng2", Activity: "idle", Alive: true, Visible: true, Beads: []string{}},
	})
	resp, _ := json.Marshal(tui.IPCResponse{OK: true, Data: string(panes)})
	_, cleanup := startATIPCServer(t, tui.SocketPath(projectDir, "demo"), responseMode{raw: string(resp) + "\n"})
	defer cleanup()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	var eng1, eng2 string
	for _, line := range strings.Split(out.String(), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == "eng1" {
			eng1 = line
		}
		if len(f) > 0 && f[0] == "eng2" {
			eng2 = line
		}
	}
	if !strings.Contains(eng1, "no bead (bump main to 0.28.0 in pack...)") {
		t.Errorf("eng1 row = %q, want \"no bead\" and the first words", eng1)
	}
	if strings.Contains(eng1, "ini-s.1") || strings.Contains(eng1, "in_progress") {
		t.Errorf("eng1 row = %q; the no-bead work is the newer dispatch and wins", eng1)
	}
	if strings.Contains(eng2, "no bead") {
		t.Errorf("eng2 row = %q, shows a marker it does not have", eng2)
	}
}
