// inbox_reply.go delivers the operator's answer to the agent that posted the
// item, and records what the SEND PATH says happened to it (ini-3wkl.6).
//
// Two rules shape every line here, and both were bought by someone else's
// measurement rather than reasoned out:
//
// SAME PATH AS A MESSAGE, NEVER A LOWER INJECT. The reply goes through
// PaneView.SendText, the primitive that carries the suspension guard and the
// wake callback (pane.go, ini-g7fl). TUI.injectText does not: it drops
// straight into sendPaneTextLocked, which returns immediately when ptmx is
// nil -- exactly what a suspended pane has -- so a reply sent that way would
// vanish while the item read "answered". injectText's one production caller
// drains the queue AFTER a resume, where re-queueing would loop.
//
// DELIVERY IS THE PATH'S VERDICT, NEVER THE PANEL'S. The operator's act is
// recorded at the keypress; whether it reached the agent is a separate fact
// with five starting values and two late corrections, all derived from what
// the path itself did -- a dialog it deferred behind, a submit its belt
// withheld, a wake that failed. Blank is never written as success: the store
// documents blank as unconfirmed, and an answered item that is not confirmed
// delivered is never pruned, because its stored reply may be the only copy of
// the operator's answer.
package tui

import (
	"strings"
)

// Delivery statuses. These are reported to the operator and printed by
// --check, so they read as the path's own account rather than as state names.
const (
	// inboxDeliverySending is the in-flight value written at the keypress. It
	// is deliberately not blank: blank means "nothing ever tried".
	inboxDeliverySending = "sending"

	inboxDeliveryQueuedSuspended = "queued — suspended, wakes and delivers"
	inboxDeliveryNotRunning      = "not running — stored"
	inboxDeliveryHeldDialog      = "held — dialog open"
	inboxDeliveryAwaitingSubmit  = "typed, awaiting submit"

	// inboxDeliveryNotDeliveredPrefix and inboxDeliveryWakeFailed are the two
	// LATE corrections, written when the send path reports on its own
	// initiative long after the keypress.
	inboxDeliveryNotDeliveredPrefix = "not delivered — "
	inboxDeliveryWakeFailed         = "wake failed — still queued"
)

// inboxReplyQuoteLen is how much of the item's first line the reply quotes.
const inboxReplyQuoteLen = 60

// inboxComposeReply renders the message the agent receives for a typed reply.
func inboxComposeReply(item InboxItem, reply string) string {
	return "re " + quoteInboxFirstLine(item) + " — " + reply
}

// inboxComposeAccept renders the message for an accepted default.
//
// ACCEPT IS A REPLY, NOT A SPECIAL CASE. This function composes text and
// nothing else; the caller hands it to the same seam a typed reply takes, so
// the item's reply text is stored by the ordinary write. An accept that
// marked the item answered without that write reported "answered:" with
// nothing after it and survived a whole first-pass suite (eng1, child A).
func inboxComposeAccept(item InboxItem) string {
	return "re " + quoteInboxFirstLine(item) + " — go with your default: " + item.DefaultText
}

// quoteInboxFirstLine renders the quoted, truncated first line.
func quoteInboxFirstLine(item InboxItem) string {
	line := strings.TrimSpace(item.FirstLine())
	if len(line) > inboxReplyQuoteLen {
		line = line[:inboxReplyQuoteLen-1] + "…"
	}
	return `"` + line + `"`
}

// wireInboxDelivery connects the panel's two keys to this bead's seam.
//
// Wired at the panel, not at construction, so a TUI that never opens the
// inbox never takes the dependency -- and so that the seam has exactly one
// place to be connected. The panel reports rather than pretends when these
// are nil (child C's own rule for the `a` key), which is what kept the two
// beads independently landable.
func (t *TUI) wireInboxDelivery() {
	if t.inbox.onReply == nil {
		t.inbox.onReply = func(id, reply string) {
			if err := t.ReplyToInboxItem(id, reply); err != nil {
				t.inbox.note = "could not reply: " + err.Error()
			}
		}
	}
	if t.inbox.onAccept == nil {
		t.inbox.onAccept = func(id string) {
			if err := t.AcceptInboxDefault(id); err != nil {
				t.inbox.note = "could not accept: " + err.Error()
			}
		}
	}
}

// ReplyToInboxItem records the operator's typed reply and delivers it.
// Main goroutine only; returns as soon as the act is stored.
func (t *TUI) ReplyToInboxItem(id, reply string) error {
	return t.answerInboxItem(id, func(item InboxItem) string {
		return inboxComposeReply(item, reply)
	})
}

