package tui

import (
	"testing"
	"time"

	vt "github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// mockPaneView is a PaneView mock for testing conviction scoring and live engine.
type mockPaneView struct {
	name           string
	host           string
	alive          bool
	suspended      bool
	pinned         bool
	beadID         string
	activity       ActivityState
	runStart       time.Time
	runBytes       int64
	msgReceived    time.Time
	eventTime      time.Time
	lastOutputTime time.Time
}

func (m *mockPaneView) Name() string                   { return m.name }
func (m *mockPaneView) Host() string                   { return m.host }
func (m *mockPaneView) IsAlive() bool                  { return m.alive }
func (m *mockPaneView) IsSuspended() bool              { return m.suspended }
func (m *mockPaneView) IsPinned() bool                 { return m.pinned }
func (m *mockPaneView) BeadID() string                 { return m.beadID }
func (m *mockPaneView) Activity() ActivityState        { return m.activity }
func (m *mockPaneView) ActiveRunStart() time.Time      { return m.runStart }
func (m *mockPaneView) ActiveRunBytes() int64          { return m.runBytes }
func (m *mockPaneView) LastMessageReceived() time.Time { return m.msgReceived }
func (m *mockPaneView) LastEventTime() time.Time       { return m.eventTime }
func (m *mockPaneView) LastOutputTime() time.Time      { return m.lastOutputTime }

// Unused PaneView methods.
func (m *mockPaneView) SessionDesc() string                                          { return "" }
func (m *mockPaneView) AgentType() string                                            { return "" }
func (m *mockPaneView) SubmitKey() string                                            { return "" }
func (m *mockPaneView) SetBead(id, title string)                                     {}
func (m *mockPaneView) SendKey(_ *tcell.EventKey)                                    {}
func (m *mockPaneView) SendText(_ string, _ bool)                                    {}
func (m *mockPaneView) Render(_ tcell.Screen, _ bool, _ bool, _ int, _ Selection)    {}
func (m *mockPaneView) Resize(_, _ int)                                              {}
func (m *mockPaneView) Close()                                                       {}
func (m *mockPaneView) GetRegion() Region                                            { return Region{} }
func (m *mockPaneView) Emulator() *vt.SafeEmulator                                  { return nil }

// Compile-time check: mockPaneView satisfies PaneView.
var _ PaneView = (*mockPaneView)(nil)

// ── convictionScore tests ───────────────────────────────────────────

func TestConvictionScore(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		pane *mockPaneView
		want int
	}{
		{
			name: "dead agent always zero",
			pane: &mockPaneView{alive: false, beadID: "ini-abc"},
			want: 0,
		},
		{
			name: "suspended agent always zero",
			pane: &mockPaneView{alive: true, suspended: true, beadID: "ini-abc"},
			want: 0,
		},
		{
			name: "idle no signals",
			pane: &mockPaneView{alive: true, activity: StateIdle},
			want: 0,
		},
		{
			name: "bead only",
			pane: &mockPaneView{alive: true, activity: StateIdle, beadID: "ini-abc"},
			want: 30,
		},
		{
			name: "running but brief under 5s",
			pane: &mockPaneView{alive: true, activity: StateRunning, runStart: now.Add(-3 * time.Second)},
			want: 0,
		},
		{
			name: "running sustained over 5s",
			pane: &mockPaneView{alive: true, activity: StateRunning, runStart: now.Add(-10 * time.Second)},
			want: 20,
		},
		{
			name: "output volume over 10KB",
			pane: &mockPaneView{alive: true, activity: StateIdle, runBytes: 11 * 1024},
			want: 15,
		},
		{
			name: "output volume exactly 10KB not over",
			pane: &mockPaneView{alive: true, activity: StateIdle, runBytes: 10 * 1024},
			want: 0,
		},
		{
			name: "recent dispatch within 30s",
			pane: &mockPaneView{alive: true, activity: StateIdle, msgReceived: now.Add(-15 * time.Second)},
			want: 25,
		},
		{
			name: "stale dispatch over 30s",
			pane: &mockPaneView{alive: true, activity: StateIdle, msgReceived: now.Add(-60 * time.Second)},
			want: 0,
		},
		{
			name: "recent event within 10s",
			pane: &mockPaneView{alive: true, activity: StateIdle, eventTime: now.Add(-5 * time.Second)},
			want: 10,
		},
		{
			name: "stale event over 10s",
			pane: &mockPaneView{alive: true, activity: StateIdle, eventTime: now.Add(-20 * time.Second)},
			want: 0,
		},
		{
			name: "all signals active max score",
			pane: &mockPaneView{
				alive: true, activity: StateRunning, beadID: "ini-xyz",
				runStart: now.Add(-30 * time.Second), runBytes: 50 * 1024,
				msgReceived: now.Add(-10 * time.Second), eventTime: now.Add(-3 * time.Second),
			},
			want: 100,
		},
		{
			name: "bead plus sustained plus dispatch",
			pane: &mockPaneView{
				alive: true, activity: StateRunning, beadID: "ini-abc",
				runStart: now.Add(-10 * time.Second), msgReceived: now.Add(-5 * time.Second),
			},
			want: 75,
		},
		{
			name: "zero time message never received",
			pane: &mockPaneView{alive: true, activity: StateIdle},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convictionScore(tt.pane, now)
			if got != tt.want {
				t.Errorf("convictionScore() = %d, want %d", got, tt.want)
			}
		})
	}
}

