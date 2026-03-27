// Package tui resource management.
//
// resource.go is the home for all resource-aware agent lifecycle code:
// memory monitoring, auto-suspend policy, and resume-on-message. All of this
// is gated behind the autoSuspend bool on the TUI struct.
//
// When autoSuspend is false (the default), nothing in this file runs. The
// memory monitor goroutine is never started, the suspend policy never checks,
// and agents are never automatically suspended or resumed.
package tui

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ── Feature gate ────────────────────────────────────────────────────

// ResourceEnabled reports whether resource-aware auto-suspend is active for
// this TUI instance. All resource management code should check this gate
// before taking any action.
func (t *TUI) ResourceEnabled() bool {
	return t.autoSuspend
}

// PressureThreshold returns the configured RSS percentage above which agents
// may be auto-suspended. Returns 85 (the default) when not explicitly set.
func (t *TUI) PressureThreshold() int {
	if t.pressureThreshold > 0 {
		return t.pressureThreshold
	}
	return 85
}

// SystemMemoryAvailable returns the last polled available system RAM in
// kilobytes. Returns 0 if not yet polled or if the query failed.
func (t *TUI) SystemMemoryAvailable() int64 {
	return t.systemMemAvail
}

// SystemMemoryTotal returns total system RAM in kilobytes.
func (t *TUI) SystemMemoryTotal() int64 {
	return t.systemMemTotal
}

// ── Memory monitor ──────────────────────────────────────────────────

// startMemoryMonitor launches a goroutine that polls RSS per agent and system
// available memory every 10 seconds. Only called when autoSuspend is true.
func (t *TUI) startMemoryMonitor() {
	// Query total system memory once at startup.
	if total, err := systemMemoryTotal(); err == nil {
		t.systemMemTotal = total
	} else {
		LogWarn("resource", "failed to query total memory", "err", err)
	}

	LogInfo("resource", "memory monitor starting",
		"threshold", t.PressureThreshold(),
		"total_mb", t.systemMemTotal/1024)

	t.safeGo(func() { t.memoryMonitorLoop() })
}

// memoryMonitorLoop is the long-running goroutine body. It ticks every 10s
// and polls each pane's RSS plus system available memory.
func (t *TUI) memoryMonitorLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Do an initial poll immediately rather than waiting 10s.
	t.pollAllRSS()

	for {
		select {
		case <-ticker.C:
			t.pollAllRSS()
		case <-t.quitCh:
			return
		}
	}
}

// pollAllRSS reads RSS for every pane and updates system available memory.
func (t *TUI) pollAllRSS() {
	// Snapshot pane PIDs and references from the main goroutine.
	type paneSnap struct {
		pane *Pane
		pid  int
	}
	var snaps []paneSnap

	t.runOnMain(func() {
		snaps = make([]paneSnap, len(t.panes))
		for i, p := range t.panes {
			snaps[i] = paneSnap{pane: p, pid: p.pid}
		}
	})

	// Poll each pane's RSS outside the main goroutine (ps is blocking I/O).
	for _, s := range snaps {
		rss := pollPaneRSS(s.pid)
		s.pane.setMemoryRSS(rss)
	}

	// Poll system available memory.
	if avail, err := systemMemoryAvail(); err == nil {
		t.systemMemAvail = avail
	}
}

// pollPaneRSS queries the RSS of a single process via ps. Returns the RSS in
// kilobytes, or 0 if the PID is invalid or ps fails (process died).
func pollPaneRSS(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", fmt.Sprintf("%d", pid)).Output()
	if err != nil {
		return 0 // Process likely dead.
	}
	rss, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return rss
}

// ── Message queue for suspended panes ───────────────────────────────

// maxMessageQueue is the maximum number of messages buffered for a suspended
// pane. When the queue is full, the oldest message is dropped to make room.
const maxMessageQueue = 20

// QueuedMessage is a message waiting to be delivered to a suspended pane.
// On resume, the queue is drained in FIFO order via injectText.
type QueuedMessage struct {
	Text  string
	Enter bool
	Time  time.Time
}

// EnqueueMessage appends a message to the pane's queue. If the queue is at
// capacity (maxMessageQueue), the oldest message is dropped. Returns true if
// a message was dropped to make room.
//
// Caller must be on the main goroutine (via runOnMain).
func (p *Pane) EnqueueMessage(text string, enter bool) bool {
	dropped := false
	if len(p.messageQueue) >= maxMessageQueue {
		p.messageQueue = p.messageQueue[1:]
		dropped = true
	}
	p.messageQueue = append(p.messageQueue, QueuedMessage{
		Text:  text,
		Enter: enter,
		Time:  time.Now(),
	})
	return dropped
}

// DrainQueue returns all queued messages in FIFO order and clears the queue.
// Called on resume to deliver buffered messages.
//
// Caller must be on the main goroutine (via runOnMain).
func (p *Pane) DrainQueue() []QueuedMessage {
	if len(p.messageQueue) == 0 {
		return nil
	}
	msgs := p.messageQueue
	p.messageQueue = nil
	return msgs
}

// QueueLen returns the number of messages waiting in the queue.
func (p *Pane) QueueLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.messageQueue)
}
