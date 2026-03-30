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
// exists, timers and the ID counter are loaded from it. If the file doesn't
// exist or is invalid, the store starts empty.
func NewTimerStore(path string) *TimerStore {
	ts := &TimerStore{
		path:   path,
		nextID: 1,
	}
	ts.load()
	return ts
}

// Add creates a new timer and persists to disk. Returns the created timer.
func (ts *TimerStore) Add(target, host, text string, enter bool, fireAt time.Time) Timer {
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
	ts.save()
	return t
}

// Cancel removes a timer by ID and persists. Returns the canceled timer.
func (ts *TimerStore) Cancel(id string) (Timer, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	for i, t := range ts.timers {
		if t.ID == id {
			ts.timers = append(ts.timers[:i], ts.timers[i+1:]...)
			ts.save()
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
// removal. Returns nil if no timers are due.
func (ts *TimerStore) FireDue(now time.Time) []Timer {
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
		return nil
	}
	ts.timers = remaining
	ts.save()
	return due
}

// Pending returns the count of pending timers.
func (ts *TimerStore) Pending() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return len(ts.timers)
}

// load reads timers.json into the store. Errors are silently ignored
// (store starts empty on missing/corrupt file).
func (ts *TimerStore) load() {
	data, err := os.ReadFile(ts.path)
	if err != nil {
		return
	}
	var f timerFile
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	ts.timers = f.Timers
	ts.nextID = f.NextID
	if ts.nextID < 1 {
		ts.nextID = 1
	}
}

// save writes timers.json atomically (temp file + rename).
func (ts *TimerStore) save() {
	f := timerFile{
		NextID: ts.nextID,
		Timers: ts.timers,
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return
	}

	dir := filepath.Dir(ts.path)
	os.MkdirAll(dir, 0700)

	tmp := ts.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return
	}
	os.Rename(tmp, ts.path)
}

// fireTimers checks for due timers and delivers them. Called from the TUI
// tick loop (every 33ms) and once at startup for overdue timers.
func (t *TUI) fireTimers() {
	if t.timers == nil {
		return
	}
	due := t.timers.FireDue(time.Now())
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