// ── LiveEngine.Tick tests ───────────────────────────────────────────

func TestLiveEngine_Tick_HighestScoreGetsSlot(t *testing.T) {
	now := time.Now()
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle},
		&mockPaneView{name: "eng2", alive: true, activity: StateRunning, runStart: now.Add(-10 * time.Second), beadID: "ini-abc"},
		&mockPaneView{name: "eng3", alive: true, activity: StateIdle, beadID: "ini-xyz"},
	}
	le := NewLiveEngine(2, nil, nil)
	slots := le.Tick(panes, now)

	// eng2 has score 50 (bead=30 + activity=20), eng3 has score 30 (bead only).
	if slots[0] != "eng2" {
		t.Errorf("slot 0 = %q, want eng2 (highest score)", slots[0])
	}
	if slots[1] != "eng3" {
		t.Errorf("slot 1 = %q, want eng3 (second highest)", slots[1])
	}
}

func TestLiveEngine_Tick_PinnedAgentsFixed(t *testing.T) {
	now := time.Now()
	panes := []PaneView{
		&mockPaneView{name: "super", alive: true, activity: StateIdle},
		&mockPaneView{name: "eng1", alive: true, activity: StateRunning, runStart: now.Add(-10 * time.Second), beadID: "ini-abc"},
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle},
	}
	pinned := map[string]int{"super": 0}
	le := NewLiveEngine(2, pinned, nil)
	slots := le.Tick(panes, now)

	if slots[0] != "super" {
		t.Errorf("slot 0 = %q, want super (pinned)", slots[0])
	}
	if slots[1] != "eng1" {
		t.Errorf("slot 1 = %q, want eng1 (highest scoring unpinned)", slots[1])
	}
}

func TestLiveEngine_Tick_DeadAgentsExcluded(t *testing.T) {
	now := time.Now()
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: false, beadID: "ini-abc"},
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle},
	}
	le := NewLiveEngine(2, nil, nil)
	slots := le.Tick(panes, now)

	if slots[0] != "eng2" {
		t.Errorf("slot 0 = %q, want eng2 (only alive agent)", slots[0])
	}
	if slots[1] != "" {
		t.Errorf("slot 1 = %q, want empty (only one alive agent)", slots[1])
	}
}

func TestLiveEngine_Tick_SuspendedExcluded(t *testing.T) {
	now := time.Now()
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, suspended: true, beadID: "ini-abc"},
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle},
	}
	le := NewLiveEngine(2, nil, nil)
	slots := le.Tick(panes, now)

	if slots[0] != "eng2" {
		t.Errorf("slot 0 = %q, want eng2 (only non-suspended)", slots[0])
	}
	if slots[1] != "" {
		t.Errorf("slot 1 = %q, want empty", slots[1])
	}
}

func TestLiveEngine_Tick_AllIdleFallback(t *testing.T) {
	now := time.Now()
	// All idle, no signals, but different lastOutputTime for tiebreaking.
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, lastOutputTime: now.Add(-30 * time.Second)},
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle, lastOutputTime: now.Add(-5 * time.Second)},
		&mockPaneView{name: "eng3", alive: true, activity: StateIdle, lastOutputTime: now.Add(-60 * time.Second)},
	}
	le := NewLiveEngine(3, nil, nil)
	slots := le.Tick(panes, now)

	// All scores are 0, so tiebreak by most recent output time.
	if slots[0] != "eng2" {
		t.Errorf("slot 0 = %q, want eng2 (most recent output)", slots[0])
	}
	if slots[1] != "eng1" {
		t.Errorf("slot 1 = %q, want eng1", slots[1])
	}
	if slots[2] != "eng3" {
		t.Errorf("slot 2 = %q, want eng3 (oldest output)", slots[2])
	}
}

func TestLiveEngine_Tick_FewerAgentsThanSlots(t *testing.T) {
	now := time.Now()
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle},
	}
	le := NewLiveEngine(3, nil, nil)
	slots := le.Tick(panes, now)

	if slots[0] != "eng1" {
		t.Errorf("slot 0 = %q, want eng1", slots[0])
	}
	if slots[1] != "" || slots[2] != "" {
		t.Errorf("extra slots should be empty, got %v", slots)
	}
}

func TestLiveEngine_Tick_PinnedOutOfRange(t *testing.T) {
	now := time.Now()
	panes := []PaneView{
		&mockPaneView{name: "super", alive: true},
		&mockPaneView{name: "eng1", alive: true, beadID: "ini-abc"},
	}
	// Pin to slot 99 (out of range for 2 slots) -- should be ignored.
	pinned := map[string]int{"super": 99}
	le := NewLiveEngine(2, pinned, nil)
	slots := le.Tick(panes, now)

	// super is not pinned (out of range), both compete normally.
	// eng1 has bead (score=30), super has nothing (score=0).
	if slots[0] != "eng1" {
		t.Errorf("slot 0 = %q, want eng1 (highest score)", slots[0])
	}
	if slots[1] != "super" {
		t.Errorf("slot 1 = %q, want super", slots[1])
	}
}

