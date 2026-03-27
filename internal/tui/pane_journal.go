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
)

// watchJSONL polls for new JSONL entries in the session directory and feeds
// them into the pane's journal ring buffer and event detectors.
func (p *Pane) watchJSONL() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastFile string
	var lastOffset int64

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

		// File rotation: new file -> reset offset.
		if file != lastFile {
			lastFile = file
			lastOffset = 0
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

			// Bead auto-detection: check new entries for bd claim/clear signals.
			if p.eventCh != nil {
				p.applyBeadDetection(entries)
			}
		}

		p.runDetectors(entries)
	}
}

// applyBeadDetection runs detectBeadClaim on new entries and applies the result:
// sets or clears the pane's bead display and emits an event if appropriate.
// Must be called outside p.mu (it acquires the lock internally via SetBead).
func (p *Pane) applyBeadDetection(entries []JournalEntry) {
	beadID, clear := detectBeadClaim(entries)
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

// idleNotifyCooldown is the minimum time between EventAgentIdleWithBead
// emissions for a single pane. Prevents notification spam from burst output
// patterns that straddle the idle threshold.
const idleNotifyCooldown = 60 * time.Second

// updateActivity derives activity state from PTY output recency.
// Called per pane on every render tick. Detects running->idle edge transitions
// and emits EventAgentIdleWithBead when the pane holds a bead and the cooldown
// has elapsed.
func (p *Pane) updateActivity() {
	p.mu.Lock()
	defer p.mu.Unlock()

	prev := p.activity
	if !p.alive {
		if p.suspended {
			p.activity = StateSuspended
		} else {
			p.activity = StateDead
		}
		return
	}
	if time.Since(p.lastOutputTime) < ptyIdleTimeout {
		p.activity = StateRunning
	} else {
		p.activity = StateIdle
	}

	// Detect running->idle edge with a bead assigned and cooldown elapsed.
	if prev == StateRunning && p.activity == StateIdle &&
		p.beadID != "" && p.eventCh != nil &&
		time.Since(p.lastIdleNotify) > idleNotifyCooldown {
		p.lastIdleNotify = time.Now()
		EmitEvent(p.eventCh, AgentEvent{
			Type:   EventAgentIdleWithBead,
			Pane:   p.name,
			BeadID: p.beadID,
			Detail: p.beadID,
		})
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
	beadID := p.beadID
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

	// Stuck detection on full journal (every tick).
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
