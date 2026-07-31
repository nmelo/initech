// Tests for the grid agents modal's search (/ keystroke). The grid DIMS
// non-matches in place rather than filtering rows out (ini-2rc, spec:
// "the spatial layout is the thing being navigated, so it must not reflow
// under the query") -- these tests were TestAgentsRefilter_* against the
// flat modal's filtered-list model; adapted to agentsMatched/matchCells/
// matchNav against the grid.
package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestAgentsMatched_EmptyQueryMatchesAll(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"), testPane("qa1"))
	tui.agents.searching = true
	tui.agents.searchBuf = nil

	for i := range tui.panes {
		if !tui.agentsMatched(i) {
			t.Errorf("pane %d should match an empty query", i)
		}
	}
}

func TestAgentsMatched_SubstringMatch(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("eng2"), testPane("qa1"), testPane("super"))
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("eng")

	want := map[int]bool{0: true, 1: true, 2: false, 3: false}
	for i, w := range want {
		if got := tui.agentsMatched(i); got != w {
			t.Errorf("pane %d (%s) matched=%v, want %v", i, tui.panes[i].Name(), got, w)
		}
	}
}

func TestAgentsMatched_CaseInsensitive(t *testing.T) {
	tui := newTestTUI(testPane("Eng1"), testPane("eng2"), testPane("QA1"))
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("ENG")

	if !tui.agentsMatched(0) || !tui.agentsMatched(1) {
		t.Error("ENG should match Eng1 and eng2 case-insensitively")
	}
	if tui.agentsMatched(2) {
		t.Error("ENG should not match QA1")
	}
}

func TestAgentsMatched_NumberPrefix(t *testing.T) {
	// Pane numbers are 1-based positions: eng1 is pane 1, the tenth pane is 10.
	tui := newTestTUI(testPane("eng1"), testPane("a"), testPane("b"), testPane("c"),
		testPane("d"), testPane("e"), testPane("f"), testPane("g"), testPane("h"),
		testPane("i"), testPane("j"), testPane("k"))
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("1")

	// "1" should match pane 1 (eng1) by number prefix, and also panes 10, 11
	// by prefix -- but not pane 2.
	if !tui.agentsMatched(0) { // pane number 1
		t.Error("query '1' should match pane number 1 by prefix")
	}
	if tui.agentsMatched(1) { // pane number 2
		t.Error("query '1' should not match pane number 2")
	}
	if !tui.agentsMatched(9) { // pane number 10
		t.Error("query '1' should match pane number 10 by prefix")
	}
}

func TestAgentsMatched_NoMatches(t *testing.T) {
	tui := newTestTUI(testPane("eng1"), testPane("qa1"))
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("xyz")

	for i := range tui.panes {
		if tui.agentsMatched(i) {
			t.Errorf("pane %d should not match 'xyz'", i)
		}
	}
}

func TestAgentsMatchCells_ReturnsGridOrderIndices(t *testing.T) {
	tui, s := newTestTUIWithScreen("super", "eng1", "eng2", "qa1")
	tui.openAgentsModal()
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("eng")
	sw, _ := s.Size()

	members := tui.agentsGroupMembers()
	perRow := agentsGridPerRow(members, tui.layoutState.Groups, sw)
	cells := agentsGridLayoutCells(members, tui.layoutState.Groups, 4, 0, perRow)

	mc := tui.agentsMatchCells(cells)
	if len(mc) != 2 {
		t.Fatalf("expected 2 matching cells for 'eng', got %d", len(mc))
	}
	for _, ci := range mc {
		name := tui.panes[cells[ci].paneIdx].Name()
		if name != "eng1" && name != "eng2" {
			t.Errorf("matched cell has unexpected pane %q", name)
		}
	}
}

func TestAgentsEnsureMatchSelected_SnapsToFirstMatch(t *testing.T) {
	tui, s := newTestTUIWithScreen("super", "eng1", "eng2", "qa1")
	tui.openAgentsModal()
	tui.agents.selected = 3 // qa1 -- about to stop matching
	sw, _ := s.Size()

	members := tui.agentsGroupMembers()
	perRow := agentsGridPerRow(members, tui.layoutState.Groups, sw)
	cells := agentsGridLayoutCells(members, tui.layoutState.Groups, 4, 0, perRow)

	tui.agents.searching = true
	tui.agents.searchBuf = []rune("eng")
	tui.agentsEnsureMatchSelected(cells)

	name := tui.panes[tui.agents.selected].Name()
	if name != "eng1" && name != "eng2" {
		t.Errorf("selection should snap to a match, got %q", name)
	}
}

