package tui

import (
	"sort"
	"time"
)

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

// LiveEngine manages dynamic slot assignments for Live Mode.
// It ranks agents by conviction score and assigns them to slots,
// respecting pinned assignments.
type LiveEngine struct {
	Slots  []string       // Current agent per slot.
	Pinned map[string]int // Agent name -> fixed slot index.
}

// NewLiveEngine creates a LiveEngine with the given number of slots
// and pinned assignments.
func NewLiveEngine(numSlots int, pinned map[string]int) *LiveEngine {
	return &LiveEngine{
		Slots:  make([]string, numSlots),
		Pinned: pinned,
	}
}

// Tick computes slot assignments from the current pane states.
// Pinned agents are placed in their fixed slots. Dynamic slots are
// filled by the highest-scoring unpinned agents. When all scores are
// zero (everyone idle), dynamic slots show agents sorted by most
// recent output time descending.
//
// Returns the ordered list of pane names, one per slot. Empty strings
// indicate unfilled slots.
func (le *LiveEngine) Tick(panes []PaneView, now time.Time) []string {
	numSlots := len(le.Slots)
	result := make([]string, numSlots)

	// Place pinned agents first.
	pinnedSet := make(map[string]bool, len(le.Pinned))
	for name, slot := range le.Pinned {
		if slot >= 0 && slot < numSlots {
			result[slot] = name
			pinnedSet[name] = true
		}
	}

	// Score and rank unpinned, alive, non-suspended agents.
	type ranked struct {
		name           string
		score          int
		lastOutputTime time.Time
	}
	var candidates []ranked
	for _, p := range panes {
		pk := paneKey(p)
		if pinnedSet[pk] {
			continue
		}
		if !p.IsAlive() || p.IsSuspended() {
			continue
		}
		candidates = append(candidates, ranked{
			name:           pk,
			score:          convictionScore(p, now),
			lastOutputTime: p.LastOutputTime(),
		})
	}

	// Sort: highest score first, then most recent output time as tiebreaker.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].lastOutputTime.After(candidates[j].lastOutputTime)
	})

	// Fill dynamic (unpinned) slots.
	ci := 0
	for slot := 0; slot < numSlots; slot++ {
		if result[slot] != "" {
			continue // Pinned slot.
		}
		if ci < len(candidates) {
			result[slot] = candidates[ci].name
			ci++
		}
	}

	le.Slots = result
	return result
}

// liveTickSlots is a convenience function called by computeLayout.
// It creates a temporary LiveEngine, runs one Tick, and returns the
// slot name list. This keeps computeLayout stateless; persistent
// LiveEngine state (for anti-thrashing in ini-eny.3) will be held
// on the TUI struct.
func liveTickSlots(panes []PaneView, pinned map[string]int, numSlots int) []string {
	if numSlots < 1 {
		numSlots = len(panes)
	}
	le := NewLiveEngine(numSlots, pinned)
	return le.Tick(panes, time.Now())
}
