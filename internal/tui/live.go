package tui

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Anti-thrashing constants for Live Mode. These are intentionally
// hardcoded per operator decision (not configurable).
const (
	liveHoldDuration   = 10 * time.Second // Minimum time a slot keeps its agent after assignment.
	liveClaimThreshold = 40               // Minimum score to claim a dynamic slot.
	liveKeepThreshold  = 10               // Minimum score to retain a dynamic slot.
	liveClaimMargin    = 20               // Challenger must exceed current occupant by this much.
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

// ranked holds a candidate's identity and scoring data for slot assignment.
type ranked struct {
	name           string
	score          int
	lastOutputTime time.Time
}

// LiveEngine manages dynamic slot assignments for Live Mode.
// It ranks agents by conviction score and assigns them to slots,
// respecting pinned assignments. Anti-thrashing mechanisms (hold time,
// hysteresis, one-swap-per-tick) prevent flickering when agent scores
// change rapidly.
type LiveEngine struct {
	Slots     []string       // Current agent per slot.
	Pinned    map[string]int // Agent name -> fixed slot index.
	holdUntil []time.Time    // Per-slot hold expiry; slot cannot change until this time.
}

// NewLiveEngine creates a LiveEngine with the given number of slots
// and pinned assignments.
func NewLiveEngine(numSlots int, pinned map[string]int) *LiveEngine {
	return &LiveEngine{
		Slots:     make([]string, numSlots),
		Pinned:    pinned,
		holdUntil: make([]time.Time, numSlots),
	}
}

// bestUnplaced returns the highest-ranked candidate not already placed.
func bestUnplaced(candidates []ranked, placed map[string]bool) (ranked, bool) {
	for _, c := range candidates {
		if !placed[c.name] {
			return c, true
		}
	}
	return ranked{}, false
}

// Tick computes slot assignments from the current pane states.
//
// Pinned agents are placed in their fixed slots unconditionally.
// Dynamic slots use anti-thrashing rules:
//   - Hold time: a slot keeps its agent for at least liveHoldDuration after assignment.
//   - Hysteresis: claiming requires score >= liveClaimThreshold and beating the
//     current occupant by liveClaimMargin. Keeping only requires >= liveKeepThreshold.
//   - One-swap-per-tick: at most one displacement per call, preventing full-screen flashes.
//
// Filling an empty slot is not a displacement and is always allowed.
// Returns the ordered list of pane names, one per slot. Empty strings
// indicate unfilled slots.
func (le *LiveEngine) Tick(panes []PaneView, now time.Time) []string {
	numSlots := len(le.Slots)

	// Log pane roster received by Tick.
	names := make([]string, len(panes))
	for i, p := range panes {
		names[i] = paneKey(p)
	}
	LogInfo("live-tick", "roster", "count", len(panes), "names", fmt.Sprintf("%v", names), "slots", numSlots)

	// Ensure holdUntil is correctly sized (handles engine reuse across resizes).
	if len(le.holdUntil) != numSlots {
		hold := make([]time.Time, numSlots)
		copy(hold, le.holdUntil)
		le.holdUntil = hold
	}

	result := make([]string, numSlots)

	// Place pinned agents first.
	pinnedSet := make(map[string]bool, len(le.Pinned))
	for name, slot := range le.Pinned {
		if slot >= 0 && slot < numSlots {
			result[slot] = name
			pinnedSet[name] = true
		}
	}

	// Score unpinned, alive, non-suspended agents.
	scores := make(map[string]ranked, len(panes))
	for _, p := range panes {
		pk := paneKey(p)
		if pinnedSet[pk] {
			LogInfo("live-tick", "pinned (skip scoring)", "agent", pk, "slot", le.Pinned[pk])
			continue
		}
		if !p.IsAlive() || p.IsSuspended() {
			LogInfo("live-tick", "skip dead/suspended", "agent", pk, "alive", p.IsAlive(), "suspended", p.IsSuspended())
			continue
		}
		s := convictionScore(p, now)
		LogInfo("live-tick", "score",
			"agent", pk,
			"total", s,
			"bead", p.BeadID(),
			"activity", p.Activity(),
			"runBytes", p.ActiveRunBytes(),
			"lastMsg", now.Sub(p.LastMessageReceived()).Truncate(time.Second),
			"lastEvent", now.Sub(p.LastEventTime()).Truncate(time.Second),
		)
		scores[pk] = ranked{
			name:           pk,
			score:          s,
			lastOutputTime: p.LastOutputTime(),
		}
	}

	// Build sorted candidate list for filling and challenging.
	candidates := make([]ranked, 0, len(scores))
	for _, s := range scores {
		candidates = append(candidates, s)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if !candidates[i].lastOutputTime.Equal(candidates[j].lastOutputTime) {
			return candidates[i].lastOutputTime.After(candidates[j].lastOutputTime)
		}
		return candidates[i].name < candidates[j].name
	})

	// Track which candidates are consumed by a slot.
	placed := make(map[string]bool, len(pinnedSet)+len(candidates))
	for name := range pinnedSet {
		placed[name] = true
	}

	swapped := false // one displacement per tick

	// Log current slot state before processing.
	for i, name := range le.Slots {
		holdLeft := le.holdUntil[i].Sub(now).Truncate(time.Second)
		if holdLeft < 0 {
			holdLeft = 0
		}
		LogInfo("live-tick", "slot-state", "slot", i, "occupant", name, "holdLeft", holdLeft)
	}

	for slot := 0; slot < numSlots; slot++ {
		if result[slot] != "" {
			LogInfo("live-tick", "slot-pinned", "slot", slot, "agent", result[slot])
			continue // pinned slot
		}

		prev := le.Slots[slot]
		prevInfo, prevAlive := scores[prev]

		// Empty slot: fill with best available candidate (not a displacement).
		if prev == "" {
			if best, ok := bestUnplaced(candidates, placed); ok {
				result[slot] = best.name
				placed[best.name] = true
				le.holdUntil[slot] = now.Add(liveHoldDuration)
				LogInfo("live-tick", "fill-empty", "slot", slot, "agent", best.name, "score", best.score)
			} else {
				LogInfo("live-tick", "empty-no-candidate", "slot", slot)
			}
			continue
		}

		// Hold time active: keep current occupant regardless of scores.
		if now.Before(le.holdUntil[slot]) {
			result[slot] = prev
			if prevAlive {
				placed[prev] = true
			}
			LogInfo("live-tick", "hold-active", "slot", slot, "agent", prev, "remaining", le.holdUntil[slot].Sub(now).Truncate(time.Second))
			continue
		}

		// Current occupant below keep threshold or no longer a valid candidate
		// (dead/suspended). Eligible for displacement by anyone >= claim threshold.
		if !prevAlive || prevInfo.score < liveKeepThreshold {
			if !swapped {
				if best, ok := bestUnplaced(candidates, placed); ok && best.score >= liveClaimThreshold {
					result[slot] = best.name
					placed[best.name] = true
					le.holdUntil[slot] = now.Add(liveHoldDuration)
					swapped = true
					LogInfo("live-tick", "displace-weak", "slot", slot, "old", prev, "oldScore", prevInfo.score, "oldAlive", prevAlive, "new", best.name, "newScore", best.score)
					continue
				}
			}
			// No swap available or already swapped this tick.
			// Evict: agent below keep threshold loses the slot regardless.
			// The empty slot can be filled on a future tick when someone qualifies.
			LogInfo("live-tick", "evict-weak", "slot", slot, "agent", prev, "score", prevInfo.score, "alive", prevAlive, "alreadySwapped", swapped)
			continue
		}

		// Current occupant above keep threshold: only displace if challenger
		// meets claim threshold AND exceeds by margin.
		if !swapped {
			if best, ok := bestUnplaced(candidates, placed); ok &&
				best.score >= liveClaimThreshold &&
				best.score-prevInfo.score >= liveClaimMargin {
				result[slot] = best.name
				placed[best.name] = true
				le.holdUntil[slot] = now.Add(liveHoldDuration)
				swapped = true
				LogInfo("live-tick", "displace-margin", "slot", slot, "old", prev, "oldScore", prevInfo.score, "new", best.name, "newScore", best.score, "margin", best.score-prevInfo.score)
				// Displaced agent returns to pool (not marked placed).
				continue
			}
			if best, ok := bestUnplaced(candidates, placed); ok {
				LogInfo("live-tick", "no-displace", "slot", slot, "occupant", prev, "occScore", prevInfo.score, "challenger", best.name, "chalScore", best.score, "needMargin", liveClaimMargin, "needThresh", liveClaimThreshold)
			} else {
				LogInfo("live-tick", "no-challenger", "slot", slot, "occupant", prev, "score", prevInfo.score)
			}
		} else {
			LogInfo("live-tick", "skip-already-swapped", "slot", slot, "occupant", prev, "score", prevInfo.score)
		}

		// Keep current occupant.
		result[slot] = prev
		placed[prev] = true
	}

	le.Slots = result
	LogInfo("live-tick", "result", "slots", fmt.Sprintf("%v", result))
	return result
}

// liveTickSlots is a convenience function called by computeLayout.
// It creates a temporary LiveEngine, runs one Tick, and returns the
// slot name list. Because this creates a throwaway engine, anti-thrashing
// state (hold times, occupant history) is lost between calls. For full
// anti-thrashing, use a persistent LiveEngine on the TUI struct.
func liveTickSlots(panes []PaneView, pinned map[string]int, numSlots int) []string {
	if numSlots < 1 {
		numSlots = len(panes)
	}
	le := NewLiveEngine(numSlots, pinned)
	return le.Tick(panes, time.Now())
}

// announceLiveSwap sends a fire-and-forget POST to the Agent Radio announce
// endpoint when a live mode slot swap occurs. The POST has a 1-second timeout
// and never blocks or delays rendering. Failures are logged and ignored.
func announceLiveSwap(announceURL, agent string) {
	body := fmt.Sprintf(`{"detail":"%s is now on screen","kind":"live.swap","agent":"%s"}`, agent, agent)
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Post(announceURL, "application/json", strings.NewReader(body))
	if err != nil {
		LogDebug("live", "announce swap failed", "err", err)
		return
	}
	resp.Body.Close()
}