func TestAgentsSearch_EnterKeepsSelectionAndExits(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "qa1", "super")
	tui.agents.active = true
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("eng")
	tui.agents.selected = 1 // eng2, a real match

	tui.handleAgentsSearchKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if tui.agents.searching {
		t.Error("searching should be false after Enter")
	}
	if tui.agents.selected != 1 {
		t.Errorf("selected = %d, want 1 (kept, not reset)", tui.agents.selected)
	}
}

func TestAgentsSearch_EscRestoresPreSearchSelection(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "qa1")
	tui.agents.active = true
	tui.agents.selected = 2 // qa1, before search starts

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
	if !tui.agents.searching {
		t.Fatal("expected searching to be true after /")
	}
	tui.agents.searchBuf = []rune("eng")
	tui.agents.selected = 0 // search moved selection to eng1

	tui.handleAgentsSearchKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))

	if tui.agents.searching {
		t.Error("searching should be false after Esc")
	}
	if tui.agents.searchBuf != nil {
		t.Error("searchBuf should be nil after Esc")
	}
	if tui.agents.selected != 2 {
		t.Errorf("selected = %d, want 2 (pre-search selection restored)", tui.agents.selected)
	}
}

func TestAgentsSearch_BackspaceRemovesRune(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "qa1")
	tui.agents.active = true
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("en")

	if !tui.agentsMatched(0) || tui.agentsMatched(1) {
		t.Fatal("pre-check: 'en' should match eng1 only")
	}

	tui.handleAgentsSearchKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))

	if string(tui.agents.searchBuf) != "e" {
		t.Errorf("searchBuf = %q, want %q", string(tui.agents.searchBuf), "e")
	}
	if !tui.agentsMatched(0) {
		t.Error("'e' should still match eng1")
	}
}

func TestAgentsSearch_BackspaceOnEmptyExitsAndRestoresSelection(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "qa1")
	tui.agents.active = true
	tui.agents.selected = 1

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
	tui.agents.selected = 0

	tui.handleAgentsSearchKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))

	if tui.agents.searching {
		t.Error("Backspace on an empty query should exit search")
	}
	if tui.agents.selected != 1 {
		t.Errorf("selected = %d, want 1 (restored, same as Esc)", tui.agents.selected)
	}
}

func TestAgentsSearch_ResetOnModalReopen(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "qa1")
	tui.agents.active = true
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("eng")

	tui.openAgentsModal()

	if tui.agents.searching {
		t.Error("searching should be false on reopen")
	}
	if tui.agents.searchBuf != nil {
		t.Error("searchBuf should be nil on reopen")
	}
}

// TestAgentsSearch_SpaceHidesMidSearch is a spec-explicit behavior: Space
// works mid-search (hides the selection) rather than typing a space into
// the query.
func TestAgentsSearch_SpaceHidesMidSearch(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.agents.active = true
	tui.agents.searching = true
	tui.agents.selected = 0

	tui.handleAgentsSearchKey(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone))

	if !tui.layoutState.Hidden["eng1"] {
		t.Error("Space mid-search should hide the selected agent")
	}
	if len(tui.agents.searchBuf) != 0 {
		t.Errorf("searchBuf should be untouched by Space, got %q", string(tui.agents.searchBuf))
	}
}

// TestAgentsSearch_PTypesNotPins is the spec's explicit contrast case for
// Space: "p does NOT [act] -- names contain the letter p, so typing must
// win."
func TestAgentsSearch_PTypesNotPins(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2")
	tui.layoutState.Mode = LayoutLive
	tui.agents.active = true
	tui.agents.searching = true

	tui.handleAgentsSearchKey(tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModNone))

	if string(tui.agents.searchBuf) != "p" {
		t.Errorf("searchBuf = %q, want %q -- 'p' must type into the query, not pin", string(tui.agents.searchBuf), "p")
	}
	if _, pinned := tui.layoutState.LivePinned["eng1"]; pinned {
		t.Error("'p' mid-search should not live-pin the selection")
	}
}

func TestAgentsSearch_SlashTypesIntoQueryMidSearch(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1")
	tui.agents.active = true

	tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
	if !tui.agents.searching {
		t.Fatal("expected searching to be true after /")
	}

	tui.handleAgentsSearchKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
	if string(tui.agents.searchBuf) != "/" {
		t.Errorf("expected '/' in searchBuf, got %q", string(tui.agents.searchBuf))
	}
}
