// events.go defines the typed event system for agent activity detection.
// Detection modules (JSONL watchers, stall timers) emit AgentEvents through
// a buffered channel. The main TUI loop consumes them and appends to the
// notification queue for rendering.

package tui

import (
	"strings"
	"time"
)

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

// maxNotifications is the most toasts visible at once. Oldest are dropped.
const maxNotifications = 5

// handleAgentEvent processes a single agent event. Appends it to the
// notification queue with an expiration time for rendering.
func (t *TUI) handleAgentEvent(ev AgentEvent) {
	ttl := notificationTTL
	// Completion events persist longer since they're more actionable.
	if ev.Type == EventBeadCompleted {
		ttl = 12 * time.Second
	}
	t.notifications = append(t.notifications, notification{
		event:   ev,
		expires: time.Now().Add(ttl),
	})
	// Cap at maxNotifications. Drop oldest if over limit.
	if len(t.notifications) > maxNotifications {
		t.notifications = t.notifications[len(t.notifications)-maxNotifications:]
	}
}

// detectBeadClaim scans a batch of new JSONL entries for bd state transitions.
// Returns (beadID, false) when an agent claims a bead, or ("", true) when an
// agent marks a bead ready_for_qa (clear the display). Returns ("", false) when
// no relevant transition is found.
//
// Claim signals: tool_use Content contains "bd update" and "--claim" or
// "--status in_progress". The bead ID is the first argument after "bd update".
// Clear signals: Content contains "bd update" and "--status ready_for_qa".
// Either signal is ignored when ExitCode != 0 (failed bd command).
func detectBeadClaim(entries []JournalEntry) (beadID string, clear bool) {
	for _, e := range entries {
		if e.ExitCode != 0 {
			continue
		}
		if e.ToolName != "Bash" {
			continue
		}
		content := e.Content
		if !strings.Contains(content, "bd update") {
			continue
		}
		isClaim := strings.Contains(content, "--claim") ||
			(strings.Contains(content, "--status in_progress") || strings.Contains(content, "--status=in_progress"))
		isClear := strings.Contains(content, "--status ready_for_qa") || strings.Contains(content, "--status=ready_for_qa")

		if isClear {
			return "", true
		}
		if isClaim {
			id := extractBeadID(content)
			if id != "" {
				return id, false
			}
		}
	}
	return "", false
}

// extractBeadID parses the bead ID from a bd update command string.
// Expects a token that looks like a bead ID (contains a dot, e.g. "ini-18m.5").
func extractBeadID(cmd string) string {
	fields := strings.Fields(cmd)
	for i, f := range fields {
		if f == "update" && i+1 < len(fields) {
			candidate := fields[i+1]
			// Bead IDs contain a hyphen and a dot (e.g. "ini-18m.5", "ini-q7x.1").
			if strings.Contains(candidate, "-") && strings.Contains(candidate, ".") {
				return candidate
			}
		}
	}
	return ""
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
