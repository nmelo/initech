// events.go defines the typed event system for agent activity detection.
// Detection modules (JSONL watchers, stall timers) emit AgentEvents through
// a buffered channel. The main TUI loop consumes them and appends to the
// notification queue for rendering.

package tui

import "time"

// EventType classifies semantic events from agent activity detection.
type EventType int

const (
	EventBeadCompleted EventType = iota // Agent finished a bead (DONE comment, ready_for_qa).
	EventBeadClaimed                    // Agent claimed a bead (in_progress).
	EventBeadFailed                     // QA failed a bead or agent reported failure.
	EventAgentStalled                   // No output for configurable threshold (warning).
	EventAgentStuck                     // Extended inactivity or error loop detected.
	EventAgentIdle                      // Agent returned to idle after work.
)

// String returns a human-readable label for the event type.
func (e EventType) String() string {
	switch e {
	case EventBeadCompleted:
		return "completed"
	case EventBeadClaimed:
		return "claimed"
	case EventBeadFailed:
		return "failed"
	case EventAgentStalled:
		return "stalled"
	case EventAgentStuck:
		return "stuck"
	case EventAgentIdle:
		return "idle"
	}
	return "unknown"
}

// AgentEvent represents a semantic event from an agent's activity.
// Emitted by JSONL watchers and consumed by the TUI main loop.
type AgentEvent struct {
	Type   EventType
	Pane   string    // Agent name (e.g., "eng1").
	BeadID string    // Relevant bead ID (empty if N/A).
	Detail string    // Human-readable description.
	Time   time.Time // When the event was detected.
}

// notification is a rendered event with an expiration time.
// The TUI displays active notifications and removes expired ones.
type notification struct {
	event   AgentEvent
	expires time.Time
}

// notificationTTL is how long a notification stays visible.
const notificationTTL = 10 * time.Second

// EmitEvent sends an event to the TUI's event channel without blocking.
// If the channel is full, the event is dropped (producers must not stall).
func EmitEvent(ch chan<- AgentEvent, ev AgentEvent) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	select {
	case ch <- ev:
	default:
		// Channel full. Drop the event rather than blocking the producer.
	}
}

// handleAgentEvent processes a single agent event. Appends it to the
// notification queue with an expiration time for rendering.
func (t *TUI) handleAgentEvent(ev AgentEvent) {
	t.notifications = append(t.notifications, notification{
		event:   ev,
		expires: time.Now().Add(notificationTTL),
	})
}

// pruneNotifications removes expired notifications.
func (t *TUI) pruneNotifications() {
	now := time.Now()
	alive := t.notifications[:0]
	for _, n := range t.notifications {
		if n.expires.After(now) {
			alive = append(alive, n)
		}
	}
	t.notifications = alive
}
