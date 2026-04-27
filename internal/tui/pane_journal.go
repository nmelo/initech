// pane_journal.go contains JSONL session file watching, entry parsing,
// activity state derivation, and event detection (bead claims, completions,
// stalls, stuck loops). The watchJSONL goroutine polls for new entries and
// feeds them into the pane's ring buffer and event detectors.
package tui

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// watchJSONL polls for new JSONL entries in the session directory and feeds
// them into the pane's journal ring buffer and event detectors.
func (p *Pane) watchJSONL() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastFile string
	var lastOffset int64
	initialRead := true // First read of a file populates the journal but skips bead detection.

	for {
		<-ticker.C

		p.mu.Lock()
		alive := p.alive
		p.mu.Unlock()
		if !alive {
			return
		}

		// Find the most recently modified .jsonl file in the session dir.
		file := newestJSONL(p.jsonlDir)
		if file == "" {
			continue
		}

		// File rotation: new file -> reset offset. Mark as initial read
		// so the first batch of entries doesn't trigger bead detection
		// (prevents ghost beads from stale --continue session history).
		if file != lastFile {
			lastFile = file
			lastOffset = 0
			initialRead = true
		}

		// Check file size. Truncation (size < offset) -> reset.
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		size := info.Size()
		if size < lastOffset {
			lastOffset = 0
		}
		if size == lastOffset {
			p.runDetectors(nil) // stall/stuck checks run every tick
			continue
		}

		// Read new entries from offset.
		entries, newOffset := recentJSONLEntries(file, lastOffset)
		lastOffset = newOffset

		if len(entries) > 0 {
			p.mu.Lock()
			// Append to ring buffer.
			for _, e := range entries {
				if len(p.journal) >= journalRingSize {
					p.journal = p.journal[1:]
				}
				p.journal = append(p.journal, e)
			}
			p.mu.Unlock()

			// Bead auto-detection: only run on entries written AFTER the pane
			// started watching. The initial read loads history from prior
			// --continue sessions; those old bd update commands would set ghost
			// bead IDs that no longer exist (ini-anv).
			if p.eventCh != nil && !initialRead {
				p.applyBeadDetection(entries)
			}
		}

		if initialRead {
			initialRead = false
			// Still run detectors to initialize stall/stuck state, but entries
			// from the initial read are not treated as "new" for detection.
			p.runDetectors(nil)
		} else {
			p.runDetectors(entries)
		}
	}
}

// applyBeadDetection runs detectBeadClaim on entries from the current session
// and applies the result: sets or clears the pane's bead display and emits
// an event if appropriate. Entries older than the pane's startup time are
// filtered out to prevent stale bead IDs from prior sessions (ini-6hz).
// Must be called outside p.mu (it acquires the lock internally via SetBead).
func (p *Pane) applyBeadDetection(entries []JournalEntry) {
	if !p.cfg.BeadsEnabled {
		return
	}
	// Filter to entries from this session only.
	var current []JournalEntry
	for _, e := range entries {
		if !e.Timestamp.IsZero() && e.Timestamp.Before(p.startedAt) {
			continue
		}
		current = append(current, e)
	}
	if len(current) == 0 {
		return
	}
	beadID, clear := detectBeadClaim(current)
	switch {
	case clear:
		p.SetBead("", "")
	case beadID != "":
		p.SetBead(beadID, "")
		EmitEvent(p.eventCh, AgentEvent{
			Type:   EventBeadClaimed,
			Pane:   p.name,
			BeadID: beadID,
			Detail: p.name + " claimed " + beadID,
		})
	}
}

// ptyIdleTimeout is how long to wait after the last PTY byte before declaring
// an agent idle. Claude Code's spinner runs at 10-30fps during all active
// states (thinking, tool execution, generation). A 2-second gap in output
// means the agent is genuinely idle at the prompt.
const ptyIdleTimeout = 2 * time.Second

// ptyIdleTimeoutCodex is the idle threshold for Codex and OpenCode agents.
// These agents have natural 5-10s pauses between tool calls with no spinner,
// so the 2s Claude Code threshold fires false positives. 15s accommodates
// normal inter-tool-call gaps without masking genuinely stuck agents.
const ptyIdleTimeoutCodex = 15 * time.Second

// defaultIdleWithBeadThreshold is how long a pane must be silent (no PTY
// output) before an idle-with-bead notification fires. This is deliberately
// much longer than ptyIdleTimeout (2s/15s for the activity bar) because
// agents regularly pause 5-30s during thinking, tool approval, and file reads.
const defaultIdleWithBeadThreshold = 60 * time.Second

