package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ini-x432.1: do-not-disturb part 1. A send to a pane in do-not-disturb waits
// on the pane's queue, in order, with nothing typed; dnd off drains it through
// the normal path; a full queue refuses the new message and keeps the oldest;
// mail that cannot be delivered after release is surfaced once with its wait.

// dndPane is heldMailPane with the PTY's writes kept, so a test can read what
// reached the agent's box.
func dndPane(t *testing.T, name string) (*Pane, chan AgentEvent, func() string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	var mu sync.Mutex
	var typed strings.Builder
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			mu.Lock()
			typed.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	events := make(chan AgentEvent, 256)
	p := testPane(name)
	p.ptmx = &filePty{w}
	p.eventCh = events
	p.startupSettled = true
	emu := p.emu
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { emu.Close() })
	return p, events, func() string {
		mu.Lock()
		defer mu.Unlock()
		return typed.String()
	}
}

// dndCall runs one IPC handler on a pipe and returns its reply.
func dndCall(t *testing.T, handle func(net.Conn)) IPCResponse {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { server.Close(); client.Close() })
	go func() { handle(server); server.Close() }()
	client.SetReadDeadline(time.Now().Add(20 * time.Second))
	line, err := bufio.NewReader(client).ReadBytes('\n')
	if err != nil {
		t.Fatalf("no reply: %v", err)
	}
	var resp IPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("reply %q: %v", line, err)
	}
	return resp
}

// dndSet runs the dnd action against tu for agent.
func dndSet(t *testing.T, tu *TUI, agent, onOff string) IPCResponse {
	t.Helper()
	return dndCall(t, func(c net.Conn) { dispatchIPC(tu, c, IPCRequest{Action: "dnd", Target: agent, Text: onOff}, nil) })
}

func dndQueue(p *Pane) []QueuedMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]QueuedMessage(nil), p.messageQueue...)
}

