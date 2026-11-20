package tui

// queue_cap.go is the one cap on held mail (ini-pt3j): 500 messages or 1 MiB
// of message text per pane, whichever comes first, for every held kind
// (do-not-disturb, a suspended agent, an open dialog, a pane not reading). At
// the cap the new message is refused and nothing held is ever dropped; the
// refusal is one line telling the sender not to retry in a loop and when to
// send again. Nothing in initech retries a refused send.

import "fmt"

// maxQueueBytes is the byte half of the cap: 1 MiB of message text per pane,
// which keeps the held file (rewritten on every change, ini-8rkg) to a few
// MiB for a full rig.
const maxQueueBytes = 1 << 20

// When a refused sender can send again, by why the pane holds mail.
const (
	sendAgainWhenResumes  = "it resumes"
	sendAgainWhenDialog   = "its dialog closes"
	sendAgainWhenReading  = "it reads its input again"
	sendAgainWhenTurnEnds = "its turn ends"
	sendAgainWhenDNDOff   = "initech dnd off"
)

func queueBytes(q []QueuedMessage) int {
	n := 0
	for _, m := range q {
		n += len(m.Text)
	}
	return n
}

// queueLimitLocked names the limit adding m would pass ("500 messages" or
// "1 MiB of messages"), or "" when it fits. Call with p.mu held.
func (p *Pane) queueLimitLocked(m QueuedMessage) string {
	switch {
	case len(p.messageQueue) >= maxMessageQueue:
		return fmt.Sprintf("%d messages", maxMessageQueue)
	case queueBytes(p.messageQueue)+len(m.Text) > maxQueueBytes:
		return "1 MiB of messages"
	}
	return ""
}

// queueRefusal is the one line a refused sender reads (after the CLI's
// "not delivered to <agent>: ").
func queueRefusal(pane, limit, when string) string {
	return fmt.Sprintf("%s is holding %s (its limit); do not retry in a loop: send again after %s", pane, limit, when)
}

// dndWhenLocked is when a do-not-disturb hold ends, as a refusal says it.
// Call with p.mu held.
func (p *Pane) dndWhenLocked() string {
	switch {
	case p.dndUntilOff:
		return sendAgainWhenDNDOff
	case !p.dndUntil.IsZero():
		return "do-not-disturb ends at " + p.dndUntil.Local().Format("15:04")
	}
	return sendAgainWhenTurnEnds
}
