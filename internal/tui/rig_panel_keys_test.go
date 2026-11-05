package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// ini-cdgj: the rig panel's one form, its keys and its layout.yaml keys.
// It replaces ini-5d3's two-form tests: what they asserted of the band
// (Option+s hides it and the grid grows, its height, the restart, the
// second window, one write per change, the needs-input list below it) is
// asserted here of the panel; the floating form and the flip are retired.

var panelNames = []string{"p01", "p02", "p03", "p04"}

// panelWindow is window 1 with four agents in a 2x2 grid on a w x h screen,
// the panel shown.
func panelWindow(t *testing.T, w, h int) (*TUI, tcell.SimulationScreen) {
	t.Helper()
	tui, s := effortTUI(t)
	s.SetSize(w, h)
	tui.lastW, tui.lastH = w, h
	tui.projectRoot = t.TempDir()
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 2
	tui.layoutState.GridExplicit = true
	tui.layoutState.Overlay = true
	tui.applyLayout()
	return tui, s
}

func optionKey(r rune, mod tcell.ModMask) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModAlt|mod)
}

func gridTop(tui *TUI) int {
	y := 1 << 30
	for _, pr := range tui.plan.Panes {
		if pr.Region.Y < y {
			y = pr.Region.Y
		}
	}
	return y
}

// TestRigPanelKeys_OptionSHidesThePanelAndTheGridGrows: AC2.
func TestRigPanelKeys_OptionSHidesThePanelAndTheGridGrows(t *testing.T) {
	tui, _ := panelWindow(t, 120, 40)
	if y := gridTop(tui); y != 1+1+panelDefaultRows+1 {
		t.Fatalf("pinned: the grid starts on row %d, want %d (title, border, 8 rows, border)", y, 1+1+panelDefaultRows+1)
	}
	tui.handleKey(optionKey('s', 0))
	if y := gridTop(tui); y != 0 {
		t.Fatalf("hidden: the grid starts on row %d, want 0", y)
	}
	tui.handleKey(optionKey('s', 0))
	if y := gridTop(tui); y != 1+1+panelDefaultRows+1 {
		t.Fatalf("pinned again: the grid starts on row %d", y)
	}
}

// TestRigPanelKeys_ShiftOptionSAndAgentsPinFloatAreRetired: AC2. Every
// encoding of Shift+Option+s does nothing, and the two commands are unknown.
func TestRigPanelKeys_ShiftOptionSAndAgentsPinFloatAreRetired(t *testing.T) {
	tui, _ := panelWindow(t, 120, 40)
	for _, ev := range []*tcell.EventKey{
		optionKey('S', tcell.ModShift), optionKey('s', tcell.ModShift), optionKey('S', 0),
	} {
		tui.handleKey(ev)
		if !tui.layoutState.Overlay || gridTop(tui) != 1+1+panelDefaultRows+1 {
			t.Fatalf("Shift+Option+s changed the panel: overlay %v, grid top %d", tui.layoutState.Overlay, gridTop(tui))
		}
	}
	for _, cmd := range []string{"agents pin", "agents float"} {
		tui.cmd.error = ""
		tui.execCmd(cmd)
		if tui.cmd.error != `unknown command "`+cmd+`"` {
			t.Errorf("%s answered %q", cmd, tui.cmd.error)
		}
	}
}

