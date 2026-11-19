package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// stripCall runs a strip request through h and returns the reply.
func stripCall(t *testing.T, h func(net.Conn, IPCRequest), req IPCRequest) IPCResponse {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	go func() { h(server, req); server.Close() }()
	sc := bufio.NewScanner(client)
	if !sc.Scan() {
		t.Fatalf("no reply: %v", sc.Err())
	}
	var resp IPCResponse
	if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
		t.Fatalf("reply %q: %v", sc.Text(), err)
	}
	return resp
}

// stripRig is window 1 with one pane, sized at a 40-row tile, and its book in
// a temporary .initech.
func stripRig(t *testing.T) (*TUI, *Pane, string) {
	t.Helper()
	root := t.TempDir()
	p := newEmuPane("super", 120, 38)
	p.resizeFromMainLoop(38, 120)
	tu := newTestTUI(p)
	tu.strips = loadStripBook(filepath.Join(layoutDir(root), stripFileWindowOne), time.Now())
	return tu, p, filepath.Join(layoutDir(root), stripFileWindowOne)
}

func stripEmuRows(p *Pane) int { _, r := p.emuSize(); return r }

// AC2, replace: the list goes on the pane, into the file (0600, whole), and
// the agent's terminal gives up the strip's rows; a second call replaces it.
func TestStripIPC_SetsStoresAndResizes_AndReplaces(t *testing.T) {
	tu, p, path := stripRig(t)
	full := stripEmuRows(p)
	resp := stripCall(t, tu.handleIPCStrip, IPCRequest{Action: "strip", Target: "super", Items: []string{"one", "two", "three"}})
	if !resp.OK || resp.Data != "Set 3 item(s) in the strip for super." {
		t.Fatalf("reply %+v", resp)
	}
	if got := p.StripItems(); !reflect.DeepEqual(got, []string{"one", "two", "three"}) {
		t.Fatalf("pane items %q", got)
	}
	if got := stripEmuRows(p); got >= full {
		t.Fatalf("terminal %d rows, want fewer than %d", got, full)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file %v mode %v, want 0600", err, fi)
	}
	lists, err := parseStripFile(mustRead(t, path))
	if err != nil || !reflect.DeepEqual(lists["super"], []string{"one", "two", "three"}) {
		t.Fatalf("file holds %v (%v)", lists, err)
	}
	stripCall(t, tu.handleIPCStrip, IPCRequest{Action: "strip", Target: "super", Items: []string{"only"}})
	if got := p.StripItems(); !reflect.DeepEqual(got, []string{"only"}) {
		t.Fatalf("not replaced: %q", got)
	}
}

// AC2, clear: no items removes the strip, the file, and gives the rows back.
func TestStripIPC_ClearRemovesTheStripAndGivesTheRowsBack(t *testing.T) {
	tu, p, path := stripRig(t)
	full := stripEmuRows(p)
	stripCall(t, tu.handleIPCStrip, IPCRequest{Action: "strip", Target: "super", Items: []string{"one"}})
	resp := stripCall(t, tu.handleIPCStrip, IPCRequest{Action: "strip", Target: "super"})
	if !resp.OK || resp.Data != "Cleared the strip for super." {
		t.Fatalf("reply %+v", resp)
	}
	if p.StripItems() != nil || stripEmuRows(p) != full {
		t.Fatalf("items %q rows %d, want none and %d", p.StripItems(), stripEmuRows(p), full)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still there after the last list went: %v", err)
	}
}

