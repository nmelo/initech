package tui

import "fmt"

// SendVerdict is what a send learned about its own delivery (ini-69fy): the
// outcome the sender is told, and the reason in the words the toast already
// uses. The send path decides it as it delivers, so a sender waiting on the
// reply hears the verdict instead of "handed to the pane".
type SendVerdict struct {
	Outcome string
	Reason  string
}

// Send outcomes, as they travel in IPCResponse.Delivery and
// ControlResp.Delivery. A reply with none comes from an initech that answers
// before its verdict.
const (
	// DeliveryDelivered: our text was seen in the box and the box cleared.
	// Also a --no-enter send, which submits nothing and so has nothing to
	// confirm, and a message queued for a suspended agent that a wake
	// delivers.
	DeliveryDelivered = "delivered"
	// DeliveryNotDelivered: our text is still in the box after the one
	// resend, or was never written. Travels as an error.
	DeliveryNotDelivered = "not_delivered"
	// DeliveryUnconfirmed: the send could not tell either way.
	DeliveryUnconfirmed = "unconfirmed"
	// DeliveryUnchecked: an agent type with no composer to check (Codex,
	// OpenCode, generic). Not a failure: nothing can be checked there.
	DeliveryUnchecked = "unchecked"
	// DeliveryDeferred: held behind an open dialog, delivered when it closes.
	DeliveryDeferred = "deferred"
)

// Reasons the send path gives that are not the watcher's own.
const (
	withheldVerdictReason  = "submit withheld, awaiting composer repaint; the TUI delivers it when the box repaints and reports"
	uncheckedVerdictReason = "no delivery check for this agent type"
	noPTYVerdictReason     = "pane has no PTY"
	retryMissVerdictReason = "Enter not taken twice; the text is still in the composer"
	olderPeerVerdictFormat = "older initech on %s"
	relayedVerdictReason   = "relayed on to another machine, which does not answer a relay"
)

// verdictIPCResponse is the reply to a send whose verdict is v, for the agent
// named name. Not delivered is an error, so every caller of the send action
// treats it as the refusal it already handles; the rest are OK with the
// outcome beside them, and an older CLI reads those as delivered. deferred is
// the deferral text a held send answers with.
func verdictIPCResponse(name string, v SendVerdict, deferred string) IPCResponse {
	switch v.Outcome {
	case DeliveryNotDelivered:
		return IPCResponse{Error: fmt.Sprintf("not delivered to %s: %s", name, v.Reason), Delivery: v.Outcome, Data: v.Reason}
	case DeliveryDeferred:
		return IPCResponse{OK: true, Delivery: v.Outcome, Data: deferred}
	}
	return IPCResponse{OK: true, Delivery: v.Outcome, Data: v.Reason}
}

// verdictControlResp is verdictIPCResponse for the control stream, where the
// connected initech names the agent itself: not delivered carries the bare
// reason as its error.
func verdictControlResp(v SendVerdict, deferred string) ControlResp {
	switch v.Outcome {
	case DeliveryNotDelivered:
		return ControlResp{Error: v.Reason, Delivery: v.Outcome}
	case DeliveryDeferred:
		return ControlResp{OK: true, Delivery: v.Outcome, Data: deferred}
	}
	return ControlResp{OK: true, Delivery: v.Outcome, Data: v.Reason}
}
