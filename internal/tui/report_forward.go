package tui

// report_forward.go carries a serve daemon's delivery reports to the
// connected initech (ini-ei8l). A daemon-hosted agent holds, releases and
// gives up on mail through the same code as a local one (maintenance.go), but
// its panes have no event channel, so before this the reports reached only the
// daemon's own log. Now each report also goes to the pane's report sink; a
// serve daemon's sink queues it, and one goroutine writes it to every
// connected initech as an agent_event, which the client feeds into its own
// event path: the same toast and event-log line as a local agent's.
//
// Only reports go: the hold, the release and the NOT DELIVERED forms. The
// daemon's panes keep a nil event channel, so bead detection and the stall
// detectors stay off there, as before.

import (
	"net"
	"sync"
	"time"
)

// agentEventAction is the unsolicited control-stream message carrying one
// delivery report from a serve daemon to a connected initech. An older client
// does not know the action and its consumeEvents switch passes over it.
const agentEventAction = "agent_event"

// reportSinkFn receives a delivery report a pane raised.
type reportSinkFn func(AgentEvent)

// agentEventWireTypes names the event types a report can carry on the wire.
// Reports use exactly these two; anything else is not forwarded.
var agentEventWireTypes = map[EventType]string{
	EventMessageSent:  EventMessageSent.String(),
	EventAgentStalled: EventAgentStalled.String(),
}

// agentEventTypeFromWire is the receiving side of agentEventWireTypes.
func agentEventTypeFromWire(s string) (EventType, bool) {
	for t, name := range agentEventWireTypes {
		if name == s {
			return t, true
		}
	}
	return 0, false
}

// reportEvent is how a pane raises a delivery report. It goes on the pane's
// event channel exactly as EmitEvent always sent it, then to the report sink
// when the pane has one.
func (p *Pane) reportEvent(ev AgentEvent) {
	deliverReport(p.eventCh, p.reportSink.Load(), ev)
}

// deliverReport is reportEvent with the channel and sink already read, for a
// report raised asynchronously after the pane may have changed.
func deliverReport(events chan<- AgentEvent, sink *reportSinkFn, ev AgentEvent) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	EmitEvent(events, ev)
	if sink != nil {
		(*sink)(ev)
	}
}

// reportQueueDepth bounds the reports waiting to be written to clients. A
// report beyond it is dropped with a warning; its own log line is already
// written by the code that raised it.
const reportQueueDepth = 256

// adoptReports gives a pane this daemon's report sink. Only a serve daemon
// has a report queue (RunDaemon); window 1's in-TUI daemon does not, and its
// panes report through the TUI's own event channel.
func (d *Daemon) adoptReports(p *Pane) {
	if d.reportQ == nil {
		return
	}
	sink := reportSinkFn(d.queueReport)
	p.reportSink.Store(&sink)
}

// queueReport hands a report to the forwarder without blocking: the hold
// report is raised with the pane's send lock held.
func (d *Daemon) queueReport(ev AgentEvent) {
	select {
	case d.reportQ <- ev:
	default:
		LogWarn("daemon", "agent_event queue full, report not forwarded", "pane", ev.Pane, "detail", ev.Detail)
	}
}

// runReportForwarder writes queued reports to the connected clients, in the
// order they were raised, until stop closes.
func (d *Daemon) runReportForwarder(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case ev := <-d.reportQ:
			d.forwardReport(ev)
		}
	}
}

// forwardReport writes one report to every client that has completed the
// handshake, each under that client's control-stream write lock. Only live
// clients get it: nothing is replayed on a reconnect, so a report arrives at
// most once per connection.
func (d *Daemon) forwardReport(ev AgentEvent) {
	typ, ok := agentEventWireTypes[ev.Type]
	if !ok || ev.Pane == "" || ev.Detail == "" {
		LogWarn("daemon", "report not forwarded: not a delivery report", "type", ev.Type.String(), "pane", ev.Pane)
		return
	}
	msg := ControlResp{
		Action:      agentEventAction,
		Name:        ev.Pane,
		Text:        ev.Detail,
		EventType:   typ,
		EventTimeMs: ev.Time.UnixMilli(),
	}
	type client struct {
		peer string
		conn net.Conn
		mu   *sync.Mutex
	}
	d.sessionsMu.Lock()
	clients := make([]client, 0, len(d.clients))
	for peer, conn := range d.clients {
		clients = append(clients, client{peer, conn, d.clientCtrlMu[peer]})
	}
	d.sessionsMu.Unlock()
	for _, c := range clients {
		if c.mu != nil {
			c.mu.Lock()
		}
		err := writeJSON(c.conn, msg)
		if c.mu != nil {
			c.mu.Unlock()
		}
		if err != nil {
			LogWarn("daemon", "agent_event write failed", "peer", c.peer, "pane", ev.Pane, "err", err)
			continue
		}
		LogInfo("daemon", "agent_event forwarded", "peer", c.peer, "pane", ev.Pane, "type", typ)
	}
}

// servedSend is a serve daemon's answer to a send for one of its agents: the
// answer the TUI's send handler gives for the same state (ini-ei8l). A process
// that is gone or still booting is refused before anything is typed; a send
// held behind a dialog is answered deferred, with the latch age and the depth
// of the mail already held.
func servedSend(p *Pane, text string, enter bool) ControlResp {
	if err := p.notRunningReason(); err != nil {
		LogInfo("daemon", "SEND NOT DELIVERED", "target", p.Name(), "reason", err)
		return ControlResp{Error: err.Error()}
	}
	if err := p.awaitStartupSettled(); err != nil {
		LogInfo("daemon", "SEND NOT DELIVERED", "target", p.Name(), "reason", err)
		return ControlResp{Error: err.Error()}
	}
	deferred := paneHasModal(p)
	age, _ := p.latchAge(time.Now())
	depth := p.QueuedMessageCount()
	p.SendText(text, enter)
	if deferred {
		return ControlResp{OK: true, Data: deferredMsg(p.Name(), age, depth)}
	}
	return ControlResp{OK: true}
}

// onRemoteAgentEvent is the connected initech's side: a report from a serve
// daemon becomes the same AgentEvent a local agent's would be, named
// host:agent, and goes through the same event path (toast, event log, the
// inbox step). A report for an agent this window does not know, or of a type
// it does not know, is logged and dropped.
func (t *TUI) onRemoteAgentEvent(peer string, ev ControlResp) {
	typ, ok := agentEventTypeFromWire(ev.EventType)
	if !ok || ev.Name == "" || ev.Text == "" {
		LogWarn("remote", "agent_event ignored: unknown type, no agent or no detail", "peer", peer, "type", ev.EventType, "agent", ev.Name)
		return
	}
	if rp, _ := t.findRemoteTarget(IPCRequest{Host: peer, Target: ev.Name}); rp == nil {
		LogWarn("remote", "agent_event ignored: unknown agent", "peer", peer, "agent", ev.Name)
		return
	}
	at := time.Now()
	if ev.EventTimeMs > 0 {
		at = time.UnixMilli(ev.EventTimeMs)
	}
	LogInfo("remote", "agent_event", "peer", peer, "agent", ev.Name, "type", ev.EventType)
	EmitEvent(t.agentEvents, AgentEvent{Type: typ, Pane: peer + ":" + ev.Name, Detail: ev.Text, Time: at})
}
