package tui

// Live Mode Invariants
//
// 1. SCORE determines WHO QUALIFIES.
//    An agent must meet claimThreshold (40) to enter a slot,
//    keepThreshold (10) to retain one, and exceed the current
//    occupant by claimMargin (20) to displace them.
//
// 2. SCORE determines WHO DISPLACES.
//    bestUnplaced() returns the highest-scoring agent not already
//    in a slot. Score descending is the ONLY sort order for
//    candidate selection. Ties break by yaml role index.
//
// 3. YAML ORDER determines WHERE agents SIT.
//    After the live engine decides which agents are visible,
//    the final pane list is sorted by config.Roles index.
//    Pinned agents first, then unpinned by role order.
//    This is a DISPLAY concern, not a SELECTION concern.
//
// 4. ANTI-THRASHING determines WHEN.
//    Hold time (10s) prevents a slot from changing too soon.
//    One-swap-per-tick prevents multiple simultaneous changes.
//    These are TIMING constraints, independent of scoring.
//
// 5. OCCUPANTS ARE EXCLUDED from the unplaced pool.
//    An agent currently in a slot is never a candidate for
//    bestUnplaced(), even if another slot would score higher.
//    An agent can only be in one slot at a time.
//
// 6. bestUnplaced() ALWAYS returns the highest-scoring
//    non-placed agent, or nil if none qualify.
//    It never considers yaml order for selection.

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
	Slots      []string       // Current agent per slot.
	Pinned     map[string]int // Agent name -> fixed slot index.
	RolesOrder []string       // Config roles list for stable ordering in auto mode.
	holdUntil  []time.Time    // Per-slot hold expiry; slot cannot change until this time.
}