func TestLiveEngine_Tick_MultiplePinned(t *testing.T) {
	now := time.Now()
	panes := []PaneView{
		&mockPaneView{name: "super", alive: true},
		&mockPaneView{name: "pm", alive: true},
		&mockPaneView{name: "eng1", alive: true, beadID: "ini-abc", activity: StateRunning, runStart: now.Add(-10 * time.Second)},
		&mockPaneView{name: "eng2", alive: true, beadID: "ini-xyz"},
	}
	pinned := map[string]int{"super": 0, "pm": 2}
	le := NewLiveEngine(4, pinned, nil)
	slots := le.Tick(panes, now)

	if slots[0] != "super" {
		t.Errorf("slot 0 = %q, want super (pinned)", slots[0])
	}
	if slots[2] != "pm" {
		t.Errorf("slot 2 = %q, want pm (pinned)", slots[2])
	}
	// Dynamic slots 1 and 3: eng1 (score=50) then eng2 (score=30).
	if slots[1] != "eng1" {
		t.Errorf("slot 1 = %q, want eng1 (highest dynamic)", slots[1])
	}
	if slots[3] != "eng2" {
		t.Errorf("slot 3 = %q, want eng2", slots[3])
	}
}

func TestLiveEngine_Tick_NoPanes(t *testing.T) {
	le := NewLiveEngine(2, nil, nil)
	slots := le.Tick(nil, time.Now())

	if slots[0] != "" || slots[1] != "" {
		t.Errorf("all slots should be empty with no panes, got %v", slots)
	}
}

func TestLiveEngine_Tick_RemotePaneKey(t *testing.T) {
	now := time.Now()
	panes := []PaneView{
		&mockPaneView{name: "eng1", host: "workbench", alive: true, beadID: "ini-abc"},
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle},
	}
	le := NewLiveEngine(2, nil, nil)
	slots := le.Tick(panes, now)

	// Remote pane has key "workbench:eng1", local has "eng1".
	if slots[0] != "workbench:eng1" {
		t.Errorf("slot 0 = %q, want workbench:eng1 (higher score from bead)", slots[0])
	}
	if slots[1] != "eng1" {
		t.Errorf("slot 1 = %q, want eng1 (local)", slots[1])
	}
}

// ── Anti-thrashing tests ────────────────────────────────────────────

func TestLiveEngine_HoldTimePreventsSwap(t *testing.T) {
	now := time.Now()

	// Tick 1: fill empty slots. eng1 (score=30 from bead) gets slot 0, eng2 (score=0) gets slot 1.
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "ini-a"},
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle},
	}
	le := NewLiveEngine(2, nil, nil)
	slots := le.Tick(panes, now)
	if slots[0] != "eng1" || slots[1] != "eng2" {
		t.Fatalf("tick 1: got %v, want [eng1 eng2]", slots)
	}

	// Tick 2: 5 seconds later (within hold time). eng3 appears with high score.
	// eng3: bead(30) + running>5s(20) = 50.
	tick2 := now.Add(5 * time.Second)
	panes2 := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "ini-a"},
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle},
		&mockPaneView{name: "eng3", alive: true, beadID: "ini-b", activity: StateRunning, runStart: tick2.Add(-10 * time.Second)},
	}
	slots = le.Tick(panes2, tick2)

	// Hold time prevents any change: eng1 and eng2 stay.
	if slots[0] != "eng1" {
		t.Errorf("tick 2 slot 0 = %q, want eng1 (hold time active)", slots[0])
	}
	if slots[1] != "eng2" {
		t.Errorf("tick 2 slot 1 = %q, want eng2 (hold time active)", slots[1])
	}
}

func TestLiveEngine_HoldTimeExpiryAllowsSwap(t *testing.T) {
	now := time.Now()

	// Tick 1: fill. eng1 (score=30) slot 0, eng2 (score=0) slot 1.
	le := NewLiveEngine(2, nil, nil)
	le.Tick([]PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "ini-a"},
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle},
	}, now)

	// Tick 2: 15 seconds later (hold time expired). eng3 with score 50 appears.
	tick2 := now.Add(15 * time.Second)
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "ini-a"},                                            // score 30
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle},                                                              // score 0
		&mockPaneView{name: "eng3", alive: true, beadID: "ini-b", activity: StateRunning, runStart: tick2.Add(-10 * time.Second)}, // score 50
	}
	slots := le.Tick(panes, tick2)

	// eng2 (score 0) is below keepThreshold (10). eng3 (50) >= claimThreshold (40).
	// Slot 1 swaps first (slot 0 occupant eng1 has score 30 >= keep, eng3 needs margin).
	// eng3(50) - eng1(30) = 20 >= margin(20). Slot 0 is also eligible.
	// Iteration order: slot 0 first. eng1(30) above keep. eng3(50) >= claim(40), margin=20 >= 20. Swap!
	if slots[0] != "eng3" {
		t.Errorf("slot 0 = %q, want eng3 (hold expired, challenger wins)", slots[0])
	}
	// Slot 1: eng2 below keep threshold, evicted to empty.
	if slots[1] != "" {
		t.Errorf("slot 1 = %q, want empty (evict below keep threshold)", slots[1])
	}
}

