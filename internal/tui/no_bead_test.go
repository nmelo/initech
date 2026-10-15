package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// ── ini-1q3x.2: work dispatched with no bead shows on the ribbon ──

// noBeadSocket starts a real TUI IPC server over the given panes.
func noBeadSocket(t *testing.T, panes ...*Pane) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix domain socket directly")
	}
	tu := &TUI{panes: toPaneViews(panes)}
	dir, err := os.MkdirTemp("", "q3x2")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	cleanup, err := tu.startIPC(sock)
	if err != nil {
		t.Fatalf("startIPC: %v", err)
	}
	t.Cleanup(cleanup)
	return sock
}

// listPane returns one pane's entry from a real list reply, raw.
func listPane(t *testing.T, sock, name string) map[string]json.RawMessage {
	t.Helper()
	resp := ipcAsk(t, sock, IPCRequest{Action: "list"})
	if !resp.OK {
		t.Fatalf("list: %s", resp.Error)
	}
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resp.Data), &raw); err != nil {
		t.Fatalf("list data: %v", err)
	}
	for _, p := range raw {
		var n string
		json.Unmarshal(p["name"], &n)
		if n == name {
			return p
		}
	}
	t.Fatalf("no pane %s in list", name)
	return nil
}

// The whole round trip over the real socket: no_bead sets the marker, the
// list reply shows it with no bead id, a bead registration replaces it, and a
// clear removes it.
func TestNoBead_MarkerOverTheSocketIsReplacedByABeadAndClearedByClear(t *testing.T) {
	p := newEmuPane("eng3", 80, 24)
	p.SetBead("ini-old", "Old")
	sock := noBeadSocket(t, p)

	if r := ipcAsk(t, sock, IPCRequest{Action: "no_bead", Target: "eng3", Text: "bump main to 0.28.0"}); !r.OK {
		t.Fatalf("no_bead: %s", r.Error)
	}
	e := listPane(t, sock, "eng3")
	if string(e["no_bead"]) != `"bump main to 0.28.0"` {
		t.Errorf("list no_bead = %s, want the words", e["no_bead"])
	}
	if string(e["beads"]) != `[]` {
		t.Errorf("list beads = %s, want [] -- the marker replaces the bead and is never one", e["beads"])
	}
	if p.BeadID() != "" || p.BeadIDs() != nil {
		t.Errorf("BeadID=%q BeadIDs=%v while the marker is up; it must never read as a bead id", p.BeadID(), p.BeadIDs())
	}

	// Criterion 5: the next bead replaces it.
	if r := ipcAsk(t, sock, IPCRequest{Action: "bead", Target: "eng3", Text: "ini-x\tX"}); !r.OK {
		t.Fatalf("bead: %s", r.Error)
	}
	if e := listPane(t, sock, "eng3"); e["no_bead"] != nil || string(e["beads"]) != `["ini-x"]` {
		t.Errorf("after a bead: no_bead=%s beads=%s, want no marker and ini-x", e["no_bead"], e["beads"])
	}

	// And --clear removes it.
	ipcAsk(t, sock, IPCRequest{Action: "no_bead", Target: "eng3", Text: "again"})
	if r := ipcAsk(t, sock, IPCRequest{Action: "bead", Target: "eng3", Text: ""}); !r.OK {
		t.Fatalf("clear: %s", r.Error)
	}
	if e := listPane(t, sock, "eng3"); e["no_bead"] != nil {
		t.Errorf("after --clear: no_bead=%s, want gone", e["no_bead"])
	}
}