// dndWait polls cond for up to d.
func dndWait(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// AC1, through the TUI's own send handler: the sender gets the held line, OK
// with the held verdict; the box receives nothing; the queue holds the
// message with the do-not-disturb mark. At base the text was typed in.
func TestDND_ASendIsHeldAndNothingIsTyped(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	if r := dndSet(t, tu, "a", "on"); !r.OK {
		t.Fatalf("dnd on: %+v", r)
	}
	resp := dndCall(t, func(c net.Conn) { tu.handleIPCSend(c, IPCRequest{Action: "send", Target: "a", Text: "[from b] one", Enter: true}) })
	if !resp.OK || resp.Delivery != DeliveryHeld || resp.Data != "held: a is in do-not-disturb" {
		t.Errorf("reply %+v, want OK, delivery held, data %q", resp, "held: a is in do-not-disturb")
	}
	time.Sleep(200 * time.Millisecond)
	if got := typed(); strings.Contains(got, "one") {
		t.Errorf("the box received %q while in do-not-disturb", got)
	}
	q := dndQueue(p)
	if len(q) != 1 || q[0].Text != "[from b] one" || !q[0].Enter || !q[0].DND || q[0].HeldForDialog {
		t.Errorf("queue %+v, want the one message marked do-not-disturb", q)
	}
	for _, ev := range tu.eventLog {
		if ev.Type == EventMessageSent {
			t.Errorf("event log says it was sent: %q", ev.Detail)
		}
	}
}

// AC2: dnd off delivers the held messages in arrival order through the
// normal path, mail a dialog held before do-not-disturb first; the queue is
// empty after.
func TestDND_OffDeliversEverythingInArrivalOrder(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	p.enqueueHeld("[from c] zero", true) // a dialog held it before dnd on
	dndSet(t, tu, "a", "on")
	for _, s := range []string{"one", "two", "three"} {
		if v := p.SendTextVerdict("[from b] "+s, true); v.Outcome != DeliveryHeld {
			t.Fatalf("send %s: %+v, want held", s, v)
		}
	}
	if got := typed(); got != "" {
		t.Fatalf("typed %q before dnd off", got)
	}
	resp := dndSet(t, tu, "a", "off")
	if !resp.OK || !strings.Contains(resp.Data, "3 held messages delivering in order") {
		t.Errorf("dnd off reply %+v, want the three it held named", resp)
	}
	if !dndWait(15*time.Second, func() bool { return p.QueueLen() == 0 && strings.Contains(typed(), "three") }) {
		t.Fatalf("drain did not finish: queue %d, typed %q", p.QueueLen(), typed())
	}
	got := typed()
	order := []int{strings.Index(got, "zero"), strings.Index(got, "one"), strings.Index(got, "two"), strings.Index(got, "three")}
	for i := 1; i < len(order); i++ {
		if order[i-1] < 0 || order[i] < order[i-1] {
			t.Fatalf("delivered out of order (positions %v): %q", order, got)
		}
	}
	if p.InDND() {
		t.Error("still in do-not-disturb after off")
	}
}

// AC3: at the cap the new message is refused, not delivered, and the queue is
// unchanged with the oldest message still first.
func TestDND_AFullQueueRefusesTheNewMessageAndKeepsTheOldest(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	dndSet(t, tu, "a", "on")
	for i := range maxMessageQueue {
		if v := p.SendTextVerdict(fmt.Sprintf("m%02d", i), true); v.Outcome != DeliveryHeld {
			t.Fatalf("send %d: %+v, want held", i, v)
		}
	}
	before := dndQueue(p)
	resp := dndCall(t, func(c net.Conn) { tu.handleIPCSend(c, IPCRequest{Action: "send", Target: "a", Text: "one too many", Enter: true}) })
	if resp.OK || resp.Delivery != DeliveryNotDelivered || resp.Error != "not delivered to a: do-not-disturb queue is full" {
		t.Errorf("reply %+v, want not delivered: do-not-disturb queue is full", resp)
	}
	after := dndQueue(p)
	if len(after) != maxMessageQueue || after[0].Text != "m00" || after[len(after)-1].Text != before[len(before)-1].Text {
		t.Errorf("queue changed: %d messages, first %q", len(after), after[0].Text)
	}
	if strings.Contains(typed(), "one too many") {
		t.Error("the refused message was typed")
	}
}

// While do-not-disturb is on, no drain types the queue: not the read loop's,
// not the maintenance tick's, not one already started.
func TestDND_NoDrainRunsWhileItIsOn(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	p.enqueueHeld("[from c] dialog mail", true)
	dndSet(t, tu, "a", "on")
	p.maybeDrainModalQueue()
	tu.modalMaintenance(time.Now())
	p.drainModalQueue()
	time.Sleep(300 * time.Millisecond)
	if got := typed(); got != "" {
		t.Errorf("a drain typed %q while in do-not-disturb", got)
	}
	if p.QueueLen() != 1 {
		t.Errorf("queue %d, want the dialog mail still waiting", p.QueueLen())
	}
}

// A drain's own message that meets do-not-disturb (set again mid-drain) goes
// back to the FRONT with its time, and the drain stops.
func TestDND_ADrainMessageThatMeetsItGoesBackToTheFront(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	held := time.Now().Add(-time.Minute)
	first := QueuedMessage{Text: "first", Enter: true, Time: held, DND: true}
	p.enqueue(QueuedMessage{Text: "second", Enter: true, Time: time.Now(), DND: true})
	p.beginRedelivery(first) // popped by a drain
	p.startDND()
	if v := p.SendTextVerdict(first.Text, first.Enter); v.Outcome != DeliveryHeld {
		t.Fatalf("verdict %+v, want held", v)
	}
	if p.endRedelivery() {
		t.Error("the drain was told its message went through; want handed back")
	}
	q := dndQueue(p)
	if len(q) != 2 || q[0].Text != "first" || !q[0].Time.Equal(held) || q[1].Text != "second" {
		t.Errorf("queue %+v, want first back at the front with its time", q)
	}
	if typed() != "" {
		t.Errorf("typed %q", typed())
	}
}

// AC4: a released message that then waits behind a dialog gets the dialog
// deadline counted from its release, and is surfaced once with its whole
// wait; a dead pane's held mail is surfaced once too.
func TestDND_UndeliverableHeldMailIsSurfacedOnceWithItsWait(t *testing.T) {
	p, events, _ := dndPane(t, "a")
	tu := newTestTUI(p)
	arrived := time.Now().Add(-30 * time.Minute)
	p.startDND()
	p.enqueue(QueuedMessage{Text: "[from b] waited", Enter: true, Time: arrived, DND: true})
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()
	dndSet(t, tu, "a", "off") // the dialog keeps the drain from popping
	time.Sleep(200 * time.Millisecond)
	q := dndQueue(p)
	if len(q) != 1 || q[0].Released.IsZero() {
		t.Fatalf("queue %+v, want the message waiting, marked released", q)
	}
	released := q[0].Released
	if n := p.resolveHeldMail(released.Add(heldMessageDeadline - time.Second)); n != 0 {
		t.Errorf("surfaced %d before the dialog deadline from release; the 30m it waited in do-not-disturb is not the dialog's", n)
	}
	if n := p.resolveHeldMail(released.Add(heldMessageDeadline)); n != 1 {
		t.Fatalf("surfaced %d at the deadline, want 1", n)
	}
	got := undelivered(events)
	if len(got) != 1 || !strings.Contains(got[0].Detail, "held 40m") || !strings.Contains(got[0].Detail, "[from b] waited") {
		t.Errorf("reports %+v, want one naming its 40m wait and the message", got)
	}
	if n := p.resolveHeldMail(released.Add(2 * heldMessageDeadline)); n != 0 {
		t.Errorf("surfaced again: %d", n)
	}

	// A dead pane in do-not-disturb: everything held is surfaced, once.
	d, devents, _ := dndPane(t, "d")
	d.startDND()
	d.SendTextVerdict("[from b] lost?", true)
	d.mu.Lock()
	d.alive = false
	d.mu.Unlock()
	if n := d.resolveHeldMail(time.Now()); n != 1 {
		t.Fatalf("dead pane surfaced %d, want 1", n)
	}
	if got := undelivered(devents); len(got) != 1 || !strings.Contains(got[0].Detail, "process exited") {
		t.Errorf("dead pane reports %+v", got)
	}
	if n := d.resolveHeldMail(time.Now()); n != 0 {
		t.Errorf("dead pane surfaced again: %d", n)
	}
}

// Feed it nothing: dnd off when never on, an empty queue drained, a send with
// an empty body, a bad word, no target, an unknown agent, an agent another
// process owns.
func TestDND_TheActionWithNothingToDo(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	tu.panes = append(tu.panes, &RemotePane{name: "far", alive: true})

	if r := dndSet(t, tu, "a", "off"); !r.OK || r.Data != "a was not in do-not-disturb" {
		t.Errorf("off when never on: %+v", r)
	}
	dndSet(t, tu, "a", "on")
	if r := dndSet(t, tu, "a", "on"); !r.OK || !p.InDND() {
		t.Errorf("on twice: %+v", r)
	}
	if r := dndSet(t, tu, "a", "off"); !r.OK || !strings.Contains(r.Data, "0 held messages") {
		t.Errorf("off with an empty queue: %+v", r)
	}
	time.Sleep(200 * time.Millisecond)
	if typed() != "" {
		t.Errorf("an empty drain typed %q", typed())
	}

	dndSet(t, tu, "a", "on")
	if v := p.SendTextVerdict("", true); v.Outcome != DeliveryHeld {
		t.Errorf("empty body: %+v, want held", v)
	}
	if q := dndQueue(p); len(q) != 1 || q[0].Text != "" || !q[0].DND {
		t.Errorf("empty body queue %+v", q)
	}
	dndSet(t, tu, "a", "off")
	if !dndWait(10*time.Second, func() bool { return p.QueueLen() == 0 }) {
		t.Errorf("empty body never drained: queue %d", p.QueueLen())
	}

	for _, c := range []struct {
		req  IPCRequest
		want string
	}{
		{IPCRequest{Action: "dnd", Target: "a", Text: "maybe"}, `dnd takes on or off, not "maybe"`},
		{IPCRequest{Action: "dnd", Text: "on"}, "target is required"},
		{IPCRequest{Action: "dnd", Target: "ghost", Text: "on"}, `pane "ghost" not found`},
		{IPCRequest{Action: "dnd", Target: "far", Text: "on"}, "far runs in another window or on another machine; run initech dnd there"},
	} {
		r := dndCall(t, func(conn net.Conn) { dispatchIPC(tu, conn, c.req, nil) })
		if r.OK || r.Error != c.want {
			t.Errorf("%+v: %+v, want error %q", c.req, r, c.want)
		}
	}
}

// The operator's paste from another window is typing, not a send: it goes in.
func TestDND_TheOperatorsPasteGoesIn(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	p.startDND()
	p.SendOperatorText("operator words", false)
	if !dndWait(5*time.Second, func() bool { return strings.Contains(typed(), "operator words") }) {
		t.Errorf("operator paste held: typed %q, queue %d", typed(), p.QueueLen())
	}
}

// A pane on another machine answers from its own process with the same
// words: the serve daemon's send and the window server's both pass through
// the same primitive.
func TestDND_AServedPaneAnswersWithTheSameWords(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	p.startDND()
	for _, resp := range []ControlResp{servedSend(p, "[from b] far", true), verdictSend(p, "[from b] near", true)} {
		if !resp.OK || resp.Delivery != DeliveryHeld || resp.Data != "held: a is in do-not-disturb" {
			t.Errorf("reply %+v, want held with the held line", resp)
		}
	}
	for p.QueueLen() < maxMessageQueue {
		p.enqueue(QueuedMessage{Text: "x", Time: time.Now(), DND: true})
	}
	if resp := servedSend(p, "[from b] full", true); resp.OK || resp.Delivery != DeliveryNotDelivered || resp.Error != dndQueueFullReason {
		t.Errorf("full reply %+v", resp)
	}
	if typed() != "" {
		t.Errorf("typed %q", typed())
	}
}

// AC6: on and off write nothing: the layout and fleet-state files are
// byte-identical before and after.
func TestDND_NothingIsWrittenToDisk(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		filepath.Join(dir, ".initech", "layout.yaml"):      []byte("panes: [a]\n"),
		filepath.Join(dir, ".initech", "fleet-state.yaml"): []byte("agents: {}\n"),
	}
	os.MkdirAll(filepath.Join(dir, ".initech"), 0o755)
	for f, b := range files {
		os.WriteFile(f, b, 0o644)
	}
	t.Chdir(dir)
	p, _, _ := dndPane(t, "a")
	tu := newTestTUI(p)
	dndSet(t, tu, "a", "on")
	p.SendTextVerdict("[from b] one", true)
	dndSet(t, tu, "a", "off")
	dndWait(10*time.Second, func() bool { return p.QueueLen() == 0 })
	for f, b := range files {
		got, err := os.ReadFile(f)
		if err != nil || string(got) != string(b) {
			t.Errorf("%s changed: %q (%v)", f, got, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".initech"))
	if len(entries) != len(files) {
		t.Errorf("%d files in .initech, want %d", len(entries), len(files))
	}
}

// While do-not-disturb holds the queue, nothing in it is reported as kept by
// a dialog: not mail a dialog held before, not mail an earlier dnd off
// released and dnd on held again.
func TestDND_NothingIsSurfacedWhileItHolds(t *testing.T) {
	p, events, _ := dndPane(t, "a")
	long := time.Now().Add(-time.Hour)
	p.enqueue(QueuedMessage{Text: "dialog mail", Enter: true, Time: long, HeldForDialog: true})
	p.enqueue(QueuedMessage{Text: "dnd mail", Enter: true, Time: long, DND: true})
	p.latchDialogOpen("Claude needs your permission") // keeps the drains from popping
	p.noteDialogSighting()
	p.startDND()
	p.endDND(long.Add(time.Minute)) // released long ago
	p.startDND()                    // and held again
	if n := p.resolveHeldMail(time.Now()); n != 0 {
		t.Errorf("surfaced %d while in do-not-disturb; want 0", n)
	}
	if got := undelivered(events); len(got) != 0 {
		t.Errorf("reports %+v", got)
	}
	released := time.Now()
	p.endDND(released)
	if n := p.resolveHeldMail(released.Add(heldMessageDeadline - time.Second)); n != 0 {
		t.Errorf("surfaced %d before the deadline from the last release", n)
	}
	if n := p.resolveHeldMail(released.Add(heldMessageDeadline)); n != 2 {
		t.Errorf("surfaced %d at the deadline from the last release, want 2", n)
	}
}

// Held at once: a send to a pane in do-not-disturb does not first wait for a
// booting agent to settle.
func TestDND_HeldAtOnceWithoutTheStartupWait(t *testing.T) {
	old := startupSettleStable
	startupSettleStable = 3 * time.Second
	t.Cleanup(func() { startupSettleStable = old })
	p, _, _ := dndPane(t, "a")
	p.startupSettled = false
	p.startedAt = time.Now()
	p.lastOutputTime = time.Now()
	p.startDND()
	start := time.Now()
	if v := p.SendTextVerdict("[from b] now", true); v.Outcome != DeliveryHeld {
		t.Fatalf("verdict %+v, want held", v)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("held after %v; want at once, not after the startup wait", d)
	}
}

// Do-not-disturb set while a message waited out the operator's typing holds
// that message too.
func TestDND_SetWhileAMessageWaitsForTheOperatorsPause(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	p.noteOperatorKey()
	got := make(chan SendVerdict, 1)
	go func() { got <- p.SendTextVerdict("[from b] waited for a pause", true) }()
	if !dndWait(2*time.Second, func() bool { return p.HeldMessages() == 1 }) {
		t.Fatal("the message never waited in the typing hold")
	}
	p.startDND()
	select {
	case v := <-got:
		if v.Outcome != DeliveryHeld {
			t.Errorf("verdict %+v, want held", v)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the send never returned")
	}
	if strings.Contains(typed(), "waited for a pause") {
		t.Errorf("typed %q after do-not-disturb was set", typed())
	}
	if q := dndQueue(p); len(q) != 1 || !q[0].DND {
		t.Errorf("queue %+v", q)
	}
}

// The read loop's drain trigger starts nothing while do-not-disturb is on: it
// runs on every PTY read, and a drain started each time would only return.
func TestDND_TheReadLoopStartsNoDrainWhileOn(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	var started int
	p.safeGo = func(fn func()) { started++; fn() }
	p.enqueueHeld("[from c] dialog mail", true)
	p.startDND()
	p.maybeDrainModalQueue()
	if started != 0 {
		t.Errorf("%d drains started while in do-not-disturb, want 0", started)
	}
	p.endDND(time.Now())
	if started != 1 {
		t.Errorf("%d drains started by dnd off, want 1", started)
	}
}

// A drain that runs while do-not-disturb is on touches nothing: it pops no
// message only for the send path to put it back, which would also stamp the
// pane as just sent a message and keep it in live mode's view (live.go)
// for the whole quiet.
func TestDND_ADrainLeavesTheQueueAndTheMessageClockAlone(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	p.enqueueHeld("[from c] dialog mail", true)
	p.startDND()
	p.drainModalQueue()
	if got := p.LastMessageReceived(); !got.IsZero() {
		t.Errorf("the drain stamped a message received at %v while in do-not-disturb", got)
	}
	if q := dndQueue(p); len(q) != 1 || q[0].Text != "[from c] dialog mail" || typed() != "" {
		t.Errorf("queue %+v, typed %q", q, typed())
	}
}
