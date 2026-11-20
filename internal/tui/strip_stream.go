package tui

// strip_stream.go carries an agent's list strip to every window and machine
// that shows the agent without owning it (D8). The owner sends the list on
// the control stream the viewer already has, at connect and on every change;
// each viewer draws it in its own tile. The owner also takes the strip's rows
// off every size a viewer asks for (Pane.resizeUnder), so whichever window's
// size wins, the agent never draws under the strip, and a viewer that predates
// the message shows blank rows there and no strip.

import (
	"net"
	"sync"
	"sync/atomic"
)

// stripAction is the control message carrying one agent's list.
const stripAction = "strip"

// stripMsg is the message as written: the agent and its items, nothing else.
type stripMsg struct {
	Action string   `json:"action"`
	Name   string   `json:"name"`
	Items  []string `json:"items"`
}

// broadcastStrip sends one agent's list to every connected client.
func (d *Daemon) broadcastStrip(agent string, items []string) {
	d.sessionsMu.Lock()
	type cw struct {
		ctrl net.Conn
		mu   *sync.Mutex
	}
	var writers []cw
	for o, c := range d.clients {
		writers = append(writers, cw{ctrl: c, mu: d.clientCtrlMu[o]})
	}
	d.sessionsMu.Unlock()
	msg := stripMsg{Action: stripAction, Name: agent, Items: items}
	for _, w := range writers {
		writeLocked(w.ctrl, w.mu, msg)
	}
}

// sendStrips sends every list this process keeps to one client that has just
// connected, after its handshake, under its control-stream lock.
func (d *Daemon) sendStrips(ctrl net.Conn, mu *sync.Mutex) {
	if d.strips == nil {
		return
	}
	for agent, items := range d.strips.all() {
		writeLocked(ctrl, mu, stripMsg{Action: stripAction, Name: agent, Items: items})
	}
}

func writeLocked(ctrl net.Conn, mu *sync.Mutex, msg any) {
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	writeJSON(ctrl, msg) //nolint:errcheck // a dead client is dropped by its own reader
}

// attachStrips makes d send the book's lists: to each client at connect, and
// to all on every change.
func (d *Daemon) attachStrips(b *stripBook) {
	if b == nil {
		return
	}
	d.strips = b
	b.mu.Lock()
	prev := b.onChange
	b.onChange = func(agent string, items []string) {
		if prev != nil {
			prev(agent, items)
		}
		d.broadcastStrip(agent, items)
	}
	b.mu.Unlock()
}

// RemotePane side.

// remoteStrip is a viewer pane's copy of its agent's list, and the size its
// own emulator was last given, so a list change resizes it on the main loop.
type remoteStrip struct {
	items atomic.Pointer[[]string]
	// Main goroutine only.
	reqRows, reqCols int
	appliedH         int
}

// SetStrip is the owner's list for this pane, from the control stream.
func (rp *RemotePane) SetStrip(items []string) {
	if len(items) == 0 {
		rp.strip.items.Store(nil)
		return
	}
	cp := append([]string(nil), items...)
	rp.strip.items.Store(&cp)
}

// StripItems is the list the owner last sent; nil when none.
func (rp *RemotePane) StripItems() []string {
	if v := rp.strip.items.Load(); v != nil {
		return *v
	}
	return nil
}

// stripRowsFor sizes the viewer's emulator for a request, less the strip, and
// remembers the request. Main goroutine.
func (rp *RemotePane) stripRowsFor(rows, cols int) int {
	rp.strip.reqRows, rp.strip.reqCols = rows, cols
	rp.strip.appliedH = stripHeight(rp.StripItems(), rows, cols)
	return rows - rp.strip.appliedH
}

// applyStripSize resizes the viewer's emulator when the list now takes a
// different height at the last requested size. Main goroutine, each frame.
func (rp *RemotePane) applyStripSize() {
	rows, cols := rp.strip.reqRows, rp.strip.reqCols
	if rows <= 0 {
		return
	}
	if h := stripHeight(rp.StripItems(), rows, cols); h != rp.strip.appliedH {
		rp.strip.appliedH = h
		rp.emu.Resize(cols, exactTerminalRows(rows-h))
	}
}

// peerConn side: the lists a peer has sent, by agent, for panes that attach
// after the message.

func (pc *peerConn) applyStrip(agent string, items []string) {
	pc.panesMu.Lock()
	if pc.strips == nil {
		pc.strips = map[string][]string{}
	}
	if len(items) == 0 {
		delete(pc.strips, agent)
	} else {
		pc.strips[agent] = append([]string(nil), items...)
	}
	var rp *RemotePane
	for _, p := range pc.panes {
		if r, ok := p.(*RemotePane); ok && p.Name() == agent {
			rp = r
			break
		}
	}
	pc.panesMu.Unlock()
	if rp != nil {
		rp.SetStrip(items)
	}
}

// stripFor is a peer's list for agent, for a pane being attached.
func (pc *peerConn) stripFor(agent string) []string {
	pc.panesMu.Lock()
	defer pc.panesMu.Unlock()
	return append([]string(nil), pc.strips[agent]...)
}

// stripOfPane is a pane's list strip as the list reply carries it: a copy of
// the items of a pane this process runs, [] when it has none; the items this
// window holds for a pane another process runs, nil (null) when it holds
// none, since an older daemon sends no strips and sends nothing for an
// agent with none, so this window cannot tell an empty list from an unsent
// one.
func stripOfPane(pv PaneView) *[]string {
	switch p := pv.(type) {
	case *Pane:
		items := append([]string{}, p.StripItems()...)
		return &items
	case *RemotePane:
		if held := p.StripItems(); len(held) > 0 {
			items := append([]string(nil), held...)
			return &items
		}
	}
	return nil
}
