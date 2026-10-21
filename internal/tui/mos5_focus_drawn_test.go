package tui

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// ini-mos5: the pane that receives keys is the pane drawn as focused, in
// every mode, at every frame. Three paths broke it: live eviction (focus
// validated before live re-picked the drawn set), an explicit grid smaller
// than the fleet (focus validated on hidden-ness only), and the mouse wheel
// (focus set without a new plan).

func mos5TUI(names ...string) *TUI {
	panes := make([]*Pane, len(names))
	for i, n := range names {
		panes[i] = testPane(n)
	}
	return newTestTUI(panes...)
}

// mos5Highlighted is every pane the current plan draws as focused.
func mos5Highlighted(tui *TUI) []string {
	var out []string
	for _, pr := range tui.plan.Panes {
		if pr.Focused {
			out = append(out, agentKey(pr.Pane))
		}
	}
	return out
}

func mos5Drawn(tui *TUI) map[string]bool {
	out := map[string]bool{}
	for _, pr := range tui.plan.Panes {
		out[agentKey(pr.Pane)] = true
	}
	return out
}

// mos5Agree fails unless exactly one pane is drawn as focused and it is the
// pane focusedPane() routes keys to. With nothing drawn, nothing may be
// highlighted.
func mos5Agree(t *testing.T, tui *TUI, what string) {
	t.Helper()
	hl := mos5Highlighted(tui)
	if len(tui.plan.Panes) == 0 {
		if len(hl) != 0 {
			t.Fatalf("%s: nothing drawn but %v highlighted", what, hl)
		}
		return
	}
	fp := tui.focusedPane()
	if len(hl) != 1 || fp == nil || agentKey(fp) != hl[0] {
		got := "<nil>"
		if fp != nil {
			got = agentKey(fp)
		}
		t.Fatalf("%s: highlighted %v, keys go to %s; want one pane, the same one", what, hl, got)
	}
}

// Criterion 1: live mode, the focused agent evicted from the slots.
func TestMos5_LiveEvictionMovesTheFocusToADrawnPane(t *testing.T) {
	tui := mos5TUI("a", "b", "c")
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 1
	tui.layoutState.LiveSlots = []string{"a", "b"}
	tui.layoutState.Focused = "c"
	tui.applyLayout()
	mos5Agree(t, tui, "live, focused c evicted")
}

// Criterion 2: six visible panes on an explicit 2x1 grid, focus on e.
func TestMos5_AGridSmallerThanTheFleetFocusesADrawnPane(t *testing.T) {
	tui := mos5TUI("a", "b", "c", "d", "e", "f")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 1
	tui.layoutState.GridExplicit = true
	tui.layoutState.Focused = "e"
	tui.applyLayout()
	mos5Agree(t, tui, "grid 2x1, focused e")
	if !mos5Drawn(tui)[tui.layoutState.Focused] {
		t.Fatalf("focus %q is not drawn (drawn %v)", tui.layoutState.Focused, mos5Drawn(tui))
	}
}

// Criterion 2: Option+Right on a 2x1 grid of six steps through the drawn
// panes only.
func TestMos5_OptionArrowsStepOnlyThroughDrawnPanes(t *testing.T) {
	tui := mos5TUI("a", "b", "c", "d", "e", "f")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 1
	tui.layoutState.GridExplicit = true
	tui.layoutState.Focused = "a"
	tui.applyLayout()
	for i := 0; i < 4; i++ {
		tui.handleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt))
		if !mos5Drawn(tui)[tui.layoutState.Focused] {
			t.Fatalf("step %d landed on %q, which is not drawn (drawn %v)", i+1, tui.layoutState.Focused, mos5Drawn(tui))
		}
		mos5Agree(t, tui, "after Option+Right")
	}
	// Leftward from the first drawn pane wraps to the last DRAWN one. Walking
	// every non-hidden pane would step onto undrawn f, which the layout snaps
	// back to a: the focus would be stuck on a.
	tui.layoutState.Focused = "a"
	tui.applyLayout()
	tui.handleKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModAlt))
	if tui.layoutState.Focused != "b" {
		t.Fatalf("Option+Left from a landed on %q, want b, the last drawn pane", tui.layoutState.Focused)
	}
}

// Criterion 3: a wheel notch over the unfocused pane focuses it and the next
// frame highlights it.
func TestMos5_AWheelNotchRePlansLikeAClick(t *testing.T) {
	tui := mos5TUI("a", "b")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 1
	tui.layoutState.Focused = "a"
	tui.applyLayout()
	var bRegion Region
	for _, pr := range tui.plan.Panes {
		if pr.Pane.Name() == "b" {
			bRegion = pr.Region
		}
	}
	tui.handleMouse(tcell.NewEventMouse(bRegion.X+2, bRegion.Y+2, tcell.WheelUp, tcell.ModNone))
	if hl := mos5Highlighted(tui); len(hl) != 1 || hl[0] != "b" {
		t.Fatalf("after a wheel notch over b the frame highlights %v, want b", hl)
	}
	mos5Agree(t, tui, "after the wheel")
}

