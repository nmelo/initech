package tui

// Fleet management belongs to the main window (ini-fn77).
//
// Operator decision: child windows are VIEWERS. You look, focus, type into
// agents, scroll. The agents panel and the command modal are window 1's, so
// fleet management has one authority and a child window cannot act on a stale
// view of it.

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func chordTUI(t *testing.T, windowID string) *TUI {
	t.Helper()
	return &TUI{
		windowID:    windowID,
		agentEvents: make(chan AgentEvent, 16),
		quitCh:      make(chan struct{}),
	}
}

func backtick() *tcell.EventKey { return tcell.NewEventKey(tcell.KeyRune, '`', tcell.ModNone) }

// TestChords_BacktickOpensTheCommandModalInTheMainWindow is the control: the
// gate must not cost window 1 the feature.
func TestChords_BacktickOpensTheCommandModalInTheMainWindow(t *testing.T) {
	tui := chordTUI(t, WindowOne)
	tui.handleKey(backtick())
	if !tui.cmd.active {
		t.Fatal("backtick did not open the command modal in the main window")
	}
}

// TestChords_BacktickIsSwallowedInAChildWindow. Per the operator's literal
// wording: nothing happens. Not forwarded to the pane -- a window-2 user must
// not be able to type a backtick into a composer when a window-1 user cannot.
func TestChords_BacktickIsSwallowedInAChildWindow(t *testing.T) {
	tui := chordTUI(t, "window-2")
	rec := &keyRecorder{}
	tui.panes = []PaneView{rec}
	tui.layoutState.Focused = agentKey(rec)

	if consumed := tui.handleKey(backtick()); consumed {
		t.Error("backtick quit the TUI in a child window")
	}
	if tui.cmd.active {
		t.Error("the command modal opened in a child window; fleet management is the " +
			"main window's")
	}
	if rec.keys != 0 {
		t.Errorf("the backtick reached the focused agent (%d key(s)); it must be "+
			"swallowed, or a child-window user can type into a composer what a "+
			"main-window user cannot", rec.keys)
	}
}

// TestChords_AgentsPanelIsMainWindowOnlyAndSaysSo. A chord that does nothing
// reads as broken (ini-162m), so the child window gets a notice.
func TestChords_AgentsPanelIsMainWindowOnlyAndSaysSo(t *testing.T) {
	tui := chordTUI(t, "window-2")
	tui.openAgentsModal()

	if tui.agents.active {
		t.Fatal("the agents panel opened in a child window")
	}
	assertMainWindowNotice(t, tui)
}

func TestChords_AgentsPanelStillOpensInTheMainWindow(t *testing.T) {
	tui := chordTUI(t, WindowOne)
	tui.openAgentsModal()
	if !tui.agents.active {
		t.Fatal("the agents panel did not open in the main window")
	}
}

// TestChords_AgentsCommandIsGatedToo keeps the defence in depth the AC asked
// for: :agents is unreachable in a child window once the modal is gone, and
// the gate stays anyway.
func TestChords_AgentsCommandIsGatedToo(t *testing.T) {
	tui := chordTUI(t, "window-2")
	tui.execCmd("agents")
	if tui.agents.active {
		t.Fatal(":agents opened the panel in a child window")
	}
	assertMainWindowNotice(t, tui)
}

// TestChords_HelpSaysWhichWindowOwnsThem is Ship-It gate #4: the user must be
// able to learn WHY the chord does nothing here.
func TestChords_HelpSaysWhichWindowOwnsThem(t *testing.T) {
	text := strings.Join(getHelpLines(), "\n")
	for _, want := range []string{"agents", "main window only"} {
		if !strings.Contains(text, want) {
			t.Errorf("help text lacks %q; a chord that does nothing with no explanation "+
				"is indistinguishable from a broken build", want)
		}
	}
}

func assertMainWindowNotice(t *testing.T, tui *TUI) {
	t.Helper()
	select {
	case ev := <-tui.agentEvents:
		if !strings.Contains(strings.ToLower(ev.Detail), "main window") {
			t.Errorf("notice reads %q; it must say where the panel lives", ev.Detail)
		}
	default:
		t.Error("no notice: the chord did nothing and said nothing, which reads as a " +
			"broken build rather than a decision (ini-162m)")
	}
}

// keyRecorder counts keys forwarded to a pane. PaneView is embedded so it can
// never do more than the real type.
type keyRecorder struct {
	PaneView
	keys int
}

func (k *keyRecorder) Name() string              { return "eng2" }
func (k *keyRecorder) Host() string              { return "" }
func (k *keyRecorder) SendKey(_ *tcell.EventKey) { k.keys++ }
func (k *keyRecorder) FlushPaste(_ []byte)       {}
