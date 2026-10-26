package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-67k7: status --json beads read what the table reads -- bd's answer for
// the agent -- and the ribbon only when bd is off or gives no answer.

// k67Beads runs status --json with the given bd script and returns each
// agent's beads.
func k67Beads(t *testing.T, panesJSON, bdScript string, beadsOff bool) map[string]any {
	t.Helper()
	restoreColor := disableColor(t)
	defer restoreColor()

	projectDir := shortProjectDir(t)
	writeBasicConfig(t, projectDir, "demo")
	if beadsOff {
		f, err := os.OpenFile(filepath.Join(projectDir, "initech.yaml"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString("beads:\n  enabled: false\n")
		f.Close()
	}
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	binDir := filepath.Join(projectDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(binDir, "bd"), bdScript)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	respBytes, err := json.Marshal(tui.IPCResponse{OK: true, Data: panesJSON})
	if err != nil {
		t.Fatal(err)
	}
	_, cleanup := startATIPCServer(t, tui.SocketPath(projectDir, "demo"), responseMode{raw: string(respBytes) + "\n"})
	defer cleanup()

	statusJSON = true
	defer func() { statusJSON = false }()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	got := map[string]any{}
	for _, o := range l6omObjects(t, out.String()) {
		got[o["role"].(string)] = o["beads"]
	}
	return got
}

const k67Panes = `[
 {"name":"eng1","activity":"idle","alive":true,"visible":true,"beads":[]},
 {"name":"eng2","activity":"idle","alive":true,"visible":true,"beads":["ini-stale"]},
 {"name":"eng3","activity":"idle","alive":true,"visible":true,"beads":["ini-r"]}
]`

const k67BD = `#!/bin/sh
cat <<'EOF'
[{"id":"ini-a","title":"A","status":"in_progress","assignee":"eng1"},
 {"id":"ini-b","title":"B","status":"in_qa","assignee":"eng1"}]
EOF
`

// Bug 3: an agent that claimed in bd without initech bead lists its beads,
// and a stale ribbon does not override bd.
func TestStatusJSON_BeadsAreBdsAnswer(t *testing.T) {
	got := k67Beads(t, k67Panes, k67BD, false)
	want := map[string]any{
		"eng1": []any{"ini-a", "ini-b"},
		"eng2": []any{},
		"eng3": []any{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("beads = %v, want %v", got, want)
	}
}

// With bd failing or beads off, the ribbon is what there is.
func TestStatusJSON_BeadsFallBackToTheRibbon(t *testing.T) {
	want := map[string]any{
		"eng1": []any{},
		"eng2": []any{"ini-stale"},
		"eng3": []any{"ini-r"},
	}
	failing := "#!/bin/sh\nexit 1\n"
	if got := k67Beads(t, k67Panes, failing, false); !reflect.DeepEqual(got, want) {
		t.Errorf("bd failing: beads = %v, want the ribbon %v", got, want)
	}
	if got := k67Beads(t, k67Panes, k67BD, true); !reflect.DeepEqual(got, want) {
		t.Errorf("beads off: beads = %v, want the ribbon %v", got, want)
	}
	garbage := "#!/bin/sh\necho 'bd: database locked'\n"
	if got := k67Beads(t, k67Panes, garbage, false); !reflect.DeepEqual(got, want) {
		t.Errorf("bd printing no beads list: beads = %v, want the ribbon %v", got, want)
	}
}
