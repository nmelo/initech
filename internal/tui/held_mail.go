package tui

import (
	"fmt"
	"time"
)

// Held mail: messages deferred because a dialog was open (ini-1898).
//
// The modal guard (ini-7txh) defers a send rather than paste into an open
// dialog, and the deferred message waits on the pane's queue. Two defects let
// one message sit there forever while the log grew by a line every 500 ms
// (nayutal eng3, 2026-10-14: 52,270 lines over seven hours, never reported):
//
//   - drainModalQueue popped the message, the send found the dialog still up
//     and put it back on the queue, and the drain's loop popped it again after
//     queueDrainInterval. Each pass logged a fresh "deferred" line and reset
//     the message's time. A drain only has to START in a moment the dialog is
//     not drawn -- Claude repaints constantly while booting -- to spin until
//     the dialog closes. On a dead pane it never closes.
//   - Nothing ever gave up: no liveness check, no deadline, no report.
//
// Now a message is deferred and logged once; a re-delivery that meets the
// dialog again goes back to the FRONT of the queue with its original time and
// the drain stops; and held mail that cannot be delivered is surfaced to the
// operator once, with how long it waited, instead of being retried or dropped.

// heldMessageDeadline bounds how long a message may wait for a dialog to close
// on a live pane before it is surfaced as not delivered. Operator-confirmed
// (2026-10-14). The message is reported, never silently dropped.
const heldMessageDeadline = 10 * time.Minute

// emptyBoxHoldBound ends mail held for a Codex or agy input box that never
// reads empty: the same five minutes a withheld submit waits, so a sender
// has one figure for "held and not typed" (ini-1etm). The clock runs while
// the box reads neither empty, nor busy, nor at a dialog, and starts again
// at each start of initech.
const emptyBoxHoldBound = withheldSubmitWindow

// enqueueHeld queues a message deferred for an open dialog. It is the
// dialog-held twin of EnqueueMessage; only held messages are subject to the
// deadline, because a suspended pane's queue is resume mail with its own path.
func (p *Pane) enqueueHeld(text string, enter bool) bool {
	return p.enqueueHeldWhen(text, enter, sendAgainWhenDialog) != ""
}

// enqueueHeldWhen is enqueueHeld returning the refusal at the cap, naming
// when the sender can send again.
func (p *Pane) enqueueHeldWhen(text string, enter bool, when string) string {
	refusal, _ := p.enqueueHeldReceipt(text, enter, when)
	return refusal
}

// enqueueHeldReceipt is enqueueHeldWhen returning the held message's receipt
// too (ini-yjcy): the send's own when it is a queued message re-sent.
func (p *Pane) enqueueHeldReceipt(text string, enter bool, when string) (refusal, receipt string) {
	p.mu.Lock()
	from, receipt := p.sendFrom, p.sendReceipt
	p.mu.Unlock()
	return p.enqueueReceipt(QueuedMessage{Text: text, Enter: enter, Time: time.Now(), HeldForDialog: true, From: from, Receipt: receipt}, when)
}

// setSendFrom records whose send the send path is typing, for a hold inside
// it (enqueueHeld). The caller holds sendMu around the send it names.
func (p *Pane) setSendFrom(from string) {
	p.mu.Lock()
	p.sendFrom = from
	p.mu.Unlock()
}

// setSendReceipt records the receipt of the queued message being re-sent,
// for a hold inside the send (ini-yjcy). The caller holds sendMu.
func (p *Pane) setSendReceipt(id string) {
	p.mu.Lock()
	p.sendReceipt = id
	p.mu.Unlock()
}

// setSendWaited records whether a caller waits on the verdict of the send
// being typed (ini-w6kr): a refusal before typing is then said by the
// verdict alone.
func (p *Pane) setSendWaited(waited bool) {
	p.mu.Lock()
	p.sendWaited = waited
	p.mu.Unlock()
}

// requeueFront puts a message back at the head of the queue unchanged, so a
// re-held message keeps both its place and its original time.
func (p *Pane) requeueFront(m QueuedMessage) {
	defer p.heldChanged() // the held file follows every change (ini-8rkg)
	p.mu.Lock()
	// Never trimmed: m was held already, and nothing held is dropped
	// (ini-pt3j).
	p.messageQueue = append([]QueuedMessage{m}, p.messageQueue...)
	p.mu.Unlock()
}

// beginRedelivery records the message a drain is about to hand to the send
// path, so the send's modal guard can recognise it as a re-delivery rather
// than new mail.
func (p *Pane) beginRedelivery(m QueuedMessage) {
	defer p.heldChanged() // the held file follows every change (ini-8rkg)
	p.mu.Lock()
	p.redelivering = &m
	p.mu.Unlock()
}

// takeRedelivery claims the in-flight re-delivery if text and enter match it.
// Matching on content keeps a concurrent, different send from being mistaken
// for the drain's message.
func (p *Pane) takeRedelivery(text string, enter bool) (QueuedMessage, bool) {
	defer p.heldChanged() // the held file follows every change (ini-8rkg)
	p.mu.Lock()
	defer p.mu.Unlock()
	m := p.redelivering
	if m == nil || m.Text != text || m.Enter != enter {
		return QueuedMessage{}, false
	}
	p.redelivering = nil
	return *m, true
}

// endRedelivery clears the in-flight mark and reports whether the send path
// kept the message (true) or handed it back because the dialog was open.
func (p *Pane) endRedelivery() (kept bool) {
	defer p.heldChanged() // the held file follows every change (ini-8rkg)
	p.mu.Lock()
	defer p.mu.Unlock()
	kept = p.redelivering != nil
	p.redelivering = nil
	return kept
}