func TestLiveEngine_HysteresisClaimThreshold(t *testing.T) {
	now := time.Now()

	// Fill: eng1 (score=30) in slot 0.
	le := NewLiveEngine(1, nil, nil)
	le.Tick([]PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "ini-a"}, // score 30
	}, now)

	// After hold time: challenger eng2 with bead only (score=30). < claimThreshold(40). Can't claim.
	tick2 := now.Add(15 * time.Second)
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "ini-a"}, // score 30
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle, beadID: "ini-b"}, // score 30
	}
	slots := le.Tick(panes, tick2)

	// eng2 (30) < claimThreshold (40). Cannot displace eng1.
	if slots[0] != "eng1" {
		t.Errorf("slot 0 = %q, want eng1 (challenger below claim threshold)", slots[0])
	}
}

func TestLiveEngine_HysteresisMarginRequired(t *testing.T) {
	now := time.Now()

	// Fill: eng1 (score=30) in slot 0.
	le := NewLiveEngine(1, nil, nil)
	le.Tick([]PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "ini-a"}, // score 30
	}, now)

	// Challenger eng2 has score 45 (bead=30, volume>10KB=15). Meets claim threshold (40).
	// But margin: 45 - 30 = 15 < 20. Not enough to displace.
	tick2 := now.Add(15 * time.Second)
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "ini-a"},                          // score 30
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle, beadID: "ini-b", runBytes: 11 * 1024}, // score 45
	}
	slots := le.Tick(panes, tick2)

	if slots[0] != "eng1" {
		t.Errorf("slot 0 = %q, want eng1 (challenger margin too small: 15 < 20)", slots[0])
	}
}

func TestLiveEngine_HysteresisKeepThreshold(t *testing.T) {
	now := time.Now()

	// Fill: eng1 with event (score=10: recent event only).
	le := NewLiveEngine(1, nil, nil)
	le.Tick([]PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, eventTime: now.Add(-3 * time.Second)}, // score 10
	}, now)

	// After hold, eng1's event is stale (score=0, below keep=10).
	// eng2 has score 50 (bead + running). >= claim threshold.
	tick2 := now.Add(15 * time.Second)
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle}, // score 0 (event expired)
		&mockPaneView{name: "eng2", alive: true, beadID: "ini-b", activity: StateRunning, runStart: tick2.Add(-10 * time.Second)}, // score 50
	}
	slots := le.Tick(panes, tick2)

	// eng1 below keep threshold (0 < 10). eng2 >= claim threshold (50 >= 40). Swap.
	if slots[0] != "eng2" {
		t.Errorf("slot 0 = %q, want eng2 (occupant below keep threshold)", slots[0])
	}
}

func TestLiveEngine_BelowKeepNoQualifiedChallenger(t *testing.T) {
	now := time.Now()

	// Fill: eng1 with score 10 (recent event).
	le := NewLiveEngine(1, nil, nil)
	le.Tick([]PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, eventTime: now.Add(-3 * time.Second)},
	}, now)

	// After hold, eng1 score drops to 0. eng2 has score 30 (bead only) < claimThreshold(40).
	tick2 := now.Add(15 * time.Second)
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle},                  // score 0
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle, beadID: "ini-b"}, // score 30
	}
	slots := le.Tick(panes, tick2)

	// eng1 below keep threshold, evicted regardless of challenger qualification.
	if slots[0] != "" {
		t.Errorf("slot 0 = %q, want empty (evict below keep threshold)", slots[0])
	}
}

