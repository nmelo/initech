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

func TestExecCmd_ShowReorder(t *testing.T) {
	tui := cmdTestTUI("a", "b", "c", "d")
	tui.execCmd("show c, a")
	if tui.panes[0].Name() != "c" || tui.panes[1].Name() != "a" {
		t.Errorf("show reorder: got [%s, %s, ...], want [c, a, ...]", tui.panes[0].Name(), tui.panes[1].Name())
	}
	if tui.panes[2].Name() != "b" || tui.panes[3].Name() != "d" {
		t.Errorf("remaining order: got [..., %s, %s], want [..., b, d]", tui.panes[2].Name(), tui.panes[3].Name())
	}
}

func TestExecCmd_ShowAll(t *testing.T) {
	tui := cmdTestTUI("c", "a", "b")
	tui.execCmd("show all")
	// show all resets to alphabetical.
	if tui.panes[0].Name() != "a" || tui.panes[1].Name() != "b" || tui.panes[2].Name() != "c" {
		t.Errorf("show all: got [%s, %s, %s], want [a, b, c]", tui.panes[0].Name(), tui.panes[1].Name(), tui.panes[2].Name())
	}
}

func TestExecCmd_ShowNoArgs(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("show")
	if tui.cmd.error == "" {
		t.Error("show with no args should set error")
	}
}

func TestExecCmd_ShowUnknown(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("show nonexistent")
	if tui.cmd.error == "" {
		t.Error("show with unknown name should set error")
	}
}

func TestExecCmd_ShowDeduplicate(t *testing.T) {
	tui := cmdTestTUI("a", "b", "c")
	tui.execCmd("show a, a, b")
	if tui.panes[0].Name() != "a" || tui.panes[1].Name() != "b" || tui.panes[2].Name() != "c" {
		t.Errorf("show dedup: got [%s, %s, %s], want [a, b, c]", tui.panes[0].Name(), tui.panes[1].Name(), tui.panes[2].Name())
	}
}

func TestExecCmd_Unhide(t *testing.T) {
	tui := cmdTestTUI("eng1", "eng2")
	tui.layoutState.Hidden["eng2"] = true
	tui.execCmd("unhide eng2")
	if tui.layoutState.Hidden["eng2"] {
		t.Error("unhide should unhide eng2")
	}
}

func TestExecCmd_UnhideAll(t *testing.T) {
	tui := cmdTestTUI("eng1", "eng2")
	tui.layoutState.Hidden["eng1"] = true
	tui.layoutState.Hidden["eng2"] = true
	tui.execCmd("unhide all")
	if tui.layoutState.Hidden["eng1"] || tui.layoutState.Hidden["eng2"] {
		t.Error("unhide all should unhide all panes")
	}
}

func TestExecCmd_Hide(t *testing.T) {
	tui := cmdTestTUI("eng1", "eng2")
	tui.execCmd("hide eng1")
	if !tui.layoutState.Hidden["eng1"] {
		t.Error("hide should hide eng1")
	}
}

func TestExecCmd_HideLastBlocked(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("hide eng1")
	if tui.layoutState.Hidden["eng1"] {
		t.Error("hiding the last visible pane should be blocked")
	}
}

func TestExecCmd_View(t *testing.T) {
	tui := cmdTestTUI("eng1", "eng2")
	tui.execCmd("view eng2")
	if tui.layoutState.Focused != "eng2" {
		t.Errorf("view should focus eng2, got %q", tui.layoutState.Focused)
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

func TestExecCmd_Pin(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.execCmd("pin eng1")
	if !tui.layoutState.Pinned["eng1"] {
		t.Error("pin should pin eng1")
	}
}

func TestExecCmd_Unpin(t *testing.T) {
	tui := cmdTestTUI("eng1")
	tui.layoutState.Pinned["eng1"] = true
	tui.panes[0].(*Pane).SetPinned(true)
	tui.execCmd("unpin eng1")
	if tui.layoutState.Pinned["eng1"] {
		t.Error("unpin should unpin eng1")
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
