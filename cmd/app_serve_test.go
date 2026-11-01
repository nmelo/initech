package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nmelo/initech/internal/appserve"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// serveFakeTUI answers every request on sockPath with resp, counting them.
func serveFakeTUI(t *testing.T, sockPath string, resp tui.IPCResponse) *atomic.Int32 {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var n atomic.Int32
	data, _ := json.Marshal(resp)
	// One line, built before any connection: each goroutine only reads it
	// (an append per connection raced on data's spare capacity).
	line := append(append([]byte(nil), data...), '\n')
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if _, err := bufio.NewReader(c).ReadString('\n'); err != nil {
					return
				}
				n.Add(1)
				c.Write(line)
			}()
		}
	}()
	return &n
}

const appServePanes = `[
 {"name":"eng1","activity":"running","alive":true,"visible":true,"beads":["ini-x"],"banner":"none","context_percent":41},
 {"name":"super","activity":"idle","alive":true,"visible":true,"beads":[],"banner":"permission_prompt","modal_latched":true,"modal_age_sec":12},
 {"name":"qa1","activity":"suspended","alive":false,"visible":false,"banner":""}
]`

const appServeBD = `#!/bin/sh
cat <<'EOF'
[{"id":"ini-a","title":"A","status":"in_progress","assignee":"eng1"},
 {"id":"ini-b","title":"B","status":"in_qa","assignee":"super"}]
EOF
`

// ini-r0q.1 AC1: the service's status for a rig is what initech status
// --json prints on that rig, bd's bead answer included, object for object
// (the service adds only screen). Red at base: no service.
func TestAppServeStatus_MatchesStatusJSONOnTheRig(t *testing.T) {
	skipWindows(t)
	restoreColor := disableColor(t)
	defer restoreColor()
	dir := shortProjectDir(t)
	writeBasicConfig(t, dir, "demo")
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0o755)
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	// bd answers only in the rig's root, so a service that read bd anywhere
	// else would get the ribbon's beads instead.
	writeExecutable(t, filepath.Join(binDir, "bd"), "#!/bin/sh\n[ \"$(pwd -P)\" = \""+real+"\" ] || exit 1\n"+strings.TrimPrefix(appServeBD, "#!/bin/sh\n"))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INITECH_SOCKET", "")
	sock := tui.SocketPath(dir, "demo")
	serveFakeTUI(t, sock, tui.IPCResponse{OK: true, Data: appServePanes})

	restoreWD := chdirForTest(t, dir)
	statusJSON = true
	defer func() { statusJSON = false }()
	var out bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&out)
	c.SetErr(&bytes.Buffer{})
	if err := runStatus(c, nil); err != nil {
		t.Fatal(err)
	}
	var cli []map[string]any
	if err := json.Unmarshal(out.Bytes(), &cli); err != nil {
		t.Fatal(err)
	}
	// The service runs from wherever launchd starts it, not the rig.
	restoreWD()
	defer chdirForTest(t, t.TempDir())()

	b := &appBackend{dirs: []string{filepath.Dir(dir)}, runner: &iexec.DefaultRunner{}}
	var rig appserve.Rig
	for _, rg := range b.FindRigs() {
		if rg.Root == dir {
			rig = rg
		}
	}
	if rig.Socket != sock || rig.Name != "demo" {
		t.Fatalf("the scan did not find the rig at %s: %+v", dir, rig)
	}
	got, err := b.Status(rig)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, cli) {
		t.Errorf("service status differs from initech status --json.\nservice: %v\ncli:     %v", got, cli)
	}
	if beads := cli[1]["beads"]; !reflect.DeepEqual(beads, []any{"ini-b"}) {
		t.Errorf("fixture: super's beads %v, want bd's [ini-b] (the ribbon says none)", beads)
	}

	panes, err := b.Panes(rig)
	if err != nil || len(panes) != 3 || panes[1] != (appserve.PaneState{Name: "super", State: "idle", Banner: "permission_prompt"}) || panes[2].State != "suspended" {
		t.Errorf("panes = %+v %v", panes, err)
	}
}

// ini-r0q.1 AC6 feed-it-nothing: a folder with no rig, and a rig whose
// socket has no TUI, give no rig and an error, never a hang.
func TestAppServeBackend_NoRigAndNoTUI(t *testing.T) {
	skipWindows(t)
	empty := t.TempDir()
	b := &appBackend{dirs: []string{empty}, runner: &iexec.DefaultRunner{}}
	if rigs := b.FindRigs(); len(rigs) != 0 {
		t.Errorf("empty folder: %v", rigs)
	}
	dir := shortProjectDir(t)
	writeBasicConfig(t, dir, "demo")
	b.dirs = []string{filepath.Dir(dir)}
	for _, rg := range b.FindRigs() {
		if rg.Root == dir {
			if _, err := b.Panes(rg); err == nil {
				t.Error("a rig with no TUI answered")
			}
			return
		}
	}
	t.Error("the rig was not found")
}
