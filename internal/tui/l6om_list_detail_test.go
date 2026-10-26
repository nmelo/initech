package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ini-l6om: the list reply carries footer values and the dialog kind when
// the request asks for detail, for live panes only, and nothing new when it
// does not.

// l6omPainted is a live 160x45 pane showing a captured Haiku screen.
func l6omPainted(t *testing.T, name, fixture string) *Pane {
	t.Helper()
	p := newEmuPane(name, 160, 45)
	lvxwPaint(p, lvxwFixture(t, fixture))
	return p
}

// l6omList asks a TUI for its list over the real socket and returns each
// pane's raw fields by name.
func l6omList(t *testing.T, tu *TUI, detail bool) map[string]map[string]json.RawMessage {
	t.Helper()
	dir, err := os.MkdirTemp("", "l6o")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	cleanup, err := tu.startIPC(filepath.Join(dir, "l.sock"))
	if err != nil {
		t.Fatalf("startIPC: %v", err)
	}
	defer cleanup()
	resp := ipcAsk(t, filepath.Join(dir, "l.sock"), IPCRequest{Action: "list", Detail: detail})
	if !resp.OK {
		t.Fatalf("list: %s", resp.Error)
	}
	return l6omByName(t, resp.Data)
}

func l6omByName(t *testing.T, data string) map[string]map[string]json.RawMessage {
	t.Helper()
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatalf("list data: %v", err)
	}
	out := map[string]map[string]json.RawMessage{}
	for _, p := range raw {
		var name string
		json.Unmarshal(p["name"], &name)
		out[name] = p
	}
	return out
}

var l6omDetailKeys = []string{"context_percent", "background", "banner", "account"}

func TestIPCList_DetailReadsEachLivePanesScreen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix domain socket directly")
	}
	working := l6omPainted(t, "eng1", "haiku310_footer_bgagent.txt")
	asking := l6omPainted(t, "eng2", "haiku310_write_prompt.txt")
	asking.latchDialogOpen("Claude needs your permission")
	gone := l6omPainted(t, "eng3", "haiku310_footer_idle.txt")
	gone.alive = false
	// A dialog announced but not on screen: the latch is up, no kind known.
	held := l6omPainted(t, "eng4", "haiku310_footer_idle.txt")
	held.latchDialogOpen("Claude is waiting for your input")
	tu := &TUI{panes: toPaneViews([]*Pane{working, asking, gone, held})}

	got := l6omList(t, tu, true)
	want := map[string]map[string]string{
		"eng1": {"context_percent": `9`, "background": `{"shells":1,"monitors":1,"tasks":1}`,
			"banner": `"none"`, "account": `{"user":"Personal","model":"Haiku 5.5"}`},
		// The dialog hides the footer: no footer values, the kind named.
		"eng2": {"banner": `"permission_prompt"`},
		// Not alive: its screen is from before it stopped, so nothing is read.
		"eng3": {},
		"eng4": {"context_percent": `0`, "background": `{"shells":0,"monitors":0,"tasks":0}`,
			"banner": `"unknown"`, "account": `{"user":"Personal","model":"Haiku 5.5"}`},
	}
	for name, fields := range want {
		for _, k := range l6omDetailKeys {
			g, present := got[name][k]
			w, wanted := fields[k]
			switch {
			case wanted && string(g) != w:
				t.Errorf("%s.%s = %s, want %s", name, k, g, w)
			case !wanted && present:
				t.Errorf("%s.%s = %s, want it left out (null)", name, k, g)
			}
		}
	}
}

// Without detail the reply is what it was: none of the new fields, and the
// screen is not read.
func TestIPCList_NoDetailAddsNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix domain socket directly")
	}
	tu := &TUI{panes: toPaneViews([]*Pane{l6omPainted(t, "eng1", "haiku310_footer_bgagent.txt")})}
	got := l6omList(t, tu, false)
	for _, k := range l6omDetailKeys {
		if v, ok := got["eng1"][k]; ok {
			t.Errorf("eng1.%s = %s without detail, want it left out", k, v)
		}
	}
}

// The daemon's panes report the same fields.
func TestIPCList_TheDaemonReadsDetailToo(t *testing.T) {
	d := &Daemon{panes: []*Pane{l6omPainted(t, "eng1", "haiku310_footer_shell.txt")}}
	conn := &dispatchConn{}
	dispatchIPC(d, conn, IPCRequest{Action: "list", Detail: true}, nil)
	resp := decodeDispatchResponse(t, conn)
	if !resp.OK {
		t.Fatalf("list: %s", resp.Error)
	}
	got := l6omByName(t, resp.Data)["eng1"]
	if string(got["context_percent"]) != `8` || string(got["background"]) != `{"shells":1,"monitors":0,"tasks":0}` {
		t.Errorf("daemon eng1: context %s background %s", got["context_percent"], got["background"])
	}
}

// An older client decodes a new reply: the new fields are extra keys it
// ignores, and the fields it knows keep their values.
func TestIPCList_AnOlderClientDecodesANewReply(t *testing.T) {
	type oldPaneInfo struct {
		Name     string   `json:"name"`
		Activity string   `json:"activity"`
		Alive    bool     `json:"alive"`
		Visible  bool     `json:"visible"`
		Beads    []string `json:"beads"`
	}
	seven := 7
	data, err := json.Marshal([]PaneInfo{{Name: "eng1", Activity: "idle", Alive: true, Visible: true,
		Beads: []string{"ini-a"}, ContextPercent: &seven, Banner: "none",
		Background: &PaneBackground{Shells: &seven}}})
	if err != nil {
		t.Fatal(err)
	}
	var old []oldPaneInfo
	if err := json.Unmarshal(data, &old); err != nil {
		t.Fatalf("old client: %v", err)
	}
	if len(old) != 1 || old[0].Name != "eng1" || old[0].Activity != "idle" || !old[0].Alive || len(old[0].Beads) != 1 {
		t.Errorf("old client read %+v", old)
	}
}
