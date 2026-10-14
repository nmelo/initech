package tui

import (
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

// enqueueHeld queues a message deferred for an open dialog. It is the
// dialog-held twin of EnqueueMessage; only held messages are subject to the
// deadline, because a suspended pane's queue is resume mail with its own path.
func (p *Pane) enqueueHeld(text string, enter bool) bool {
	return p.enqueue(QueuedMessage{Text: text, Enter: enter, Time: time.Now(), HeldForDialog: true})
}

// requeueFront puts a message back at the head of the queue unchanged, so a
// re-held message keeps both its place and its original time.
func (p *Pane) requeueFront(m QueuedMessage) {
	p.mu.Lock()
	p.messageQueue = append([]QueuedMessage{m}, p.messageQueue...)
	if len(p.messageQueue) > maxMessageQueue {
		p.messageQueue = p.messageQueue[:maxMessageQueue]
	}
	p.mu.Unlock()
}

// beginRedelivery records the message a drain is about to hand to the send
// path, so the send's modal guard can recognise it as a re-delivery rather
// than new mail.
func (p *Pane) beginRedelivery(m QueuedMessage) {
	p.mu.Lock()
	p.redelivering = &m
	p.mu.Unlock()
}

// takeRedelivery claims the in-flight re-delivery if text and enter match it.
// Matching on content keeps a concurrent, different send from being mistaken
// for the drain's message.
func (p *Pane) takeRedelivery(text string, enter bool) (QueuedMessage, bool) {
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
	p.mu.Lock()
	defer p.mu.Unlock()
	kept = p.redelivering != nil
	p.redelivering = nil
	return kept
}

// takeQueued removes and returns the queued messages pick selects, keeping
// the rest in order.
func (p *Pane) takeQueued(pick func(QueuedMessage) bool) []QueuedMessage {
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
// one Info line and one "NOT delivered (" event each, the format the toast,
// the event log and the inbox already read for the belt's reports. The wait
// is in both, so the operator knows the message sat, not just that it failed.
func (p *Pane) surfaceHeldMail(msgs []QueuedMessage, why string, now time.Time) {
	for _, m := range msgs {
		waited := now.Sub(m.Time).Round(time.Second)
		preview := m.Text
		if len(preview) > 60 {
			preview = preview[:57] + "..."
		}
		LogInfo("inject", "held message NOT DELIVERED", "pane", p.name, "reason", why,
			"waited", waited, "text", preview)
		EmitEvent(p.eventCh, AgentEvent{
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
	msgs := p.takeQueued(func(m QueuedMessage) bool {
		return m.HeldForDialog && now.Sub(m.Time) >= heldMessageDeadline
	})
	p.surfaceHeldMail(msgs, "a dialog stayed open past "+heldMessageDeadline.String(), now)
	return len(msgs)
}