func TestLiveEngine_OneSwapPerTick_MultipleEligible(t *testing.T) {
	now := time.Now()

	// Fill 3 slots with low-scoring agents.
	le := NewLiveEngine(3, nil, nil)
	le.Tick([]PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, eventTime: now.Add(-3 * time.Second)}, // score 10
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle, eventTime: now.Add(-3 * time.Second)}, // score 10
		&mockPaneView{name: "eng3", alive: true, activity: StateIdle, eventTime: now.Add(-3 * time.Second)}, // score 10
	}, now)

	// After hold: all occupants drop to 0. Three challengers qualify.
	tick2 := now.Add(15 * time.Second)
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle}, // score 0 (event expired)
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle}, // score 0
		&mockPaneView{name: "eng3", alive: true, activity: StateIdle}, // score 0
		&mockPaneView{name: "hot1", alive: true, beadID: "ini-a", activity: StateRunning, runStart: tick2.Add(-10 * time.Second)}, // score 50
		&mockPaneView{name: "hot2", alive: true, beadID: "ini-b", activity: StateRunning, runStart: tick2.Add(-10 * time.Second)}, // score 50
		&mockPaneView{name: "hot3", alive: true, beadID: "ini-c", activity: StateRunning, runStart: tick2.Add(-10 * time.Second)}, // score 50
	}
	slots := le.Tick(panes, tick2)

	// Slot 0: eng1 (0) below keep, hot1 (50) displaces (one swap used).
	if slots[0] != "hot1" {
		t.Errorf("slot 0 = %q, want hot1 (first swap, alpha tiebreak)", slots[0])
	}
	// Slots 1,2: eng2,eng3 below keep threshold, evicted to empty.
	if slots[1] != "" {
		t.Errorf("slot 1 = %q, want empty (evict below keep threshold)", slots[1])
	}
	if slots[2] != "" {
		t.Errorf("slot 2 = %q, want empty (evict below keep threshold)", slots[2])
	}

	// Tick 3: slots 1,2 are empty, so filling them is not a displacement.
	tick3 := tick2.Add(16 * time.Millisecond)
	slots = le.Tick(panes, tick3)

	// Slot 0: hot1 under hold time, stays.
	if slots[0] != "hot1" {
		t.Errorf("tick 3 slot 0 = %q, want hot1 (hold time)", slots[0])
	}
	// Slots 1,2: empty, filled by hot2 and hot3 (not displacements).
	if slots[1] != "hot2" {
		t.Errorf("tick 3 slot 1 = %q, want hot2 (fill empty slot)", slots[1])
	}
	if slots[2] != "hot3" {
		t.Errorf("tick 3 slot 2 = %q, want hot3 (fill empty slot)", slots[2])
	}
}

func TestLiveEngine_TiebreakerDeterminism(t *testing.T) {
	now := time.Now()

	// Two agents with identical scores and output times.
	// Alphabetical name tiebreak: "alpha" < "beta".
	panes := []PaneView{
		&mockPaneView{name: "beta", alive: true, beadID: "ini-a", activity: StateRunning, runStart: now.Add(-10 * time.Second)},
		&mockPaneView{name: "alpha", alive: true, beadID: "ini-b", activity: StateRunning, runStart: now.Add(-10 * time.Second)},
	}
	le := NewLiveEngine(2, nil, nil)
	slots := le.Tick(panes, now)

	// Both score 50. Same lastOutputTime (zero). Alpha wins by name.
	if slots[0] != "alpha" {
		t.Errorf("slot 0 = %q, want alpha (alphabetical tiebreak)", slots[0])
	}
	if slots[1] != "beta" {
		t.Errorf("slot 1 = %q, want beta", slots[1])
	}
}

func TestLiveEngine_DeadAgentHoldThenReplace(t *testing.T) {
	now := time.Now()

	// Fill: eng1 (score=30) in slot 0.
	le := NewLiveEngine(1, nil, nil)
	le.Tick([]PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "ini-a"},
	}, now)

	// Agent dies within hold time. eng2 available with high score.
	tick2 := now.Add(5 * time.Second)
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: false},
		&mockPaneView{name: "eng2", alive: true, beadID: "ini-b", activity: StateRunning, runStart: tick2.Add(-10 * time.Second)},
	}
	slots := le.Tick(panes, tick2)

	// Hold time still active: dead agent stays (operator notices).
	if slots[0] != "eng1" {
		t.Errorf("slot 0 = %q, want eng1 (dead but hold active)", slots[0])
	}

	// After hold time: dead agent gets replaced.
	tick3 := now.Add(15 * time.Second)
	slots = le.Tick(panes, tick3)
	if slots[0] != "eng2" {
		t.Errorf("slot 0 = %q, want eng2 (dead agent replaced after hold)", slots[0])
	}
}

func TestLiveEngine_OccupantNotChallengerForOwnSlot(t *testing.T) {
	now := time.Now()

	// Fill 2 slots: eng1 (score 30, bead only) and pm (score 30, bead only).
	le := NewLiveEngine(2, nil, nil)
	le.Tick([]PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "ini-a"},
		&mockPaneView{name: "pm", alive: true, activity: StateIdle, beadID: "ini-b"},
	}, now)

	// After hold: eng1 and pm still score 30, but pmm scores 60.
	// pmm should be the challenger, not the occupant challenging itself.
	tick2 := now.Add(15 * time.Second)
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "ini-a"}, // score 30
		&mockPaneView{name: "pm", alive: true, activity: StateIdle, beadID: "ini-b"},   // score 30
		&mockPaneView{name: "pmm", alive: true, beadID: "ini-c", activity: StateRunning, runStart: tick2.Add(-10 * time.Second), msgReceived: tick2.Add(-5 * time.Second)}, // score 30+20+25=75
	}
	slots := le.Tick(panes, tick2)

	// pmm (75) >= claimThreshold (40) and pmm(75) - eng1(30) = 45 >= margin(20).
	// One swap per tick: first eligible slot (slot 0) gets displaced.
	displaced := false
	for _, s := range slots {
		if s == "pmm" {
			displaced = true
			break
		}
	}
	if !displaced {
		t.Errorf("pmm should displace an occupant but slots = %v", slots)
	}
}

