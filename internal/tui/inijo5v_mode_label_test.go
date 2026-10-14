package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// ini-jo5v: the status bar names the mode the operator chose, in the help
// overlay's words. Before the fix it switched on layoutState.Mode alone, so
// the Option+f focus split and the Option+Shift+F live focus split -- both
// Layout2Col -- read "Main".

func TestModeLabel_NamesEveryMode(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*TUI)
		want  string
		green bool
	}{
		{"focus", func(u *TUI) { u.layoutState.Mode = LayoutFocus }, "Focus", false},
		{"main", func(u *TUI) { u.layoutState.Mode = Layout2Col }, "Main", false},
		{"focus split", func(u *TUI) {
			u.layoutState.Mode = Layout2Col
			u.focusSplitPrev = &focusSplitSnapshot{mode: LayoutGrid}
		}, "Focus split", false},
		{"live focus", func(u *TUI) {
			u.layoutState.Mode = Layout2Col
			u.liveFocus = &liveFocusState{}
			u.layoutState.RightSetActive = true
			u.layoutState.RightSet = []string{"b", "c", "d"}
		}, "Live focus (3)", true},
		{"live auto", func(u *TUI) {
			u.layoutState.Mode = LayoutLive
			u.layoutState.LiveAuto = true
			u.layoutState.LiveSlots = []string{"a", "b"}
		}, "Live Auto (2)", true},
		{"live CxR", func(u *TUI) {
			u.layoutState.Mode = LayoutLive
			u.layoutState.GridCols, u.layoutState.GridRows = 2, 3
		}, "Live 2x3", true},
		{"grid CxR", func(u *TUI) {
			u.layoutState.Mode = LayoutGrid
			u.layoutState.GridCols, u.layoutState.GridRows = 3, 2
		}, "Grid 3x2", false},
		{"zoomed", func(u *TUI) {
			u.layoutState.Mode = LayoutGrid
			u.layoutState.GridCols, u.layoutState.GridRows = 2, 2
			u.layoutState.Zoomed = true
		}, "Grid 2x2 (zoomed)", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := newTestTUI(testPane("a"), testPane("b"))
			c.setup(u)
			got, green := u.modeLabel()
			if got != c.want || green != c.green {
				t.Fatalf("modeLabel() = %q green=%v, want %q green=%v", got, green, c.want, c.green)
			}
		})
	}
}

// AC 2 driven through the real keys, so the label tracks the state each key
// actually leaves behind rather than a hand-built one.
func TestModeLabel_FollowsTheKeys(t *testing.T) {
	u := liveFocusTUI(t) // grid 2x2, a focused; a, b, c working
	u.layoutPresets = defaultLayoutPresets()
	want := func(when, label string) {
		t.Helper()
		if got, _ := u.modeLabel(); got != label {
			t.Fatalf("%s: label = %q, want %q", when, got, label)
		}
	}
	want("start", "Grid 2x2")
	altShiftF(u)
	want("Option+Shift+F", "Live focus (2)")
	altShiftF(u)
	want("Option+Shift+F again", "Grid 2x2")
	u.handleKey(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt))
	want("Option+f", "Focus split")
	u.handleKey(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt))
	want("Option+f again", "Grid 2x2")
	u.handleKey(tcell.NewEventKey(tcell.KeyRune, '1', tcell.ModAlt))
	want("Option+1", "Focus")
	// Option+f inside the live focus split freezes it into a Focus split.
	u.handleKey(tcell.NewEventKey(tcell.KeyRune, '5', tcell.ModAlt)) // back to a grid
	altShiftF(u)
	u.handleKey(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt))
	want("Option+f inside the live focus split", "Focus split")
}

// The label reaches the drawn bar, not just the helper.
func TestModeLabel_IsDrawnInTheStatusBar(t *testing.T) {
	u, s := newTestTUIWithScreen("a", "b", "c")
	u.layoutState.Mode = Layout2Col
	u.focusSplitPrev = &focusSplitSnapshot{mode: LayoutGrid}
	u.applyLayout()
	u.renderHints()
	s.Show()
	rows := screenRows(t, s)
	last := rows[len(rows)-1]
	if !strings.Contains(last, "Focus split") {
		t.Fatalf("status bar = %q, want it to name Focus split", last)
	}
}