// AcceptInboxDefault records acceptance of the item's stated default and
// delivers it. Same seam as ReplyToInboxItem by construction -- there is no
// accept-specific write to drift.
func (t *TUI) AcceptInboxDefault(id string) error {
	return t.answerInboxItem(id, inboxComposeAccept)
}

// answerInboxItem is the one seam both entry points take.
//
// THE OPERATOR'S ACT IS SYNCHRONOUS; THE DELIVERY IS NOT. Answer() and the
// initial status are written before this returns, so the operator's keypress
// is durable the instant it is made. The send itself runs on its own
// goroutine because it is allowed to take a long time on purpose -- the codex
// ready poll waits seconds, sendMu is held across a whole bracketed paste,
// and the wake callback blocks until a respawned agent initializes. None of
// that may sit on the key or render loop.
func (t *TUI) answerInboxItem(id string, compose func(InboxItem) string) error {
	ib := t.inboxState()
	item, ok := ib.Item(id)
	if !ok {
		return ErrInboxNoSuchItem
	}
	text := compose(item)
	if err := ib.Answer(id, text); err != nil {
		return err
	}
	// Best effort: an item whose status cannot be written is still answered,
	// and an unwritten status reads as unconfirmed, which keeps it unpruned.
	_ = ib.SetDeliveryStatus(id, inboxDeliverySending)
	t.noteInboxOutstanding(item.Agent, id)

	// Add on THIS goroutine, before the launch: the count must exist before
	// the delivery can run, or a waiter could see zero and proceed while the
	// goroutine is about to start (ini-5rvq). Nothing in production waits on
	// this; it is the edge a test joins so it never removes a directory the
	// delivery is still writing into.
	t.inboxDeliveries.Add(1)
	t.safeGo(func() {
		defer t.inboxDeliveries.Done()
		t.deliverInboxReply(id, item.Agent, text)
	})
	return nil
}

// deliverInboxReply performs the send and records the path's verdict. Runs on
// its own goroutine.
func (t *TUI) deliverInboxReply(id, agent, text string) {
	var pv PaneView
	var suspended, alive, modal bool
	if !t.runOnMain(func() {
		pv = t.findPaneByName(agent)
		if lp, ok := pv.(*Pane); ok {
			suspended = lp.IsSuspended()
			alive = lp.IsAlive()
			modal = paneHasModal(lp)
		} else if pv != nil {
			alive = pv.IsAlive()
		}
	}) {
		return // Shutting down; the item keeps "sending", which never prunes.
	}
	if pv == nil {
		t.setInboxDelivery(id, agent, inboxDeliveryNotRunning)
		return
	}

	// ONE CALL, THE SAME ONE initech send MAKES. Suspension, the modal queue
	// and the submit belt are all handled inside it; what follows only reads
	// what it did.
	pv.SendText(text, true)

	switch {
	case suspended:
		// SendText queued it and fired the wake callback. A failed wake
		// arrives later as an event and corrects this.
		t.setInboxDeliveryKeepOutstanding(id, inboxDeliveryQueuedSuspended)
		return
	case !alive:
		t.setInboxDelivery(id, agent, inboxDeliveryNotRunning)
		return
	case modal:
		// Deferred into the pane's queue; readLoop re-delivers when the
		// dialog closes. Observed the same way and with the same best-effort
		// caveat handleIPCSend's own deferral report carries.
		t.setInboxDelivery(id, agent, inboxDeliveryHeldDialog)
		return
	}

	withheld := false
	t.runOnMain(func() {
		if lp, ok := pv.(*Pane); ok {
			lp.mu.Lock()
			withheld = lp.pendingSubmit != nil
			lp.mu.Unlock()
		}
	})
	if withheld {
		// The never-submit belt kept the terminator: the body is in the
		// composer, the message is not delivered. Resolution arrives later as
		// an event, either way.
		t.setInboxDeliveryKeepOutstanding(id, inboxDeliveryAwaitingSubmit)
		return
	}
	// Live pane, no dialog, no withheld submit: a submit actually went out.
	// This is the ONLY value that lets the item prune.
	t.setInboxDelivery(id, agent, InboxDelivered)
}

// THE OUTSTANDING MAP CARRIES ITS OWN LOCK (found by -race on this bead).
//
// Every access is on the main goroutine in production -- the keypress notes,
// the event handler takes, and the delivery goroutine drops through runOnMain
// -- so the first version relied on that for mutual exclusion. That is a rule
// about who calls, not a property of the data: runOnMain executes DIRECTLY on
// the caller when there is no dispatch channel, which is every test and any
// future caller that runs before the loop exists, and two deliveries then race
// on a bare map. A mutex costs nothing here and removes the dependency on
// which goroutine happens to be running.

