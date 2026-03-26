package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestExecCmdHelp_OpensModal(t *testing.T) {
	tui := &TUI{}
	tui.execCmd("help")
	if !tui.help.active {
		t.Error("execCmd(help): help.active should be true")
	}
	if tui.help.scrollOffset != 0 {
		t.Errorf("execCmd(help): scrollOffset = %d, want 0", tui.help.scrollOffset)
	}
}

func TestExecCmdQuestionMark_OpensModal(t *testing.T) {
	tui := &TUI{}
	tui.execCmd("?")
	if !tui.help.active {
		t.Error("execCmd(?): help.active should be true")
	}
}

func TestHelpKey_EscapeCloses(t *testing.T) {
	tui := &TUI{help: helpModal{active: true}}
	ev := tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
	tui.handleHelpKey(ev)
	if tui.help.active {
		t.Error("Esc should close help modal")
	}
}

func TestHelpKey_BacktickCloses(t *testing.T) {
	tui := &TUI{help: helpModal{active: true}}
	ev := tcell.NewEventKey(tcell.KeyRune, '`', tcell.ModNone)
	tui.handleHelpKey(ev)
	if tui.help.active {
		t.Error("backtick should close help modal")
	}
}

func TestHelpKey_QCloses(t *testing.T) {
	tui := &TUI{help: helpModal{active: true}}
	ev := tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)
	tui.handleHelpKey(ev)
	if tui.help.active {
		t.Error("q should close help modal")
	}
}

func TestHelpKey_ScrollDown(t *testing.T) {
	tui := &TUI{help: helpModal{active: true, scrollOffset: 0}}
	ev := tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone)
	tui.handleHelpKey(ev)
	if tui.help.scrollOffset != 1 {
		t.Errorf("j: scrollOffset = %d, want 1", tui.help.scrollOffset)
	}
}

func TestHelpKey_ScrollUp_ClampedAtZero(t *testing.T) {
	tui := &TUI{help: helpModal{active: true, scrollOffset: 0}}
	ev := tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone)
	tui.handleHelpKey(ev)
	if tui.help.scrollOffset != 0 {
		t.Errorf("k at offset 0: scrollOffset = %d, want 0", tui.help.scrollOffset)
	}
}

func TestHelpKey_ScrollUpDecrement(t *testing.T) {
	tui := &TUI{help: helpModal{active: true, scrollOffset: 3}}
	ev := tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone)
	tui.handleHelpKey(ev)
	if tui.help.scrollOffset != 2 {
		t.Errorf("k at offset 3: scrollOffset = %d, want 2", tui.help.scrollOffset)
	}
}

func TestHelpInterceptsBeforeTopAndCmd(t *testing.T) {
	// With help active, handleKey should call handleHelpKey and not reach
	// the top or cmd modal handlers. A known side effect of handleHelpKey
	// is that pressing q closes help (active=false). Verify this happens.
	tui := &TUI{
		help: helpModal{active: true},
		top:  topModal{active: true}, // also active — help should win
	}
	ev := tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)
	tui.handleKey(ev)
	if tui.help.active {
		t.Error("handleKey with help active: q should close help, not top")
	}
	if !tui.top.active {
		t.Error("handleKey: top should remain active (help intercepted q)")
	}
}

func TestHelpLinesNotEmpty(t *testing.T) {
	if len(helpLines) == 0 {
		t.Error("helpLines must not be empty")
	}
	// Verify keybindings and commands sections are present.
	foundKeybindings := false
	foundCommands := false
	for _, line := range helpLines {
		if line == "Keybindings" {
			foundKeybindings = true
		}
		if len(line) > 8 && line[:8] == "Commands" {
			foundCommands = true
		}
	}
	if !foundKeybindings {
		t.Error("helpLines missing 'Keybindings' section header")
	}
	if !foundCommands {
		t.Error("helpLines missing 'Commands' section header")
	}
}