// AC2, refusals: each is one line, and the pane and the file stay as they were.
func TestStripIPC_EachRefusalLeavesTheListUnchanged(t *testing.T) {
	tu, p, path := stripRig(t)
	stripCall(t, tu.handleIPCStrip, IPCRequest{Action: "strip", Target: "super", Items: []string{"keep me"}})
	before := string(mustRead(t, path))
	rows := stripEmuRows(p)
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = "item"
	}
	for name, c := range map[string]struct {
		req  IPCRequest
		want string
	}{
		"outside a pane": {IPCRequest{Action: "strip", Items: []string{"x"}}, "INITECH_AGENT is not set"},
		"11 items":       {IPCRequest{Action: "strip", Target: "super", Items: eleven}, "11 items; the limit is 10"},
		"301 characters": {IPCRequest{Action: "strip", Target: "super", Items: []string{strings.Repeat("a", 301)}}, "301 characters; the limit is 300"},
		"a tab":          {IPCRequest{Action: "strip", Target: "super", Items: []string{"a\tb"}}, "control character"},
		"a newline":      {IPCRequest{Action: "strip", Target: "super", Items: []string{"a\nb"}}, "control character"},
		"another pane":   {IPCRequest{Action: "strip", Target: "eng9", Items: []string{"x"}}, "has no pane in this window"},
	} {
		resp := stripCall(t, tu.handleIPCStrip, c.req)
		if resp.OK || !strings.Contains(resp.Error, c.want) || strings.Contains(resp.Error, "\n") {
			t.Errorf("%s: reply %+v, want one line with %q", name, resp, c.want)
		}
		if got := p.StripItems(); !reflect.DeepEqual(got, []string{"keep me"}) || stripEmuRows(p) != rows {
			t.Errorf("%s: list %q rows %d changed", name, got, stripEmuRows(p))
		}
		if string(mustRead(t, path)) != before {
			t.Errorf("%s: the file changed", name)
		}
	}
	// Twins at the limits: 10 items and 300 characters are taken.
	ten := eleven[:10]
	if resp := stripCall(t, tu.handleIPCStrip, IPCRequest{Action: "strip", Target: "super", Items: ten}); !resp.OK {
		t.Errorf("10 items refused: %+v", resp)
	}
	if resp := stripCall(t, tu.handleIPCStrip, IPCRequest{Action: "strip", Target: "super", Items: []string{"  " + strings.Repeat("a", 300) + "  "}}); !resp.OK {
		t.Errorf("300 characters with spaces at the ends refused: %+v", resp)
	}
}

// AC3: the list survives a pane restart (a new pane of the same agent) and a
// rig restart (the file read again); feed-it-nothing: a missing or empty file
// is no strips.
func TestStrip_SurvivesAPaneAndARigRestart(t *testing.T) {
	tu, _, path := stripRig(t)
	stripCall(t, tu.handleIPCStrip, IPCRequest{Action: "strip", Target: "super", Items: []string{"one", "two"}})

	setProcessStrips(tu.strips)
	defer setProcessStrips(nil)
	np := NewParkedPane(PaneConfig{Name: "super"}, 38, 120)
	if got := np.StripItems(); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("restarted pane items %q", got)
	}
	if _, rows := np.emuSize(); rows != 38-2 {
		t.Fatalf("restarted pane terminal %d rows, want 36", rows)
	}
	other := NewParkedPane(PaneConfig{Name: "eng1"}, 38, 120)
	if other.StripItems() != nil {
		t.Fatal("another agent's pane got a strip")
	}

	again := loadStripBook(path, time.Now())
	if got := again.get("super"); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("after a rig restart: %q", got)
	}
	for _, body := range []string{"", "  \n"} {
		empty := filepath.Join(t.TempDir(), "strip.yaml")
		os.WriteFile(empty, []byte(body), 0o600)
		if b := loadStripBook(empty, time.Now()); len(b.all()) != 0 {
			t.Errorf("empty file %q gave %v", body, b.all())
		}
	}
	if b := loadStripBook(filepath.Join(t.TempDir(), "none.yaml"), time.Now()); len(b.all()) != 0 {
		t.Error("a missing file gave strips")
	}
}