func TestLiveEngine_DisplacedAgentFillsEmptySlot(t *testing.T) {
	now := time.Now()

	// Fill: eng1 (score=30) slot 0, slot 1 remains empty (only one candidate).
	le := NewLiveEngine(2, nil, nil)
	le.Tick([]PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "ini-a"},
	}, now)

	// After hold: eng2 appears with score 50, displaces eng1 from slot 0.
	// eng1 (displaced) should fill empty slot 1.
	tick2 := now.Add(15 * time.Second)
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "ini-a"},                                            // score 30
		&mockPaneView{name: "eng2", alive: true, beadID: "ini-b", activity: StateRunning, runStart: tick2.Add(-10 * time.Second)}, // score 50
	}
	slots := le.Tick(panes, tick2)

	// eng2 displaces eng1 from slot 0. eng1 fills empty slot 1.
	if slots[0] != "eng2" {
		t.Errorf("slot 0 = %q, want eng2 (displaced eng1)", slots[0])
	}
	if slots[1] != "eng1" {
		t.Errorf("slot 1 = %q, want eng1 (displaced agent fills empty slot)", slots[1])
	}
}

func TestLiveEngine_EmptySlotFillNotLimitedBySwapCap(t *testing.T) {
	now := time.Now()

	// First tick fills all 3 empty slots in one call.
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, beadID: "ini-a", activity: StateRunning, runStart: now.Add(-10 * time.Second)}, // score 50
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle, beadID: "ini-b"},                                          // score 30
		&mockPaneView{name: "eng3", alive: true, activity: StateIdle},                                                            // score 0
	}
	le := NewLiveEngine(3, nil, nil)
	slots := le.Tick(panes, now)

	// All empty slots filled in one tick (fills are not displacements).
	if slots[0] != "eng1" {
		t.Errorf("slot 0 = %q, want eng1", slots[0])
	}
	if slots[1] != "eng2" {
		t.Errorf("slot 1 = %q, want eng2", slots[1])
	}
	if slots[2] != "eng3" {
		t.Errorf("slot 2 = %q, want eng3", slots[2])
	}
}

func TestTick_RolesOrderDeterminesSlotAssignment(t *testing.T) {
	now := time.Now()
	// Roles order: [eng1, eng2, eng3]. eng3 has highest score but should get
	// slot 2 (last), not slot 0, because eng1 comes first in roles list.
	panes := []PaneView{
		&mockPaneView{name: "eng3", alive: true, activity: StateRunning, beadID: "bb-3", runStart: now.Add(-10 * time.Second), runBytes: 20000},
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "bb-1"},
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle, beadID: "bb-2"},
	}
	le := NewLiveEngine(3, nil, []string{"eng1", "eng2", "eng3"})
	result := le.Tick(panes, now)
	// Slots should fill in roles order: eng1=slot0, eng2=slot1, eng3=slot2.
	if result[0] != "eng1" {
		t.Errorf("slot 0: expected eng1 (roles[0]), got %q", result[0])
	}
	if result[1] != "eng2" {
		t.Errorf("slot 1: expected eng2 (roles[1]), got %q", result[1])
	}
	if result[2] != "eng3" {
		t.Errorf("slot 2: expected eng3 (roles[2]), got %q", result[2])
	}
}

// TestTick_DisplacementByScoreNotYamlOrder verifies invariant 2: bestUnplaced
// returns the highest-scoring unplaced agent for displacement, not the one
// earliest in yaml role order. This is the regression scenario from ini-ae9.
func TestTick_DisplacementByScoreNotYamlOrder(t *testing.T) {
	now := time.Now()
	// Roles order: eng1 before eng3. eng1 has lower score.
	// One dynamic slot occupied by a weak agent that will be displaced.
	panes := []PaneView{
		&mockPaneView{name: "weak", alive: true, activity: StateIdle}, // score 0 (below keep)
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "bb-1"},                                                  // score 30
		&mockPaneView{name: "eng3", alive: true, activity: StateRunning, beadID: "bb-3", runStart: now.Add(-10 * time.Second), runBytes: 20000}, // score 65
	}
	le := NewLiveEngine(1, nil, []string{"eng1", "eng2", "eng3"})
	// First tick: fill with highest score (eng3, 65).
	le.Tick(panes, now)

	// Evict eng3 so "weak" can take the slot, then let weak expire.
	le.Slots = []string{"weak"}
	le.holdUntil = []time.Time{now.Add(-1 * time.Second)} // hold expired

	// Tick: weak (score 0) is below keep threshold. Displacement should pick
	// eng3 (score 65), NOT eng1 (score 30) even though eng1 is earlier in yaml.
	result := le.Tick(panes, now)
	if result[0] != "eng3" {
		t.Errorf("slot 0 = %q, want eng3 (highest score wins displacement, not yaml order)", result[0])
	}
}

// TestTick_ScoreTiebreakByYamlOrder verifies that when two candidates have
// identical scores, yaml role order breaks the tie (invariant 2 tiebreaker).
func TestTick_ScoreTiebreakByYamlOrder(t *testing.T) {
	now := time.Now()
	// Both eng1 and eng3 have score 30 (bead only, idle).
	// eng1 is earlier in yaml, so wins the tiebreak.
	panes := []PaneView{
		&mockPaneView{name: "eng3", alive: true, activity: StateIdle, beadID: "bb-3"}, // score 30
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "bb-1"}, // score 30
	}
	le := NewLiveEngine(1, nil, []string{"eng1", "eng2", "eng3"})
	result := le.Tick(panes, now)
	if result[0] != "eng1" {
		t.Errorf("slot 0 = %q, want eng1 (yaml tiebreaker when scores equal)", result[0])
	}
}