// NewLiveEngine creates a LiveEngine with the given number of slots
// and pinned assignments. rolesOrder provides stable ordering for auto mode;
// pass nil for fixed-slot mode where order is determined by slot index.
func NewLiveEngine(numSlots int, pinned map[string]int, rolesOrder []string) *LiveEngine {
	return &LiveEngine{
		Slots:      make([]string, numSlots),
		Pinned:     pinned,
		RolesOrder: rolesOrder,
		holdUntil:  make([]time.Time, numSlots),
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

	// Build candidate lists. Two sort orders serve different purposes:
	// - candidates: score descending (highest score wins displacement).
	// - fillOrder:  roles order (stable slot positioning when filling empties).
	roleIdx := make(map[string]int, len(le.RolesOrder))
	for i, r := range le.RolesOrder {
		roleIdx[r] = i
	}
	maxRoleIdx := len(le.RolesOrder)
	roleCmp := func(a, b ranked) bool {
		ai, aOK := roleIdx[a.name]
		bi, bOK := roleIdx[b.name]
		if !aOK {
			ai = maxRoleIdx
		}
		if !bOK {
			bi = maxRoleIdx
		}
		if ai != bi {
			return ai < bi
		}
		if !a.lastOutputTime.Equal(b.lastOutputTime) {
			return a.lastOutputTime.After(b.lastOutputTime)
		}
		return a.name < b.name
	}

	candidates := make([]ranked, 0, len(scores))
	for _, s := range scores {
		candidates = append(candidates, s)
	}
	// Score-sorted: used by bestUnplaced for BOTH displacement and fill decisions.
	// Invariant #2: score determines who gets a slot. Yaml order is tiebreaker only.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return roleCmp(candidates[i], candidates[j])
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

		// Empty slot: fill with highest-scoring unplaced agent (not a displacement).
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
		// Temporarily exclude the occupant from candidates so bestUnplaced
		// doesn't return the occupant itself as its own challenger.
		placed[prev] = true
		if !swapped {
			if best, ok := bestUnplaced(candidates, placed); ok &&
				best.score >= liveClaimThreshold &&
				best.score-prevInfo.score >= liveClaimMargin {
				result[slot] = best.name
				placed[best.name] = true
				le.holdUntil[slot] = now.Add(liveHoldDuration)
				swapped = true
				// Displaced occupant returns to pool.
				delete(placed, prev)
				LogInfo("live-tick", "displace-margin", "slot", slot, "old", prev, "oldScore", prevInfo.score, "new", best.name, "newScore", best.score, "margin", best.score-prevInfo.score)
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

		// Keep current occupant (already in placed from exclusion above).
		result[slot] = prev
	}

	// Invariant #3: yaml order determines WHERE agents sit (display concern).
	// After score-based placement decisions, re-sort dynamic slots by yaml
	// role index so the grid layout matches the config file order.
	if len(le.RolesOrder) > 0 {
		type dynEntry struct {
			name string
			hold time.Time
		}
		var dynSlots []int
		var dynAgents []dynEntry
		for i := 0; i < numSlots; i++ {
			if pinnedSet[result[i]] {
				continue
			}
			dynSlots = append(dynSlots, i)
			if result[i] != "" {
				dynAgents = append(dynAgents, dynEntry{result[i], le.holdUntil[i]})
			}
		}
		sort.Slice(dynAgents, func(i, j int) bool {
			ai, bi := maxRoleIdx, maxRoleIdx
			if idx, ok := roleIdx[dynAgents[i].name]; ok {
				ai = idx
			}
			if idx, ok := roleIdx[dynAgents[j].name]; ok {
				bi = idx
			}
			if ai != bi {
				return ai < bi
			}
			return dynAgents[i].name < dynAgents[j].name
		})
		for i, si := range dynSlots {
			if i < len(dynAgents) {
				result[si] = dynAgents[i].name
				le.holdUntil[si] = dynAgents[i].hold
			} else {
				result[si] = ""
				le.holdUntil[si] = time.Time{}
			}
		}
	}

	le.Slots = result
	LogInfo("live-tick", "result", "slots", fmt.Sprintf("%v", result))
	return result
}

// TickAuto computes the visible agent list for Live Auto mode.
// Unlike Tick (fixed slot count), TickAuto dynamically grows and shrinks
// the visible set based on conviction scores:
//   - Pinned agents are always visible regardless of score.
//   - Other agents are visible if score >= liveKeepThreshold.
//   - Hold time: a newly visible agent stays visible for liveHoldDuration
//     even if its score drops below threshold.
//   - One change per tick: at most one agent added or removed per call,
//     preventing the grid from jumping sizes instantly.
//
// Returns the ordered list of visible agent names (pinned first, then by
// score descending).
func (le *LiveEngine) TickAuto(panes []PaneView, now time.Time) []string {
	// Score all panes.
	type scored struct {
		name  string
		score int
	}
	var allScored []scored
	pinnedNames := make(map[string]bool, len(le.Pinned))
	for name := range le.Pinned {
		pinnedNames[name] = true
	}

	for _, p := range panes {
		pk := paneKey(p)
		s := convictionScore(p, now)
		allScored = append(allScored, scored{pk, s})
	}

	// Build current visible set from le.Slots.
	currentVisible := make(map[string]bool, len(le.Slots))
	for _, name := range le.Slots {
		if name != "" {
			currentVisible[name] = true
		}
	}

	// Build hold map: index in le.Slots -> name, for hold time checks.
	holdMap := make(map[string]time.Time, len(le.Slots))
	for i, name := range le.Slots {
		if name != "" && i < len(le.holdUntil) {
			holdMap[name] = le.holdUntil[i]
		}
	}

	// Determine desired visibility for each agent.
	wantVisible := make(map[string]bool)
	for _, s := range allScored {
		if pinnedNames[s.name] {
			wantVisible[s.name] = true
			continue
		}
		if s.score >= liveKeepThreshold {
			wantVisible[s.name] = true
			continue
		}
		// Below threshold but under hold time: keep visible.
		if currentVisible[s.name] {
			if hold, ok := holdMap[s.name]; ok && now.Before(hold) {
				wantVisible[s.name] = true
			}
		}
	}

	// Pinned agents are always added immediately (bypass one-change-per-tick).
	for _, s := range allScored {
		if pinnedNames[s.name] && !currentVisible[s.name] {
			currentVisible[s.name] = true
			holdMap[s.name] = now.Add(liveHoldDuration)
			LogInfo("live-auto", "add-pinned", "agent", s.name)
		}
	}

	// Compute dynamic additions and removals.
	var toAdd []scored
	var toRemove []string
	for _, s := range allScored {
		if pinnedNames[s.name] {
			continue // Already handled above.
		}
		if wantVisible[s.name] && !currentVisible[s.name] {
			toAdd = append(toAdd, s)
		}
	}
	for name := range currentVisible {
		if !wantVisible[name] {
			toRemove = append(toRemove, name)
		}
	}

	// Sort additions by score descending for priority.
	sort.Slice(toAdd, func(i, j int) bool {
		if toAdd[i].score != toAdd[j].score {
			return toAdd[i].score > toAdd[j].score
		}
		return toAdd[i].name < toAdd[j].name
	})
	sort.Strings(toRemove)

	// One change per tick: either add one or remove one (dynamic only).
	changed := false
	if len(toAdd) > 0 {
		currentVisible[toAdd[0].name] = true
		holdMap[toAdd[0].name] = now.Add(liveHoldDuration)
		changed = true
		LogInfo("live-auto", "add", "agent", toAdd[0].name, "score", toAdd[0].score)
	}
	if len(toRemove) > 0 && !changed {
		delete(currentVisible, toRemove[0])
		delete(holdMap, toRemove[0])
		LogInfo("live-auto", "remove", "agent", toRemove[0])
	}

	// Build role index lookup for stable ordering.
	roleIdx := make(map[string]int, len(le.RolesOrder))
	for i, r := range le.RolesOrder {
		roleIdx[r] = i
	}
	// sortByRole returns a comparator that sorts by roles index, with agents
	// not in the roles list pushed to the end (alphabetically among themselves).
	maxIdx := len(le.RolesOrder)
	sortByRole := func(a, b scored) int {
		ai, aOK := roleIdx[a.name]
		bi, bOK := roleIdx[b.name]
		if !aOK {
			ai = maxIdx
		}
		if !bOK {
			bi = maxIdx
		}
		if ai != bi {
			if ai < bi {
				return -1
			}
			return 1
		}
		if a.name < b.name {
			return -1
		}
		if a.name > b.name {
			return 1
		}
		return 0
	}

	// Build result: pinned first (role order), then remaining (role order).
	var pinResult []scored
	var dynResult []scored
	for _, s := range allScored {
		if !currentVisible[s.name] {
			continue
		}
		if pinnedNames[s.name] {
			pinResult = append(pinResult, s)
		} else {
			dynResult = append(dynResult, s)
		}
	}
	sort.Slice(pinResult, func(i, j int) bool { return sortByRole(pinResult[i], pinResult[j]) < 0 })
	sort.Slice(dynResult, func(i, j int) bool { return sortByRole(dynResult[i], dynResult[j]) < 0 })

	result := make([]string, 0, len(pinResult)+len(dynResult))
	for _, s := range pinResult {
		result = append(result, s.name)
	}
	for _, s := range dynResult {
		result = append(result, s.name)
	}

	// Update engine state: rebuild Slots and holdUntil to match visible set.
	le.Slots = result
	le.holdUntil = make([]time.Time, len(result))
	for i, name := range result {
		if h, ok := holdMap[name]; ok {
			le.holdUntil[i] = h
		}
	}

	LogInfo("live-auto", "result", "visible", len(result), "names", fmt.Sprintf("%v", result))
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
	le := NewLiveEngine(numSlots, pinned, nil)
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
