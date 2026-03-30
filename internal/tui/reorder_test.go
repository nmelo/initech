package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestCmdOrder_PopulatesItems(t *testing.T) {
	tui := newTestTUI(
		testPane("super"),
		testPane("eng1"),
		testPane("qa1"),
	)
	tui.cmdOrder()

	if !tui.reorder.active {
		t.Fatal("reorder modal should be active")
	}
	if len(tui.reorder.items) != 3 {
		t.Fatalf("items = %d, want 3", len(tui.reorder.items))
	}
	if tui.reorder.items[0] != "super" || tui.reorder.items[1] != "eng1" || tui.reorder.items[2] != "qa1" {
		t.Errorf("items = %v, want [super eng1 qa1]", tui.reorder.items)
	}
}

func TestReorderKey_CursorMovement(t *testing.T) {
	tui := &TUI{
		reorder: reorderModal{
			active: true,
			items:  []string{"a", "b", "c"},
			cursor: 0,
		},
	}

	// j moves down.
	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyRune, 'j', 0))
	if tui.reorder.cursor != 1 {
		t.Errorf("cursor = %d, want 1 after j", tui.reorder.cursor)
	}

	// k moves up.
	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyRune, 'k', 0))
	if tui.reorder.cursor != 0 {
		t.Errorf("cursor = %d, want 0 after k", tui.reorder.cursor)
	}

	// k at top is no-op.
	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyRune, 'k', 0))
	if tui.reorder.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (at top)", tui.reorder.cursor)
	}

	// j to bottom, then j again is no-op.
	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyRune, 'j', 0))
	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyRune, 'j', 0))
	if tui.reorder.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (at bottom)", tui.reorder.cursor)
	}
	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyRune, 'j', 0))
	if tui.reorder.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (no-op at bottom)", tui.reorder.cursor)
	}
}

func TestReorderKey_PickAndMove(t *testing.T) {
	tui := &TUI{
		reorder: reorderModal{
			active: true,
			items:  []string{"a", "b", "c"},
			cursor: 0,
		},
	}

	// Pick up item at 0.
	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !tui.reorder.moving {
		t.Fatal("should be moving after Enter")
	}

	// j while moving swaps item down.
	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyRune, 'j', 0))
	if tui.reorder.items[0] != "b" || tui.reorder.items[1] != "a" {
		t.Errorf("items = %v, want [b a c]", tui.reorder.items)
	}
	if tui.reorder.cursor != 1 {
		t.Errorf("cursor = %d, want 1 after move down", tui.reorder.cursor)
	}

	// Drop.
	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if tui.reorder.moving {
		t.Error("should not be moving after second Enter")
	}
}

func TestReorderKey_Escape_Cancels(t *testing.T) {
	tui := &TUI{
		reorder: reorderModal{
			active: true,
			items:  []string{"a", "b"},
			cursor: 0,
		},
		panes: toPaneViews([]*Pane{{name: "a"}, {name: "b"}}),
	}

	// Move item.
	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyRune, 'j', 0))
	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	// items is now [b, a]. Escape should discard.
	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))

	if tui.reorder.active {
		t.Error("modal should be closed after Esc")
	}
	// Panes should be unchanged.
	if tui.panes[0].Name() != "a" || tui.panes[1].Name() != "b" {
		t.Errorf("panes = [%s %s], want [a b] (cancel should not apply)", tui.panes[0].Name(), tui.panes[1].Name())
	}
}

func TestReorderKey_Space_Confirms(t *testing.T) {
	a := testPane("a")
	b := testPane("b")
	tui := newTestTUI(a, b)
	tui.reorder = reorderModal{
		active: true,
		items:  []string{"b", "a"}, // Reversed order.
		cursor: 0,
	}

	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))

	if tui.reorder.active {
		t.Error("modal should be closed after Space")
	}
	if len(tui.layoutState.Order) != 2 || tui.layoutState.Order[0] != "b" || tui.layoutState.Order[1] != "a" {
		t.Errorf("Order = %v, want [b a]", tui.layoutState.Order)
	}
	if tui.panes[0].Name() != "b" || tui.panes[1].Name() != "a" {
		t.Errorf("panes = [%s %s], want [b a] after confirm", tui.panes[0].Name(), tui.panes[1].Name())
	}
}

func TestReorderKey_ArrowKeys(t *testing.T) {
	tui := &TUI{
		reorder: reorderModal{
			active: true,
			items:  []string{"a", "b", "c"},
			cursor: 1,
		},
	}

	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if tui.reorder.cursor != 2 {
		t.Errorf("cursor = %d, want 2 after Down", tui.reorder.cursor)
	}

	tui.handleReorderKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	if tui.reorder.cursor != 1 {
		t.Errorf("cursor = %d, want 1 after Up", tui.reorder.cursor)
	}
}

func TestRenderReorder_NoPanic(t *testing.T) {
	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(80, 24)
	tui := &TUI{
		screen: s,
		panes:  toPaneViews([]*Pane{{name: "eng1", visible: true}, {name: "qa1", visible: true}}),
		reorder: reorderModal{
			active: true,
			items:  []string{"eng1", "qa1"},
			cursor: 0,
		},
	}
	tui.renderReorder() // Must not panic.
}