// ── liveTickSlots tests ─────────────────────────────────────────────

func TestLiveTickSlots_DefaultsToLen(t *testing.T) {
	panes := []PaneView{
		&mockPaneView{name: "a", alive: true},
		&mockPaneView{name: "b", alive: true},
	}
	slots := liveTickSlots(panes, nil, 0)
	if len(slots) != 2 {
		t.Errorf("liveTickSlots(0) should default to len(panes)=%d, got %d", len(panes), len(slots))
	}
}

// ── TickAuto tests ────────────────────────────────────────────────────

func TestTickAuto_PinnedAlwaysVisible(t *testing.T) {
	now := time.Now()
	// Super is pinned but idle (score 0). Should still appear.
	panes := []PaneView{
		&mockPaneView{name: "super", alive: true, activity: StateIdle},
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle},
	}
	le := NewLiveEngine(0, map[string]int{"super": 0}, nil)
	result := le.TickAuto(panes, now)
	if len(result) != 1 || result[0] != "super" {
		t.Errorf("expected [super], got %v", result)
	}
}

func TestTickAuto_ActiveAgentBecomesVisible(t *testing.T) {
	now := time.Now()
	// eng1 has bead (score 30, above keepThreshold 10).
	panes := []PaneView{
		&mockPaneView{name: "super", alive: true, activity: StateIdle},
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "bb-1"},
	}
	le := NewLiveEngine(0, map[string]int{"super": 0}, nil)

	// Tick 1: super is always visible. eng1 gets added (one add per tick).
	r1 := le.TickAuto(panes, now)
	found := false
	for _, name := range r1 {
		if name == "eng1" {
			found = true
		}
	}
	if !found {
		t.Errorf("eng1 with bead should become visible, got %v", r1)
	}
}

func TestTickAuto_IncrementalGrowth(t *testing.T) {
	now := time.Now()
	// super pinned, eng1/eng2/eng3 all active with beads.
	panes := []PaneView{
		&mockPaneView{name: "super", alive: true, activity: StateIdle},
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "bb-1"},
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle, beadID: "bb-2"},
		&mockPaneView{name: "eng3", alive: true, activity: StateIdle, beadID: "bb-3"},
	}
	le := NewLiveEngine(0, map[string]int{"super": 0}, nil)

	// Each tick should add at most one agent (one-change-per-tick).
	r1 := le.TickAuto(panes, now)
	// super + one eng = 2
	if len(r1) > 2 {
		t.Errorf("tick 1: expected at most 2 visible (one add), got %d: %v", len(r1), r1)
	}

	r2 := le.TickAuto(panes, now)
	if len(r2) > 3 {
		t.Errorf("tick 2: expected at most 3 visible, got %d: %v", len(r2), r2)
	}

	r3 := le.TickAuto(panes, now)
	if len(r3) != 4 {
		t.Errorf("tick 3: expected 4 visible, got %d: %v", len(r3), r3)
	}
}

func TestTickAuto_IncrementalShrink(t *testing.T) {
	now := time.Now()
	expired := now.Add(-1 * time.Second) // Hold time expired.
	// Start with 3 visible: super (pinned), eng1, eng2.
	le := NewLiveEngine(0, map[string]int{"super": 0}, nil)
	le.Slots = []string{"super", "eng1", "eng2"}
	le.holdUntil = []time.Time{expired, expired, expired}

	// All engs go idle (score 0, below keepThreshold).
	panes := []PaneView{
		&mockPaneView{name: "super", alive: true, activity: StateIdle},
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle},
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle},
	}

	r1 := le.TickAuto(panes, now)
	// Should remove one (one-change-per-tick). super stays, one eng removed.
	if len(r1) != 2 {
		t.Errorf("tick 1: expected 2 (remove one), got %d: %v", len(r1), r1)
	}

	r2 := le.TickAuto(panes, now)
	if len(r2) != 1 || r2[0] != "super" {
		t.Errorf("tick 2: expected [super], got %v", r2)
	}
}

func TestTickAuto_HoldTimePreventsRemoval(t *testing.T) {
	now := time.Now()
	// eng1 is visible with hold time still active.
	le := NewLiveEngine(0, map[string]int{"super": 0}, nil)
	le.Slots = []string{"super", "eng1"}
	le.holdUntil = []time.Time{
		now.Add(-1 * time.Second), // super: expired (doesn't matter, pinned)
		now.Add(5 * time.Second),  // eng1: 5s remaining
	}

	// eng1 idle (score 0) but hold time active. Should stay.
	panes := []PaneView{
		&mockPaneView{name: "super", alive: true, activity: StateIdle},
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle},
	}

	result := le.TickAuto(panes, now)
	found := false
	for _, name := range result {
		if name == "eng1" {
			found = true
		}
	}
	if !found {
		t.Errorf("eng1 should stay visible during hold time, got %v", result)
	}
}

