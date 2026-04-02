// timer.go implements the timer data model and JSON persistence for scheduled
// sends ("initech at"). Timers survive restarts via .initech/timers.json.
package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Timer represents a scheduled send: deliver Text to Target at FireAt.
type Timer struct {
	ID        string    `json:"id"`
	Target    string    `json:"target"`
	Host      string    `json:"host,omitempty"`
	Text      string    `json:"text"`
	Enter     bool      `json:"enter"`
	FireAt    time.Time `json:"fire_at"`
	CreatedAt time.Time `json:"created_at"`
}

// timerFile is the JSON structure persisted to disk.
type timerFile struct {
	NextID int     `json:"next_id"`
	Timers []Timer `json:"timers"`
}

// TimerStore manages scheduled timers with JSON persistence.
// All methods are safe for concurrent use.
type TimerStore struct {
	mu     sync.Mutex
	timers []Timer
	nextID int
	path   string
}

// NewTimerStore creates a store backed by the given file path. If the file
// exists, timers and the ID counter are loaded from it. Missing files are
// treated as empty (first run). Corrupt files log a warning and start empty
// so the operator is aware of data loss.
func NewTimerStore(path string) *TimerStore {
	ts := &TimerStore{
		path:   path,
		nextID: 1,
	}
	if err := ts.load(); err != nil {
		LogWarn("timer", "corrupt timer file, starting empty",
			"path", path, "err", err)
	}
	return ts
}

// Add creates a new timer and persists to disk. Returns the created timer
// and any persistence error. On save failure, the in-memory state is rolled
// back so it stays consistent with disk.
func (ts *TimerStore) Add(target, host, text string, enter bool, fireAt time.Time) (Timer, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	t := Timer{
		ID:        fmt.Sprintf("at-%d", ts.nextID),
		Target:    target,
		Host:      host,
		Text:      text,
		Enter:     enter,
		FireAt:    fireAt.UTC(),
		CreatedAt: time.Now().UTC(),
	}
	ts.nextID++
	ts.timers = append(ts.timers, t)

	if err := ts.save(); err != nil {
		// Rollback: remove the timer and restore the ID counter.
		ts.timers = ts.timers[:len(ts.timers)-1]
		ts.nextID--
		return Timer{}, fmt.Errorf("timer not persisted: %w", err)
	}
	return t, nil
}

// Cancel removes a timer by ID and persists. Returns the canceled timer
// and any error (not-found or persistence failure). On save failure, the
// in-memory removal is rolled back.
func (ts *TimerStore) Cancel(id string) (Timer, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	for i, t := range ts.timers {
		if t.ID == id {
			ts.timers = append(ts.timers[:i], ts.timers[i+1:]...)
			if err := ts.save(); err != nil {
				// Rollback: re-insert the timer at its original position.
				ts.timers = append(ts.timers[:i], append([]Timer{t}, ts.timers[i:]...)...)
				return Timer{}, fmt.Errorf("cancel not persisted: %w", err)
			}
			return t, nil
		}
	}
	return Timer{}, fmt.Errorf("timer %q not found", id)
}

// List returns all pending timers sorted by FireAt (earliest first).
func (ts *TimerStore) List() []Timer {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	out := make([]Timer, len(ts.timers))
	copy(out, ts.timers)
	sort.Slice(out, func(i, j int) bool {
		return out[i].FireAt.Before(out[j].FireAt)
	})
	return out
}

// FireDue returns and removes all timers where FireAt <= now. Persists after
// removal. Returns due timers and any save error. Even on save failure, the
// due timers are returned so they can still be fired (but the caller should
// log the error since the timers may resurrect after restart).
func (ts *TimerStore) FireDue(now time.Time) ([]Timer, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	var due, remaining []Timer
	for _, t := range ts.timers {
		if !t.FireAt.After(now) {
			due = append(due, t)
		} else {
			remaining = append(remaining, t)
		}
	}
	if len(due) == 0 {
		return nil, nil
	}
	ts.timers = remaining
	err := ts.save()
	return due, err
}

// Pending returns the count of pending timers.
func (ts *TimerStore) Pending() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return len(ts.timers)
}

// load reads timers.json into the store. Returns nil for missing files
// (normal first-run case). Returns an error for corrupt or unreadable files.
func (ts *TimerStore) load() error {
	data, err := os.ReadFile(ts.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // First run, no file yet.
		}
		return fmt.Errorf("read: %w", err)
	}
	var f timerFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	ts.timers = f.Timers
	ts.nextID = f.NextID
	if ts.nextID < 1 {
		ts.nextID = 1
	}
	return nil
}

// save writes timers.json atomically (temp file + rename). Returns an error
// if any step fails.
func (ts *TimerStore) save() error {
	f := timerFile{
		NextID: ts.nextID,
		Timers: ts.timers,
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	dir := filepath.Dir(ts.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	tmp := ts.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, ts.path); err != nil {
		os.Remove(tmp) // Clean up orphaned tmp.
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// fireTimers checks for due timers and delivers them. Called from the TUI
// tick loop (every 33ms) and once at startup for overdue timers.
func (t *TUI) fireTimers() {
	if t.timers == nil {
		return
	}
	due, err := t.timers.FireDue(time.Now())
	if err != nil {
		LogWarn("timer", "persistence error after firing timers",
			"err", err, "count", len(due))
	}
	for _, timer := range due {
		t.fireScheduledSend(timer)
	}
}

// fireScheduledSend delivers a single timer's message to its target agent.
// Logs warnings for missing/dead agents rather than failing hard.
func (t *TUI) fireScheduledSend(timer Timer) {
	delay := time.Since(timer.FireAt)
	if delay > time.Second {
		LogInfo("timer", "firing overdue",
			"id", timer.ID, "target", timer.Target,
			"scheduled", timer.FireAt.Format(time.RFC3339),
			"delay", delay.Truncate(time.Second).String())
	} else {
		LogInfo("timer", "firing", "id", timer.ID, "target", timer.Target)
	}

	// Find the target pane.
	var target PaneView
	for _, p := range t.panes {
		if timer.Host != "" {
			// Remote: match host + name.
			if p.Host() == timer.Host && p.Name() == timer.Target {
				target = p
				break
			}
		} else {
			// Local: match name only.
			if p.Name() == timer.Target {
				target = p
				break
			}
		}
	}

	if target == nil {
		hostInfo := ""
		if timer.Host != "" {
			hostInfo = " on peer " + timer.Host
		}
		LogWarn("timer", "agent not found, message not delivered",
			"id", timer.ID, "target", timer.Target+hostInfo)
		return
	}

	if !target.IsAlive() {
		LogWarn("timer", "agent is dead, message not delivered",
			"id", timer.ID, "target", timer.Target)
		return
	}

	target.SendText(timer.Text, timer.Enter)

	// Emit event for the event log.
	preview := timer.Text
	if len(preview) > 50 {
		preview = preview[:47] + "..."
	}
	if t.agentEvents != nil {
		EmitEvent(t.agentEvents, AgentEvent{
			Type:   EventTimerFired,
			Pane:   timer.Target,
			Detail: fmt.Sprintf("Timer %s fired: %s", timer.ID, preview),
			Time:   time.Now(),
		})
	}
}
