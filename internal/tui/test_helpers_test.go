package tui

import (
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// testPane creates a minimal Pane for testing (no PTY or process).
// Includes a SafeEmulator so layout, render, and visibility tests work.
func testPane(name string) *Pane {
	return &Pane{
		name:    name,
		emu:     vt.NewSafeEmulator(10, 5),
		alive:   true,
		visible: true,
	}
}

// hiddenTestPane creates a testPane with visible=false.
func hiddenTestPane(name string) *Pane {
	p := testPane(name)
	p.visible = false
	return p
}

// testPanes creates a []PaneView from names, each with a SafeEmulator.
func testPanes(names ...string) []PaneView {
	panes := make([]PaneView, len(names))
	for i, n := range names {
		panes[i] = testPane(n)
	}
	return panes
}

// toPaneViews converts a []*Pane to []PaneView.
func toPaneViews(panes []*Pane) []PaneView {
	views := make([]PaneView, len(panes))
	for i, p := range panes {
		views[i] = p
	}
	return views
}

// newTestTUI creates a TUI with the given panes and no screen.
// Panes with visible=false are added to layoutState.Hidden.
func newTestTUI(panes ...*Pane) *TUI {
	names := make([]string, len(panes))
	for i, p := range panes {
		names[i] = p.name
	}
	ls := DefaultLayoutState(names)
	for _, p := range panes {
		if !p.visible {
			if ls.Hidden == nil {
				ls.Hidden = make(map[string]bool)
			}
			ls.Hidden[p.name] = true
		}
	}
	views := make([]PaneView, len(panes))
	for i, p := range panes {
		views[i] = p
	}
	return &TUI{panes: views, layoutState: ls}
}

// newTestTUIWithScreen creates a TUI with a 120x40 SimulationScreen.
// Each pane gets a SafeEmulator with a background response drain goroutine
// (prevents SendKey from blocking on the internal io.Pipe).
func newTestTUIWithScreen(names ...string) (*TUI, tcell.SimulationScreen) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)

	panes := make([]*Pane, len(names))
	for i, n := range names {
		emu := vt.NewSafeEmulator(40, 10)
		go func() {
			buf := make([]byte, 256)
			for {
				_, err := emu.Read(buf)
				if err != nil {
					return
				}
			}
		}()
		panes[i] = &Pane{
			name:    n,
			emu:     emu,
			alive:   true,
			visible: true,
			region:  Region{X: i * 60, Y: 0, W: 60, H: 20},
		}
	}

	ls := DefaultLayoutState(names)
	views := make([]PaneView, len(panes))
	for i, p := range panes {
		views[i] = p
	}
	t := &TUI{
		screen:      s,
		panes:       views,
		layoutState: ls,
		lastW:       120,
		lastH:       40,
	}
	t.plan = computeLayout(ls, views, 120, 40)
	return t, s
}

// fakeRemotePaneView is a no-op PaneView for tests that only need remote
// identity semantics (paneKey, layout filtering, top actions) without the
// network machinery and resize timers of a real RemotePane.
type fakeRemotePaneView struct {
	name     string
	host     string
	emu      *vt.SafeEmulator
	region   Region
	alive    bool
	visible  bool
	beadID   string
	activity ActivityState
}

func newFakeRemotePaneView(name, host string) *fakeRemotePaneView {
	return &fakeRemotePaneView{
		name:    name,
		host:    host,
		emu:     vt.NewSafeEmulator(10, 5),
		region:  Region{W: 10, H: 5},
		alive:   true,
		visible: true,
	}
}

func (p *fakeRemotePaneView) Name() string                     { return p.name }
func (p *fakeRemotePaneView) Host() string                     { return p.host }
func (p *fakeRemotePaneView) Visible() bool                    { return p.visible }
func (p *fakeRemotePaneView) IsAlive() bool                    { return p.alive }
func (p *fakeRemotePaneView) IsSuspended() bool                { return false }
func (p *fakeRemotePaneView) IsPinned() bool                   { return false }
func (p *fakeRemotePaneView) Activity() ActivityState          { return p.activity }
func (p *fakeRemotePaneView) LastOutputTime() time.Time        { return time.Time{} }
func (p *fakeRemotePaneView) BeadID() string                   { return p.beadID }
func (p *fakeRemotePaneView) SessionDesc() string              { return "" }
func (p *fakeRemotePaneView) IdleWithBacklog() bool            { return false }
func (p *fakeRemotePaneView) BacklogCount() int                { return 0 }
func (p *fakeRemotePaneView) Emulator() *vt.SafeEmulator       { return p.emu }
func (p *fakeRemotePaneView) GetRegion() Region                { return p.region }
func (p *fakeRemotePaneView) SetBead(id, title string)         { p.beadID = id }
func (p *fakeRemotePaneView) SendKey(ev *tcell.EventKey)       {}
func (p *fakeRemotePaneView) SendText(text string, enter bool) {}
func (p *fakeRemotePaneView) AgentType() string                { return "" }
func (p *fakeRemotePaneView) SubmitKey() string                { return "" }
func (p *fakeRemotePaneView) SetVisible(v bool)                { p.visible = v }
func (p *fakeRemotePaneView) Render(screen tcell.Screen, focused bool, dimmed bool, index int, sel Selection) {
}
func (p *fakeRemotePaneView) Resize(rows, cols int) {}
func (p *fakeRemotePaneView) Close()                {}