// D3: a file that does not parse, or has another version, is set aside whole
// and never read; twin: a good file is read.
func TestStripBook_ABadFileIsSetAsideNotRead(t *testing.T) {
	for name, body := range map[string]string{
		"not yaml":         "version: 1\nagents: [\n",
		"another version":  "version: 2\nagents:\n  super: [one]\n",
		"an item too long": "version: 1\nagents:\n  super: [" + strings.Repeat("a", 301) + "]\n",
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "strip.yaml")
		os.WriteFile(path, []byte(body), 0o600)
		now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
		b := loadStripBook(path, now)
		if len(b.all()) != 0 {
			t.Errorf("%s: read %v", name, b.all())
		}
		if got, err := os.ReadFile(path + ".bad-20261119T120000Z"); err != nil || string(got) != body {
			t.Errorf("%s: not set aside whole: %v", name, err)
		}
	}
	good := filepath.Join(t.TempDir(), "strip.yaml")
	os.WriteFile(good, []byte("version: 1\nagents:\n  super: [one, two]\n"), 0o600)
	if got := loadStripBook(good, time.Now()).get("super"); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("good file read as %q", got)
	}
}

// AC3: removing the agent removes its list.
func TestStrip_RemovingTheAgentRemovesItsList(t *testing.T) {
	_, p, path := stripRig(t)
	tu := newTestTUI(p, newEmuPane("eng1", 120, 38)) // the last agent cannot be removed
	tu.strips = loadStripBook(path, time.Now())
	stripCall(t, tu.handleIPCStrip, IPCRequest{Action: "strip", Target: "super", Items: []string{"one"}})
	tu.strips.set("eng1", []string{"stays"})
	if err := tu.removePane("super"); err != nil {
		t.Fatal(err)
	}
	lists, _ := parseStripFile(mustRead(t, path))
	if _, had := lists["super"]; had || !reflect.DeepEqual(lists["eng1"], []string{"stays"}) {
		t.Fatalf("file after removal: %v", lists)
	}
}

// D5: a strip change during a send's typing resizes only after the send: the
// list is stored at once, the terminal keeps its size while the send holds
// the pane, and takes the strip's rows the moment it lets go.
func TestStrip_NoResizeWhileASendIsTyping(t *testing.T) {
	tu, p, _ := stripRig(t)
	full := stripEmuRows(p)
	stored := make(chan struct{}, 1)
	tu.strips.onChange = func(string, []string) { stored <- struct{}{} }

	p.sendMu.Lock() // a send is typing
	done := make(chan IPCResponse, 1)
	go func() {
		done <- stripCall(t, tu.handleIPCStrip, IPCRequest{Action: "strip", Target: "super", Items: []string{"one", "two"}})
	}()
	<-stored
	if got := stripEmuRows(p); got != full {
		p.sendMu.Unlock()
		t.Fatalf("terminal resized to %d rows while a send held the pane", got)
	}
	select {
	case r := <-done:
		p.sendMu.Unlock()
		t.Fatalf("the change finished during the send: %+v", r)
	default:
	}
	p.sendMu.Unlock() // the send's Enter is in
	if r := <-done; !r.OK {
		t.Fatalf("reply %+v", r)
	}
	if got := stripEmuRows(p); got >= full {
		t.Fatalf("terminal %d rows after the send, want fewer than %d", got, full)
	}
}

// The serve daemon sets a strip for the panes it hosts, by the same rules.
func TestStripIPC_TheServeDaemonSetsItsPanes(t *testing.T) {
	p := newEmuPane("eng1", 100, 30)
	p.ResizeExact(30, 100)
	root := t.TempDir()
	d := &Daemon{headless: true, panes: []*Pane{p},
		strips: loadStripBook(filepath.Join(layoutDir(root), stripFileServe), time.Now())}
	full := stripEmuRows(p)
	if resp := stripCall(t, d.handleIPCStrip, IPCRequest{Action: "strip", Target: "eng1", Items: []string{"one"}}); !resp.OK {
		t.Fatalf("reply %+v", resp)
	}
	if got := stripEmuRows(p); got != full-1 {
		t.Fatalf("terminal %d rows, want %d", got, full-1)
	}
	if resp := stripCall(t, d.handleIPCStrip, IPCRequest{Action: "strip", Target: "eng1", Items: make([]string, 0, 1)}); !resp.OK || p.StripItems() != nil {
		t.Fatalf("clear: %+v %q", resp, p.StripItems())
	}
	if resp := stripCall(t, d.handleIPCStrip, IPCRequest{Action: "strip", Target: "nobody", Items: []string{"x"}}); resp.OK {
		t.Fatal("a pane the daemon does not host was set")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
