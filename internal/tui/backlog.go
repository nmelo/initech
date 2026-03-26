// backlog.go detects when idle agents have ready work in the bead backlog.
// It runs a periodic check every 2 minutes: if any agent is idle without a
// bead AND bd has ready beads, it emits EventAgentIdle events so the operator
// knows dispatch is needed.
package tui

import (
	"encoding/json"
	"fmt"
	"time"

	iexec "github.com/nmelo/initech/internal/exec"
)

const backlogCheckInterval = 2 * time.Minute

// watchBacklog periodically checks for idle agents with no bead assignment
// against the bd ready queue. Emits EventAgentIdle events to eventCh when
// both conditions are met. Deduplicates: an agent is only notified once per
// idle episode (until it gains a bead or becomes active, then it's eligible
// again).
//
// Runs as a goroutine; exits when t.quitCh is closed.
func (t *TUI) watchBacklog(runner iexec.Runner) {
	ticker := time.NewTicker(backlogCheckInterval)
	defer ticker.Stop()

	// notified tracks which agents have already been notified for the current
	// idle episode. Cleared when the agent gains a bead or becomes active.
	notified := make(map[string]bool)

	for {
		select {
		case <-t.quitCh:
			return
		case <-ticker.C:
		}

		// Find idle agents with no bead.
		idle := t.idleAgentsWithoutBead()

		// Update dedup: clear any agent that is no longer idle or now has a bead.
		for name := range notified {
			stillIdle := false
			for _, n := range idle {
				if n == name {
					stillIdle = true
					break
				}
			}
			if !stillIdle {
				delete(notified, name)
			}
		}

		if len(idle) == 0 {
			continue
		}

		// Query bd for ready work.
		readyCount := queryBdReady(runner)
		if readyCount == 0 {
			continue
		}

		// Emit one event per idle agent, skipping already-notified ones.
		for _, name := range idle {
			if notified[name] {
				continue
			}
			notified[name] = true
			EmitEvent(t.agentEvents, AgentEvent{
				Type:   EventAgentIdle,
				Pane:   name,
				Detail: fmt.Sprintf("%s: idle, %d bead(s) ready", name, readyCount),
				Time:   time.Now(),
			})
		}
	}
}

// idleAgentsWithoutBead returns the names of panes that are alive, idle, and
// have no active bead assignment. These are candidates for new work dispatch.
func (t *TUI) idleAgentsWithoutBead() []string {
	var names []string
	for _, p := range t.panes {
		if p.IsAlive() && p.Activity() == StateIdle && p.BeadID() == "" {
			names = append(names, p.name)
		}
	}
	return names
}

// queryBdReady calls "bd ready --json" and returns the number of ready beads.
// Returns 0 on any error (bd not found, empty output, invalid JSON, etc.)
// so the caller can safely ignore detection when bd is unavailable.
func queryBdReady(runner iexec.Runner) int {
	out, err := runner.Run("bd", "ready", "--json")
	if err != nil || out == "" {
		return 0
	}
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		return 0
	}
	return len(items)
}
