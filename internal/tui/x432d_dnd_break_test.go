package tui

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/config"
)

// ini-x432.4: the operator's send and inbox reply go through do-not-disturb;
// super's --urgent goes through and is logged in the TUI log and the event
// log; any other --urgent is refused with nothing queued; --urgent to an agent
// not in do-not-disturb is a plain send; the sender travels across windows
// and machines; keystrokes are never held.

func dndSend(t *testing.T, tu *TUI, from string, urgent bool, text string) IPCResponse {
	t.Helper()
	return dndCall(t, func(c net.Conn) {
		tu.handleIPCSend(c, IPCRequest{Action: "send", Target: "a", Text: text, Enter: true, From: from, Urgent: urgent})
	})
}

func dndEvents(events chan AgentEvent, needle string) int {
	n := 0
	for {
		select {
		case ev := <-events:
			if strings.Contains(ev.Detail, needle) {
				n++
			}
		default:
			return n
		}
	}
}

// AC1: a send with no agent identity (the operator's) lands now. Red at
// base: held.
func TestDNDBreak_TheOperatorsSendLandsNow(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	p.startDND()
	r := dndSend(t, tu, operatorSender, false, "[operator] look now")
	if !r.OK || r.Delivery == DeliveryHeld {
		t.Errorf("reply %+v, want delivered, not held", r)
	}
	if !strings.Contains(typed(), "[operator] look now") || p.QueueLen() != 0 {
		t.Errorf("typed %q queue %d, want it in the box", typed(), p.QueueLen())
	}
	if !p.InDND() {
		t.Error("the operator's send ended do-not-disturb")
	}
}

// AC1: an inbox reply from the operator's window lands now.
func TestDNDBreak_AnInboxReplyLandsNow(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	p.startDND()
	tu.deliverInboxReply("item-1", "a", "[reply] yes, go ahead")
	if !dndWait(5*time.Second, func() bool { return strings.Contains(typed(), "[reply] yes, go ahead") }) {
		t.Errorf("inbox reply held: typed %q, queue %d", typed(), p.QueueLen())
	}
}

// AC2: super --urgent lands now, with one line in the TUI log and one event.
func TestDNDBreak_SuperUrgentLandsAndIsLogged(t *testing.T) {
	logs := captureLogs(t)
	p, events, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	p.startDND()
	p.SendTextVerdictFrom("[from b] waiting", true, sendOrigin{From: "b"})
	r := dndSend(t, tu, "super", true, "[from super] P0: stop")
	if !r.OK || r.Delivery == DeliveryHeld || !strings.Contains(typed(), "[from super] P0: stop") {
		t.Errorf("reply %+v typed %q, want delivered now", r, typed())
	}
	const line = "urgent from super broke do-not-disturb on a"
	// The TUI log's own line; the event channel's debug record of the event
	// is not a second log line.
	if n := countLines(logs, "[dnd] "+line); n != 1 {
		t.Errorf("%d log lines %q, want 1", n, line)
	}
	if n := dndEvents(events, line); n != 1 {
		t.Errorf("%d events %q, want 1", n, line)
	}
	if q := dndQueue(p); len(q) != 1 || q[0].Text != "[from b] waiting" || !p.InDND() {
		t.Errorf("queue %+v (dnd %v): the break must leave the rest held", q, p.InDND())
	}
}

// AC3: --urgent from anyone but super is refused, exit 1 at the CLI, and
// nothing is queued; the queue is unchanged.
func TestDNDBreak_OtherUrgentIsRefusedAndNotQueued(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	p.startDND()
	p.SendTextVerdictFrom("[from b] first", true, sendOrigin{From: "b"})
	for _, from := range []string{"eng1", "ghost", ""} {
		r := dndSend(t, tu, from, true, "[from "+from+"] let me in")
		if r.OK || r.Delivery != DeliveryNotDelivered || r.Error != "not delivered to a: only super may break do-not-disturb" {
			t.Errorf("from %q: reply %+v, want the refusal", from, r)
		}
	}
	if q := dndQueue(p); len(q) != 1 || q[0].Text != "[from b] first" {
		t.Errorf("queue %+v, want it unchanged", q)
	}
	if strings.Contains(typed(), "let me in") {
		t.Errorf("a refused message was typed: %q", typed())
	}
}

// AC4: --urgent to an agent not in do-not-disturb is an ordinary send, from
// super or anyone, and logs nothing.
func TestDNDBreak_UrgentToAnAgentNotInItIsAPlainSend(t *testing.T) {
	logs := captureLogs(t)
	p, events, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	for _, from := range []string{"super", "eng1"} {
		if r := dndSend(t, tu, from, true, "[from "+from+"] plain"); !r.OK {
			t.Errorf("from %s: %+v", from, r)
		}
	}
	if !strings.Contains(typed(), "[from super] plain") || !strings.Contains(typed(), "[from eng1] plain") {
		t.Errorf("typed %q", typed())
	}
	if n := countLines(logs, "broke do-not-disturb"); n != 0 {
		t.Errorf("%d break lines logged for agents not in do-not-disturb", n)
	}
	if n := dndEvents(events, "broke do-not-disturb"); n != 0 {
		t.Errorf("%d break events", n)
	}
}

