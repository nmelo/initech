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

// dndMaxFor is the longest do-not-disturb timer: a longer one is cut to it,
// so a forgotten flag cannot hold mail all night.
const dndMaxFor = time.Hour

// Notices the dnd action answers with, printed by the CLI on stderr.
const (
	dndCappedNotice = "do-not-disturb capped at 1h"
	dndIdleNotice   = "pane is idle; do-not-disturb ends at turn end"
)

// dndQueueFullReason is why a send to a pane in do-not-disturb is refused
// when its queue is at maxMessageQueue. The oldest message is never dropped.
const dndQueueFullReason = "do-not-disturb queue is full"

// dndHeldLine is the sender's line for a message held for do-not-disturb:
// how long the agent has been in it, in the overlay's short form, and how
// many messages are queued ahead of this one.
func dndHeldLine(name string, age time.Duration, ahead int) string {
	n := fmt.Sprintf("%d messages ahead", ahead)
	if ahead == 1 {
		n = "1 message ahead"
	}
	return fmt.Sprintf("held: %s is in do-not-disturb (%s, %s)", name, shortAge(age), n)
}

// dndAge is how long the pane has been in do-not-disturb, and false when it
// is not.
func (p *Pane) dndAge(now time.Time) (time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.dnd {
		return 0, false
	}
	return now.Sub(p.dndSince), true
}

// dndFields is a pane's do-not-disturb as the list and patrol replies carry
// it: on or off, and the age in seconds while on. A pane another process
// runs answers nil for both: this process cannot know.
func dndFields(pv PaneView, now time.Time) (on *bool, ageSec *int) {
	p, ok := pv.(*Pane)
	if !ok {
		return nil, nil
	}
	age, in := p.dndAge(now)
	if in {
		s := int(age.Seconds())
		ageSec = &s
	}
	return &in, ageSec
}

// InDND reports whether the pane is in do-not-disturb.
func (p *Pane) InDND() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dnd
}

// startDND puts the pane in do-not-disturb with no timer of its own. It
// reports whether it was off.
func (p *Pane) startDND() bool {
	return p.startDNDFor(time.Now(), 0)
}

// startDNDFor is startDND with a timer: d > 0 ends it at now+d, replacing
// a timer already running; d == 0 keeps whatever timer runs (none: it ends
// at turn end). It reports whether it was off.
//
// The flag and the timer change in one step: the once-a-second tick must
// never see a timed do-not-disturb without its timer, or it would end it at
// turn end on an idle pane.
func (p *Pane) startDNDFor(now time.Time, d time.Duration) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	was := p.dnd
	p.dnd = true
	if !was {
		p.dndSince = now
	}
	if d > 0 {
		p.dndUntil = now.Add(d)
	}
	return !was
}

// dndTimer is when the pane's do-not-disturb timer ends, and false when it
// has none.
func (p *Pane) dndTimer() (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dndUntil, p.dnd && !p.dndUntil.IsZero()
}

// dndTurnEnded is the turn-end rule's reading of the pane: anything but
// running ends the turn -- idle, waiting on a dialog, background (quiet with
// a background job), suspended or dead. Deliberately not paneIsWorking,
// which keeps background in view as work (ini-fg1q): an agent that set
// do-not-disturb and went quiet with a job running has ended its turn.
func dndTurnEnded(p *Pane) bool {
	return p.Activity() != StateRunning
}

// endDNDIfDue ends do-not-disturb when its time has come: at the timer when
// one runs (turn end does not end it), else at turn end. Run once a second
// from maintainModal, in the TUI and the serve daemon alike. It reports
// whether it ended it.
func (p *Pane) endDNDIfDue(now time.Time) bool {
	p.mu.Lock()
	on, until := p.dnd, p.dndUntil
	p.mu.Unlock()
	if !on {
		return false
	}
	why := "timer"
	if until.IsZero() {
		if !dndTurnEnded(p) {
			return false
		}
		why = "turn end"
	} else if now.Before(until) {
		return false
	}
	held, was := p.endDND(now)
	if was {
		LogInfo("dnd", "do-not-disturb ended", "pane", p.name, "by", why, "held", held)
	}
	return was
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
	p.dndUntil = time.Time{}
	if !was {
		// Never on: dialog mail keeps its own clock.
		p.mu.Unlock()
		return 0, false
	}
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
	p.startModalDrain()
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
	now := time.Now()
	held := SendVerdict{Outcome: DeliveryHeld, Reason: dndHeldLine(p.name, now.Sub(p.dndSince), len(p.messageQueue))}
	if m := p.redelivering; m != nil && m.Text == text && m.Enter == enter {
		p.redelivering = nil
		p.messageQueue = append([]QueuedMessage{*m}, p.messageQueue...)
		return held, true
	}
	if len(p.messageQueue) >= maxMessageQueue {
		LogInfo("dnd", "SEND NOT DELIVERED: do-not-disturb queue full", "pane", p.name, "queued", len(p.messageQueue))
		return SendVerdict{Outcome: DeliveryNotDelivered, Reason: dndQueueFullReason}, true
	}
	p.messageQueue = append(p.messageQueue, QueuedMessage{Text: text, Enter: enter, Time: now, DND: true})
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
		now := time.Now()
		d := time.Duration(req.ForSec) * time.Second // 0 or less: no timer
		var notices []string
		if d > dndMaxFor {
			d = dndMaxFor
			notices = append(notices, dndCappedNotice)
		}
		p.startDNDFor(now, d)
		until, timed := p.dndTimer()
		if !timed && dndTurnEnded(p) {
			notices = append(notices, dndIdleNotice)
		}
		LogInfo("dnd", "do-not-disturb on", "pane", p.name, "until", until)
		line := fmt.Sprintf("do-not-disturb on for %s: messages from other agents wait until your turn ends or initech dnd off", p.name)
		if timed {
			line = fmt.Sprintf("do-not-disturb on for %s until %s (%s): messages from other agents wait until then or initech dnd off",
				p.name, until.Format("15:04:05"), shortAge(until.Sub(now).Round(time.Second)))
		}
		writeIPCResponse(conn, IPCResponse{OK: true, Data: line, Notices: notices})
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