// idleNotifyCooldown is the minimum time between EventAgentIdleWithBead
// emissions for a single pane. Prevents notification spam from burst output
// patterns that straddle the idle threshold.
const idleNotifyCooldown = 60 * time.Second

// effectiveIdleTimeout returns the idle threshold for this pane's agent type.
// Codex/OpenCode agents get a longer threshold because they pause 5-10s
// between tool calls with no spinner output.
func (p *Pane) effectiveIdleTimeout() time.Duration {
	if config.IsCodexLikeAgentType(p.agentType) {
		return ptyIdleTimeoutCodex
	}
	return ptyIdleTimeout
}

// updateActivity derives activity state from PTY output recency.
// Called per pane on every render tick. The activity state (Running/Idle)
// uses ptyIdleTimeout (2s/15s) for responsive UI indicators. The
// idle-with-bead notification uses a separate, longer threshold (default
// 60s) to avoid false positives during normal thinking pauses (ini-hu2).
func (p *Pane) updateActivity() {
	p.mu.Lock()
	now := time.Now()

	prev := p.activity
	if !p.alive {
		if p.suspended {
			p.activity = StateSuspended
		} else {
			p.activity = StateDead
		}
		p.mu.Unlock()
		return
	}
	silenceDur := now.Sub(p.lastOutputTime)
	if silenceDur < p.effectiveIdleTimeout() {
		p.activity = StateRunning
	} else {
		p.activity = StateIdle
	}

	// Track conviction scoring edges.
	if prev != StateRunning && p.activity == StateRunning {
		p.activeRunStart = now
		p.activeRunBytes = 0
	}

	// Reset idle-with-bead flag when output resumes.
	if p.activity == StateRunning {
		p.idleBeadNotified = false
	}

	var idleEvent *AgentEvent
	primaryBead := ""
	if len(p.beadIDs) > 0 {
		primaryBead = p.beadIDs[0]
	}
	// Fire idle-with-bead once when silence exceeds the bead threshold.
	// Threshold of 0 disables entirely. The flag prevents re-firing every
	// tick; cooldown is a secondary safety net. The beadAssignedAt grace
	// window prevents false positives when a bead is assigned to an agent
	// that was already idle — the threshold is measured from assignment
	// time, not from last output (ini-t42).
	beadAge := now.Sub(p.beadAssignedAt)
	if p.idleWithBeadThreshold > 0 &&
		silenceDur > p.idleWithBeadThreshold &&
		beadAge > p.idleWithBeadThreshold &&
		!p.idleBeadNotified &&
		primaryBead != "" && p.eventCh != nil &&
		now.Sub(p.lastIdleNotify) > idleNotifyCooldown {
		p.idleBeadNotified = true
		p.lastIdleNotify = now
		idleEvent = &AgentEvent{
			Type:   EventAgentIdleWithBead,
			Pane:   p.name,
			BeadID: primaryBead,
			Detail: primaryBead,
		}
	}
	p.mu.Unlock()

	if idleEvent != nil {
		EmitEvent(p.eventCh, *idleEvent)
	}
}

// runDetectors runs all event detectors (completion, stall, stuck) and emits
// discovered events to p.eventCh. newEntries contains entries read since the
// last tick; pass nil when no new data arrived (stall/stuck still check every
// tick). Safe to call from watchJSONL goroutine only.
func (p *Pane) runDetectors(newEntries []JournalEntry) {
	if p.eventCh == nil || p.dedupEvents == nil {
		return
	}

	// Read protected fields atomically.
	p.mu.Lock()
	beadID := ""
	if len(p.beadIDs) > 0 {
		beadID = p.beadIDs[0]
	}
	journal := make([]JournalEntry, len(p.journal))
	copy(journal, p.journal)
	stallReported := p.stallReported
	stuckReported := p.stuckReported
	p.mu.Unlock()

	// Derive last JSONL entry time from the ring buffer for stall detection.
	var lastTime time.Time
	if len(journal) > 0 {
		lastTime = journal[len(journal)-1].Timestamp
	}

	// Bead-related detection (completion, stall) only when beads enabled.
	if p.cfg.BeadsEnabled {
		// Completion/claimed/failed detection on new entries only.
		if len(newEntries) > 0 {
			for _, ev := range detectCompletion(newEntries, p.name) {
				if p.dedupEvents.shouldEmit(ev) {
					EmitEvent(p.eventCh, ev)
				}
			}
			// New activity clears the stall state so the next silence
			// triggers a fresh stall notification rather than staying silent.
			if stallReported {
				p.mu.Lock()
				p.stallReported = false
				p.mu.Unlock()
			}
		}

		// Stall detection (every tick).
		if ev := detectStall(lastTime, beadID, p.name, DefaultStallThreshold); ev != nil {
			if !stallReported {
				p.mu.Lock()
				p.stallReported = true
				p.mu.Unlock()
				if p.dedupEvents.shouldEmit(*ev) {
					EmitEvent(p.eventCh, *ev)
				}
			}
		}
	}

	// Stuck detection on full journal (every tick). Not bead-specific.
	if ev := detectStuck(journal, p.name); ev != nil {
		if !stuckReported {
			p.mu.Lock()
			p.stuckReported = true
			p.mu.Unlock()
			if p.dedupEvents.shouldEmit(*ev) {
				EmitEvent(p.eventCh, *ev)
			}
		}
	} else if stuckReported {
		// Error loop cleared (success seen). Reset so next loop triggers again.
		p.mu.Lock()
		p.stuckReported = false
		p.mu.Unlock()
	}

	// Periodically prune the dedup map to avoid unbounded growth.
	p.dedupEvents.prune()
}

