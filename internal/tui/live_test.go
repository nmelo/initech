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
	le := NewLiveEngine(2, nil)
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
	le := NewLiveEngine(2, pinned)
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
	le := NewLiveEngine(2, nil)
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
	le := NewLiveEngine(2, nil)
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
	le := NewLiveEngine(3, nil)
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
	le := NewLiveEngine(3, nil)
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
	le := NewLiveEngine(2, pinned)
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
	le := NewLiveEngine(4, pinned)
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
	le := NewLiveEngine(2, nil)
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
	le := NewLiveEngine(2, nil)
	slots := le.Tick(panes, now)

	// Remote pane has key "workbench:eng1", local has "eng1".
	if slots[0] != "workbench:eng1" {
		t.Errorf("slot 0 = %q, want workbench:eng1 (higher score from bead)", slots[0])
	}
	if slots[1] != "eng1" {
		t.Errorf("slot 1 = %q, want eng1 (local)", slots[1])
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
