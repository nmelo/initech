package tui

// Carrying deferred mail across a pane respawn (ini-1z6i).
//
// restartPane built a fresh Pane and carried `protected` and nothing else, so
// every message deferred behind a modal died with the old pane -- no notice to
// the senders, none to the recipient. resumePane, the sibling respawn path,
// has carried the queue since ini-g7fl. The queue is the durable record of
// mail we accepted and have not delivered; a path that cannot deliver must
// hand it on or say so, never drop it quietly (the ini-a9d8 class).

import "strings"

// carryQueueForRestart moves deferred messages from a pane being replaced onto
// its replacement, and reports how many moved.
//
// It MOVES rather than copies: leaving them on the old pane too would deliver
// everything twice if that pane were ever drained again.
//
// Nothing here delivers. The maintenance tick drains the new pane once it is
// up, and it can: the modal latch does not survive the Pane, so the condition
// that deferred this mail in the first place is gone by construction.
func carryQueueForRestart(old, fresh *Pane) int {
	return giveQueueToRestartedPane(fresh, old.DrainQueue())
}

// giveQueueToRestartedPane is the second half, split out because restartPane
// cannot use the combined form: it must take the queue BEFORE Close and hand
// it over only AFTER the replacement spawns, with a failure path in between.
//
// Split so the product calls exactly what the tests drive. The first version
// left restartPane with its own inline copy of these three lines, which meant
// the cells could pass while the real path was broken.
func giveQueueToRestartedPane(fresh *Pane, msgs []QueuedMessage) int {
	if len(msgs) == 0 {
		return 0
	}
	fresh.mu.Lock()
	fresh.messageQueue = append(fresh.messageQueue, msgs...)
	fresh.mu.Unlock()

	LogInfo("restart", "carried deferred messages to the new pane",
		"agent", fresh.Name(), "count", len(msgs), "senders", queuePreview(msgs))
	return len(msgs)
}

// restoreQueueAfterFailedRestart puts drained mail back when the replacement
// pane never came up.
//
// Without this the fix would create a second silent discard exactly where the
// first one was: restartPane drains the queue, NewPane fails, and the messages
// are held by a local variable that is about to go out of scope.
func restoreQueueAfterFailedRestart(old *Pane, msgs []QueuedMessage) {
	if len(msgs) == 0 {
		return
	}
	old.mu.Lock()
	old.messageQueue = append(msgs, old.messageQueue...)
	old.mu.Unlock()

	LogWarn("restart", "restart failed with mail in hand; messages kept on the old pane",
		"agent", old.Name(), "count", len(msgs), "senders", queuePreview(msgs))
	EmitEvent(old.eventCh, AgentEvent{
		Type: EventAgentStalled,
		Pane: old.Name(),
		Detail: "restart failed; " + itoa(len(msgs)) +
			" undelivered message(s) still queued: " + queuePreview(msgs),
	})
}

// queuePreview names who is waiting, as far as the queue can.
//
// QueuedMessage carries no sender field, so this is the message text rather
// than an author: in practice fleet messages open with "[from <agent>]", which
// is what makes the preview identify the sender. Stated because the difference
// matters if that convention ever changes -- this would silently stop naming
// anyone while still looking informative.
func queuePreview(msgs []QueuedMessage) string {
	var parts []string
	for i, m := range msgs {
		if i == 3 {
			parts = append(parts, "...+"+itoa(len(msgs)-3)+" more")
			break
		}
		t := strings.TrimSpace(m.Text)
		if len(t) > 40 {
			t = t[:37] + "..."
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, " | ")
}
