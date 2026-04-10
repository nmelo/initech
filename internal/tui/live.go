package tui

import "time"

// convictionScore returns a weighted score (0-100) representing how strongly
// we believe a pane is actively working. Used by Live Mode to rank agents
// for dynamic slot assignment.
//
// Signals and weights:
//   - Bead assigned:          30  (has work to do)
//   - Sustained activity >5s: 20  (actively producing output)
//   - Output volume >10KB:    15  (meaningful output, not just a prompt)
//   - Recent dispatch <30s:   25  (just received a message)
//   - Recent event <10s:      10  (semantic event fired recently)
func convictionScore(p PaneView, now time.Time) int {
	if !p.IsAlive() {
		return 0
	}
	if p.IsSuspended() {
		return 0
	}

	score := 0

	if p.BeadID() != "" {
		score += 30
	}
	if p.Activity() == StateRunning {
		if dur := now.Sub(p.ActiveRunStart()); dur > 5*time.Second {
			score += 20
		}
	}
	if p.ActiveRunBytes() > 10*1024 {
		score += 15
	}
	if !p.LastMessageReceived().IsZero() && now.Sub(p.LastMessageReceived()) < 30*time.Second {
		score += 25
	}
	if !p.LastEventTime().IsZero() && now.Sub(p.LastEventTime()) < 10*time.Second {
		score += 10
	}

	return score
}
