package tui

import (
	"testing"
	"time"
)

// ini-y71 regression: when le.Slots carries stale duplicates from a prior
// tick, the same agent must not be kept in multiple slots. The dedup guard
// at the top of the slot loop treats a duplicate occupant as an empty slot
// so bestUnplaced can assign a different agent.
func TestTick_StaleDuplicateOccupantsDeduped(t *testing.T) {
	now := time.Now()
	panes := []PaneView{
		&mockPaneView{name: "super", alive: true, activity: StateIdle},
		&mockPaneView{name: "pmm", alive: true, activity: StateRunning, beadID: "bb-1", runStart: now.Add(-10 * time.Second), runBytes: 20000},
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "bb-2"},
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle, beadID: "bb-3"},
		&mockPaneView{name: "qa1", alive: true, activity: StateIdle},
	}

	// Simulate prior state: pmm duplicated across all dynamic slots.
	le := NewLiveEngine(4, map[string]int{"super": 0}, []string{"super", "eng1", "eng2", "qa1", "pmm"})
	le.Slots = []string{"super", "pmm", "pmm", "pmm"}
	expired := now.Add(-1 * time.Second)
	le.holdUntil = []time.Time{expired, expired, expired, expired}

	result := le.Tick(panes, now)

	seen := make(map[string]int)
	for i, name := range result {
		if name != "" {
			seen[name]++
			if seen[name] > 1 {
				t.Errorf("slot %d: duplicate agent %q, result: %v", i, name, result)
			}
		}
	}

	// All 4 slots should be filled with unique agents.
	filled := 0
	for _, name := range result {
		if name != "" {
			filled++
		}
	}
	if filled != 4 {
		t.Errorf("expected 4 filled slots, got %d: %v", filled, result)
	}
}
