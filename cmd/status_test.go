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

func TestRunStatus_LocalSessionWithoutHostColumn(t *testing.T) {
	restoreColor := disableColor(t)
	defer restoreColor()

	projectDir := shortProjectDir(t)
	writeStandupConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	binDir := filepath.Join(projectDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	writeExecutable(t, filepath.Join(binDir, "bd"), `#!/bin/sh
case "$*" in
  "list --status in_progress --json")
    cat <<'EOF'
[{"id":"ini-s.1","title":"This title is intentionally longer than thirty characters","status":"in_progress","assignee":"eng1"}]
EOF
    ;;
  *)
    echo "unexpected args: $*" >&2
    exit 1
    ;;
esac
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	panesJSON, err := json.Marshal([]map[string]any{
		{"name": "eng1", "activity": "idle", "alive": true, "visible": true},
		{"name": "qa1", "activity": "dead", "alive": false, "visible": false},
	})
	if err != nil {
		t.Fatalf("Marshal panes: %v", err)
	}
	respBytes, err := json.Marshal(tui.IPCResponse{OK: true, Data: string(panesJSON)})
	if err != nil {
		t.Fatalf("Marshal IPC response: %v", err)
	}

	sockPath := tui.SocketPath(projectDir, "demo")
	reqCh, cleanup := startATIPCServer(t, sockPath, responseMode{raw: string(respBytes) + "\n"})
	defer cleanup()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}

	var req tui.IPCRequest
	waitATRequest(t, reqCh, &req)
	if req.Action != "list" {
		t.Fatalf("IPC action = %q, want list", req.Action)
	}

	got := out.String()
	for _, want := range []string{
		"Session: demo (1 agents, 1 stopped)",
		"Role",
		"Alive",
		"Bead",
		"Status",
		"eng1",
		"yes",
		"ini-s.1",
		"(This title is intentionally...",
		"in_progress",
		"qa1",
		"no",
		"stopped [hidden]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Host") {
		t.Fatalf("local-only status should not render Host column:\n%s", got)
	}
}

func TestRunStatus_RemotesRenderHostColumn(t *testing.T) {
	restoreColor := disableColor(t)
	defer restoreColor()

	projectDir := shortProjectDir(t)
	writeStandupConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	binDir := filepath.Join(projectDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	writeExecutable(t, filepath.Join(binDir, "bd"), "#!/bin/sh\nprintf '[]\\n'\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	panesJSON, err := json.Marshal([]map[string]any{
		{"name": "eng1", "activity": "running", "alive": true, "visible": true},
		{"name": "eng2", "host": "workbench", "activity": "idle", "alive": true, "visible": false},
	})
	if err != nil {
		t.Fatalf("Marshal panes: %v", err)
	}
	respBytes, err := json.Marshal(tui.IPCResponse{OK: true, Data: string(panesJSON)})
	if err != nil {
		t.Fatalf("Marshal IPC response: %v", err)
	}

	sockPath := tui.SocketPath(projectDir, "demo")
	reqCh, cleanup := startATIPCServer(t, sockPath, responseMode{raw: string(respBytes) + "\n"})
	defer cleanup()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	var req tui.IPCRequest
	waitATRequest(t, reqCh, &req)

	got := out.String()
	for _, want := range []string{
		"Host",
		"local",
		"workbench",
		"workbench:eng2",
		"idle [hidden]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("remote status output missing %q:\n%s", want, got)
		}
	}
}

func TestRunStatus_ParsePaneListError(t *testing.T) {
	projectDir := shortProjectDir(t)
	writeStandupConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	binDir := filepath.Join(projectDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	writeExecutable(t, filepath.Join(binDir, "bd"), "#!/bin/sh\nprintf '[]\\n'\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	respBytes, err := json.Marshal(tui.IPCResponse{OK: true, Data: "{not-json}"})
	if err != nil {
		t.Fatalf("Marshal IPC response: %v", err)
	}
	sockPath := tui.SocketPath(projectDir, "demo")
	reqCh, cleanup := startATIPCServer(t, sockPath, responseMode{raw: string(respBytes) + "\n"})
	defer cleanup()

	err = runStatus(&cobra.Command{}, nil)
	if err == nil || !strings.Contains(err.Error(), "parse pane list") {
		t.Fatalf("runStatus parse error = %v", err)
	}
	var req tui.IPCRequest
	waitATRequest(t, reqCh, &req)
}

func TestRunStatus_IPCError(t *testing.T) {
	projectDir := shortProjectDir(t)
	writeStandupConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	binDir := filepath.Join(projectDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	writeExecutable(t, filepath.Join(binDir, "bd"), "#!/bin/sh\nprintf '[]\\n'\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	respBytes, err := json.Marshal(tui.IPCResponse{OK: false, Error: "session unavailable"})
	if err != nil {
		t.Fatalf("Marshal IPC response: %v", err)
	}
	sockPath := tui.SocketPath(projectDir, "demo")
	reqCh, cleanup := startATIPCServer(t, sockPath, responseMode{raw: string(respBytes) + "\n"})
	defer cleanup()

	err = runStatus(&cobra.Command{}, nil)
	if err == nil || !strings.Contains(err.Error(), "session unavailable") {
		t.Fatalf("runStatus IPC error = %v", err)
	}
	var req tui.IPCRequest
	waitATRequest(t, reqCh, &req)
}
