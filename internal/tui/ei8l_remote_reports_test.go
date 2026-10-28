package tui

import (
	"bufio"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ini-ei8l: a serve daemon answers a send the way the TUI does (refused,
// deferred or OK), and forwards its agents' delivery reports (hold, release,
// NOT DELIVERED) to the connected initech as agent_event, which the client
// shows through its own event path as host:agent.

// ei8lServe is a serve daemon hosting one pane held behind a corroborated
// dialog, with one connected client, "hub", on a pipe. It returns the client
// end and the daemon's end of that pipe.
func ei8lServe(t *testing.T) (*Daemon, *Pane, net.Conn, net.Conn) {
	t.Helper()
	p := daemonPane(t)
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()
	d := &Daemon{
		serve:        true,
		reportQ:      make(chan AgentEvent, reportQueueDepth),
		panes:        []*Pane{p},
		clients:      map[string]net.Conn{},
		clientCtrlMu: map[string]*sync.Mutex{},
	}
	d.adoptReports(p)
	client, server := ei8lConnect(t, d, "hub")
	return d, p, client, server
}

// ei8lConnect registers a client for peer as the handshake does, replacing
// any earlier one (a reconnect), and returns both ends.
func ei8lConnect(t *testing.T, d *Daemon, peer string) (net.Conn, net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	d.sessionsMu.Lock()
	d.clients[peer] = server
	d.clientCtrlMu[peer] = &sync.Mutex{}
	d.sessionsMu.Unlock()
	return client, server
}

// ei8lRead collects every line the daemon writes to a client.
func ei8lRead(c net.Conn) <-chan ControlResp {
	out := make(chan ControlResp, 64)
	go func() {
		sc := NewIPCScanner(c)
		for sc.Scan() {
			var r ControlResp
			if json.Unmarshal(sc.Bytes(), &r) == nil {
				out <- r
			}
		}
	}()
	return out
}

// ei8lCollect returns the agent_event lines that arrive within d.
func ei8lCollect(lines <-chan ControlResp, d time.Duration) []ControlResp {
	var got []ControlResp
	deadline := time.After(d)
	for {
		select {
		case r := <-lines:
			if r.Action == agentEventAction {
				got = append(got, r)
			}
		case <-deadline:
			return got
		}
	}
}

// ei8lSend sends over the control stream and returns the answer to it.
func ei8lSend(t *testing.T, d *Daemon, client, server net.Conn) ControlResp {
	t.Helper()
	go d.handleControlStream(server, bufio.NewScanner(server), "hub")
	lines := ei8lRead(client)
	if err := writeJSON(client, ControlCmd{ID: "s1", Action: "send", Target: "eng1", Text: "[from super] held", Enter: true}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case r := <-lines:
			if r.ID == "s1" {
				return r
			}
		case <-deadline:
			t.Fatal("no answer to the send within 5s")
		}
	}
}

func ei8lForwarder(t *testing.T, d *Daemon) {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { d.runReportForwarder(stop); close(done) }()
	t.Cleanup(func() { close(stop); <-done })
}

// Criterion 1: a send held behind the dialog is answered deferred, with the
// latch age and queue depth, and the message is held.
func TestServeSend_HeldSendIsAnsweredDeferred(t *testing.T) {
	d, p, client, server := ei8lServe(t)
	resp := ei8lSend(t, d, client, server)
	if !resp.OK || !strings.HasPrefix(resp.Data, "deferred to eng1 — modal open") || !strings.Contains(resp.Data, "queued") {
		t.Fatalf("answer %+v, want OK with the deferred form and the queue depth", resp)
	}
	if q := p.QueuedMessageCount(); q != 1 {
		t.Errorf("%d held, want the message held", q)
	}
}

// The positive twin: no dialog, OK, not deferred, now carrying the send's
// verdict (ini-69fy): this pane draws no box, so it cannot confirm.
func TestServeSend_WithoutADialogTheAnswerIsOKWithItsVerdict(t *testing.T) {
	p := daemonPane(t)
	d := &Daemon{serve: true, panes: []*Pane{p}, clients: map[string]net.Conn{}, clientCtrlMu: map[string]*sync.Mutex{}}
	client, server := ei8lConnect(t, d, "hub")
	if resp := ei8lSend(t, d, client, server); !resp.OK || resp.Error != "" || resp.Delivery != DeliveryUnconfirmed || resp.Data != "no composer visible" {
		t.Fatalf("answer %+v, want OK, could not confirm, no composer visible", resp)
	}
}

// A stopped agent is refused before anything is typed, as the TUI refuses it.
func TestServeSend_AStoppedAgentIsRefused(t *testing.T) {
	p := daemonPane(t)
	p.Close()
	d := &Daemon{serve: true, panes: []*Pane{p}, clients: map[string]net.Conn{}, clientCtrlMu: map[string]*sync.Mutex{}}
	client, server := ei8lConnect(t, d, "hub")
	if resp := ei8lSend(t, d, client, server); resp.OK || !strings.Contains(resp.Error, "is stopped") {
		t.Fatalf("answer %+v, want the stopped refusal", resp)
	}
}

// Window 1's window server is the same Daemon type without serve: its panes
// get no report sink, and since ini-69fy (G7) it answers with the verdict, so
// a send to an agent another window shows hears it too.
func TestServeSend_TheWindowServerAnswersWithTheVerdict(t *testing.T) {
	p := daemonPane(t)
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()
	d := &Daemon{panes: []*Pane{p}, clients: map[string]net.Conn{}, clientCtrlMu: map[string]*sync.Mutex{}}
	d.adoptReports(p)
	if p.reportSink.Load() != nil {
		t.Error("a window-server pane got a report sink")
	}
	client, server := ei8lConnect(t, d, "hub")
	if resp := ei8lSend(t, d, client, server); !resp.OK || resp.Delivery != DeliveryDeferred || !strings.HasPrefix(resp.Data, "deferred to eng1 — modal open") {
		t.Fatalf("window server answer %+v, want OK, deferred, the deferral words", resp)
	}
}

// Criterion 1, the connected initech's half: the peer's deferral reaches the
// CLI's answer. Before this forwardSendToRemote dropped Data.
func TestForwardSendToRemote_PassesThePeersDeferral(t *testing.T) {
	r := newCrossRig(t)
	r.reply = func(ControlCmd) *ControlResp {
		return &ControlResp{OK: true, Data: "deferred to eng1 — modal open, 1 queued"}
	}
	resp := r.call(t, sendH, IPCRequest{Action: "send", Host: "workbench", Target: "eng1", Text: "hi", Enter: true})
	if !resp.OK || resp.Data != "deferred to eng1 — modal open, 1 queued" {
		t.Fatalf("IPC answer %+v, want OK carrying the peer's deferral", resp)
	}
	// An older daemon answers a bare OK before its verdict (ini-69fy item
	// 4): could not confirm, naming why, never delivered.
	r.reply = func(ControlCmd) *ControlResp { return &ControlResp{OK: true} }
	if resp := r.call(t, sendH, IPCRequest{Action: "send", Host: "workbench", Target: "eng1", Text: "hi", Enter: true}); !resp.OK || resp.Delivery != DeliveryUnconfirmed || resp.Data != "older initech on workbench" {
		t.Fatalf("IPC answer %+v from an older daemon, want could not confirm, older initech on workbench", resp)
	}
}

// Criterion 2: held mail past its deadline reaches the connected initech as
// one agent_event per message, NOT DELIVERED, for the agent.
func TestServeReports_NotDeliveredIsForwarded(t *testing.T) {
	d, p, client, _ := ei8lServe(t)
	lines := ei8lRead(client)
	ei8lForwarder(t, d)
	p.SendText("[from super] one", true)
	p.SendText("[from super] two", true)
	now := time.Now()
	d.maintain(now.Add(heldMessageDeadline + time.Minute))
	var nd []ControlResp
	for _, r := range ei8lCollect(lines, 500*time.Millisecond) {
		if strings.HasPrefix(r.Text, undeliveredSubmitPrefix) {
			nd = append(nd, r)
		}
	}
	if len(nd) != 2 {
		t.Fatalf("%d NOT DELIVERED agent_events, want one per held message (2)", len(nd))
	}
	for _, r := range nd {
		if r.Name != "eng1" || r.EventType != "stalled" || r.EventTimeMs == 0 {
			t.Errorf("agent_event %+v, want eng1, type stalled, a time", r)
		}
	}
}

// Criterion 3: the hold and the release forward the same way.
func TestServeReports_HoldAndReleaseAreForwarded(t *testing.T) {
	p := daemonPane(t)
	d := &Daemon{serve: true, reportQ: make(chan AgentEvent, reportQueueDepth), panes: []*Pane{p},
		clients: map[string]net.Conn{}, clientCtrlMu: map[string]*sync.Mutex{}}
	d.adoptReports(p)
	client, _ := ei8lConnect(t, d, "hub")
	lines := ei8lRead(client)
	ei8lForwarder(t, d)
	// A declared dialog that never appears: the send is held, and the latch
	// heals at the corroboration window, releasing it.
	raise := time.Now()
	p.latchDialogOpen("Claude needs your permission")
	p.mu.Lock()
	p.dialogOpenAt = raise
	p.mu.Unlock()
	p.SendText("[from super] held", true)
	d.maintain(raise.Add(latchCorroborationWindow + time.Second))
	var hold, release int
	for _, r := range ei8lCollect(lines, 500*time.Millisecond) {
		switch {
		case strings.HasPrefix(r.Text, "deferred (modal open)") && r.EventType == "message":
			hold++
		case strings.HasPrefix(r.Text, "held mail released") && r.EventType == "message":
			release++
		}
	}
	if hold != 1 || release != 1 {
		t.Fatalf("hold %d, release %d agent_events, want one of each", hold, release)
	}
}

// Criterion 3: a reconnect mid-hold. The report after it reaches the new
// connection once, and nothing reaches the old one.
func TestServeReports_ReconnectMidHoldDeliversOnce(t *testing.T) {
	d, p, oldClient, _ := ei8lServe(t)
	oldLines := ei8lRead(oldClient)
	ei8lForwarder(t, d)
	p.SendText("[from super] held", true)
	ei8lCollect(oldLines, 300*time.Millisecond) // the hold went to the old connection
	newClient, _ := ei8lConnect(t, d, "hub")
	newLines := ei8lRead(newClient)
	d.maintain(time.Now().Add(heldMessageDeadline + time.Minute))
	got := ei8lCollect(newLines, 500*time.Millisecond)
	if len(got) != 1 || !strings.HasPrefix(got[0].Text, undeliveredSubmitPrefix) {
		t.Fatalf("new connection got %d agent_events (%+v), want the one NOT DELIVERED", len(got), got)
	}
	if late := ei8lCollect(oldLines, 200*time.Millisecond); len(late) != 0 {
		t.Errorf("the replaced connection still got %d agent_events", len(late))
	}
}

// Criterion 2, the client: the report becomes the toast and the event-log
// line for host:agent, through the same handler as a local event.
func TestRemoteAgentEvent_ShowsAsTheLocalEventWould(t *testing.T) {
	r := newCrossRig(t)
	tui := r.tui
	tui.agentEvents = make(chan AgentEvent, 8)
	at := time.Now().Add(-time.Second).Truncate(time.Millisecond)
	detail := undeliveredSubmitPrefix + "held 10m0s, dialog open), re-send it: hi"
	tui.onRemoteAgentEvent("workbench", ControlResp{Action: agentEventAction, Name: "eng1", Text: detail, EventType: "stalled", EventTimeMs: at.UnixMilli()})
	var ev AgentEvent
	select {
	case ev = <-tui.agentEvents:
	default:
		t.Fatal("no event reached the client's event path")
	}
	if ev.Pane != "workbench:eng1" || ev.Type != EventAgentStalled || ev.Detail != detail || !ev.Time.Equal(at) {
		t.Fatalf("event %+v, want workbench:eng1, stalled, the detail and the daemon's time", ev)
	}
	tui.handleAgentEvent(ev)
	if len(tui.notifications) != 1 || tui.notifications[0].event.Pane != "workbench:eng1" {
		t.Errorf("notifications %+v, want one toast for workbench:eng1", tui.notifications)
	}
	if len(tui.eventLog) != 1 || tui.eventLog[0].Detail != detail {
		t.Errorf("event log %+v, want the one NOT DELIVERED line", tui.eventLog)
	}
}

// The control stream routes agent_event to the callback, with the peer.
func TestConsumeEvents_RoutesAgentEvent(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	mux := NewControlMux(client)
	defer mux.Close()
	got := make(chan string, 1)
	done := make(chan struct{})
	pm := &peerManager{quit: make(chan struct{}), onAgentEvent: func(peer string, ev ControlResp) {
		got <- peer + "/" + ev.Name + "/" + ev.Text
	}}
	go pm.consumeEvents("workbench", &peerConn{mux: mux}, done)
	defer close(done)
	writeJSON(server, ControlResp{Action: agentEventAction, Name: "eng1", Text: "held mail released: pane returned to prompt", EventType: "message"})
	select {
	case s := <-got:
		if s != "workbench/eng1/held mail released: pane returned to prompt" {
			t.Errorf("callback got %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent_event never reached the callback")
	}
}

// Criterion 4: a client without the callback (as an older client has no
// case) passes over agent_event and keeps serving the stream.
func TestConsumeEvents_AClientWithoutTheHandlerPassesOver(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	mux := NewControlMux(client)
	defer mux.Close()
	forwarded := make(chan struct{})
	done := make(chan struct{})
	pm := &peerManager{quit: make(chan struct{}), onForwardSend: func(string, string, bool, sendOrigin) (SendVerdict, error) {
		close(forwarded)
		return SendVerdict{}, nil
	}}
	go pm.consumeEvents("workbench", &peerConn{mux: mux}, done)
	defer close(done)
	writeJSON(server, ControlResp{Action: agentEventAction, Name: "eng1", Text: "x", EventType: "message"})
	writeJSON(server, ControlCmd{Action: "forward_send", Target: "super", Text: "after", Enter: true})
	select {
	case <-forwarded:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream stopped after an agent_event the client has no handler for")
	}
}

// Feed it nothing: an unknown agent, an unknown type, no agent and no detail
// are logged and dropped on the client; the daemon forwards no report
// without an agent or a detail.
func TestRemoteAgentEvent_IgnoresWhatItCannotPlace(t *testing.T) {
	r := newCrossRig(t)
	tui := r.tui
	tui.agentEvents = make(chan AgentEvent, 8)
	for name, ev := range map[string]ControlResp{
		"unknown agent": {Name: "ghost", Text: "x", EventType: "stalled"},
		"unknown type":  {Name: "eng1", Text: "x", EventType: "bead_claimed"},
		"no agent":      {Text: "x", EventType: "stalled"},
		"no detail":     {Name: "eng1", EventType: "stalled"},
	} {
		ev.Action = agentEventAction
		tui.onRemoteAgentEvent("workbench", ev)
		if len(tui.agentEvents) != 0 {
			t.Errorf("%s: an event reached the event path", name)
			<-tui.agentEvents
		}
	}
	d := &Daemon{serve: true, clients: map[string]net.Conn{}, clientCtrlMu: map[string]*sync.Mutex{}}
	client, _ := ei8lConnect(t, d, "hub")
	lines := ei8lRead(client)
	d.forwardReport(AgentEvent{Type: EventAgentStalled, Pane: "eng1"})
	d.forwardReport(AgentEvent{Type: EventAgentStalled, Detail: "x"})
	d.forwardReport(AgentEvent{Type: EventBeadClaimed, Pane: "eng1", Detail: "x"})
	if got := ei8lCollect(lines, 200*time.Millisecond); len(got) != 0 {
		t.Errorf("the daemon forwarded %d reports it should have dropped", len(got))
	}
}

// Criterion 5, a census: only the serve daemon's pane creation adopts the
// report sink, and adoptReports is the only writer of it.
func TestServeReports_OnlyServePanesGetASink(t *testing.T) {
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	adopters := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range af.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name == "adoptReports" {
					adopters[fn.Name.Name] = true
				}
				if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "reportSink" && sel.Sel.Name == "Store" && fn.Name.Name != "adoptReports" {
					t.Errorf("%s stores a report sink; only adoptReports may", fn.Name.Name)
				}
				return true
			})
		}
	}
	for fn := range adopters {
		if fn != "RunDaemon" && fn != "startPushedPane" {
			t.Errorf("%s adopts a report sink; only the serve daemon's pane creation may", fn)
		}
	}
	if !adopters["RunDaemon"] {
		t.Error("RunDaemon's panes do not adopt the report sink")
	}
}