// Criterion 4: the invariant across modes, after a fixed-seed random walk of
// hide, show, presets, grids, split entries and exits, live ticks, wheel
// notches and Option+arrows. Checked after every step.
func TestMos5_TheInvariantHoldsAcrossModes(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f", "g"}
	tui := mos5TUI(names...)
	tui.layoutState.Groups = []string{"core"}
	tui.layoutPresets, _ = ResolvePresets(nil)
	tui.applyLayout()
	rng := rand.New(rand.NewSource(20261021))
	setActivity(tui, "c", StateIdle)
	setActivity(tui, "f", StateIdle)
	ops := []struct {
		name string
		do   func()
	}{
		{"hide", func() {
			k := names[rng.Intn(len(names))]
			if len(mos5Drawn(tui)) > 1 {
				if tui.layoutState.Hidden == nil {
					tui.layoutState.Hidden = map[string]bool{}
				}
				tui.layoutState.Hidden[k] = true
				tui.applyLayout()
			}
		}},
		{"show", func() {
			delete(tui.layoutState.Hidden, names[rng.Intn(len(names))])
			tui.applyLayout()
		}},
		{"preset", func() { tui.handleKey(tcell.NewEventKey(tcell.KeyRune, rune('1'+rng.Intn(7)), tcell.ModAlt)) }},
		{"grid", func() { tui.execCmd([]string{"grid 2x1", "grid 3x1", "grid 2x2", "grid"}[rng.Intn(4)]) }},
		{"main", func() { tui.execCmd("main") }},
		{"focus", func() { tui.execCmd("focus") }},
		{"live", func() { tui.execCmd("live 2x1") }},
		{"Option+f", func() { altF(tui) }},
		{"Option+Shift+F", func() { altShiftF(tui) }},
		{"Option+Shift+L", func() { altShiftL(tui) }},
		{"tick", func() { tui.applyLayout() }},
		{"activity", func() {
			setActivity(tui, names[rng.Intn(len(names))], []ActivityState{StateRunning, StateIdle}[rng.Intn(2)])
		}},
		{"wheel", func() {
			if len(tui.plan.Panes) == 0 {
				return
			}
			r := tui.plan.Panes[rng.Intn(len(tui.plan.Panes))].Region
			tui.handleMouse(tcell.NewEventMouse(r.X+1, r.Y+1, tcell.WheelDown, tcell.ModNone))
		}},
		{"Option+Right", func() { tui.handleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt)) }},
		{"Option+Left", func() { tui.handleKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModAlt)) }},
	}
	for step := 0; step < 600; step++ {
		op := ops[rng.Intn(len(ops))]
		op.do()
		// "activity" changes no layout until the next tick; every other op is
		// a frame the operator types into.
		if op.name != "activity" {
			mos5Agree(t, tui, fmt.Sprintf("step %d, after %s", step, op.name))
		}
	}
}

// Feed it nothing: zero panes, one pane, and a 1x1 grid whose panes are all
// hidden. No panic, and with nothing drawn no key reaches a pane.
func TestMos5_NothingToFocus(t *testing.T) {
	empty := mos5TUI()
	empty.applyLayout()
	empty.handleKey(tcell.NewEventKey(tcell.KeyRune, 'x', 0))
	mos5Agree(t, empty, "zero panes")

	one := mos5TUI("solo")
	one.applyLayout()
	mos5Agree(t, one, "one pane")

	hidden := mos5TUI("a", "b")
	hidden.layoutState.Mode = LayoutGrid
	hidden.layoutState.GridCols, hidden.layoutState.GridRows = 1, 1
	hidden.layoutState.Hidden = map[string]bool{"a": true, "b": true}
	hidden.layoutState.Focused = "a"
	hidden.applyLayout()
	mos5Agree(t, hidden, "all hidden")
	if p := hidden.inputPane(); p != nil {
		t.Fatalf("nothing drawn, yet input would go to %q", p.Name())
	}
}

// The delivery side: keys go to the pane the last frame drew as focused even
// if something changed layoutState.Focused without re-planning.
func TestMos5_KeysGoToTheDrawnFocusEvenIfStateMovedWithoutAFrame(t *testing.T) {
	tui := mos5TUI("a", "b")
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 1
	tui.layoutState.Focused = "a"
	tui.applyLayout()
	tui.layoutState.Focused = "b" // a writer that forgot to re-plan
	if p := tui.inputPane(); p == nil || p.Name() != "a" {
		t.Fatalf("input goes to %v, want a: the pane drawn as focused", p)
	}
}

// mos5InputRec is a pane that records the keys and pastes it is given.
type mos5InputRec struct {
	*Pane
	keys   []rune
	pastes []string
}

func (r *mos5InputRec) SendKey(ev *tcell.EventKey) { r.keys = append(r.keys, ev.Rune()) }
func (r *mos5InputRec) FlushPaste(b []byte)        { r.pastes = append(r.pastes, string(b)) }

// The delivery side, end to end through handleKey and the paste path: with
// the state moved to b by a writer that did not re-plan, a typed key and a
// paste both land in a, the pane drawn as focused, and b gets nothing.
func TestMos5_TypedKeysAndPastesLandInTheDrawnPane(t *testing.T) {
	a := &mos5InputRec{Pane: testPane("a")}
	b := &mos5InputRec{Pane: testPane("b")}
	tui := &TUI{panes: []PaneView{a, b}, quitCh: make(chan struct{})}
	tui.layoutState = DefaultLayoutState([]string{"a", "b"})
	tui.layoutState.Mode = LayoutGrid
	tui.layoutState.GridCols, tui.layoutState.GridRows = 2, 1
	tui.layoutState.Focused = "a"
	tui.applyLayout()
	tui.layoutState.Focused = "b" // a writer that forgot to re-plan

	tui.handleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	tui.handlePaste(true)
	tui.pasteBuf = append(tui.pasteBuf, "pasted"...)
	tui.handlePaste(false)

	if string(a.keys) != "x" || len(a.pastes) != 1 || a.pastes[0] != "pasted" {
		t.Errorf("a (drawn as focused) got keys %q pastes %q, want x and one paste", string(a.keys), a.pastes)
	}
	if len(b.keys) != 0 || len(b.pastes) != 0 {
		t.Errorf("b (not drawn as focused) got keys %q pastes %q, want nothing", string(b.keys), b.pastes)
	}
}