// takeQueued removes and returns the queued messages pick selects, keeping
// the rest in order.
func (p *Pane) takeQueued(pick func(QueuedMessage) bool) []QueuedMessage {
	defer p.heldChanged() // the held file follows every change (ini-8rkg)
	p.mu.Lock()
	defer p.mu.Unlock()
	var out, keep []QueuedMessage
	for _, m := range p.messageQueue {
		if pick(m) {
			out = append(out, m)
		} else {
			keep = append(keep, m)
		}
	}
	p.messageQueue = keep
	return out
}

// surfaceHeldMail reports messages the fleet accepted and will not deliver:
// one Info line and one "NOT delivered (" event each, the format the toast
// and the event log already read for the belt's reports. The wait
// is in both, so the operator knows the message sat, not just that it failed.
func (p *Pane) surfaceHeldMail(msgs []QueuedMessage, why string, now time.Time) {
	for _, m := range msgs {
		p.dropReceipt(m, why)
		waited := now.Sub(m.Time).Round(time.Second)
		preview := m.Text
		if len(preview) > 60 {
			preview = preview[:57] + "..."
		}
		LogInfo("inject", "held message NOT DELIVERED", "pane", p.name, "reason", why,
			"waited", waited, "text", preview)
		p.reportEvent(AgentEvent{
			Type: EventAgentStalled,
			Pane: p.name,
			Detail: undeliveredSubmitPrefix + "held " + waited.String() + ", " + why +
				"), re-send it: " + preview,
			Time: now,
		})
	}
}

// resolveHeldMail is the maintenance tick's give-up rule. A pane whose process
// has exited (and is not merely suspended, whose queue is resume mail) can
// never take its mail: all of it is surfaced. On a live pane, a message held
// for a dialog longer than heldMessageDeadline is surfaced. Returns how many
// messages were surfaced.
func (p *Pane) resolveHeldMail(now time.Time) int {
	if p.QueuedMessageCount() == 0 {
		return 0
	}
	if !p.IsAlive() && !p.IsSuspended() {
		msgs := p.takeQueued(func(QueuedMessage) bool { return true })
		p.surfaceHeldMail(msgs, "the agent's process exited", now)
		return len(msgs)
	}
	// Do-not-disturb holds the whole queue: no dialog is keeping it
	// (ini-x432). Mail it released waits for a dialog like held mail, the
	// deadline counted from the release; the report gives the whole wait.
	if p.InDND() {
		return 0
	}
	msgs := p.takeQueued(func(m QueuedMessage) bool {
		if !m.Released.IsZero() {
			return now.Sub(m.Released) >= heldMessageDeadline
		}
		return m.HeldForDialog && now.Sub(m.Time) >= heldMessageDeadline
	})
	p.surfaceHeldMail(msgs, "a dialog stayed open past "+heldMessageDeadline.String(), now)
	return len(msgs) + p.endEmptyBoxHolds(now)
}

// noteBoxWait runs the clock emptyBoxHoldBound reads: since when an agent
// pane's input box has read neither empty, nor busy, nor at a dialog, on a
// live pane that is not suspended, in do-not-disturb, or wedged. Any other
// read stops it. rows is the maintenance tick's one read.
func (p *Pane) noteBoxWait(rows []string, now time.Time) {
	r := p.agentReader()
	waiting := false
	if r != noAgentReader && p.IsAlive() && !p.IsSuspended() && !p.InDND() && !p.dialogLatched() && !p.inputWedged() && !screenShowsModal(rows) {
		f := r.read(rows)
		waiting = !f.free() && !f.busy && !f.dialog
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case !waiting:
		p.boxWaitSince = time.Time{}
	case p.boxWaitSince.IsZero():
		p.boxWaitSince = now
	}
}

// endEmptyBoxHolds ends agent mail that has waited emptyBoxHoldBound for an
// input box that never read empty, counted from the later of its hold and
// the start of the box's current wait. Dialog, do-not-disturb and --after
// mail keep their own ends. Each is taken off the queue and out of the held
// file under the pane's lock, so it is never typed afterwards, and its sender
// is told once; one with no sender recorded is logged only.
func (p *Pane) endEmptyBoxHolds(now time.Time) int {
	p.mu.Lock()
	since := p.boxWaitSince
	p.mu.Unlock()
	if since.IsZero() {
		return 0
	}
	msgs := p.takeQueued(func(m QueuedMessage) bool {
		if !m.HeldForAgent || m.HeldForDialog || m.DND || !m.Released.IsZero() || m.After {
			return false
		}
		return now.Sub(later(m.Time, since)) >= emptyBoxHoldBound
	})
	for _, m := range msgs {
		p.dropReceipt(m, "its input box never read empty")
		line := fmt.Sprintf("the message to %s held since %s was not delivered: its input box never read empty; send it again", p.name, m.Time.Format("15:04"))
		LogInfo("inject", "held message NOT DELIVERED", "pane", p.name, "reason", "input box never read empty",
			"waited", now.Sub(m.Time).Round(time.Second), "has_sender", m.From != "")
		if m.From != "" {
			p.reportEvent(AgentEvent{Type: EventMessageSent, Pane: p.name, Detail: line, From: m.From, ToSender: line, Time: now})
		}
	}
	return len(msgs)
}

// later is the later of two times.
func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
