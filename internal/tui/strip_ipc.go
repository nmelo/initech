package tui

import (
	"fmt"
	"net"
	"path/filepath"
	"time"
)

// openStripBook loads the process's strip file and makes it the book NewPane
// reads, so the panes spawned after it come up with their lists. root "" (no
// project) keeps no strips.
func openStripBook(root, file string) *stripBook {
	if root == "" {
		return nil
	}
	b := loadStripBook(filepath.Join(layoutDir(root), file), time.Now())
	setProcessStrips(b)
	return b
}

// setStrip replaces the pane's items; nil or empty clears them.
func (p *Pane) setStrip(items []string) {
	if len(items) == 0 {
		p.strip.Store(nil)
		return
	}
	cp := append([]string(nil), items...)
	p.strip.Store(&cp)
}

// stripRequest is the size last asked of the pane, before the strip.
func (p *Pane) stripRequest() (rows, cols int, exact bool) {
	return int(p.reqRows.Load()), int(p.reqCols.Load()), p.reqExact.Load()
}

// stripReply is the line the command prints for a change.
func stripReply(agent string, items []string) string {
	if len(items) == 0 {
		return fmt.Sprintf("Cleared the strip for %s.", agent)
	}
	return fmt.Sprintf("Set %d item(s) in the strip for %s.", len(items), agent)
}

// handleIPCStrip sets the list strip of a pane this window owns (D1, D2, D3,
// D5). The list is checked, stored, then applied. The agent's terminal is
// resized only while no send is typing into it: this IPC goroutine takes the
// pane's send lock, waiting for a send to finish, and the main loop applies
// the size while it is held.
func (t *TUI) handleIPCStrip(conn net.Conn, req IPCRequest) {
	reply := func(resp IPCResponse) {
		result := "ok"
		if !resp.OK {
			result = "refused: " + resp.Error
		}
		LogInfo("ipc", "strip action", "target", req.Target, "items", len(req.Items), "result", result)
		writeIPCResponse(conn, resp)
	}
	if req.Target == "" {
		reply(IPCResponse{Error: "initech strip runs inside an agent's pane (INITECH_AGENT is not set)"})
		return
	}
	items, err := CheckStripItems(req.Items)
	if err != nil {
		reply(IPCResponse{Error: err.Error()})
		return
	}
	if t.strips == nil {
		reply(IPCResponse{Error: "this window keeps no strips (no project)"})
		return
	}
	if err := t.stripCallerIs(conn, req.Target); err != nil {
		reply(IPCResponse{Error: err.Error()})
		return
	}
	var p *Pane
	if !t.runOnMain(func() {
		if pv := t.findPaneByName(req.Target); pv != nil {
			p, _ = pv.(*Pane)
		}
	}) {
		reply(IPCResponse{Error: "TUI shutting down"})
		return
	}
	if p == nil {
		reply(IPCResponse{Error: fmt.Sprintf("%s has no pane in this window", req.Target)})
		return
	}
	if err := t.strips.set(req.Target, items); err != nil {
		reply(IPCResponse{Error: fmt.Sprintf("the strip could not be saved, nothing changed: %v", err)})
		return
	}
	p.sendMu.Lock()
	rows, cols, _ := p.stripRequest()
	before := stripHeight(p.StripItems(), rows, cols)
	p.setStrip(items)
	if rows > 0 && stripHeight(items, rows, cols) != before {
		t.runOnMain(func() { p.resizeFromMainLoop(rows, cols) })
	}
	p.sendMu.Unlock()
	reply(IPCResponse{OK: true, Data: stripReply(req.Target, items)})
}

// handleIPCStrip is the serve daemon's: the same for the panes it hosts,
// sized by its viewers' requests.
func (d *Daemon) handleIPCStrip(conn net.Conn, req IPCRequest) {
	reply := func(resp IPCResponse) {
		result := "ok"
		if !resp.OK {
			result = "refused: " + resp.Error
		}
		LogInfo("daemon", "strip action", "target", req.Target, "items", len(req.Items), "result", result)
		writeIPCResponse(conn, resp)
	}
	if req.Target == "" {
		reply(IPCResponse{Error: "initech strip runs inside an agent's pane (INITECH_AGENT is not set)"})
		return
	}
	items, err := CheckStripItems(req.Items)
	if err != nil {
		reply(IPCResponse{Error: err.Error()})
		return
	}
	if d.strips == nil {
		reply(IPCResponse{Error: "this daemon keeps no strips (no project)"})
		return
	}
	if caller, err := d.identifySendingPane(conn); err != nil || caller != req.Target {
		reply(IPCResponse{Error: stripNotYours(req.Target, caller, err)})
		return
	}
	p := d.findPane(req.Target)
	if p == nil {
		reply(IPCResponse{Error: fmt.Sprintf("%s has no pane on this daemon", req.Target)})
		return
	}
	if err := d.strips.set(req.Target, items); err != nil {
		reply(IPCResponse{Error: fmt.Sprintf("the strip could not be saved, nothing changed: %v", err)})
		return
	}
	p.sendMu.Lock()
	rows, cols, exact := p.stripRequest()
	before := stripHeight(p.StripItems(), rows, cols)
	p.setStrip(items)
	if rows > 0 && stripHeight(items, rows, cols) != before {
		if exact || d.headless {
			p.ResizeExact(rows, cols)
		} else {
			p.Resize(rows, cols)
		}
	}
	p.sendMu.Unlock()
	reply(IPCResponse{OK: true, Data: stripReply(req.Target, items)})
}

// stripCallerIs refuses a strip request that does not come from inside the
// pane it names: the list belongs to its pane's agent (point 5), so it is
// attributed by the caller's process tree, as initech post is, never by what
// the request says.
func (t *TUI) stripCallerIs(conn net.Conn, agent string) error {
	identify := t.identifyStripCaller
	if identify == nil {
		identify = func(c net.Conn) (string, error) {
			id, err := t.identifyPostConnection(c)
			return id.agent, err
		}
	}
	caller, err := identify(conn)
	if err != nil || caller != agent {
		return fmt.Errorf("%s", stripNotYours(agent, caller, err))
	}
	return nil
}

// stripNotYours is the refusal for a request from outside the pane it names.
func stripNotYours(agent, caller string, err error) string {
	if err != nil || caller == "" {
		return fmt.Sprintf("initech strip sets only the strip of the pane it runs in; could not tell that this call comes from %s's pane", agent)
	}
	return fmt.Sprintf("initech strip sets only the strip of the pane it runs in; this call comes from %s's pane, not %s's", caller, agent)
}
