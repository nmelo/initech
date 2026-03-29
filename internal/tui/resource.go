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
	"sort"
	"strconv"
	"strings"
	"time"
)

// resumeGraceDuration is how long after resume a pane is exempt from
// auto-suspend. Prevents the idle detector from immediately re-suspending
// an agent that hasn't had time to start working yet.
const resumeGraceDuration = 2 * time.Minute

// maxSuspendPerCycle caps how many agents can be suspended in a single
// monitor tick. Prevents cascading suspension of many agents at once.
const maxSuspendPerCycle = 2

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
// and polls each pane's RSS plus system available memory, then evaluates the
// suspend policy.
func (t *TUI) memoryMonitorLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Do an initial poll immediately rather than waiting 10s.
	t.pollAllRSS()
	t.checkSuspendPolicy()

	for {
		select {
		case <-ticker.C:
			t.pollAllRSS()
			t.checkSuspendPolicy()
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
		snaps = make([]paneSnap, 0, len(t.panes))
		for _, pv := range t.panes {
			if p, ok := pv.(*Pane); ok {
				snaps = append(snaps, paneSnap{pane: p, pid: p.pid})
			}
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

// suspendCandidate holds the data needed to rank and suspend an agent.
type suspendCandidate struct {
	pane           *Pane
	lastOutputTime time.Time
	rssKB          int64
}

// checkSuspendPolicy evaluates memory pressure and suspends LRU idle agents
// if usage exceeds the configured threshold. Runs after each RSS poll.
func (t *TUI) checkSuspendPolicy() {
	total := t.systemMemTotal
	avail := t.systemMemAvail
	if total <= 0 || avail < 0 {
		return // Can't compute pressure without valid memory stats.
	}

	threshold := t.PressureThreshold()

	suspended := 0
	for suspended < maxSuspendPerCycle {
		// Recompute pressure each iteration (avail changes after suspend).
		usedPct := int((total - avail) * 100 / total)
		if usedPct <= threshold {
			return // Below threshold, nothing to do.
		}

		// Build candidate list on the main goroutine.
		var candidates []suspendCandidate
		t.runOnMain(func() {
			focused := t.layoutState.Focused
			for _, pv := range t.panes {
				p, ok := pv.(*Pane)
				if !ok {
					continue
				}
				p.mu.Lock()
				alive := p.alive
				activity := p.activity
				bead := p.beadID
				pinned := p.pinned
				name := p.name
				lot := p.lastOutputTime
				rss := p.memoryRSS
				inGrace := time.Now().Before(p.resumeGrace)
				p.mu.Unlock()

				if !alive || activity == StateSuspended || activity == StateDead {
					continue
				}
				if activity != StateIdle {
					continue
				}
				if bead != "" {
					continue // Never suspend agents with beads.
				}
				if pinned {
					continue
				}
				if name == focused {
					continue // Never suspend the focused agent.
				}
				if inGrace {
					continue // Recently resumed, give it time.
				}
				candidates = append(candidates, suspendCandidate{
					pane:           p,
					lastOutputTime: lot,
					rssKB:          rss,
				})
			}
		})

		if len(candidates) == 0 {
			LogDebug("resource", "pressure high but no suspend candidates",
				"used_pct", usedPct, "threshold", threshold)
			return
		}

		// Sort by lastOutputTime ascending (oldest/most idle first).
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].lastOutputTime.Before(candidates[j].lastOutputTime)
		})

		victim := candidates[0]
		freedKB := victim.rssKB
		victimName := victim.pane.name

		// Suspend: close the pane process and mark as suspended.
		// Must happen on the main goroutine since Close() touches TUI state.
		// Both suspended and activity must be set: suspended is the persistent
		// flag that handleIPCSend checks for message queueing, and activity
		// drives the overlay/ribbon display. Without setting suspended=true,
		// updateActivity would see alive=false + suspended=false and derive
		// StateDead instead of StateSuspended.
		t.runOnMain(func() {
			victim.pane.sendMu.Lock()
			victim.pane.Close()
			victim.pane.sendMu.Unlock()
			victim.pane.mu.Lock()
			victim.pane.suspended = true
			victim.pane.activity = StateSuspended
			victim.pane.mu.Unlock()
		})

		// Update available memory estimate (actual poll happens next cycle).
		avail += freedKB

		idleDur := time.Since(victim.lastOutputTime).Truncate(time.Second)
		detail := fmt.Sprintf("Suspended %s (idle %s, freed %s)",
			victimName, idleDur, formatRSSHuman(freedKB))

		LogInfo("resource", "agent suspended",
			"agent", victimName,
			"idle", idleDur,
			"freed_kb", freedKB,
			"used_pct", usedPct,
			"threshold", threshold)

		EmitEvent(t.agentEvents, AgentEvent{
			Type:   EventAgentSuspended,
			Pane:   victimName,
			Detail: detail,
		})

		suspended++
	}
}

// formatRSSHuman formats kilobytes into a human-readable string.
func formatRSSHuman(kb int64) string {
	if kb > 1048576 {
		return fmt.Sprintf("%.1f GB", float64(kb)/1048576)
	} else if kb > 1024 {
		return fmt.Sprintf("%.0f MB", float64(kb)/1024)
	}
	return fmt.Sprintf("%d KB", kb)
}

// pollPaneRSS returns the total RSS of a process and all its descendants in
// kilobytes. This is necessary because the pane's stored PID is the shell
// wrapper (/bin/sh), not the actual Claude process which is 2-3 levels deep:
//   /bin/sh (2MB) -> node/ccs (75MB) -> claude (500-900MB)
// Reading only the shell PID would report ~2MB instead of the real ~600-900MB.
func pollPaneRSS(pid int) int64 {
	return processTreeRSS(pid)
}

