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

// ini-l8s5: with INITECH_SOCKET pointing at another rig's TUI, the status
// header names that rig, from the list reply, not the project of the folder
// it was run from. Without an override, or with a reply that names no
// project (an older TUI), the header is what it has always been.

// l8s5Status runs initech status from a folder whose initech.yaml names
// folderProject ("" for no initech.yaml), against a fake TUI whose list reply
// carries replyProject (nil for no project field). overrideSocket points
// INITECH_SOCKET at the fake TUI; otherwise the folder's own socket is used.
// The reply is built as raw JSON so the test runs at any version.
func l8s5Status(t *testing.T, folderProject string, replyProject map[string]any, overrideSocket bool) string {
	t.Helper()
	skipWindows(t)
	restoreColor := disableColor(t)
	t.Cleanup(restoreColor)

	dir := shortProjectDir(t)
	if folderProject != "" {
		writeBasicConfig(t, dir, folderProject)
	}
	t.Cleanup(chdirForTest(t, dir))

	// A bd that answers with no beads, so the real one is never consulted.
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(binDir, "bd"), "#!/bin/sh\necho '[]'\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	panes, _ := json.Marshal([]map[string]any{{"name": "eng1", "activity": "idle", "alive": true, "visible": true}})
	reply := map[string]any{"ok": true, "data": string(panes)}
	if replyProject != nil {
		reply["project"] = replyProject
	}
	raw, _ := json.Marshal(reply)

	var sockPath string
	if overrideSocket {
		sockPath = tui.SocketPath(shortProjectDir(t), "other-rig")
		t.Setenv("INITECH_SOCKET", sockPath)
	} else {
		t.Setenv("INITECH_SOCKET", "")
		sockPath = tui.SocketPath(dir, folderProject)
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

func l8s5Header(t *testing.T, out, want string) {
	t.Helper()
	if !strings.Contains(out, "Session: "+want+" (") {
		t.Errorf("header does not name %q:\n%s", want, out)
	}
}

// Criterion 1: run from this-rig's folder against other-rig's socket.
func TestRunStatus_AnOverriddenSocketNamesItsProject(t *testing.T) {
	out := l8s5Status(t, "this-rig", map[string]any{"name": "other-rig"}, true)
	l8s5Header(t, out, "other-rig")
}

// Criterion 2: no override, so the folder's project names the header, even
// when the reply names another.
func TestRunStatus_WithoutAnOverrideTheFolderNamesTheHeader(t *testing.T) {
	out := l8s5Status(t, "this-rig", map[string]any{"name": "named-by-reply"}, false)
	l8s5Header(t, out, "this-rig")
}

// Criterion 3: an override from a folder with no initech.yaml.
func TestRunStatus_AnOverrideWithoutAProjectFolderNamesTheSocketsProject(t *testing.T) {
	out := l8s5Status(t, "", map[string]any{"name": "other-rig"}, true)
	l8s5Header(t, out, "other-rig")
	if strings.Contains(out, "(unknown project)") {
		t.Errorf("the header still reads unknown:\n%s", out)
	}
}

// Criterion 4: an older TUI's reply names no project; the header is today's.
// Feed it nothing: an empty name is the same.
func TestRunStatus_AReplyWithoutAProjectFallsBack(t *testing.T) {
	l8s5Header(t, l8s5Status(t, "this-rig", nil, true), "this-rig")
	l8s5Header(t, l8s5Status(t, "", nil, true), "(unknown project)")
	l8s5Header(t, l8s5Status(t, "this-rig", map[string]any{"name": ""}, true), "this-rig")
}
