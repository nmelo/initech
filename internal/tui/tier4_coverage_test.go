// Coverage tests Tier 4: execCmd command dispatch and helpers.
package tui

import (
	"testing"

	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// helper: TUI with simulation screen and panes for command tests.
func cmdTestTUI(names ...string) *TUI {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)

	panes := make([]*Pane, len(names))
	for i, n := range names {
		emu := vt.NewSafeEmulator(40, 10)
		go func() {
			buf := make([]byte, 256)
			for {
				if _, err := emu.Read(buf); err != nil {
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
	tui := &TUI{
		screen:      s,
		panes:       toPaneViews(panes),
		layoutState: ls,
		lastW:       120,
		lastH:       40,
	}
	tui.plan = computeLayout(ls, toPaneViews(panes), 120, 40)
	return tui
}

// ── execCmd dispatch ────────────────────────────────────────────────

func TestExecCmd_Empty(t *testing.T) {
	tui := cmdTestTUI("eng1")
	if tui.execCmd("") {
		t.Error("empty command should return false")
	}
}

func TestExecCmd_UnknownCommand(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("badcmd")
	if tui.cmd.error == "" {
		t.Error("unknown command should set error")
	}
}

func TestExecCmd_Grid(t *testing.T) {
	tui := cmdTestTUI("a", "b", "c", "d")
	tui.execCmd("grid 2x2")
	if tui.layoutState.Mode != LayoutGrid {
		t.Error("grid should set LayoutGrid mode")
	}
	if tui.layoutState.GridCols != 2 || tui.layoutState.GridRows != 2 {
		t.Errorf("grid = %dx%d, want 2x2", tui.layoutState.GridCols, tui.layoutState.GridRows)
	}
}

func TestExecCmd_GridNoArgs(t *testing.T) {
	tui := cmdTestTUI("a", "b", "c", "d")
	tui.execCmd("grid")
	if tui.layoutState.Mode != LayoutGrid {
		t.Error("grid without args should auto-calculate and set LayoutGrid")
	}
}

func TestExecCmd_GridInvalid(t *testing.T) {
	tui := cmdTestTUI("a", "b")
	tui.execCmd("grid abc")
	if tui.cmd.error == "" {
		t.Error("invalid grid format should set error")
	}
}

func TestExecCmd_Focus(t *testing.T) {
	tui := cmdTestTUI("eng1", "eng2")
	tui.execCmd("focus eng2")
	if tui.layoutState.Mode != LayoutFocus {
		t.Error("focus should set LayoutFocus mode")
	}
	if tui.layoutState.Focused != "eng2" {
		t.Errorf("focused = %q, want 'eng2'", tui.layoutState.Focused)
	}
}

func TestExecCmd_FocusNoArgs(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("focus")
	if tui.layoutState.Mode != LayoutFocus {
		t.Error("focus without args should set LayoutFocus")
	}
}

func TestExecCmd_FocusUnknown(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("focus nonexistent")
	if tui.cmd.error == "" {
		t.Error("focusing nonexistent pane should set error")
	}
}

func TestExecCmd_Zoom(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("zoom")
	if !tui.layoutState.Zoomed {
		t.Error("zoom should toggle zoomed on")
	}
	tui.execCmd("zoom")
	if tui.layoutState.Zoomed {
		t.Error("second zoom should toggle zoomed off")
	}
}

func TestExecCmd_Panel(t *testing.T) {
	tui := cmdTestTUI("eng1")
	// Default is Overlay=true; first toggle turns it off.
	tui.execCmd("panel")
	if tui.layoutState.Overlay {
		t.Error("panel should toggle overlay off (default was on)")
	}
	tui.execCmd("panel")
	if !tui.layoutState.Overlay {
		t.Error("second panel should toggle overlay back on")
	}
}

func TestExecCmd_Main(t *testing.T) {
	tui := cmdTestTUI("eng1", "eng2")
	tui.execCmd("main")
	if tui.layoutState.Mode != Layout2Col {
		t.Error("main should set Layout2Col mode")
	}
}

func TestExecCmd_RetiredCommandsAreUnknown(t *testing.T) {
	for _, cmd := range []string{"show eng1", "hide eng1", "unhide eng1", "view eng1", "pin eng1", "unpin eng1"} {
		tui := cmdTestTUI("eng1", "eng2")
		tui.execCmd(cmd)
		if tui.cmd.error == "" {
			t.Errorf("%q should produce unknown command error", cmd)
		}
	}
}

func TestExecCmd_Top(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("top")
	if !tui.top.active {
		t.Error("top should activate the top modal")
	}
}

func TestExecCmd_Ps(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("ps")
	if !tui.top.active {
		t.Error("ps should activate the top modal (alias for top)")
	}
}

func TestExecCmd_Log(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("log")
	if !tui.eventLogM.active {
		t.Error("log should activate the event log modal")
	}
}

func TestExecCmd_Events(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("events")
	if !tui.eventLogM.active {
		t.Error("events should activate the event log modal (alias for log)")
	}
}

func TestExecCmd_Help(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("help")
	if !tui.help.active {
		t.Error("help should activate the help modal")
	}
}

func TestExecCmd_QuestionMark(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("?")
	if !tui.help.active {
		t.Error("? should activate the help modal")
	}
}

func TestExecCmd_QuitConfirm(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("quit")
	if tui.cmd.pendingConfirm != "quit" {
		t.Errorf("quit should set pendingConfirm = 'quit', got %q", tui.cmd.pendingConfirm)
	}
}

func TestExecCmd_QShortcut(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("q")
	if tui.cmd.pendingConfirm != "quit" {
		t.Errorf("q should set pendingConfirm = 'quit', got %q", tui.cmd.pendingConfirm)
	}
}

func TestExecCmd_Patrol(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(120, 40)
	emu := vt.NewSafeEmulator(40, 10)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	tui := &TUI{
		screen:      s,
		panes:       toPaneViews([]*Pane{{name: "eng1", emu: emu, alive: true, visible: true}}),
		layoutState: DefaultLayoutState([]string{"eng1"}),
	}
	// Patrol peeks all agents; just verify it doesn't crash.
	tui.execCmd("patrol")
}
