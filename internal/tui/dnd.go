package tui

import (
	"fmt"
	"net"
	"time"
)

// Do-not-disturb (ini-x432): an agent runs initech dnd on in its own pane and
// sends from other agents wait on the pane's message queue -- the one a
// suspended pane and an open dialog already use -- until initech dnd off
// drains them in order through the normal send path.
//
// The hold sits in Pane.sendText, the primitive every send reaches (the IPC
// send, window 2's forward_send, the serve daemon), as the suspension guard
// does (ini-g7fl), so each answers with the same words without knowing
// do-not-disturb exists. The operator's own typing and pastes never pass
// through it. Nothing is written to disk: a restart clears it.

// dndQueueFullReason is why a send to a pane in do-not-disturb is refused
// when its queue is at maxMessageQueue. The oldest message is never dropped.
const dndQueueFullReason = "do-not-disturb queue is full"

// dndHeldLine is the sender's line for a message held for do-not-disturb.
func dndHeldLine(name string) string {
	return "held: " + name + " is in do-not-disturb"
}

// InDND reports whether the pane is in do-not-disturb.
func (p *Pane) InDND() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dnd
}

// startDND puts the pane in do-not-disturb. It reports whether it was off.
func (p *Pane) startDND() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	was := p.dnd
	p.dnd = true
	return !was
}

// endDND takes the pane out of do-not-disturb and drains its queue, one
// message at a time, through the dialog drain. Held and dialog mail is
// marked released, so the dialog deadline counts from now, not from the
// quiet it sat through (an earlier release is replaced). It returns how many messages do-not-disturb held and
// whether the pane was in it.
func (p *Pane) endDND(now time.Time) (held int, was bool) {
	p.mu.Lock()
	was = p.dnd
	p.dnd = false
	for i := range p.messageQueue {
		m := &p.messageQueue[i]
		if m.DND || m.HeldForDialog {
			m.Released = now
		}
		if m.DND {
			held++
		}
	}
	p.mu.Unlock()
	if was {
		p.startModalDrain()
	}
	return held, was
}

// holdIfDND holds a send for a pane in do-not-disturb, reporting whether it
// did and the verdict the sender gets: held, or not delivered when the queue
// is full. A drain's own message that meets do-not-disturb (set again while
// the drain ran) goes back to the front with its time, and the drain stops
// (the ini-1898 re-hold rule).
func (p *Pane) holdIfDND(text string, enter bool) (SendVerdict, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.dnd {
		return SendVerdict{}, false
	}
	held := SendVerdict{Outcome: DeliveryHeld, Reason: dndHeldLine(p.name)}
	if m := p.redelivering; m != nil && m.Text == text && m.Enter == enter {
		p.redelivering = nil
		p.messageQueue = append([]QueuedMessage{*m}, p.messageQueue...)
		return held, true
	}
	if len(p.messageQueue) >= maxMessageQueue {
		LogInfo("dnd", "SEND NOT DELIVERED: do-not-disturb queue full", "pane", p.name, "queued", len(p.messageQueue))
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: dndQueueFullReason}, true
	}
	p.messageQueue = append(p.messageQueue, QueuedMessage{Text: text, Enter: enter, Time: time.Now(), DND: true})
	LogInfo("dnd", "held: do-not-disturb", "pane", p.name, "queued", len(p.messageQueue))
	return held, true
}

// handleIPCDND is the dnd action, shared by the TUI and the serve daemon:
// Text "on" or "off" for the agent named by Target. The flag lives on the
// pane in this process only.
func handleIPCDND(h IPCHost, conn net.Conn, req IPCRequest) {
	if req.Target == "" {
		writeIPCResponse(conn, IPCResponse{Error: "target is required"})
		return
	}
	if req.Text != "on" && req.Text != "off" {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("dnd takes on or off, not %q", req.Text)})
		return
	}
	pv, ok := h.FindPaneView(req.Target)
	if !ok {
		writeIPCResponse(conn, IPCResponse{Error: "shutting down"})
		return
	}
	if pv == nil {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf("pane %q not found", req.Target)})
		return
	}
	p, ok := pv.(*Pane)
	if !ok {
		writeIPCResponse(conn, IPCResponse{Error: fmt.Sprintf(
			"%s runs in another window or on another machine; run initech dnd there", req.Target)})
		return
	}
	if req.Text == "on" {
		p.startDND()
		LogInfo("dnd", "do-not-disturb on", "pane", p.name)
		writeIPCResponse(conn, IPCResponse{OK: true, Data: fmt.Sprintf(
			"do-not-disturb on for %s: messages from other agents wait until initech dnd off", p.name)})
		return
	}
	held, was := p.endDND(time.Now())
	if !was {
		writeIPCResponse(conn, IPCResponse{OK: true, Data: fmt.Sprintf("%s was not in do-not-disturb", p.name)})
		return
	}
	LogInfo("dnd", "do-not-disturb off", "pane", p.name, "held", held)
	writeIPCResponse(conn, IPCResponse{OK: true, Data: fmt.Sprintf(
		"do-not-disturb off for %s; %s delivering in order", p.name, heldCount(held))})
}

// heldCount words a count of held messages.
func heldCount(n int) string {
	if n == 1 {
		return "1 held message"
	}
	return fmt.Sprintf("%d held messages", n)
}