// A sender the CLI did not name (an initech older than this part) and an
// agent's plain send are held, as before; so is a name that is neither super
// nor an agent.
func TestDNDBreak_UnknownAndAgentSendersAreHeld(t *testing.T) {
	p, _, typed := dndPane(t, "a")
	tu := newTestTUI(p)
	p.startDND()
	for _, from := range []string{"", "b", "super", "ghost"} {
		if r := dndSend(t, tu, from, false, "[from "+from+"] wait"); r.Delivery != DeliveryHeld {
			t.Errorf("from %q: %+v, want held", from, r)
		}
	}
	if typed() != "" {
		t.Errorf("typed %q", typed())
	}
}

// Feed it nothing: super --urgent with an empty body goes through.
func TestDNDBreak_SuperUrgentWithAnEmptyBody(t *testing.T) {
	p, _, _ := dndPane(t, "a")
	tu := newTestTUI(p)
	p.startDND()
	if r := dndSend(t, tu, "super", true, ""); !r.OK || r.Delivery == DeliveryHeld {
		t.Errorf("reply %+v", r)
	}
	if p.QueueLen() != 0 {
		t.Errorf("queued %d", p.QueueLen())
	}
}

// The sender crosses windows and machines: a RemotePane's send carries it,
// a forward_send hands it to the owner, and the owner's served send judges
// by it.
func TestDNDBreak_TheSenderTravelsWithTheSend(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	mux := NewControlMux(client)
	defer mux.Close()
	rp := &RemotePane{name: "a", host: "box", alive: true, mux: mux}
	got := make(chan ControlCmd, 1)
	go func() {
		line, err := bufio.NewReader(server).ReadBytes('\n')
		if err != nil {
			return
		}
		var cmd ControlCmd
		json.Unmarshal(line, &cmd)
		got <- cmd
		writeJSON(server, ControlResp{ID: cmd.ID, OK: true, Delivery: DeliveryDelivered})
	}()
	rp.SendTextFrom("hi", true, sendOrigin{From: "super", Urgent: true})
	select {
	case cmd := <-got:
		if cmd.Action != "send" || cmd.From != "super" || !cmd.Urgent {
			t.Errorf("sent %+v, want from super, urgent", cmd)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing sent")
	}

	var seen sendOrigin
	pm := &peerManager{onForwardSend: func(_, _ string, _ bool, o sendOrigin) (SendVerdict, error) {
		seen = o
		return SendVerdict{Outcome: DeliveryDelivered}, nil
	}}
	pm.answerForwardSend("box", ControlResp{Action: "forward_send", Target: "a", Text: "x", Enter: true, From: operatorSender})
	if seen.From != operatorSender {
		t.Errorf("forward_send handed %+v, want the operator", seen)
	}

	p, _, typed := dndPane(t, "a")
	p.startDND()
	if r := servedSend(p, "[operator] remote", true, sendOrigin{From: operatorSender}); !r.OK || r.Delivery == DeliveryHeld {
		t.Errorf("served operator send %+v", r)
	}
	if r := servedSend(p, "[from eng1] remote", true, sendOrigin{From: "eng1", Urgent: true}); r.OK || r.Error != "only super may break do-not-disturb" {
		t.Errorf("served eng1 --urgent %+v", r)
	}
	if !strings.Contains(typed(), "[operator] remote") {
		t.Errorf("typed %q", typed())
	}
}

// AC5: keys typed into a pane in do-not-disturb reach it at once.
func TestDNDBreak_KeystrokesReachThePaneAtOnce(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	p := testPane("a")
	p.ptmx = &filePty{w}
	var mu sync.Mutex
	var keys strings.Builder
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := p.emu.Read(buf)
			mu.Lock()
			keys.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	go io.Copy(io.Discard, r) //nolint:errcheck
	t.Cleanup(func() { p.emu.Close() })
	p.startDND()
	for _, ch := range "hi" {
		p.SendKey(tcell.NewEventKey(tcell.KeyRune, ch, tcell.ModNone))
	}
	if !dndWait(2*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return strings.Contains(keys.String(), "hi") }) {
		t.Errorf("keys %q, want them at once", keys.String())
	}
	if p.QueueLen() != 0 {
		t.Errorf("a keystroke was queued")
	}
}

