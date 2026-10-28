package tui

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// ini-x432.2: the held line carries how long the agent has been in
// do-not-disturb and how many messages are ahead; the list reply carries dnd
// and dnd_age_sec for every pane this process runs, and queued_count while it
// is on; patrol carries the age.

// AC1: with two held, the third sender reads 2 messages ahead, and the age
// grows with time.
func TestDNDStatus_TheLineCarriesTheAgeAndTheCountAhead(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	p.startDND()
	p.mu.Lock()
	p.dndSince = time.Now().Add(-12 * time.Minute)
	p.mu.Unlock()
	want := []string{
		"held: a is in do-not-disturb (12m, 0 messages ahead)",
		"held: a is in do-not-disturb (12m, 1 message ahead)",
		"held: a is in do-not-disturb (12m, 2 messages ahead)",
	}
	for i, w := range want {
		if v := p.SendTextVerdict("m", true); v.Outcome != DeliveryHeld || v.Reason != w {
			t.Errorf("send %d: %+v, want %q", i, v, w)
		}
	}
	p.mu.Lock()
	p.dndSince = time.Now().Add(-61 * time.Minute)
	p.mu.Unlock()
	if v := p.SendTextVerdict("m", true); v.Reason != "held: a is in do-not-disturb (1h, 3 messages ahead)" {
		t.Errorf("an hour on: %q", v.Reason)
	}
}

// The age is from when it went on: on twice does not restart the clock; off
// and on again does.
func TestDNDStatus_TheAgeCountsFromWhenItWentOn(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	p.startDND()
	p.mu.Lock()
	p.dndSince = time.Now().Add(-5 * time.Minute)
	p.mu.Unlock()
	p.startDND()
	if age, on := p.dndAge(time.Now()); !on || age < 5*time.Minute {
		t.Errorf("on twice: age %v (%v), want the first on's 5m", age, on)
	}
	p.endDND(time.Now())
	if _, on := p.dndAge(time.Now()); on {
		t.Error("age reported after off")
	}
	p.startDND()
	if age, on := p.dndAge(time.Now()); !on || age > time.Minute {
		t.Errorf("on again: age %v (%v), want a fresh clock", age, on)
	}
}

func dndListEntry(t *testing.T, tu *TUI, name string) map[string]any {
	t.Helper()
	resp := dndCall(t, func(c net.Conn) { dispatchIPC(tu, c, IPCRequest{Action: "list"}, nil) })
	var raw []map[string]any
	if err := json.Unmarshal([]byte(resp.Data), &raw); err != nil {
		t.Fatal(err)
	}
	for _, e := range raw {
		if e["name"] == name {
			return e
		}
	}
	t.Fatalf("%s not listed", name)
	return nil
}

// AC3 at the source: dnd true with its age and the held count while on;
// dnd false and no age when off or never on; a pane another process runs
// carries no dnd at all, so nothing is guessed for it.
func TestDNDStatus_TheListSaysWhoIsInIt(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	q, _, _ := dndPane(t, "b")
	tu := newTestTUI(p, q)
	tu.panes = append(tu.panes, &RemotePane{name: "far", host: "box", alive: true})
	dndSet(t, tu, "a", "on")
	p.mu.Lock()
	p.dndSince = time.Now().Add(-90 * time.Second)
	p.mu.Unlock()
	p.SendTextVerdict("one", true)
	p.SendTextVerdict("two", true)

	a := dndListEntry(t, tu, "a")
	if a["dnd"] != true || a["queued_count"] != float64(2) {
		t.Errorf("a while on: %v", a)
	}
	if age, ok := a["dnd_age_sec"].(float64); !ok || age < 90 || age > 95 {
		t.Errorf("a dnd_age_sec %v, want about 90", a["dnd_age_sec"])
	}
	b := dndListEntry(t, tu, "b")
	if v, ok := b["dnd"]; !ok || v != false {
		t.Errorf("b never on: dnd %v (present %v), want false", v, ok)
	}
	if _, ok := b["dnd_age_sec"]; ok {
		t.Errorf("b carries an age: %v", b)
	}
	if _, ok := dndListEntry(t, tu, "far")["dnd"]; ok {
		t.Error("a remote pane carries dnd; this process cannot know it")
	}

	dndSet(t, tu, "a", "off")
	dndWait(10*time.Second, func() bool { return p.QueueLen() == 0 })
	a = dndListEntry(t, tu, "a")
	if a["dnd"] != false || a["dnd_age_sec"] != nil || a["queued_count"] != nil {
		t.Errorf("a after off: %v", a)
	}
}

// The serve daemon's list says the same for its panes.
func TestDNDStatus_TheDaemonListSaysItToo(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	d := &Daemon{panes: []*Pane{p}}
	p.startDND()
	p.SendTextVerdict("one", true)
	panes, _ := d.AllPanes()
	if len(panes) != 1 || panes[0].DND == nil || !*panes[0].DND || panes[0].DNDAgeSec == nil || panes[0].QueuedCount != 1 {
		t.Errorf("daemon list %+v", panes)
	}
}

// Patrol carries the age while on, nothing when off.
func TestDNDStatus_PatrolCarriesTheAge(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	q, _, _ := dndPane(t, "b")
	tu := newTestTUI(p, q)
	p.startDND()
	resp := dndCall(t, func(c net.Conn) { tu.handleIPCPatrol(c, IPCRequest{Action: "patrol"}) })
	var raw []map[string]any
	if err := json.Unmarshal([]byte(resp.Data), &raw); err != nil {
		t.Fatal(err)
	}
	for _, e := range raw {
		_, has := e["dnd_age_sec"]
		if want := e["name"] == "a"; has != want {
			t.Errorf("%v: dnd_age_sec present %v, want %v", e["name"], has, want)
		}
	}
}
