package tui

import (
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ini-x432.3: do-not-disturb ends at turn end (the pane leaving running),
// or, when a timer was set, at the timer and not before; the timer is capped
// at an hour; a restart or a resume clears it and nothing reaches disk.

// dndTick runs the once-a-second maintenance over p at now.
func dndTick(p *Pane, now time.Time) {
	var last time.Time
	maintainModal([]PaneView{p}, now, &last)
}

func dndSetActivity(p *Pane, a ActivityState) {
	p.mu.Lock()
	p.activity = a
	p.mu.Unlock()
}

// AC1: running to idle, to waiting and to background each end it and drain;
// a pane that stays running keeps it. Red at base: it stays on.
func TestDNDEnd_TheTurnEndingEndsItAndDrains(t *testing.T) {
	for _, c := range []struct {
		name string
		end  func(t *testing.T, p *Pane)
	}{
		{"idle", func(t *testing.T, p *Pane) { dndSetActivity(p, StateIdle) }},
		{"waiting", func(t *testing.T, p *Pane) { dndSetActivity(p, StateWaitingInput) }},
		{"background", func(t *testing.T, p *Pane) {
			dndSetActivity(p, StateIdle)
			p.observeBackground(time.Now().Add(-time.Minute), true, parseFooter(lvxwFixture(t, "haiku310_landing_shell.txt"), 160))
			if p.Activity() != StateBackground {
				t.Fatalf("fixture reads %v, want background", p.Activity())
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, _, typed := dndPane(t, "a")
			dndSetActivity(p, StateRunning)
			p.startDND()
			p.SendTextVerdict("[from b] held", true)
			dndTick(p, time.Now())
			if !p.InDND() {
				t.Fatal("ended while the pane was still running")
			}
			c.end(t, p)
			dndTick(p, time.Now())
			if p.InDND() {
				t.Fatalf("still in do-not-disturb after the pane went %s", c.name)
			}
			if c.name == "waiting" {
				return // a dialog keeps the drain off until it closes: held mail as today
			}
			if !dndWait(10*time.Second, func() bool { return strings.Contains(typed(), "[from b] held") }) {
				t.Errorf("the held message was not drained: typed %q, queue %d", typed(), p.QueueLen())
			}
		})
	}
}

// AC2: a timer ends it at its time, drain included, and the turn ending
// before then does not.
func TestDNDEnd_ATimerEndsItAtItsTimeNotAtTurnEnd(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	dndSetActivity(p, StateRunning)
	r := dndCall(t, func(c net.Conn) {
		dispatchIPC(tu, c, IPCRequest{Action: "dnd", Target: "a", Text: "on", ForSec: 10}, nil)
	})
	if !r.OK || !strings.Contains(r.Data, "(10s)") || len(r.Notices) != 0 {
		t.Fatalf("on --for 10s: %+v", r)
	}
	until, ok := p.dndTimer()
	if !ok {
		t.Fatal("no timer set")
	}
	p.SendTextVerdict("[from b] held", true)
	dndSetActivity(p, StateIdle)
	dndTick(p, until.Add(-time.Second))
	if !p.InDND() {
		t.Fatal("the turn ending ended a timed do-not-disturb")
	}
	dndTick(p, until)
	if p.InDND() {
		t.Fatal("still on at the timer")
	}
	if !dndWait(10*time.Second, func() bool { return strings.Contains(typed(), "[from b] held") }) {
		t.Errorf("not drained at the timer: typed %q", typed())
	}
}

// AC3: a timer over an hour is cut to an hour, and the reply says so once.
func TestDNDEnd_ATimerIsCappedAtAnHour(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	tu := newTestTUI(p)
	dndSetActivity(p, StateRunning)
	before := time.Now()
	r := dndCall(t, func(c net.Conn) {
		dispatchIPC(tu, c, IPCRequest{Action: "dnd", Target: "a", Text: "on", ForSec: 2 * 3600}, nil)
	})
	if !r.OK || len(r.Notices) != 1 || r.Notices[0] != "do-not-disturb capped at 1h" {
		t.Errorf("reply %+v, want one capped notice", r)
	}
	until, _ := p.dndTimer()
	if d := until.Sub(before); d < time.Hour || d > time.Hour+5*time.Second {
		t.Errorf("timer %v from now, want an hour", d)
	}
	// Exactly an hour is not capped.
	r = dndCall(t, func(c net.Conn) {
		dispatchIPC(tu, c, IPCRequest{Action: "dnd", Target: "a", Text: "on", ForSec: 3600}, nil)
	})
	if len(r.Notices) != 0 {
		t.Errorf("an hour exactly: notices %v", r.Notices)
	}
}

// AC5: dnd on with a new --for resets the timer; without --for keeps it.
func TestDNDEnd_ReissuingOnResetsOrKeepsTheTimer(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	tu := newTestTUI(p)
	dndSetActivity(p, StateRunning)
	on := func(forSec int) IPCResponse {
		return dndCall(t, func(c net.Conn) {
			dispatchIPC(tu, c, IPCRequest{Action: "dnd", Target: "a", Text: "on", ForSec: forSec}, nil)
		})
	}
	on(600)
	first, _ := p.dndTimer()
	on(1200)
	second, _ := p.dndTimer()
	if d := second.Sub(first); d < 599*time.Second {
		t.Errorf("a new --for moved the timer by %v, want about 10m later", d)
	}
	r := on(0)
	third, ok := p.dndTimer()
	if !ok || !third.Equal(second) {
		t.Errorf("on without --for: timer %v (%v), want it kept at %v", third, ok, second)
	}
	if !strings.Contains(r.Data, "until") {
		t.Errorf("reply %q does not name the running timer", r.Data)
	}
	// Off then on without --for: no timer, turn end rules.
	dndSet(t, tu, "a", "off")
	on(0)
	if _, ok := p.dndTimer(); ok {
		t.Error("a timer survived dnd off")
	}
}

// AC6: a pane already idle when dnd on runs gets the notice, and the next
// tick ends it; a pane that is running gets no notice.
func TestDNDEnd_OnAtAnIdlePaneSaysItEndsAtTurnEnd(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	tu := newTestTUI(p)
	dndSetActivity(p, StateIdle)
	r := dndSet(t, tu, "a", "on")
	if !r.OK || len(r.Notices) != 1 || r.Notices[0] != "pane is idle; do-not-disturb ends at turn end" {
		t.Errorf("reply %+v, want the idle notice", r)
	}
	dndTick(p, time.Now())
	if p.InDND() {
		t.Error("an idle pane kept do-not-disturb past the next tick")
	}
	dndSetActivity(p, StateRunning)
	if r := dndSet(t, tu, "a", "on"); len(r.Notices) != 0 {
		t.Errorf("running pane got notices %v", r.Notices)
	}
	// An idle pane with a timer: the timer holds it, no notice.
	dndSet(t, tu, "a", "off")
	dndSetActivity(p, StateIdle)
	r = dndCall(t, func(c net.Conn) {
		dispatchIPC(tu, c, IPCRequest{Action: "dnd", Target: "a", Text: "on", ForSec: 60}, nil)
	})
	if len(r.Notices) != 0 {
		t.Errorf("idle pane with a timer got notices %v", r.Notices)
	}
	dndTick(p, time.Now())
	if !p.InDND() {
		t.Error("the turn rule ended a timed do-not-disturb")
	}
}

// The serve daemon ends it too: the ending lives in the shared maintenance.
func TestDNDEnd_TheDaemonTickEndsIt(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	d := &Daemon{panes: []*Pane{p}}
	dndSetActivity(p, StateIdle)
	p.startDND()
	d.maintain(time.Now())
	if p.InDND() {
		t.Error("the daemon's tick left do-not-disturb on an idle pane")
	}
}

// AC4: a restart hands the held mail to the new pane, which is not in
// do-not-disturb, with the mail marked released so the dialog deadline
// applies there; nothing about it is written to the layout or fleet state.
func TestDNDEnd_ARestartClearsItAndReleasesTheCarriedMail(t *testing.T) {
	old, _, _ := dndPane(t, "a")
	dialogMail := QueuedMessage{Text: "[from c] dialog", Enter: true, Time: time.Now().Add(-time.Minute), HeldForDialog: true}
	old.enqueue(dialogMail)
	old.startDND()
	old.SendTextVerdict("[from b] carried", true)
	fresh, _, _ := dndPane(t, "a")
	if n := carryQueueForRestart(old, fresh); n != 2 {
		t.Fatalf("carried %d, want 2", n)
	}
	if fresh.InDND() {
		t.Error("the restarted pane is in do-not-disturb")
	}
	q := dndQueue(fresh)
	if len(q) != 2 || !q[1].DND || q[1].Released.IsZero() {
		t.Errorf("carried mail %+v, want the held message marked released", q)
	}
	if len(q) == 2 && !q[0].Released.IsZero() {
		t.Errorf("dialog mail %+v got a release; its deadline runs from its arrival as today", q[0])
	}
}

// AC4, resume: the woken pane is a new object, not in do-not-disturb, and
// the wake takes the queue as it does today.
func TestDNDEnd_AResumeClearsIt(t *testing.T) {
	tu := newTestTUI()
	var spawned atomic.Int32
	tu.paneSpawner = fakePaneSpawner(t, &spawned)
	parked := NewParkedPane(PaneConfig{Name: "a", Command: []string{"sh", "-c", "echo up; sleep 5"}}, 10, 40)
	tu.panes = append(tu.panes, parked)
	parked.startDND()
	parked.enqueue(QueuedMessage{Text: "[from b] parked", Enter: true, Time: time.Now(), DND: true})
	if err := tu.resumePane(parked, "test"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	np, ok := tu.panes[0].(*Pane)
	if !ok || np == parked {
		t.Fatal("resume kept the parked pane object")
	}
	t.Cleanup(func() { np.Close() })
	if np.InDND() {
		t.Error("the woken pane is in do-not-disturb")
	}
	if parked.QueueLen() != 0 {
		t.Errorf("the wake left %d messages on the parked pane", parked.QueueLen())
	}
}

// AC4, disk: nothing about do-not-disturb can reach the saved layout or the
// fleet state: neither record has a field for it, at any depth.
func TestDNDEnd_NoDNDKeyOnDisk(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(LayoutState{}), reflect.TypeOf(FleetState{})} {
		for _, k := range dndYAMLKeys(typ, map[reflect.Type]bool{}) {
			if strings.Contains(k, "dnd") || strings.Contains(k, "disturb") {
				t.Errorf("%s has a do-not-disturb key %q", typ.Name(), k)
			}
		}
	}
}

// dndYAMLKeys is every field name and yaml key in typ, nested types included.
func dndYAMLKeys(typ reflect.Type, seen map[reflect.Type]bool) []string {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || seen[typ] {
		return nil
	}
	seen[typ] = true
	var out []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		out = append(out, strings.ToLower(f.Name), strings.ToLower(f.Tag.Get("yaml")))
		out = append(out, dndYAMLKeys(f.Type, seen)...)
	}
	return out
}

// dnd off on a pane that was never in do-not-disturb leaves dialog mail's
// deadline alone: it is not released by an off that ended nothing.
func TestDNDEnd_OffWhenNeverOnLeavesDialogMailsClock(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	tu := newTestTUI(p)
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()
	p.enqueueHeld("[from c] dialog mail", true)
	dndSet(t, tu, "a", "off")
	if q := dndQueue(p); len(q) != 1 || !q[0].Released.IsZero() {
		t.Errorf("queue %+v: an off that ended nothing released dialog mail", q)
	}
}