// newestJSONL finds the most recently modified .jsonl file in dir (non-recursive,
// excludes subdirectories like subagents/).
func newestJSONL(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var newest string
	var newestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = filepath.Join(dir, e.Name())
		}
	}
	return newest
}

// recentJSONLEntries reads new JSONL entries from the given file starting at
// sinceOffset. Returns parsed entries and the new file offset. Handles partial
// lines at the boundary by only consuming complete lines.
func recentJSONLEntries(path string, sinceOffset int64) ([]JournalEntry, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, sinceOffset
	}
	defer f.Close()

	if sinceOffset > 0 {
		if _, err := f.Seek(sinceOffset, 0); err != nil {
			return nil, sinceOffset
		}
	}

	var entries []JournalEntry
	// Use ReadBytes instead of bufio.Scanner so we count the actual bytes
	// consumed including the line terminator. Scanner adds a fixed +1 for the
	// newline, which drifts on CRLF files (each line needs +2). ReadBytes
	// returns the terminator as part of the slice, so len(lineBytes) is exact
	// for both LF and CRLF files.
	reader := bufio.NewReaderSize(f, 256*1024)
	bytesRead := sinceOffset

	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err == io.EOF {
			// Partial line at EOF — incomplete line, don't advance offset.
			break
		}
		if err != nil {
			break
		}
		bytesRead += int64(len(lineBytes))
		line := strings.TrimRight(string(lineBytes), "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}
		entry := parseJSONLEntry(line)
		if entry.Type != "" {
			entries = append(entries, entry)
		}
	}

	return entries, bytesRead
}

// parseJSONLEntry parses a single JSONL line into a JournalEntry.
// Extracts type, content, tool name, and exit code from the nested structure.
func parseJSONLEntry(line string) JournalEntry {
	var raw struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(line), &raw) != nil {
		return JournalEntry{}
	}

	entry := JournalEntry{Type: raw.Type}
	if raw.Timestamp != "" {
		entry.Timestamp, _ = time.Parse(time.RFC3339Nano, raw.Timestamp)
	}

	// Parse content array for assistant messages (text blocks, tool_use).
	if len(raw.Message.Content) > 0 {
		var blocks []struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Name  string `json:"name"`
			Input struct {
				Command string `json:"command"`
			} `json:"input"`
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ExitCode *int   `json:"exit_code"`
			} `json:"content"`
		}
		if json.Unmarshal(raw.Message.Content, &blocks) == nil {
			for _, b := range blocks {
				switch b.Type {
				case "text":
					entry.Content = truncateContent(b.Text)
				case "tool_use":
					entry.ToolName = b.Name
					if b.Input.Command != "" {
						entry.Content = truncateContent(b.Input.Command)
					}
				case "tool_result":
					for _, c := range b.Content {
						if c.Type == "text" {
							entry.Content = truncateContent(c.Text)
						}
						if c.ExitCode != nil {
							entry.ExitCode = *c.ExitCode
						}
					}
				}
			}
		} else {
			// Content might be a plain string (user messages).
			var text string
			if json.Unmarshal(raw.Message.Content, &text) == nil {
				entry.Content = truncateContent(text)
			}
		}
	}

	return entry
}

// truncateContent caps a string at maxContentLen bytes to prevent memory bloat
// in the ring buffer.
func truncateContent(s string) string {
	if len(s) > maxContentLen {
		return s[:maxContentLen] + "...[truncated]"
	}
	return s
}