// Every relay carries the sender: the TUI's send to an agent another process
// runs, a forwarded send delivered here, the serve daemon's own send and its
// control-stream send, and the id-less forward_send event.
func TestDNDBreak_EveryRelayCarriesTheSender(t *testing.T) {
	// The TUI's send to a remote agent puts the sender on the control stream.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	mux := NewControlMux(client)
	defer mux.Close()
	rp := &RemotePane{name: "far", host: "box", alive: true, mux: mux}
	got := make(chan ControlCmd, 1)
	go func() {
		line, err := bufio.NewReader(server).ReadBytes('\n')
		if err != nil {
			return
		}
		var cmd ControlCmd
		json.Unmarshal(line, &cmd)
		got <- cmd
		writeJSON(server, ControlResp{ID: cmd.ID, OK: true, Delivery: DeliveryDelivered})
	}()
	tu := newTestTUI()
	dndCall(t, func(c net.Conn) {
		tu.sendViaRemote(c, rp, IPCRequest{Action: "send", Target: "far", Text: "x", Enter: true, From: "super", Urgent: true})
	})
	if cmd := <-got; cmd.From != "super" || !cmd.Urgent {
		t.Errorf("sendViaRemote sent %+v", cmd)
	}

	// A forwarded send delivered by this TUI is judged by its sender.
	p, _, typed := dndPane(t, "a")
	tu = newTestTUI(p)
	p.startDND()
	if v, err := tu.deliverForwardedSend("a", "[operator] forwarded", true, sendOrigin{From: operatorSender}); err != nil || v.Outcome == DeliveryHeld {
		t.Errorf("forwarded operator send: %+v %v", v, err)
	}

	// The serve daemon's own send.
	d := &Daemon{panes: []*Pane{p}, project: &config.Project{PeerName: "box"}, serve: true}
	r := dndCall(t, func(c net.Conn) {
		d.HandleSend(c, IPCRequest{Action: "send", Target: "a", Text: "[operator] daemon", Enter: true, From: operatorSender})
	})
	if !r.OK || r.Delivery == DeliveryHeld {
		t.Errorf("daemon send: %+v", r)
	}

	// The daemon's control-stream send.
	answered := make(chan ControlResp, 1)
	d.deliverOffReader(newTargetSerializer(), p, ControlCmd{ID: "1", Action: "send", Target: "a", Text: "[operator] stream", Enter: true, From: operatorSender},
		func(_ string, resp ControlResp) bool { answered <- resp; return true })
	if resp := <-answered; !resp.OK || resp.Delivery == DeliveryHeld {
		t.Errorf("control-stream send: %+v", resp)
	}
	for _, w := range []string{"[operator] forwarded", "[operator] daemon", "[operator] stream"} {
		if !dndWait(5*time.Second, func() bool { return strings.Contains(typed(), w) }) {
			t.Errorf("%q not typed: %q", w, typed())
		}
	}

	// The id-less forward_send event.
	s2, c2 := net.Pipe()
	defer s2.Close()
	defer c2.Close()
	mux2 := NewControlMux(c2)
	defer mux2.Close()
	seen := make(chan sendOrigin, 1)
	done := make(chan struct{})
	pm := &peerManager{quit: make(chan struct{}), onForwardSend: func(_, _ string, _ bool, o sendOrigin) (SendVerdict, error) {
		seen <- o
		return SendVerdict{}, nil
	}}
	go pm.consumeEvents("box", &peerConn{mux: mux2}, done)
	defer close(done)
	writeJSON(s2, ControlCmd{Action: "forward_send", Target: "a", Text: "x", Enter: true, From: "super", Urgent: true})
	select {
	case o := <-seen:
		if o.From != "super" || !o.Urgent {
			t.Errorf("id-less forward_send handed %+v", o)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("id-less forward_send never arrived")
	}
}

// The serve daemon's forward of a send to a connected client machine carries
// the sender too.
func TestDNDBreak_TheDaemonsForwardCarriesTheSender(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	d := &Daemon{project: &config.Project{PeerName: "box"},
		clients: map[string]net.Conn{"laptop": client}, clientCtrlMu: map[string]*sync.Mutex{"laptop": {}}}
	got := make(chan ControlCmd, 1)
	go func() {
		line, err := bufio.NewReader(server).ReadBytes('\n')
		if err != nil {
			return
		}
		var cmd ControlCmd
		json.Unmarshal(line, &cmd)
		got <- cmd
		d.fwdPendingMu.Lock()
		ch := d.fwdPending[cmd.ID]
		d.fwdPendingMu.Unlock()
		if ch != nil {
			ch <- ControlResp{ID: cmd.ID, OK: true, Delivery: DeliveryDelivered}
		}
	}()
	r := dndCall(t, func(c net.Conn) {
		d.HandleSend(c, IPCRequest{Action: "send", Target: "a", Host: "laptop", Text: "x", Enter: true, From: "super", Urgent: true})
	})
	if !r.OK {
		t.Errorf("reply %+v", r)
	}
	select {
	case cmd := <-got:
		if cmd.Action != "forward_send" || cmd.From != "super" || !cmd.Urgent {
			t.Errorf("forwarded %+v, want from super, urgent", cmd)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing forwarded")
	}
}
