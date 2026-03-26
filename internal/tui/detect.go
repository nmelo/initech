// detect.go implements semantic event detection from JSONL journal entries.
// Detection functions scan recent entries for bead status transitions,
// completion patterns, stall conditions, and error loops.

package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ── Completion Detection ────────────────────────────────────────────

// bdUpdateRe matches "bd update <id> --status <status>" in tool output.
var bdUpdateRe = regexp.MustCompile(`bd\s+update\s+(\S+)\s+.*--status\s+(ready_for_qa|in_progress|in_qa)`)

// bdCloseRe matches "bd close <id>" in tool output.
var bdCloseRe = regexp.MustCompile(`bd\s+close\s+(\S+)`)

// detectCompletion scans journal entries for bead status transitions
// (bd update/close commands in Bash tool results) and DONE/FAIL patterns
// in assistant messages. Returns events for each detected transition.
func detectCompletion(entries []JournalEntry, paneName string) []AgentEvent {
	var events []AgentEvent

	for _, e := range entries {
		// Authoritative: Bash tool results with exit code 0.
		if e.ToolName == "Bash" && e.ExitCode == 0 && e.Type == "tool_result" {
			// Check for bd update --status ready_for_qa / in_progress / in_qa.
			if m := bdUpdateRe.FindStringSubmatch(e.Content); m != nil {
				beadID := m[1]
				status := m[2]
				var evType EventType
				var detail string
				switch status {
				case "ready_for_qa":
					evType = EventBeadCompleted
					detail = "marked " + beadID + " ready_for_qa"
				case "in_progress":
					evType = EventBeadClaimed
					detail = "claimed " + beadID
				case "in_qa":
					evType = EventBeadClaimed
					detail = "claimed " + beadID + " for QA"
				}
				events = append(events, AgentEvent{
					Type:   evType,
					Pane:   paneName,
					BeadID: beadID,
					Detail: detail,
					Time:   e.Timestamp,
				})
			}

			// Check for bd close.
			if m := bdCloseRe.FindStringSubmatch(e.Content); m != nil {
				events = append(events, AgentEvent{
					Type:   EventBeadCompleted,
					Pane:   paneName,
					BeadID: m[1],
					Detail: "closed " + m[1],
					Time:   e.Timestamp,
				})
			}
		}

		// Softer signal: assistant messages starting with DONE: or FAIL:.
		if e.Type == "assistant" {
			trimmed := strings.TrimSpace(e.Content)
			if strings.HasPrefix(trimmed, "DONE:") {
				events = append(events, AgentEvent{
					Type:   EventBeadCompleted,
					Pane:   paneName,
					Detail: "reported done",
					Time:   e.Timestamp,
				})
			} else if strings.HasPrefix(trimmed, "FAIL:") {
				events = append(events, AgentEvent{
					Type:   EventBeadFailed,
					Pane:   paneName,
					Detail: "reported failure",
					Time:   e.Timestamp,
				})
			}
		}
	}

	return events
}

// ── Stall Detection ─────────────────────────────────────────────────

// DefaultStallThreshold is the duration of inactivity before an agent
// with an assigned bead is considered stalled.
const DefaultStallThreshold = 10 * time.Minute

// detectStall checks whether an agent with a bead has been inactive
// for longer than threshold. Returns nil if no stall detected.
func detectStall(lastEntryTime time.Time, beadID string, paneName string, threshold time.Duration) *AgentEvent {
	// Only detect stalls for agents with an assigned bead.
	if beadID == "" {
		return nil
	}
	// No entries yet means we don't know if they're stalled.
	if lastEntryTime.IsZero() {
		return nil
	}
	if time.Since(lastEntryTime) < threshold {
		return nil
	}
	minutes := int(time.Since(lastEntryTime).Minutes())
	return &AgentEvent{
		Type:   EventAgentStalled,
		Pane:   paneName,
		BeadID: beadID,
		Detail: "no output for " + formatDuration(minutes),
		Time:   time.Now(),
	}
}

func formatDuration(minutes int) string {
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	h := minutes / 60
	m := minutes % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

// ── Stuck Detection ─────────────────────────────────────────────────

// consecutiveFailuresThreshold is how many back-to-back tool failures
// trigger an EventAgentStuck.
const consecutiveFailuresThreshold = 3

// detectStuck scans recent entries for consecutive tool failures (exit
// code != 0). Returns nil if no stuck pattern detected.
func detectStuck(entries []JournalEntry, paneName string) *AgentEvent {
	// Count consecutive failures from the end (most recent first).
	failures := 0
	var lastError string
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Type != "tool_result" {
			break // Non-tool entries (assistant, progress) end the consecutive streak (ini-a1e.3).
		}
		if e.ExitCode != 0 {
			failures++
			if lastError == "" && e.Content != "" {
				// Extract first line of error for the detail message.
				if idx := strings.IndexByte(e.Content, '\n'); idx > 0 {
					lastError = e.Content[:idx]
				} else {
					lastError = e.Content
				}
				if len(lastError) > 80 {
					lastError = lastError[:77] + "..."
				}
			}
		} else {
			break // Success breaks the consecutive failure streak.
		}
	}

	if failures < consecutiveFailuresThreshold {
		return nil
	}

	detail := "error loop"
	if lastError != "" {
		detail += ": " + lastError
	}

	return &AgentEvent{
		Type:   EventAgentStuck,
		Pane:   paneName,
		Detail: detail,
		Time:   time.Now(),
	}
}

// ── Dedup ───────────────────────────────────────────────────────────

// dedupKey uniquely identifies an event for deduplication.
type dedupKey struct {
	pane   string
	beadID string
	evType EventType
}

// dedupWindow is how long to suppress duplicate events.
const dedupWindow = 60 * time.Second

// dedup tracks recently emitted events to prevent duplicates.
type dedup struct {
	seen map[dedupKey]time.Time
}

func newDedup() *dedup {
	return &dedup{seen: make(map[dedupKey]time.Time)}
}

// shouldEmit returns true if this event hasn't been emitted recently.
// Records the emission time if true.
func (d *dedup) shouldEmit(ev AgentEvent) bool {
	key := dedupKey{pane: ev.Pane, beadID: ev.BeadID, evType: ev.Type}
	if last, ok := d.seen[key]; ok && time.Since(last) < dedupWindow {
		return false
	}
	d.seen[key] = time.Now()
	return true
}

// prune removes entries older than dedupWindow.
func (d *dedup) prune() {
	for k, t := range d.seen {
		if time.Since(t) > dedupWindow {
			delete(d.seen, k)
		}
	}
}
