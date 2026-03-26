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
	// Use a small screen (sh=5) so helpMaxOffset() > 0, enabling scroll.
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 5)
	tui := &TUI{screen: s, help: helpModal{active: true, scrollOffset: 0}}
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

// TestHandleHelpKey_ScrollClamped verifies scrollOffset does not exceed
// helpMaxOffset after repeated KeyDown presses (ini-a1e.12).
func TestHandleHelpKey_ScrollClamped(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 5) // sh=5: contentRows=3, max=len(helpLines)-3 (large positive)
	tui := &TUI{screen: s, help: helpModal{active: true, scrollOffset: 0}}

	maxOff := tui.helpMaxOffset()
	if maxOff <= 0 {
		t.Fatalf("helpMaxOffset() = %d, want > 0 for sh=5 screen", maxOff)
	}

	// Press j many more times than the max offset.
	ev := tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone)
	for i := 0; i < maxOff+10; i++ {
		tui.handleHelpKey(ev)
	}

	if tui.help.scrollOffset > maxOff {
		t.Errorf("scrollOffset = %d exceeds helpMaxOffset = %d (should be clamped)", tui.help.scrollOffset, maxOff)
	}
}

// TestHandleHelpKey_KeyDownClamped verifies KeyDown also clamps at helpMaxOffset.
func TestHandleHelpKey_KeyDownClamped(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 5)
	tui := &TUI{screen: s, help: helpModal{active: true, scrollOffset: 0}}
	maxOff := tui.helpMaxOffset()

	ev := tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	for i := 0; i < maxOff+10; i++ {
		tui.handleHelpKey(ev)
	}

	if tui.help.scrollOffset > maxOff {
		t.Errorf("KeyDown: scrollOffset = %d exceeds helpMaxOffset = %d", tui.help.scrollOffset, maxOff)
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
