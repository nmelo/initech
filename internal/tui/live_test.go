package tui

import (
	"testing"
	"time"

	vt "github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// mockPaneView is a minimal PaneView mock for testing convictionScore.
// Only the fields needed by convictionScore are populated; all other
// PaneView methods panic if called (they should never be).
type mockPaneView struct {
	alive       bool
	suspended   bool
	beadID      string
	activity    ActivityState
	runStart    time.Time
	runBytes    int64
	msgReceived time.Time
	eventTime   time.Time
}

func (m *mockPaneView) IsAlive() bool                  { return m.alive }
func (m *mockPaneView) IsSuspended() bool              { return m.suspended }
func (m *mockPaneView) BeadID() string                 { return m.beadID }
func (m *mockPaneView) Activity() ActivityState        { return m.activity }
func (m *mockPaneView) ActiveRunStart() time.Time      { return m.runStart }
func (m *mockPaneView) ActiveRunBytes() int64          { return m.runBytes }
func (m *mockPaneView) LastMessageReceived() time.Time { return m.msgReceived }
func (m *mockPaneView) LastEventTime() time.Time       { return m.eventTime }

// Unused PaneView methods — panic to catch accidental calls.
func (m *mockPaneView) Name() string                                                            { panic("unused") }
func (m *mockPaneView) Host() string                                                            { panic("unused") }
func (m *mockPaneView) IsPinned() bool                                                          { panic("unused") }
func (m *mockPaneView) LastOutputTime() time.Time                                               { panic("unused") }
func (m *mockPaneView) SessionDesc() string                                                     { panic("unused") }
func (m *mockPaneView) AgentType() string                                                       { panic("unused") }
func (m *mockPaneView) SubmitKey() string                                                       { panic("unused") }
func (m *mockPaneView) SetBead(id, title string)                                                { panic("unused") }
func (m *mockPaneView) SendKey(_ *tcell.EventKey)                                               { panic("unused") }
func (m *mockPaneView) SendText(_ string, _ bool)                                               { panic("unused") }
func (m *mockPaneView) Render(_ tcell.Screen, _ bool, _ bool, _ int, _ Selection)               { panic("unused") }
func (m *mockPaneView) Resize(_, _ int)                                                         { panic("unused") }
func (m *mockPaneView) Close()                                                                  { panic("unused") }
func (m *mockPaneView) GetRegion() Region                                                       { panic("unused") }
func (m *mockPaneView) Emulator() *vt.SafeEmulator                                              { panic("unused") }

// Compile-time check: mockPaneView satisfies PaneView.
var _ PaneView = (*mockPaneView)(nil)

func TestConvictionScore(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		pane *mockPaneView
		want int
	}{
		{
			name: "dead agent always zero",
			pane: &mockPaneView{
				alive:  false,
				beadID: "ini-abc",
			},
			want: 0,
		},
		{
			name: "suspended agent always zero",
			pane: &mockPaneView{
				alive:     true,
				suspended: true,
				beadID:    "ini-abc",
			},
			want: 0,
		},
		{
			name: "idle no signals",
			pane: &mockPaneView{
				alive:    true,
				activity: StateIdle,
			},
			want: 0,
		},
		{
			name: "bead only",
			pane: &mockPaneView{
				alive:    true,
				activity: StateIdle,
				beadID:   "ini-abc",
			},
			want: 30,
		},
		{
			name: "running but brief under 5s",
			pane: &mockPaneView{
				alive:    true,
				activity: StateRunning,
				runStart: now.Add(-3 * time.Second),
			},
			want: 0,
		},
		{
			name: "running sustained over 5s",
			pane: &mockPaneView{
				alive:    true,
				activity: StateRunning,
				runStart: now.Add(-10 * time.Second),
			},
			want: 20,
		},
		{
			name: "output volume over 10KB",
			pane: &mockPaneView{
				alive:    true,
				activity: StateIdle,
				runBytes: 11 * 1024,
			},
			want: 15,
		},
		{
			name: "output volume exactly 10KB not over",
			pane: &mockPaneView{
				alive:    true,
				activity: StateIdle,
				runBytes: 10 * 1024,
			},
			want: 0,
		},
		{
			name: "recent dispatch within 30s",
			pane: &mockPaneView{
				alive:       true,
				activity:    StateIdle,
				msgReceived: now.Add(-15 * time.Second),
			},
			want: 25,
		},
		{
			name: "stale dispatch over 30s",
			pane: &mockPaneView{
				alive:       true,
				activity:    StateIdle,
				msgReceived: now.Add(-60 * time.Second),
			},
			want: 0,
		},
		{
			name: "recent event within 10s",
			pane: &mockPaneView{
				alive:     true,
				activity:  StateIdle,
				eventTime: now.Add(-5 * time.Second),
			},
			want: 10,
		},
		{
			name: "stale event over 10s",
			pane: &mockPaneView{
				alive:     true,
				activity:  StateIdle,
				eventTime: now.Add(-20 * time.Second),
			},
			want: 0,
		},
		{
			name: "all signals active max score",
			pane: &mockPaneView{
				alive:       true,
				activity:    StateRunning,
				beadID:      "ini-xyz",
				runStart:    now.Add(-30 * time.Second),
				runBytes:    50 * 1024,
				msgReceived: now.Add(-10 * time.Second),
				eventTime:   now.Add(-3 * time.Second),
			},
			want: 100,
		},
		{
			name: "bead plus sustained plus dispatch",
			pane: &mockPaneView{
				alive:       true,
				activity:    StateRunning,
				beadID:      "ini-abc",
				runStart:    now.Add(-10 * time.Second),
				msgReceived: now.Add(-5 * time.Second),
			},
			want: 75,
		},
		{
			name: "zero time message never received",
			pane: &mockPaneView{
				alive:    true,
				activity: StateIdle,
			},
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