// TestRigPanelKeys_OptionEqualsAndMinusChangeTheHeightAndSaveIt: AC3. Each
// press is one row and one write; a bound is a no-op and writes nothing.
func TestRigPanelKeys_OptionEqualsAndMinusChangeTheHeightAndSaveIt(t *testing.T) {
	tui, _ := panelWindow(t, 120, 40)
	path := layoutPath(tui.projectRoot)
	tui.handleKey(optionKey('=', 0))
	if y := gridTop(tui); y != 1+1+9+1 {
		t.Fatalf("after Option+= the grid starts on row %d, want 12", y)
	}
	if ls, ok := LoadLayout(tui.projectRoot, panelNames); !ok || ls.PanelRows != 9 {
		t.Fatalf("layout.yaml holds panel_rows %d (ok %v), want 9", ls.PanelRows, ok)
	}
	for i := 0; i < 20; i++ {
		tui.handleKey(optionKey('-', 0))
	}
	if got := tui.panelRowsFor(40); got != panelRowsMin {
		t.Fatalf("shrunk past the floor: %d", got)
	}
	os.Remove(path)
	tui.handleKey(optionKey('-', 0))
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a press at the floor wrote layout.yaml")
	}
	for i := 0; i < 40; i++ {
		tui.handleKey(optionKey('=', 0))
	}
	if got := tui.panelRowsFor(40); got != 20 {
		t.Fatalf("grown past half the screen: %d, want 20", got)
	}
}

// TestRigPanelKeys_PanelRowsSurvivesARestartAndThePanelStartsPinned: AC3.
func TestRigPanelKeys_PanelRowsSurvivesARestartAndThePanelStartsPinned(t *testing.T) {
	root := t.TempDir()
	st := DefaultLayoutState(panelNames)
	st.PanelRows = 5
	st.Overlay = false // Hidden when it was saved.
	SaveLayout(root, st)
	b, _ := os.ReadFile(layoutPath(root))
	if !strings.Contains(string(b), "panel_rows: 5") {
		t.Fatalf("layout.yaml:\n%s", b)
	}
	ls, ok := LoadLayout(root, panelNames)
	if !ok || ls.PanelRows != 5 {
		t.Fatalf("reloaded panel_rows %d", ls.PanelRows)
	}
	// Default height: not written at all.
	SaveLayout(root, DefaultLayoutState(panelNames))
	if b, _ := os.ReadFile(layoutPath(root)); strings.Contains(string(b), "panel_rows") {
		t.Fatalf("the default height was written:\n%s", b)
	}
}

// TestRigPanelKeys_ABadPanelRowsReadsTheDefaultWithOneWarning: feed it
// nothing, and a value under the floor.
func TestRigPanelKeys_ABadPanelRowsReadsTheDefaultWithOneWarning(t *testing.T) {
	logs := captureLogs(t)
	if readPanelRows(0) != 0 || readPanelRows(3) != 3 || readPanelRows(30) != 30 {
		t.Fatal("a valid value changed")
	}
	if readPanelRows(-2) != 0 || readPanelRows(1) != 0 {
		t.Fatal("a value under the floor did not read as the default")
	}
	if n := strings.Count(logs.String(), "panel_rows is under 3"); n != 2 {
		t.Fatalf("warned %d times for two bad values", n)
	}
}

// TestRigPanelKeys_AgentsFormIsKeptAsTheFileHadIt: AC3/ruling 3. Read and
// written back unchanged, never acted on, no warning for any value.
func TestRigPanelKeys_AgentsFormIsKeptAsTheFileHadIt(t *testing.T) {
	logs := captureLogs(t)
	for _, v := range []string{"floating", "pinned", "sideways"} {
		root := t.TempDir()
		os.MkdirAll(layoutDir(root), 0o755)
		os.WriteFile(layoutPath(root), []byte("mode: grid\nagents_form: "+v+"\n"), 0o644)
		ls, ok := LoadLayout(root, panelNames)
		if !ok || ls.AgentsForm != v {
			t.Fatalf("%s read as %q", v, ls.AgentsForm)
		}
		SaveLayout(root, ls)
		if b, _ := os.ReadFile(layoutPath(root)); !strings.Contains(string(b), "agents_form: "+v) {
			t.Fatalf("%s was not written back:\n%s", v, b)
		}
	}
	if strings.Contains(logs.String(), "agents_form") {
		t.Fatalf("agents_form was warned about:\n%s", logs.String())
	}
}

