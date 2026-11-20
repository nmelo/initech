package tui

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

// ini-xmlr: the list reply carries each agent's list strip, the one place
// initech status --json (and so GET /v1/status) reads it. A pane this
// process runs always says: its items, or [] for none. A pane another
// process runs says only what this window holds: its items, or null, since
// an older daemon sends no strips and none cannot be told from unsent.

// stripOfReply is the "strip" value each agent's list entry carries on the
// wire, and whether the key is there at all.
func stripOfReply(t *testing.T, panes []PaneInfo) map[string]any {
	t.Helper()
	raw, err := json.Marshal(panes)
	if err != nil {
		t.Fatal(err)
	}
	var objs []map[string]any
	if err := json.Unmarshal(raw, &objs); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for _, o := range objs {
		v, ok := o["strip"]
		if !ok {
			v = "absent"
		}
		out[o["name"].(string)] = v
	}
	return out
}

func TestStripStatus_TheWindowsListCarriesEveryPanesStrip(t *testing.T) {
	withList := newEmuPane("eng1", 80, 24)
	withList.setStrip([]string{"one", "two ▶", "three"})
	none := newEmuPane("eng2", 80, 24)
	codex := newEmuPane("eng3", 80, 24)
	codex.agentType = config.AgentTypeCodex
	tu := newTestTUI(withList, none, codex)

	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	held := NewRemotePane("mac2:eng8", "mac2", a, drainedMux(t), 80, 24)
	held.SetStrip([]string{"remote item"})
	c, d := net.Pipe()
	defer c.Close()
	defer d.Close()
	unheld := NewRemotePane("mac2:eng9", "mac2", c, drainedMux(t), 80, 24)
	tu.panes = append(tu.panes, held, unheld)

	panes, ok := tu.AllPanes()
	if !ok {
		t.Fatal("no list")
	}
	want := map[string]any{
		"eng1":      []any{"one", "two ▶", "three"},
		"eng2":      []any{},
		"eng3":      []any{},
		"mac2:eng8": []any{"remote item"},
		"mac2:eng9": nil,
	}
	if got := stripOfReply(t, panes); !reflect.DeepEqual(got, want) {
		t.Errorf("list strips:\n got  %#v\n want %#v", got, want)
	}
}

// The reply is a copy: a later change to the pane's list does not reach a
// reply already built.
func TestStripStatus_TheReplyIsACopy(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	p.setStrip([]string{"one"})
	panes, _ := newTestTUI(p).AllPanes()
	(*panes[0].Strip)[0] = "changed"
	if got := p.StripItems(); !reflect.DeepEqual(got, []string{"one"}) {
		t.Errorf("the pane's list became %q through the reply", got)
	}
}

// The serve daemon's list says the same for the panes it runs.
func TestStripStatus_TheServeDaemonsListCarriesIt(t *testing.T) {
	withList := newEmuPane("eng1", 80, 24)
	withList.setStrip([]string{"a", "b"})
	none := newEmuPane("eng2", 80, 24)
	d := &Daemon{headless: true, panes: []*Pane{withList, none}}
	panes, ok := d.AllPanes()
	if !ok {
		t.Fatal("no list")
	}
	want := map[string]any{"eng1": []any{"a", "b"}, "eng2": []any{}}
	if got := stripOfReply(t, panes); !reflect.DeepEqual(got, want) {
		t.Errorf("daemon list strips: %#v, want %#v", got, want)
	}
}
