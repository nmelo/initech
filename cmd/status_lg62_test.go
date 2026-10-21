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

// ini-lg62: with INITECH_SOCKET pointing at another project's initech, the
// Bead column reads that project's bd, in the root the list reply names.
// The bd stub answers by the directory it runs in, so the column shows which
// database was read: OTHER-1 in the other project's root, CALLER-1 anywhere
// else. It runs through the real runner, so the test means the same thing at
// any version.

type lg62Case struct {
	override string         // "other", "own" or "" (none)
	project  map[string]any // the reply's project field; nil for none
	ribbon   []string       // eng1's ribbon in the reply; nil for none
}

// lg62Status runs status from a folder whose project is this-rig. It returns
// the output and the other project's root, which the reply may name.
func lg62Status(t *testing.T, c lg62Case, rootFor func(otherRoot string) any) string {
	t.Helper()
	skipWindows(t)
	restoreColor := disableColor(t)
	t.Cleanup(restoreColor)

	dir := shortProjectDir(t)
	writeBasicConfig(t, dir, "this-rig")
	t.Cleanup(chdirForTest(t, dir))
	otherRoot := shortProjectDir(t)
	resolved, err := filepath.EvalSymlinks(otherRoot)
	if err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(binDir, "bd"), `#!/bin/sh
if [ "$(pwd -P)" = "`+resolved+`" ]; then
  echo '[{"id":"OTHER-1","title":"other bead","status":"in_progress","assignee":"eng1"}]'
else
  echo '[{"id":"CALLER-1","title":"caller bead","status":"in_progress","assignee":"eng1"}]'
fi
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pane := map[string]any{"name": "eng1", "activity": "idle", "alive": true, "visible": true}
	if c.ribbon != nil {
		pane["beads"] = c.ribbon
	}
	panes, _ := json.Marshal([]map[string]any{pane})
	reply := map[string]any{"ok": true, "data": string(panes)}
	if c.project != nil {
		p := map[string]any{}
		for k, v := range c.project {
			p[k] = v
		}
		if rootFor != nil {
			p["root"] = rootFor(otherRoot)
		}
		reply["project"] = p
	}
	raw, _ := json.Marshal(reply)

	var sockPath string
	switch c.override {
	case "other":
		sockPath = tui.SocketPath(otherRoot, "other-rig")
		t.Setenv("INITECH_SOCKET", sockPath)
	case "own":
		sockPath = tui.SocketPath(dir, "this-rig")
		t.Setenv("INITECH_SOCKET", sockPath)
	default:
		t.Setenv("INITECH_SOCKET", "")
		sockPath = tui.SocketPath(dir, "this-rig")
	}
	_, cleanup := startATIPCServer(t, sockPath, responseMode{raw: string(raw) + "\n"})
	t.Cleanup(cleanup)

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	return out.String()
}

func theRoot(root string) any { return root }

func lg62Has(t *testing.T, out string, want, notWant []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("status output lacks %q:\n%s", w, out)
		}
	}
	for _, w := range notWant {
		if strings.Contains(out, w) {
			t.Errorf("status output shows %q:\n%s", w, out)
		}
	}
}

const lg62Note = "(bd: unknown project)"

// Criterion 1: the other project's root is where bd runs.
func TestRunStatus_TheBeadColumnReadsTheSocketsProject(t *testing.T) {
	out := lg62Status(t, lg62Case{override: "other", project: map[string]any{"name": "other-rig", "beads_enabled": true}}, theRoot)
	lg62Has(t, out, []string{"Session: other-rig", "OTHER-1"}, []string{"CALLER-1"})
}

// Criterion 2: no override, the folder's bd as today, whatever the reply says.
func TestRunStatus_WithoutAnOverrideTheFolderBdIsRead(t *testing.T) {
	out := lg62Status(t, lg62Case{project: map[string]any{"name": "this-rig", "beads_enabled": true}}, theRoot)
	lg62Has(t, out, []string{"CALLER-1"}, []string{"OTHER-1", lg62Note})
}

// Criterion 3: an older TUI names no root. The column says so and never shows
// the caller's beads.
func TestRunStatus_AnOlderTUIGetsTheNoteNotTheCallersBeads(t *testing.T) {
	out := lg62Status(t, lg62Case{override: "other"}, nil)
	lg62Has(t, out, []string{lg62Note}, []string{"CALLER-1", "OTHER-1"})
	out = lg62Status(t, lg62Case{override: "other", project: map[string]any{"name": "other-rig", "beads_enabled": true}}, nil)
	lg62Has(t, out, []string{lg62Note}, []string{"CALLER-1", "OTHER-1"})
	// With no database read, a ribbon has nothing to disagree with: no drift
	// line against beads that were never looked up.
	out = lg62Status(t, lg62Case{override: "other", ribbon: []string{"OTHER-1"}}, nil)
	lg62Has(t, out, []string{lg62Note}, []string{"ribbon:", "CALLER-1"})
}

// Feed it nothing: an empty root, and a root that does not exist here.
func TestRunStatus_AnEmptyOrMissingRootGetsTheNote(t *testing.T) {
	for name, root := range map[string]string{"empty": "", "missing": "/nonexistent/initech-lg62"} {
		r := root
		out := lg62Status(t, lg62Case{override: "other", project: map[string]any{"name": "other-rig", "beads_enabled": true}},
			func(string) any { return r })
		if !strings.Contains(out, lg62Note) || strings.Contains(out, "CALLER-1") {
			t.Errorf("%s root: want the note and no caller bead:\n%s", name, out)
		}
	}
}

// Criterion 4: the socket project's beads switch, not the folder's.
func TestRunStatus_BeadsOffFollowTheSocketsProject(t *testing.T) {
	out := lg62Status(t, lg62Case{override: "other", project: map[string]any{"name": "other-rig", "beads_enabled": false}}, theRoot)
	lg62Has(t, out, []string{"Session: other-rig"}, []string{"CALLER-1", "OTHER-1"})
}

// Criterion 5: the ribbon is compared with the same database: a ribbon of
// OTHER-1 agrees with the other project's bd, so no drift is named.
func TestRunStatus_TheRibbonIsComparedWithTheSameRoot(t *testing.T) {
	out := lg62Status(t, lg62Case{override: "other", project: map[string]any{"name": "other-rig", "beads_enabled": true}, ribbon: []string{"OTHER-1"}}, theRoot)
	lg62Has(t, out, []string{"OTHER-1"}, []string{"ribbon:", "CALLER-1"})
}

// An override that names the folder's own project socket is no override: an
// agent's own INITECH_SOCKET against a TUI too old to send a root keeps its
// beads.
func TestRunStatus_AnOverrideToTheOwnSocketReadsTheFolder(t *testing.T) {
	out := lg62Status(t, lg62Case{override: "own"}, nil)
	lg62Has(t, out, []string{"Session: this-rig", "CALLER-1"}, []string{lg62Note})
}