func TestTickAuto_ZeroActiveOnlySuper(t *testing.T) {
	now := time.Now()
	// All idle, only super pinned.
	panes := []PaneView{
		&mockPaneView{name: "super", alive: true, activity: StateIdle},
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle},
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle},
	}
	le := NewLiveEngine(0, map[string]int{"super": 0}, nil)
	result := le.TickAuto(panes, now)
	if len(result) != 1 || result[0] != "super" {
		t.Errorf("with all idle, expected only [super], got %v", result)
	}
}

func TestTickAuto_AllActive(t *testing.T) {
	now := time.Now()
	// All have beads (score 30 each, above keepThreshold).
	panes := []PaneView{
		&mockPaneView{name: "super", alive: true, activity: StateIdle, beadID: "bb-0"},
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "bb-1"},
		&mockPaneView{name: "eng2", alive: true, activity: StateIdle, beadID: "bb-2"},
	}
	le := NewLiveEngine(0, map[string]int{"super": 0}, nil)

	// One add per tick. After 3 ticks, all visible.
	le.TickAuto(panes, now)
	le.TickAuto(panes, now)
	r3 := le.TickAuto(panes, now)
	if len(r3) != 3 {
		t.Errorf("after 3 ticks with all active, expected 3, got %d: %v", len(r3), r3)
	}
}

func TestTickAuto_PinnedFirstInOrder(t *testing.T) {
	now := time.Now()
	panes := []PaneView{
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "bb-1"},
		&mockPaneView{name: "super", alive: true, activity: StateIdle},
	}
	le := NewLiveEngine(0, map[string]int{"super": 0}, nil)
	// Tick until eng1 is visible.
	le.TickAuto(panes, now)
	result := le.TickAuto(panes, now)

	if len(result) < 2 {
		t.Skipf("eng1 not yet visible (need more ticks), got %v", result)
	}
	if result[0] != "super" {
		t.Errorf("pinned agent should come first, got %v", result)
	}
}

func TestTickAuto_RolesOrderDeterminesPosition(t *testing.T) {
	now := time.Now()
	// Roles order: a, b, c. Visible: c (score 30), a (score 50).
	// Output should be [a, c] (roles order), NOT [c, a] (score order).
	panes := []PaneView{
		&mockPaneView{name: "c", alive: true, activity: StateIdle, beadID: "bb-3"},
		&mockPaneView{name: "a", alive: true, activity: StateRunning, beadID: "bb-1", runStart: now.Add(-10 * time.Second), runBytes: 20000},
	}
	le := NewLiveEngine(0, nil, []string{"a", "b", "c"})
	// Tick twice: one add per tick.
	le.TickAuto(panes, now)
	result := le.TickAuto(panes, now)
	if len(result) != 2 {
		t.Fatalf("expected 2 visible, got %d: %v", len(result), result)
	}
	if result[0] != "a" || result[1] != "c" {
		t.Errorf("expected [a c] (roles order), got %v", result)
	}
}

func TestTickAuto_HotAddedAgentsSortToEnd(t *testing.T) {
	now := time.Now()
	// Roles: [super, eng1]. Hot-added "intern" not in roles list.
	panes := []PaneView{
		&mockPaneView{name: "super", alive: true, activity: StateIdle},
		&mockPaneView{name: "intern", alive: true, activity: StateIdle, beadID: "bb-i"},
		&mockPaneView{name: "eng1", alive: true, activity: StateIdle, beadID: "bb-1"},
	}
	le := NewLiveEngine(0, map[string]int{"super": 0}, []string{"super", "eng1"})
	// Tick until all visible.
	le.TickAuto(panes, now)
	result := le.TickAuto(panes, now)
	if len(result) < 3 {
		result = le.TickAuto(panes, now)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 visible, got %d: %v", len(result), result)
	}
	// super first (pinned), eng1 second (roles[1]), intern last (not in roles).
	if result[0] != "super" {
		t.Errorf("expected super first (pinned), got %v", result)
	}
	if result[1] != "eng1" {
		t.Errorf("expected eng1 second (roles order), got %v", result)
	}
	if result[2] != "intern" {
		t.Errorf("expected intern last (hot-added), got %v", result)
	}
}

func TestTickAuto_NilRolesFallsBackToAlphabetical(t *testing.T) {
	now := time.Now()
	// No roles list: should fall back to alphabetical (current behavior).
	panes := []PaneView{
		&mockPaneView{name: "c", alive: true, activity: StateIdle, beadID: "bb-3"},
		&mockPaneView{name: "a", alive: true, activity: StateIdle, beadID: "bb-1"},
	}
	le := NewLiveEngine(0, nil, nil)
	le.TickAuto(panes, now)
	result := le.TickAuto(panes, now)
	if len(result) != 2 {
		t.Fatalf("expected 2 visible, got %d: %v", len(result), result)
	}
	// Both not in roles list -> maxIdx, tiebreak alphabetically.
	if result[0] != "a" || result[1] != "c" {
		t.Errorf("expected [a c] (alphabetical fallback), got %v", result)
	}
}
