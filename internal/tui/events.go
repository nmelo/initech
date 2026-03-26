// events.go defines the typed event system for agent activity detection.
// Detection modules (JSONL watchers, stall timers) emit AgentEvents through
// a buffered channel. The main TUI loop consumes them and appends to the
// notification queue for rendering.

package tui

import (
	"fmt"
	"regexp"
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
	EventAgentIdleWithBead              // Agent went running->idle while holding a bead.
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
	case EventAgentIdleWithBead:
		return "idle-with-bead"
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
	LogDebug("event", "emit", "type", ev.Type.String(), "pane", ev.Pane, "detail", ev.Detail)
	select {
	case ch <- ev:
	default:
		LogWarn("event", "channel full, dropped", "type", ev.Type.String(), "pane", ev.Pane)
	}
}

// maxNotifications is the most toasts visible at once. Oldest are dropped.
const maxNotifications = 5

// maxEventLog is the maximum number of events retained in the history log.
const maxEventLog = 100

// eventLogRetention is how long events are kept in the history log.
const eventLogRetention = 60 * time.Minute

// handleAgentEvent processes a single agent event. Appends it to the
// notification queue (for toasts) and the event log (for history).
func (t *TUI) handleAgentEvent(ev AgentEvent) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}

	// Clear idle-with-backlog indicator when an agent claims a bead.
	// This gives immediate feedback without waiting for the next backlog check.
	if ev.Type == EventBeadClaimed {
		if p := t.findPaneByName(ev.Pane); p != nil {
			p.ClearIdleWithBacklog()
		}
	}

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

	// When an agent goes idle while holding a bead, notify the super pane.
	// Run in a goroutine to avoid blocking the render loop.
	if ev.Type == EventAgentIdleWithBead {
		if super := t.findPaneByName("super"); super != nil && super.IsAlive() {
			msg := fmt.Sprintf("[from initech] %s is now idle (bead: %s). Check if work is complete.", ev.Pane, ev.BeadID)
			t.safeGo(func() { t.injectText(super, msg, true) })
		}
	}

	// Also append to the persistent event log.
	t.eventLog = append(t.eventLog, ev)
	t.pruneEventLog()
}

// pruneEventLog removes events that are older than eventLogRetention or
// exceed the maxEventLog cap. Oldest events are dropped first.
func (t *TUI) pruneEventLog() {
	cutoff := time.Now().Add(-eventLogRetention)
	// Drop events beyond the cap first (from the front).
	if len(t.eventLog) > maxEventLog {
		t.eventLog = t.eventLog[len(t.eventLog)-maxEventLog:]
	}
	// Drop events older than the retention window.
	start := 0
	for start < len(t.eventLog) && t.eventLog[start].Time.Before(cutoff) {
		start++
	}
	if start > 0 {
		t.eventLog = t.eventLog[start:]
	}
}

// detectBeadClaim scans a batch of new JSONL entries for bd state transitions.
// Returns (beadID, false) when an agent claims a bead, or ("", true) when an
// agent marks a bead ready_for_qa (clear the display). Returns ("", false) when
// no relevant transition is found.
//
// Only tool_result entries carry a meaningful ExitCode; tool_use entries
// (inside assistant messages) always have ExitCode 0. Checking Type ensures
// ExitCode-based filtering actually works (ini-a1e.2).
//
// The full entry list is scanned rather than returning on the first match so
// that a claim appearing after a clear in the same batch is not dropped
// (ini-a1e.5). The last matching signal wins.
//
// Claim signals: Content contains "bd update" and "--claim" or
// "--status in_progress". The bead ID is the first argument after "bd update".
// Clear signals: Content contains "bd update" and "--status ready_for_qa".
// Either signal is ignored when ExitCode != 0 (failed bd command).
func detectBeadClaim(entries []JournalEntry) (beadID string, clear bool) {
	for _, e := range entries {
		if e.Type != "tool_result" {
			continue
		}
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
			strings.Contains(content, "--status in_progress") || strings.Contains(content, "--status=in_progress")
		isClear := strings.Contains(content, "--status ready_for_qa") || strings.Contains(content, "--status=ready_for_qa")

		if isClear {
			beadID, clear = "", true
		}
		if isClaim {
			id := extractBeadID(content)
			if id != "" {
				beadID, clear = id, false
			}
		}
	}
	return beadID, clear
}

// beadIDRe matches a complete bead ID token. The dot-separated sub-bead index
// is optional so that both root-level IDs ("ini-hli", "ini-csf") and sub-bead
// IDs ("ini-a1e.14", "ini-q7x.1") are accepted. Anchored so the whole token
// must match — "some-other-thing" is rejected because of the trailing suffix.
var beadIDRe = regexp.MustCompile(`^[a-z]+-[a-z0-9]+(?:\.[0-9]+)?$`)

// extractBeadID parses the bead ID from a bd update command string.
// Accepts both root-level IDs (e.g. "ini-hli") and sub-bead IDs (e.g. "ini-a1e.14").
func extractBeadID(cmd string) string {
	fields := strings.Fields(cmd)
	for i, f := range fields {
		if f == "update" && i+1 < len(fields) {
			candidate := fields[i+1]
			if beadIDRe.MatchString(candidate) {
				return candidate
			}
		}
	}
	return ""
}

// pruneConfirmation auto-cancels a pending destructive command confirmation
// once its expiry time has passed. Called on each render tick so that the
// confirmation disappears automatically if the operator walks away.
// This keeps expiry logic out of the key handler: pressing Enter at exactly
// the deadline still confirms because the key arrives before the tick fires.
func (t *TUI) pruneConfirmation() {
	if t.cmd.pendingConfirm != "" && time.Now().After(t.cmd.confirmExpiry) {
		t.cmd.pendingConfirm = ""
		t.cmd.confirmMsg = ""
		t.cmd.active = false
	}
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