func TestNoBead_RefusedLikeTheBeadActionAndOnBadText(t *testing.T) {
	alive := newEmuPane("eng3", 80, 24)
	sus := newEmuPane("eng4", 80, 24)
	sus.alive = false
	sus.suspended = true
	sock := noBeadSocket(t, alive, sus)
	for _, tc := range []struct {
		req  IPCRequest
		want string
	}{
		{IPCRequest{Action: "no_bead", Target: "eng4", Text: "w"}, "is suspended"},
		{IPCRequest{Action: "no_bead", Target: "nobody", Text: "w"}, "not found"},
		{IPCRequest{Action: "no_bead", Target: "eng3", Text: "  "}, "first words"},
		{IPCRequest{Action: "no_bead", Target: "eng3", Text: "a\x1b[2Jb"}, "control characters"},
		{IPCRequest{Action: "no_bead", Target: "eng3", Text: strings.Repeat("x", 257)}, "too long"},
	} {
		r := ipcAsk(t, sock, tc.req)
		if r.OK || !strings.Contains(r.Error, tc.want) {
			t.Errorf("%+v: got ok=%v err=%q, want refusal containing %q", tc.req, r.OK, r.Error, tc.want)
		}
	}
	if alive.NoBeadWork() != "" {
		t.Errorf("a refused no_bead set the marker: %q", alive.NoBeadWork())
	}
}

// The ribbon reads "no bead" and the words, where a bead would be.
func TestNoBead_RibbonShowsNoBeadAndTheWords(t *testing.T) {
	p := newEmuPane("eng3", 80, 24)
	p.SetNoBeadWork("bump main to 0.28.0")
	p.region = Region{X: 0, Y: 0, W: 80, H: 6}
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(80, 6)
	p.Render(screen, false, false, 1, Selection{})
	screen.Show()
	var row strings.Builder
	for x := 0; x < 80; x++ {
		r, _, _, _ := screen.GetContent(x, 5)
		row.WriteRune(r)
	}
	if !strings.Contains(row.String(), "| no bead: bump main to 0.28.0") {
		t.Errorf("ribbon = %q, want \"| no bead: bump main to 0.28.0\"", row.String())
	}
}

func TestNoBead_SetBeadAndSetBeadsEndTheMarker(t *testing.T) {
	p := newEmuPane("eng3", 80, 24)
	p.SetNoBeadWork("w")
	p.SetBeads([]string{"a", "b"})
	if p.NoBeadWork() != "" {
		t.Error("SetBeads kept the marker")
	}
	p.SetNoBeadWork("w")
	if p.BeadIDs() != nil {
		t.Error("the marker kept the beads")
	}
	p.SetBead("", "")
	if p.NoBeadWork() != "" {
		t.Error("a clear kept the marker")
	}
}

// pm's H: the marker survives a suspend and wake, as bead ids do.
func TestNoBead_MarkerWakesWithThePane(t *testing.T) {
	p := newEmuPane("eng3", 80, 24)
	p.cfg.Command = []string{"/bin/sh", "-c", "echo ready; sleep 1"}
	p.suspended = true
	p.noBeadWork = "bump main to 0.28.0"
	tu := &TUI{
		panes:       toPaneViews([]*Pane{p}),
		quitCh:      make(chan struct{}),
		agentEvents: make(chan AgentEvent, 64),
	}
	if err := tu.resumePane(p, "test"); err != nil {
		t.Fatalf("resumePane: %v", err)
	}
	np := tu.panes[0].(*Pane)
	t.Cleanup(func() { np.Close() })
	if np == p {
		t.Fatal("resume did not replace the pane; the carry was not exercised")
	}
	if got := np.NoBeadWork(); got != "bump main to 0.28.0" {
		t.Errorf("woken pane marker = %q, want it carried", got)
	}
}

func TestNoBead_RemotePaneBeadEndsTheMarker(t *testing.T) {
	rp := &RemotePane{name: "eng1", alive: true}
	rp.ApplyStatus(nil, "w", "")
	if rp.NoBeadWork() != "w" {
		t.Fatal("marker not applied")
	}
	rp.ApplyStatus([]string{"ini-a"}, "w", "")
	if rp.NoBeadWork() != "" {
		t.Error("a bead and the marker together; a bead ends it")
	}
	rp.SetNoBeadWork("w")
	rp.SetBead("", "")
	if rp.NoBeadWork() != "" {
		t.Error("SetBead kept the marker on a remote pane")
	}
}
