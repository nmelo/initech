package tui

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// ini-pt3j: do-not-disturb holds for any length, or until turned off.

func pt3jOn(t *testing.T, tu *TUI, forSec int, untilOff bool) IPCResponse {
	t.Helper()
	return dndCall(t, func(c net.Conn) {
		dispatchIPC(tu, c, IPCRequest{Action: "dnd", Target: "a", Text: "on", ForSec: forSec, UntilOff: untilOff}, nil)
	})
}

// AC2: --until-off holds across turn ends, with no timer, until dnd off.
func TestDNDHold_UntilOffHoldsAcrossTurnEndsUntilOff(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	dndSetActivity(p, StateRunning)
	if r := pt3jOn(t, tu, 0, true); !r.OK || !strings.Contains(r.Data, "until off") {
		t.Fatalf("reply %+v, want it to name until off", r)
	}
	if v := p.SendTextVerdictFrom("[from b] HELD", true, sendOrigin{From: "b"}); v.Outcome != DeliveryHeld {
		t.Fatalf("send %+v, want held", v)
	}
	now := time.Now()
	for _, a := range []ActivityState{StateIdle, StateRunning, StateIdle} {
		dndSetActivity(p, a) // turn ends, a new turn, another turn end
		if p.endDNDIfDue(now.Add(10 * time.Hour)) {
			t.Fatalf("an until-off hold ended at a turn end (%v) or a time", a)
		}
	}
	if !p.InDND() || typed() != "" {
		t.Fatalf("in dnd %v, typed %q: want still held", p.InDND(), typed())
	}
	dndSet(t, tu, "a", "off")
	if !waitHeld(func() bool { return strings.Contains(typed(), "HELD") }) {
		t.Fatalf("dnd off did not deliver; typed %q", typed())
	}
}

// AC2: super's urgent send still breaks through an until-off hold.
func TestDNDHold_SuperStillBreaksAnUntilOffHold(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	dndSetActivity(p, StateRunning)
	pt3jOn(t, tu, 0, true)
	p.SendTextVerdictFrom("[from super] URGENT", true, sendOrigin{From: "super", Urgent: true})
	if !waitHeld(func() bool { return strings.Contains(typed(), "URGENT") }) {
		t.Fatalf("super's urgent send was held; typed %q", typed())
	}
}

// AC3: what a second dnd on does, and the reply naming what is in force.
func TestDNDHold_ASecondOnFollowsThePrecedence(t *testing.T) {
	cases := []struct {
		name               string
		first, second      func(*testing.T, *TUI) IPCResponse
		wantHold, wantWord string
	}{
		{"plain on keeps until-off",
			func(t *testing.T, tu *TUI) IPCResponse { return pt3jOn(t, tu, 0, true) },
			func(t *testing.T, tu *TUI) IPCResponse { return pt3jOn(t, tu, 0, false) },
			"off", "until off"},
		{"plain on keeps a timer",
			func(t *testing.T, tu *TUI) IPCResponse { return pt3jOn(t, tu, 1800, false) },
			func(t *testing.T, tu *TUI) IPCResponse { return pt3jOn(t, tu, 0, false) },
			"time", " until "},
		{"--for replaces until-off",
			func(t *testing.T, tu *TUI) IPCResponse { return pt3jOn(t, tu, 0, true) },
			func(t *testing.T, tu *TUI) IPCResponse { return pt3jOn(t, tu, 1800, false) },
			"time", " until "},
		{"--until-off replaces a timer",
			func(t *testing.T, tu *TUI) IPCResponse { return pt3jOn(t, tu, 1800, false) },
			func(t *testing.T, tu *TUI) IPCResponse { return pt3jOn(t, tu, 0, true) },
			"off", "until off"},
		{"a plain on alone ends at turn end",
			func(t *testing.T, tu *TUI) IPCResponse { return pt3jOn(t, tu, 0, false) },
			func(t *testing.T, tu *TUI) IPCResponse { return pt3jOn(t, tu, 0, false) },
			"", "until your turn ends"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, _, _ := dndPane(t, "a")
			tu := newTestTUI(p)
			dndSetActivity(p, StateRunning)
			c.first(t, tu)
			r := c.second(t, tu)
			hold := p.dndHold()
			switch c.wantHold {
			case "time":
				if _, err := time.Parse(time.RFC3339, hold); err != nil {
					t.Fatalf("hold %q, want a timer", hold)
				}
			default:
				if hold != c.wantHold {
					t.Fatalf("hold %q, want %q", hold, c.wantHold)
				}
			}
			if !strings.Contains(r.Data, c.wantWord) {
				t.Fatalf("reply %q, want it to name %q", r.Data, c.wantWord)
			}
		})
	}
}

// --for and --until-off together are refused by the TUI too.
func TestDNDHold_ForAndUntilOffTogetherAreRefused(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	tu := newTestTUI(p)
	r := pt3jOn(t, tu, 1800, true)
	if r.OK || !strings.Contains(r.Error, "--for and --until-off") || p.InDND() {
		t.Fatalf("reply %+v (in dnd %v), want refused naming both and nothing changed", r, p.InDND())
	}
}

// Feed it nothing: dnd off with no hold in force says so and changes nothing.
func TestDNDHold_OffWithNoHoldSaysSo(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	tu := newTestTUI(p)
	r := dndSet(t, tu, "a", "off")
	if !r.OK || !strings.Contains(r.Data, "was not in do-not-disturb") || p.InDND() {
		t.Fatalf("reply %+v, want it said and nothing changed", r)
	}
}

// 6a: the list reply carries dnd_until: "off", an RFC3339 time, or nothing.
func TestDNDHold_TheListReplyNamesHowTheHoldEnds(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	tu := newTestTUI(p)
	dndSetActivity(p, StateRunning)
	field := func() (string, bool) {
		b, _ := json.Marshal(PaneInfo{DNDUntil: dndUntilField(p)})
		var m map[string]any
		json.Unmarshal(b, &m)
		v, ok := m["dnd_until"]
		s, _ := v.(string)
		return s, ok
	}
	if v, ok := field(); ok {
		t.Fatalf("not in dnd: dnd_until %q sent, want omitted", v)
	}
	pt3jOn(t, tu, 0, false)
	if v, ok := field(); ok {
		t.Fatalf("turn-end hold: dnd_until %q sent, want omitted", v)
	}
	pt3jOn(t, tu, 0, true)
	if v, _ := field(); v != "off" {
		t.Fatalf("until-off: dnd_until %q, want off", v)
	}
	pt3jOn(t, tu, 1800, false)
	if v, _ := field(); func() error { _, err := time.Parse(time.RFC3339, v); return err }() != nil {
		t.Fatalf("timer: dnd_until %q, want an RFC3339 time", v)
	}
}

// 6a, through the real list action: the reply a status or patrol reads
// carries dnd_until for an until-off hold.
func TestDNDHold_TheListActionCarriesDndUntil(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	tu := newTestTUI(p)
	dndSetActivity(p, StateRunning)
	pt3jOn(t, tu, 0, true)
	r := dndCall(t, func(c net.Conn) { dispatchIPC(tu, c, IPCRequest{Action: "list"}, nil) })
	var panes []PaneInfo
	if err := json.Unmarshal([]byte(r.Data), &panes); err != nil || len(panes) != 1 {
		t.Fatalf("list %q: %v", r.Data, err)
	}
	if panes[0].DNDUntil != "off" {
		t.Fatalf("list reply dnd_until %q, want off", panes[0].DNDUntil)
	}
}