// processTreeRSS returns the RSS of the largest process in the tree rooted at
// pid. Uses a single `ps -eo pid,ppid,rss` call and builds the tree in memory.
// Returns the max RSS of any single descendant rather than the sum, because
// summing double-counts shared library pages mapped into every process. The
// largest descendant is typically the actual Claude binary which holds the bulk
// of the unique memory. This aligns with Activity Monitor's "Memory" column
// (physical footprint) much better than the raw RSS sum.
func processTreeRSS(pid int) int64 {
	if pid <= 0 {
		return 0
	}

	// Single syscall: get pid, ppid, rss for all processes.
	out, err := exec.Command("ps", "-eo", "pid=,ppid=,rss=").Output()
	if err != nil {
		return 0
	}

	// Parse into a parent-to-children map and a pid-to-rss map.
	children := make(map[int][]int)
	rssMap := make(map[int]int64)

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		p, err1 := strconv.Atoi(fields[0])
		pp, err2 := strconv.Atoi(fields[1])
		rss, err3 := strconv.ParseInt(fields[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		children[pp] = append(children[pp], p)
		rssMap[p] = rss
	}

	// Walk the tree, find the single process with the highest RSS.
	var maxRSS int64
	stack := []int{pid}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if rssMap[cur] > maxRSS {
			maxRSS = rssMap[cur]
		}
		stack = append(stack, children[cur]...)
	}
	return maxRSS
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

// ── Resume-on-message ───────────────────────────────────────────────

// resumeTimeout is how long to wait for a resumed agent to initialize
// before giving up.
const resumeTimeout = 30 * time.Second

// queueDrainInterval is the delay between delivering queued messages to a
// resumed pane. Gives Claude time to process each message.
const queueDrainInterval = 500 * time.Millisecond

// resumePane respawns a suspended pane and drains its message queue.
// Called from the IPC send handler when a message targets a suspended agent.
// Blocks until the agent is initialized and all queued messages are delivered,
// or returns an error if the respawn fails. On failure the message queue is
// preserved for the next attempt.
//
// Concurrent calls for the same pane are serialized by pane.resumeMu: the
// first caller performs the resume, subsequent callers re-check the suspended
// state and find the pane already alive.
func (t *TUI) resumePane(pane *Pane, senderName string) error {
	pane.resumeMu.Lock()
	defer pane.resumeMu.Unlock()

	// Re-check: another concurrent sender may have already resumed this pane.
	if !pane.IsSuspended() {
		return nil
	}

	agentName := pane.name
	LogInfo("resource", "resuming agent", "agent", agentName, "trigger", senderName)

	// Get old dimensions with fallback for dead panes.
	cols, rows := pane.emu.Width(), pane.emu.Height()
	if cols < 10 {
		cols = 80
	}
	if rows < 2 {
		rows = 24
	}

	// Create new pane process off-main (may fork/exec).
	np, err := NewPane(pane.cfg, rows, cols)
	if err != nil {
		LogError("resource", "resume failed", "agent", pane.name, "err", err)
		return fmt.Errorf("resume %s: %w", pane.name, err)
	}

	// Replace the old pane in t.panes on the main goroutine.
	var replaced bool
	var msgs []QueuedMessage
	t.runOnMain(func() {
		for i, p := range t.panes {
			if p == pane {
				np.region = pane.region
				np.eventCh = t.agentEvents
				np.safeGo = t.safeGo
				np.pinned = pane.pinned
				np.beadID = pane.beadID
				np.beadTitle = pane.beadTitle
				np.Start()
				t.panes[i] = np
				t.applyLayout()
				replaced = true

				// Drain the queue from the old pane while on main.
				msgs = pane.DrainQueue()
				break
			}
		}
	})

	if !replaced {
		np.Close()
		return fmt.Errorf("resume %s: pane not found in list", pane.name)
	}

	// Set resume grace on the NEW pane to prevent immediate re-suspension.
	np.SetResumeGrace(time.Now().Add(resumeGraceDuration))

	// Wait for the agent to initialize: poll for PTY output activity.
	if err := t.waitForInit(np); err != nil {
		LogWarn("resource", "resume init timeout", "agent", np.name, "err", err)
		// Agent started but may be slow. Continue with queue drain anyway
		// since the process is alive even if Claude hasn't fully initialized.
	}

	// Deliver queued messages with gaps.
	for _, msg := range msgs {
		t.injectText(np, msg.Text, msg.Enter)
		if len(msgs) > 1 {
			time.Sleep(queueDrainInterval)
		}
	}

	detail := fmt.Sprintf("Resumed %s (message from %s)", agentName, senderName)
	if len(msgs) > 0 {
		detail += fmt.Sprintf(", delivered %d queued", len(msgs))
	}

	LogInfo("resource", "agent resumed",
		"agent", agentName,
		"trigger", senderName,
		"queued_msgs", len(msgs))

	EmitEvent(t.agentEvents, AgentEvent{
		Type:   EventAgentResumed,
		Pane:   agentName,
		Detail: detail,
	})

	return nil
}

// waitForInit polls a newly started pane for signs of initialization.
// Returns nil when PTY output is detected, or an error after resumeTimeout.
func (t *TUI) waitForInit(pane *Pane) error {
	deadline := time.After(resumeTimeout)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
			if !pane.LastOutputTime().IsZero() {
				return nil // Agent produced output, it's alive.
			}
		case <-deadline:
			return fmt.Errorf("timeout waiting for %s to initialize", pane.name)
		case <-t.quitCh:
			return fmt.Errorf("TUI shutting down")
		}
	}
}
