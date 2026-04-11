// tracker.go maps Slack threads to agents. When a user @mentions the bot
// to dispatch work, the tracker records the agent name -> (channel, threadTS)
// mapping. When the agent completes, the responder looks up the thread to
// post the result. Conversations expire after a TTL to prevent stale threads.
package slackchat

import (
	"sync"
	"time"
)

const conversationTTL = 30 * time.Minute

// Conversation represents an active Slack thread associated with an agent.
type Conversation struct {
	Channel   string
	ThreadTS  string
	StartedAt time.Time
}

// ConversationTracker maps agent names to active Slack threads.
// Thread-safe for concurrent access from the dispatcher and responder.
type ConversationTracker struct {
	mu    sync.Mutex
	convos map[string]Conversation // keyed by agent name
}

// NewConversationTracker creates an empty tracker.
func NewConversationTracker() *ConversationTracker {
	return &ConversationTracker{convos: make(map[string]Conversation)}
}

// Track records a conversation for the named agent. Overwrites any existing
// conversation for that agent (new @mention supersedes the old one).
func (t *ConversationTracker) Track(agent, channel, threadTS, userID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.convos[agent] = Conversation{
		Channel:   channel,
		ThreadTS:  threadTS,
		StartedAt: time.Now(),
	}
}

// Lookup returns the channel and threadTS for the named agent's active
// conversation. Returns false if no conversation exists or it has expired.
func (t *ConversationTracker) Lookup(agent string) (channel string, threadTS string, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c, exists := t.convos[agent]
	if !exists {
		return "", "", false
	}
	if time.Since(c.StartedAt) > conversationTTL {
		delete(t.convos, agent)
		return "", "", false
	}
	return c.Channel, c.ThreadTS, true
}

// Complete marks the conversation for the named agent as finished.
// Removes it from the tracker. Safe to call if no conversation exists.
func (t *ConversationTracker) Complete(agent string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.convos, agent)
}
