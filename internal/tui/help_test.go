package tui

import (
	"strings"
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
	// getHelpLines() populates the package-level helpLines slice lazily via
	// sync.Once. Call it here rather than reading helpLines directly: this
	// test must not depend on some other test having already triggered the
	// Once first, or it fails under -shuffle=on whenever it draws a position
	// before that other test (ini-ls0c).
	lines := getHelpLines()
	if len(lines) == 0 {
		t.Error("helpLines must not be empty")
	}
	// Verify keybindings and commands sections are present.
	foundKeybindings := false
	foundCommands := false
	for _, line := range lines {
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

// TestHelpLinesDocumentFocusSplit guards ini-vtki's Option+F keybinding
// against silently disappearing from the help modal. Calls getHelpLines()
// directly rather than reading the package-level helpLines slice, for the
// same sync.Once shuffle-order reason as TestHelpLinesNotEmpty (ini-ls0c).
func TestHelpLinesDocumentFocusSplit(t *testing.T) {
	lines := getHelpLines()
	found := false
	for _, line := range lines {
		if strings.Contains(line, "+f") && strings.Contains(line, "split") {
			found = true
			break
		}
	}
	if !found {
		t.Error("helpLines missing the Option+F focus split keybinding")
	}
}

// TestHelpLinesDocumentQuickGridAndLive guards ini-dvy5's Option+G/Option+L
// quick grid/live popup against silently missing from the in-app help
// overlay (ini-162m: the feature shipped in v2.1.0 but neither binding
// appeared here, discovered against the installed binary, not the diff).
// Both must describe columns-then-rows, matching :grid/:live's own CxR
// convention -- ini-dvy5's spec reversed mid-build from an original
// rows-first design, and help text is exactly where that stale framing
// could quietly reappear.
func TestHelpLinesDocumentQuickGridAndLive(t *testing.T) {
	lines := getHelpLines()
	foundG, foundL := false, false
	for _, line := range lines {
		if strings.Contains(line, "+g") && strings.Contains(line, "grid") {
			foundG = true
		}
		if strings.Contains(line, "+l") && strings.Contains(line, "live") {
			foundL = true
		}
		if strings.Contains(line, "rows, then columns") || strings.Contains(line, "rows then columns") {
			t.Error("helpLines describes quick grid/live as rows-then-columns -- ini-dvy5 reversed this to columns-then-rows; do not reintroduce the stale framing")
		}
	}
	if !foundG {
		t.Error("helpLines missing the Option+G quick grid keybinding")
	}
	if !foundL {
		t.Error("helpLines missing the Option+L quick live keybinding")
	}
}

// TestHelpLinesDocumentEveryCommand guards against ini-162m's whole defect
// class recurring: a command the command bar accepts (commandNames, used for
// fuzzy-match suggestions) with no entry in the help overlay's Commands
// section ships invisibly -- a capability nobody can find has not really
// shipped. Broader than asserting specific strings so the NEXT command added
// without a help entry fails here too, instead of shipping and being found
// by a user reading release notes for a feature the product can't explain.
//
// Matches each commandNames entry as a whole punctuation-stripped token
// anywhere in the Commands section (not just line-leading position), since
// "log" and "events" are both independent commandNames entries documented
// on one shared line as "log (events)" -- same pattern already used for
// commandAliases like "restart (r)" and "top (ps)".
func TestHelpLinesDocumentEveryCommand(t *testing.T) {
	lines := getHelpLines()

	var commandLines []string
	inCommands := false
	for _, line := range lines {
		if line == "" {
			if inCommands {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "Commands") {
			inCommands = true
			continue
		}
		if inCommands {
			commandLines = append(commandLines, line)
		}
	}
	if len(commandLines) == 0 {
		t.Fatal("could not locate the Commands section in helpLines")
	}

	for _, name := range commandNames {
		found := false
		for _, line := range commandLines {
			for _, field := range strings.Fields(line) {
				if strings.Trim(field, "()[]<>.,;:") == name {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Errorf("commandNames contains %q but the help overlay's Commands section does not document it -- a command with no help entry ships invisibly (ini-162m)", name)
		}
	}
}
