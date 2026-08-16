package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// simScreenText flattens a bare SimulationScreen to text (screenText wants a
// full TUI; the ribbon render tests drive a pane directly).
func simScreenText(s tcell.SimulationScreen) string {
	var b strings.Builder
	cells, w, h := s.GetContents()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// ── ini-ap3i follow-on: suspend/wake from the agents modal ────────────
//
// Operator request (2026-08-15, after persisted suspension shipped): park and
// wake agents from the modal — `s` for the selected agent, `S` for its whole
// band — instead of round-tripping through `initech suspend <name>` per
// agent. Both keys are thin dispatchers over the same one-mechanism pair
// every other path uses: suspendAgent (truthful refusals) and resumePane.

// The ini-162m rule: a capability nobody can find has not shipped. The
// footer is the modal's discovery surface.
func TestAgentsHelpText_NamesSuspendKeys(t *testing.T) {
	if !strings.Contains(agentsHelpText, "s/S suspend") {
		t.Errorf("modal footer does not name the suspend keys: %q", agentsHelpText)
	}
	// Width budget: boxW floors at len+4 and caps at the screen, so a footer
	// past ~114 chars clips its own "Esc close" hint on a 120-col terminal —
	// exactly how the first draft of this feature failed its goldens.
	if len(agentsHelpText) > 114 {
		t.Errorf("modal footer is %d chars; >114 clips on a 120-col terminal", len(agentsHelpText))
	}
}

func testPaneRunning(t *testing.T, name string) *Pane {
	t.Helper()
	p, err := NewPane(PaneConfig{Name: name, Command: []string{"sh", "-c", "sleep 30"}}, 10, 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestAgentsToggleSuspend_ParksAndRecords(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI()
	tui.projectRoot = root
	p := testPaneRunning(t, "seo")
	tui.panes = append(tui.panes, p)
	tui.agents.selected = 0

	tui.agentsToggleSuspend()

	if !p.IsSuspended() {
		t.Fatalf("s did not park the selected agent (modal msg: %q)", tui.agents.error)
	}
	fs, _ := LoadFleetState(root)
	if !fs.IsSuspended("seo") {
		t.Error("modal suspend did not persist — next launch would boot this agent")
	}
}

func TestAgentsToggleSuspend_WakesSuspended(t *testing.T) {
	tui := newTestTUI()
	parked := NewParkedPane(PaneConfig{Name: "seo", Command: []string{"sh", "-c", "echo up; sleep 5"}}, 10, 40)
	tui.panes = append(tui.panes, parked)
	tui.agents.selected = 0

	tui.agentsToggleSuspend()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if np, ok := tui.panes[0].(*Pane); ok && np != parked && !np.IsSuspended() {
			defer np.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("s on a suspended agent did not wake it (modal msg: %q)", tui.agents.error)
}

func TestAgentsToggleSuspend_RemoteIsHonestlyRefused(t *testing.T) {
	tui := newTestTUI()
	tui.panes = append(tui.panes, &RemotePane{name: "super", host: "support", alive: true})
	tui.agents.selected = 0

	tui.agentsToggleSuspend()

	if tui.agents.error == "" || !strings.Contains(tui.agents.error, "machine") {
		t.Errorf("remote agent suspend must refuse and say why, got %q", tui.agents.error)
	}
}

func TestAgentsToggleSuspendGroup_ParksIdleSkipsBusy(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI()
	tui.projectRoot = root
	a := testPaneRunning(t, "eng1")
	b := testPaneRunning(t, "eng2")
	b.mu.Lock()
	b.activity = StateRunning // mid-work: must be skipped, truthfully
	b.mu.Unlock()
	tui.panes = append(tui.panes, a, b)
	tui.layoutState.GroupOf = map[string]string{"eng1": "eng", "eng2": "eng"}
	tui.layoutState.Groups = []string{"eng"}
	tui.agents.selected = 0

	tui.agentsToggleSuspendGroup()

	if !a.IsSuspended() {
		t.Error("group suspend did not park the idle member")
	}
	if b.IsSuspended() {
		t.Error("group suspend parked a RUNNING agent — the truthful-refusal rule exists to prevent exactly this")
	}
	if !strings.Contains(tui.agents.error, "eng2") {
		t.Errorf("skip must be reported by name, got %q", tui.agents.error)
	}
}

func TestAgentsToggleSuspendGroup_WakesAllWhenAllParked(t *testing.T) {
	tui := newTestTUI()
	a := NewParkedPane(PaneConfig{Name: "eng1", Command: []string{"sh", "-c", "echo up; sleep 5"}}, 10, 40)
	b := NewParkedPane(PaneConfig{Name: "eng2", Command: []string{"sh", "-c", "echo up; sleep 5"}}, 10, 40)
	tui.panes = append(tui.panes, a, b)
	tui.layoutState.GroupOf = map[string]string{"eng1": "eng", "eng2": "eng"}
	tui.layoutState.Groups = []string{"eng"}
	tui.agents.selected = 0

	tui.agentsToggleSuspendGroup()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		awake := 0
		for _, pv := range tui.panes {
			if np, ok := pv.(*Pane); ok && !np.IsSuspended() {
				awake++
			}
		}
		if awake == 2 {
			for _, pv := range tui.panes {
				if np, ok := pv.(*Pane); ok {
					defer np.Close()
				}
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("S on an all-parked band did not wake both members (modal msg: %q)", tui.agents.error)
}

// ── suspension must cross the window boundary ─────────────────────────
//
// Window 2 renders window 1's agents from RemotePanes fed by agent_status
// broadcasts. Suspension was not on that wire, so a parked agent kept its
// last screen and read as idle in every other window ("window 2 doesn't
// show it as suspended", operator, 2026-08-15). agent_status is the channel
// BUILT for observed agent state — suspended rides it like beads and
// waiting do.
func TestRemotePane_AppliedSuspensionShowsInActivity(t *testing.T) {
	rp := &RemotePane{name: "eng2", host: "window1", alive: true}

	rp.ApplySuspended(true)
	if rp.Activity() != StateSuspended {
		t.Fatalf("suspended remote pane reports %v, want StateSuspended — the viewer window renders this state", rp.Activity())
	}

	rp.ApplySuspended(false)
	if rp.Activity() == StateSuspended {
		t.Fatal("cleared suspension still reports StateSuspended — a woken agent would look parked in other windows forever")
	}
}

func TestApplyAgentStatus_CarriesSuspended(t *testing.T) {
	tui := newTestTUI()
	rp := &RemotePane{name: "eng2", host: "window1", alive: true}
	tui.panes = append(tui.panes, rp)

	tui.applyAgentStatus("eng2", nil, "", WaitingState{}, true)
	if rp.Activity() != StateSuspended {
		t.Fatal("agent_status with suspended=true did not park the remote pane's displayed state")
	}
	tui.applyAgentStatus("eng2", nil, "", WaitingState{}, false)
	if rp.Activity() == StateSuspended {
		t.Fatal("agent_status with suspended=false did not clear the displayed state")
	}
}

// ── the wake gesture and the stream must survive a pane replacement ────
//
// The window server wires each pane's output sink ONCE at startup, to the
// pane OBJECTS that exist then — and resumePane REPLACES the object. Post-
// wake, the new pane had no sink (window 2 saw a frozen screen) and the
// input pump still held the dead pane (window 2's keystrokes vanished).
// Found pulling on "the footer doesn't say suspended in window 2": the
// footer names a gesture ("any key"), the gesture has to work from there,
// and it could not have.
func TestPane_WakeOnStreamInput(t *testing.T) {
	p := NewParkedPane(PaneConfig{Name: "eng2"}, 10, 40)
	fired := make(chan struct{}, 1)
	p.SetOnSuspendedMessage(func(*Pane) { fired <- struct{}{} })

	if !p.WakeOnStreamInput() {
		t.Fatal("stream input at a suspended pane must be swallowed as a wake gesture")
	}
	select {
	case <-fired:
	default:
		t.Fatal("wake hook did not fire — window 2's keystroke would be silently dropped")
	}

	live, err := NewPane(PaneConfig{Name: "x", Command: []string{"sh", "-c", "sleep 5"}}, 10, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if live.WakeOnStreamInput() {
		t.Fatal("live pane input must flow to the PTY, not be swallowed")
	}
}

func TestDaemon_ReplaceLocalPane_KeepsSinkAndIdentity(t *testing.T) {
	old := NewParkedPane(PaneConfig{Name: "eng2"}, 10, 40)
	d := &Daemon{
		panes:      []*Pane{old},
		ringBufs:   map[string]*RingBuf{},
		multiSinks: map[string]*MultiSink{},
	}
	ms := NewMultiSink()
	d.multiSinks["eng2"] = ms

	np := NewParkedPane(PaneConfig{Name: "eng2"}, 10, 40)
	d.ReplaceLocalPane(np)

	if got := d.currentPane("eng2"); got != np {
		t.Fatal("daemon still serves the dead pane object — window 2's input pumps into a corpse")
	}
	np.sinkMu.Lock()
	sink := np.networkSink
	np.sinkMu.Unlock()
	if sink != ms {
		t.Fatal("replacement pane not wired to the EXISTING MultiSink — attached windows lose the stream on every wake")
	}
}

func TestRemotePaneRender_SuspendedBadge(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(60, 10)
	rp := &RemotePane{
		name: "eng2", host: "window1", alive: true,
		emu:    vt.NewSafeEmulator(58, 8),
		region: Region{X: 0, Y: 0, W: 60, H: 10},
	}
	rp.ApplySuspended(true)

	rp.Render(s, false, false, 2, Selection{})
	s.Show()
	out := simScreenText(s)
	if !strings.Contains(out, "susp") {
		t.Fatalf("viewer ribbon does not show the suspended state the authority broadcast; got:\n%s", out)
	}
}

// The wake races window geometry: the respawn sizes itself from whatever
// window last touched the pane (measured: window 2 set 37x110 at 21:12:29,
// window 1's layout re-sized to 36x104 by :41, spawn inherited window 1's
// numbers — near-agreement that run; 76-vs-37 the garbled run). Instead of
// arbitrating the writers, the VIEWER reasserts its geometry on the wake
// edge it already receives, so the child snaps to the displaying window's
// truth within one resize round-trip of booting.
func TestApplyAgentStatus_WakeEdgeReassertsGeometry(t *testing.T) {
	tui := newTestTUI()
	rp := &RemotePane{
		name: "eng2", host: "window1", alive: true,
		emu:    vt.NewSafeEmulator(80, 24), // stale belief
		region: Region{X: 0, Y: 0, W: 112, H: 41},
	}
	tui.panes = append(tui.panes, rp)

	tui.applyAgentStatus("eng2", nil, "", WaitingState{}, true)  // park
	tui.applyAgentStatus("eng2", nil, "", WaitingState{}, false) // wake edge

	wantCols, wantRows := rp.region.TerminalSize()
	if rp.emu.Width() != wantCols || rp.emu.Height() != wantRows {
		t.Fatalf("wake edge did not reassert viewer geometry: emu %dx%d, want %dx%d",
			rp.emu.Width(), rp.emu.Height(), wantCols, wantRows)
	}
}

// ── ambient peer events must not change the operator's layout mode ────
//
// Operator, live (2026-08-15): Focus mode (Option+F) "keeps bouncing back
// to Grid 3x2 by itself". Mechanism: handlePeerUpdate fires on every peer
// event — and a sleeping remote machine's reconnect loop fires one every
// couple of seconds — and its recalcGrid(true) FORCE-evicts any non-Live
// mode back to Grid. force conflates "recompute grid dims" with "exit the
// operator's chosen mode"; a network event is never a mode decision.
// Released behavior (v2.9.0 window attaches did it too), made constant by
// remote machines.
func TestPeerUpdate_PreservesFocusMode(t *testing.T) {
	tui := newTestTUI(&Pane{name: "super", visible: true})
	tui.layoutState.Mode = LayoutFocus
	tui.layoutState.Focused = "super"

	tui.handlePeerUpdate("support", nil, false) // the reconnect-loop shape

	if tui.layoutState.Mode != LayoutFocus {
		t.Fatalf("a peer event changed the layout mode to %v — ambient events must never "+
			"override an operator UI choice", tui.layoutState.Mode)
	}
}

func TestPeerPaneAdded_PreservesFocusMode(t *testing.T) {
	tui := newTestTUI(&Pane{name: "super", visible: true})
	tui.layoutState.Mode = LayoutFocus
	tui.layoutState.Focused = "super"

	tui.handlePeerPaneAdded("support", &RemotePane{name: "temp", host: "support", alive: true})

	if tui.layoutState.Mode != LayoutFocus {
		t.Fatalf("a pushed pane changed the layout mode to %v", tui.layoutState.Mode)
	}
}
