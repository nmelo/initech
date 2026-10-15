package tui

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ipcAsk sends one request over the socket and returns the reply.
func ipcAsk(t *testing.T, sockPath string, req IPCRequest) IPCResponse {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	data, _ := json.Marshal(req)
	conn.Write(append(data, '\n'))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	sc := NewIPCScanner(conn)
	if !sc.Scan() {
		t.Fatalf("no reply to %s: %v", req.Action, sc.Err())
	}
	var resp IPCResponse
	if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
		t.Fatalf("reply to %s: %v", req.Action, err)
	}
	return resp
}

// ini-1q3x.1: the list reply carries what each pane's ribbon shows, so assign
// can add one bead to a ribbon or take one off without dropping the others.
// An empty ribbon is sent as [] -- never left out, never null -- because a
// missing field is how assign recognises an older TUI.
func TestIPCList_CarriesEachPanesRibbonAndAnEmptyOneAsEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix domain socket directly")
	}
	empty := newEmuPane("eng1", 80, 24)
	one := newEmuPane("eng2", 80, 24)
	one.SetBead("ini-a", "Title A")
	two := newEmuPane("eng3", 80, 24)
	tu := &TUI{panes: toPaneViews([]*Pane{empty, one, two})}

	// A short directory: the test's own TempDir is past the socket path limit.
	dir, err := os.MkdirTemp("", "q3x")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sockPath := filepath.Join(dir, "list.sock")
	cleanup, err := tu.startIPC(sockPath)
	if err != nil {
		t.Fatalf("startIPC: %v", err)
	}
	defer cleanup()

	// Set eng3's ribbon the way assign does, through the bead action.
	if r := ipcAsk(t, sockPath, IPCRequest{Action: "bead", Target: "eng3", Text: "ini-b,ini-c"}); !r.OK {
		t.Fatalf("bead: %s", r.Error)
	}
	resp := ipcAsk(t, sockPath, IPCRequest{Action: "list"})
	if !resp.OK {
		t.Fatalf("list: %s", resp.Error)
	}

	var raw []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resp.Data), &raw); err != nil {
		t.Fatalf("list data: %v", err)
	}
	want := map[string]string{"eng1": `[]`, "eng2": `["ini-a"]`, "eng3": `["ini-b","ini-c"]`}
	for _, p := range raw {
		var name string
		json.Unmarshal(p["name"], &name)
		got, ok := p["beads"]
		if !ok {
			t.Errorf("%s: no beads field in the list reply", name)
			continue
		}
		if strings.TrimSpace(string(got)) != want[name] {
			t.Errorf("%s: beads = %s, want %s", name, got, want[name])
		}
	}
	if len(raw) != 3 {
		t.Errorf("list reply has %d panes, want 3", len(raw))
	}
}

func TestDaemonAllPanes_CarriesTheRibbonAndAnEmptyOneAsEmpty(t *testing.T) {
	d := &Daemon{}
	p := newEmuPane("eng1", 80, 24)
	q := newEmuPane("eng2", 80, 24)
	q.SetBead("ini-a", "")
	d.panes = []*Pane{p, q}
	panes, _ := d.AllPanes()
	data, _ := json.Marshal(panes)
	if !strings.Contains(string(data), `"name":"eng1","activity"`) {
		t.Fatalf("unexpected encoding: %s", data)
	}
	for _, want := range []string{`"beads":[]`, `"beads":["ini-a"]`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("daemon list reply missing %s: %s", want, data)
		}
	}
}