// TestRigPanelKeys_ASecondWindowGrowsItsOwnAndWritesNothing: window 1 alone
// writes layout.yaml.
func TestRigPanelKeys_ASecondWindowGrowsItsOwnAndWritesNothing(t *testing.T) {
	tui, _ := panelWindow(t, 120, 40)
	tui.windowID = "window-2"
	SaveLayout(tui.projectRoot, DefaultLayoutState(panelNames))
	before, _ := os.ReadFile(layoutPath(tui.projectRoot))
	tui.handleKey(optionKey('=', 0))
	// A second window that owns no agents has no pane to measure from: the
	// panel's own height is the answer.
	if got := tui.agentsPinnedTop(40); got != 1+1+9+1 {
		t.Fatalf("the second window's panel is %d rows, want 12", got)
	}
	if after, _ := os.ReadFile(layoutPath(tui.projectRoot)); string(after) != string(before) {
		t.Fatalf("a second window rewrote layout.yaml:\n%s", after)
	}
}

// TestRigPanelKeys_UnderThirtyRowsEachBoxKeepsOneRow: AC4's height fold.
func TestRigPanelKeys_UnderThirtyRowsEachBoxKeepsOneRow(t *testing.T) {
	tui, _ := panelWindow(t, 120, 28)
	if got := tui.agentsPinnedTop(28); got != 1+1+1+1 {
		t.Fatalf("at 28 rows the panel takes %d rows, want 4 (title, border, one row, border)", got)
	}
	tui.handleKey(optionKey('=', 0))
	if got := tui.agentsPinnedTop(28); got != 4 {
		t.Fatalf("Option+= changed a folded panel: %d", got)
	}
}

// TestRigPanelKeys_TheNeedsInputListDrawsBelowThePanel: the list is not
// covered by the band.
func TestRigPanelKeys_TheNeedsInputListDrawsBelowThePanel(t *testing.T) {
	tui, s := panelWindow(t, 120, 40)
	ysr2Pane(tui, "p04").SetWaitingInput("Do you want to proceed?")
	// The list, then the panel over it, in render's order.
	s.Clear()
	tui.renderAttention()
	tui.renderRigPanel(panelNow)
	s.Show()
	rows := screenRows(t, s)
	for y, r := range rows {
		if strings.Contains(r, "p04") && strings.Contains(r, "proceed") {
			if y < tui.planTop {
				t.Fatalf("the needs-input row is %d, inside the %d-row panel", y, tui.planTop)
			}
			return
		}
	}
	t.Fatalf("the needs-input list is not on screen:\n%s", strings.Join(rows, "\n"))
}

// TestRigPanelKeys_AStoredHeightIsKeptToHalfTheScreen: a panel_rows saved on
// a tall screen is clamped where it is drawn, never past half a shorter one.
func TestRigPanelKeys_AStoredHeightIsKeptToHalfTheScreen(t *testing.T) {
	tui, _ := panelWindow(t, 120, 40)
	tui.layoutState.PanelRows = 30
	if got := tui.panelRowsFor(40); got != 20 {
		t.Fatalf("panel_rows 30 on a 40-row screen draws %d rows, want 20", got)
	}
	tui.layoutState.PanelRows = 3
	if got := tui.panelRowsFor(80); got != 3 {
		t.Fatalf("panel_rows 3 draws %d rows", got)
	}
}

// TestRigPanelKeys_AFileThatSaysFloatingStillShowsTheBand: agents_form is
// never acted on (ini-cdgj).
func TestRigPanelKeys_AFileThatSaysFloatingStillShowsTheBand(t *testing.T) {
	tui, _ := panelWindow(t, 120, 40)
	for _, v := range []string{"floating", "pinned", ""} {
		tui.layoutState.AgentsForm = v
		tui.applyLayout()
		if got := tui.agentsPinnedTop(40); got != 1+1+panelDefaultRows+1 {
			t.Fatalf("agents_form %q: the band takes %d rows, want %d", v, got, 1+1+panelDefaultRows+1)
		}
	}
}