// noteInboxOutstanding records a delivery whose verdict may still change.
//
// Keyed by the DISPATCHED ID, in order, not by "the most recent answered item
// for this agent": a late event must land on the item it belongs to, and two
// replies to one agent can be in flight. The pane's withheld submit is a
// single field, so the pane serialises them and the oldest outstanding is the
// one a late report is about.
func (t *TUI) noteInboxOutstanding(agent, id string) {
	t.inboxOutstandingMu.Lock()
	defer t.inboxOutstandingMu.Unlock()
	if t.inboxOutstanding == nil {
		t.inboxOutstanding = make(map[string][]string)
	}
	t.inboxOutstanding[agent] = append(t.inboxOutstanding[agent], id)
}

// takeInboxOutstanding removes and returns the oldest outstanding delivery for
// an agent.
func (t *TUI) takeInboxOutstanding(agent string) (string, bool) {
	t.inboxOutstandingMu.Lock()
	defer t.inboxOutstandingMu.Unlock()
	ids := t.inboxOutstanding[agent]
	if len(ids) == 0 {
		return "", false
	}
	id := ids[0]
	if len(ids) == 1 {
		delete(t.inboxOutstanding, agent)
	} else {
		t.inboxOutstanding[agent] = ids[1:]
	}
	return id, true
}

// dropInboxOutstanding removes one id wherever it sits in an agent's queue.
func (t *TUI) dropInboxOutstanding(agent, id string) {
	t.inboxOutstandingMu.Lock()
	defer t.inboxOutstandingMu.Unlock()
	ids := t.inboxOutstanding[agent]
	for i, got := range ids {
		if got != id {
			continue
		}
		ids = append(ids[:i:i], ids[i+1:]...)
		if len(ids) == 0 {
			delete(t.inboxOutstanding, agent)
		} else {
			t.inboxOutstanding[agent] = ids
		}
		return
	}
}

// setInboxDelivery records a verdict that cannot change again and forgets the
// outstanding delivery.
func (t *TUI) setInboxDelivery(id, agent, status string) {
	t.setInboxDeliveryKeepOutstanding(id, status)
	t.dropInboxOutstanding(agent, id)
}

// setInboxDeliveryKeepOutstanding records a verdict that a later path report
// may still correct.
func (t *TUI) setInboxDeliveryKeepOutstanding(id, status string) {
	if err := t.inboxState().SetDeliveryStatus(id, status); err != nil {
		LogWarn("inbox", "delivery status not recorded", "item", id, "status", status, "err", err)
	}
}

// noteInboxDeliveryEvent lets the send path's own late reports correct an
// item's delivery status.
//
// Both reports already exist and already travel on the agent event stream --
// the withheld submit that never resolved (surfaceUndeliveredSubmit) and the
// wake that failed (wireSuspendResume). Reading them here rather than adding a
// new callback means the inbox learns what the path already tells everyone
// else, instead of the path learning about the inbox.
func (t *TUI) noteInboxDeliveryEvent(ev AgentEvent) {
	if ev.Type != EventAgentStalled || ev.Pane == "" {
		return
	}
	var status string
	switch {
	case strings.HasPrefix(ev.Detail, undeliveredSubmitPrefix):
		status = inboxDeliveryNotDeliveredPrefix + inboxEventReason(ev.Detail)
	case strings.HasPrefix(ev.Detail, "Resume failed:"):
		status = inboxDeliveryWakeFailed
	default:
		return
	}
	id, ok := t.takeInboxOutstanding(ev.Pane)
	if !ok {
		return
	}
	t.setInboxDeliveryKeepOutstanding(id, status)
}

// inboxEventReason trims the undelivered-submit event down to the part that
// says what happened, without the re-send instruction meant for the operator.
func inboxEventReason(detail string) string {
	// The event reads "NOT delivered (<versions>) after <waited> (<why>),
	// re-send it: <preview>". Keep the middle: the version parenthesis is the
	// probe's, and the re-send instruction is addressed to the operator.
	if i := strings.Index(detail, ") "); i >= 0 {
		detail = detail[i+2:]
	}
	if i := strings.Index(detail, ", re-send it:"); i >= 0 {
		detail = detail[:i]
	}
	return strings.TrimSpace(detail)
}

// InboxDeliveryStatusOf reports an item's current delivery status for the
// panel. Blank reads as unconfirmed, never as delivered.
func (t *TUI) InboxDeliveryStatusOf(id string) string {
	if item, ok := t.inboxState().Item(id); ok {
		if item.DeliveryStatus == "" {
			return "unconfirmed"
		}
		return item.DeliveryStatus
	}
	return ""
}
