package tui

import (
	"fmt"
	"net"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// A SEND FROM A PEER'S AGENT (ini-18pz). An agent the serve runs sends with
// initech send like any agent: its pane carries the serve's socket, and a
// name the serve does not host (super, on the Mac) is forwarded over the
// connection the Mac's window 1 already holds to this serve, the way a
// second window's send reaches window 1. Only a pane the serve started may
// do this, and the sender hears the Mac's verdict words unchanged.

// identifySendingPane names the serve's pane whose process tree the
// connection comes from, as initech post identifies its poster: never from
// what the request says.
func (d *Daemon) identifySendingPane(conn net.Conn) (string, error) {
	if d.identifySender != nil {
		return d.identifySender(conn)
	}
	if err := postPlatformError(runtime.GOOS); err != nil {
		return "", err
	}
	pid, err := postPeerPID(conn)
	if err != nil {
		return "", err
	}
	var processes []postPaneProcess
	for _, p := range d.snapshotPanes() {
		p.mu.Lock()
		if p.alive && p.pid > 0 {
			processes = append(processes, postPaneProcess{pid: p.pid, identity: postIdentity{agent: p.name}})
		}
		p.mu.Unlock()
	}
	id, err := identifyPostAncestor(pid, processes, postParentPID)
	if err != nil {
		return "", fmt.Errorf("not inside one of its panes")
	}
	return id.agent, nil
}

// sendToConnectedMachine forwards a send for a name this serve does not host
// to the machine that runs the sending agent: the client that pushed it, or
// the only one connected. It answers in a local send's words, the agent named
// as the sender named it.
func (d *Daemon) sendToConnectedMachine(conn net.Conn, req IPCRequest) {
	from, err := d.identifySendingPane(conn)
	if err != nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf(
			"agent %q is not on this machine, and only an agent this serve started can send to another machine through it (%v). For a remote agent use host:agent (e.g. 'initech send workbench:%s ...').",
			req.Target, err, req.Target)})
		return
	}
	owner := ""
	if d.ownership != nil {
		owner, _ = d.ownership.verify(from, "")
	}
	d.sessionsMu.Lock()
	var connected []string
	for name := range d.clients {
		connected = append(connected, name)
	}
	if owner == "" && len(connected) == 1 {
		owner = connected[0]
	}
	ctrl := d.clients[owner]
	var mu *sync.Mutex
	if d.clientCtrlMu != nil {
		mu = d.clientCtrlMu[owner]
	}
	d.sessionsMu.Unlock()
	switch {
	case owner == "" && len(connected) == 0:
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("not delivered to %s: no machine is connected to this serve", req.Target),
			Delivery: DeliveryNotDelivered, Data: "no machine is connected to this serve"})
		return
	case owner == "":
		sort.Strings(connected)
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf(
			"agent %q is not on this machine and %d machines are connected (%s): name one, as host:%s",
			req.Target, len(connected), strings.Join(connected, ", "), req.Target)})
		return
	case ctrl == nil || mu == nil:
		why := owner + " is not connected to this serve"
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("not delivered to %s: %s", req.Target, why),
			Delivery: DeliveryNotDelivered, Data: why})
		return
	}
	fwd := ControlCmd{Action: "forward_send", Target: req.Target, Text: req.Text, Enter: req.Enter, From: from, Urgent: req.Urgent}
	resp, err := d.forwardToClient(owner, ctrl, mu, fwd)
	if err != nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("not delivered to %s: %v", req.Target, err),
			Delivery: DeliveryNotDelivered, Data: err.Error()})
		return
	}
	switch {
	case resp.Delivery == DeliveryNotDelivered:
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("not delivered to %s: %s", req.Target, resp.Error), Delivery: resp.Delivery, Data: resp.Error})
	case resp.Error != "":
		writeIPCResponse(conn, IPCResponse{Error: resp.Error})
	case resp.Delivery != "":
		writeIPCResponse(conn, IPCResponse{OK: true, Delivery: resp.Delivery, Data: resp.Data, Removed: resp.Removed})
	default:
		writeIPCResponse(conn, IPCResponse{OK: true, Delivery: DeliveryUnconfirmed, Data: fmt.Sprintf(olderPeerVerdictFormat, owner)})
	}
}
