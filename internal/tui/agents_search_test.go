// Tests for the grid agents modal's search (/ keystroke). The grid DIMS
// non-matches in place rather than filtering rows out (ini-2rc, spec:
// "the spatial layout is the thing being navigated, so it must not reflow
// under the query") -- these tests were TestAgentsRefilter_* against the
// flat modal's filtered-list model; adapted to agentsMatched/matchCells/
// matchNav against the grid.
package tui

import (
	"strings"
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
	perRow := agentsGridColumnsPerRow(untieredTiers(tui.layoutState.Groups), sw)
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
	perRow := agentsGridColumnsPerRow(untieredTiers(tui.layoutState.Groups), sw)
	cells := agentsGridLayoutCells(members, tui.layoutState.Groups, 4, 0, perRow)

	tui.agents.searching = true
	tui.agents.searchBuf = []rune("eng")
	tui.agentsEnsureMatchSelected(cells)

	name := tui.panes[tui.agents.selected].Name()
	if name != "eng1" && name != "eng2" {
		t.Errorf("selection should snap to a match, got %q", name)
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

// ── ini-hxtg: the panel opens in the search box ────────────────────
//
// These replace four cells that pinned the removed model: Esc and an empty
// Backspace restoring the selection from before '/', Enter keeping the
// selection the search reached, and the panel reopening with search off.
// Since ini-hxtg (with the operator's amendment) the panel opens IN the box,
// Enter lands on the first match, Esc clears a term before it ever closes,
// and nothing jumps back to a pre-search selection.

func searchTUI(t *testing.T) *TUI {
	t.Helper()
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "qa1", "super")
	tui.openAgentsModal()
	return tui
}

func agentsKey(tui *TUI, k tcell.Key) { tui.handleAgentsKey(tcell.NewEventKey(k, 0, tcell.ModNone)) }
func agentsType(tui *TUI, s string) {
	for _, r := range s {
		tui.handleAgentsKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
}
func agentsSelName(tui *TUI) string { return tui.panes[tui.agents.selected].Name() }

// Item 1: the panel opens with the box focused and empty, so typing filters
// straight away; reopening resets a term left from last time.
func TestAgentsSearch_OpensInTheBoxAndTypingFilters(t *testing.T) {
	tui := searchTUI(t)
	if !tui.agents.searching || len(tui.agents.searchBuf) != 0 {
		t.Fatalf("opened with searching=%v buf=%q, want the empty box focused", tui.agents.searching, string(tui.agents.searchBuf))
	}
	agentsType(tui, "qa")
	if string(tui.agents.searchBuf) != "qa" || agentsSelName(tui) != "qa1" {
		t.Errorf("typing gave buf=%q selected=%s, want qa / qa1", string(tui.agents.searchBuf), agentsSelName(tui))
	}
	tui.closeAgentsModal()
	tui.agents.searchBuf = []rune("stale")
	tui.openAgentsModal()
	if !tui.agents.searching || len(tui.agents.searchBuf) != 0 {
		t.Errorf("reopen: searching=%v buf=%q, want a fresh empty box", tui.agents.searching, string(tui.agents.searchBuf))
	}
}

// Item 3: Down, Left and Right each leave the box for the list and KEEP the
// filter; the selection is on a match.
func TestAgentsSearch_DownLeftRightEnterTheListKeepingTheFilter(t *testing.T) {
	for _, k := range []tcell.Key{tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight} {
		tui := searchTUI(t)
		agentsType(tui, "eng")
		agentsKey(tui, k)
		if tui.agents.searching {
			t.Errorf("%v did not leave the box", k)
		}
		if string(tui.agents.searchBuf) != "eng" {
			t.Errorf("%v dropped the filter: buf=%q", k, string(tui.agents.searchBuf))
		}
		if !strings.HasPrefix(agentsSelName(tui), "eng") {
			t.Errorf("%v landed on %s, want an eng match", k, agentsSelName(tui))
		}
	}
}

// Item 3: Up from the box does nothing; with an empty box, Down enters the
// list as today with nothing filtered.
func TestAgentsSearch_UpIsInertAndEmptyDownIsTheUnfilteredList(t *testing.T) {
	tui := searchTUI(t)
	agentsKey(tui, tcell.KeyUp)
	if !tui.agents.searching {
		t.Error("Up left the box; there is nothing above it")
	}
	agentsKey(tui, tcell.KeyDown)
	if tui.agents.searching || tui.agentsFilterActive() {
		t.Errorf("empty-box Down: searching=%v filter=%v, want the unfiltered list", tui.agents.searching, tui.agentsFilterActive())
	}
}

// Edge case: a term with zero matches has nothing to land on, so arrows from
// the box stay in the box.
func TestAgentsSearch_ZeroMatchesKeepsArrowsInTheBox(t *testing.T) {
	tui := searchTUI(t)
	agentsType(tui, "zzz")
	agentsKey(tui, tcell.KeyDown)
	if !tui.agents.searching {
		t.Error("Down with zero matches left the box")
	}
}

// Amendment: Enter in the box leaves for the list on the FIRST match in
// reading order — the first agent overall with no term — and the panel
// stays open.
func TestAgentsSearch_EnterLandsOnTheFirstMatchAndStaysOpen(t *testing.T) {
	tui := searchTUI(t)
	agentsType(tui, "eng")
	tui.agentsMatchNav(tui.agentsCurrentCells(), 1) // move OFF the first match first
	agentsKey(tui, tcell.KeyEnter)
	cells := tui.agentsCurrentCells()
	first := cells[tui.agentsMatchCells(cells)[0]].paneIdx
	if tui.agents.selected != first {
		t.Errorf("Enter selected %s, want the first match %s", agentsSelName(tui), tui.panes[first].Name())
	}
	if tui.agents.searching || !tui.agents.active {
		t.Errorf("after Enter searching=%v active=%v, want the open list", tui.agents.searching, tui.agents.active)
	}

	tui = searchTUI(t)
	agentsKey(tui, tcell.KeyEnter)
	if cells := tui.agentsCurrentCells(); tui.agents.selected != cells[0].paneIdx || !tui.agents.active {
		t.Errorf("empty Enter selected %s active=%v, want the first agent, panel open", agentsSelName(tui), tui.agents.active)
	}
}

// Amendment, in the box: Esc with a term clears it and stays; only Esc with
// no term closes the panel.
func TestAgentsSearch_EscInTheBoxClearsThenCloses(t *testing.T) {
	tui := searchTUI(t)
	agentsType(tui, "eng")
	agentsKey(tui, tcell.KeyEscape)
	if !tui.agents.active || !tui.agents.searching || tui.agentsFilterActive() {
		t.Fatalf("first Esc: active=%v searching=%v filter=%v, want the open, empty box", tui.agents.active, tui.agents.searching, tui.agentsFilterActive())
	}
	agentsKey(tui, tcell.KeyEscape)
	if tui.agents.active {
		t.Error("Esc on an empty box did not close the panel")
	}
}

// Amendment, in the list: Esc with a filter clears it and stays in the list;
// Esc without one closes.
func TestAgentsSearch_EscInTheListClearsThenCloses(t *testing.T) {
	tui := searchTUI(t)
	agentsType(tui, "eng")
	agentsKey(tui, tcell.KeyDown)
	agentsKey(tui, tcell.KeyEscape)
	if !tui.agents.active || tui.agents.searching || tui.agentsFilterActive() {
		t.Fatalf("first Esc in list: active=%v searching=%v filter=%v, want the open, unfiltered list", tui.agents.active, tui.agents.searching, tui.agentsFilterActive())
	}
	agentsKey(tui, tcell.KeyEscape)
	if tui.agents.active {
		t.Error("Esc in an unfiltered list did not close the panel")
	}
}

// Backspace on an empty box is inert: it no longer leaves search (Down and
// Enter do that) and must not close or move anything.
func TestAgentsSearch_BackspaceOnAnEmptyBoxIsInert(t *testing.T) {
	tui := searchTUI(t)
	before := tui.agents.selected
	tui.handleAgentsSearchKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
	if !tui.agents.searching || !tui.agents.active || tui.agents.selected != before {
		t.Errorf("empty Backspace changed state: searching=%v active=%v selected %d->%d", tui.agents.searching, tui.agents.active, before, tui.agents.selected)
	}
}

// Item 3: in the list with a filter, arrows step ONLY through matches, in all
// four directions.
func TestAgentsSearch_ListArrowsStopOnlyOnMatches(t *testing.T) {
	tui := searchTUI(t)
	agentsType(tui, "eng")
	agentsKey(tui, tcell.KeyDown)
	for _, k := range []tcell.Key{tcell.KeyRight, tcell.KeyDown, tcell.KeyLeft, tcell.KeyUp, tcell.KeyRight, tcell.KeyRight} {
		agentsKey(tui, k)
		if !strings.HasPrefix(agentsSelName(tui), "eng") {
			t.Fatalf("after %v the selection is %s, a non-match", k, agentsSelName(tui))
		}
	}
	// And it does move between the two matches.
	agentsKey(tui, tcell.KeyLeft)
	a := agentsSelName(tui)
	agentsKey(tui, tcell.KeyRight)
	if agentsSelName(tui) == a {
		t.Errorf("Right did not move between eng matches (stuck on %s)", a)
	}
}

// Item 4: list commands act on the selected match — p pins it.
func TestAgentsSearch_PPinsTheSelectedMatch(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "qa1", "super")
	// p is a live-mode pin; the same setup TestAgentsModal_LivePinToggle uses.
	tui.layoutState.Mode = LayoutLive
	tui.layoutState.LivePinned = make(map[string]int)
	tui.layoutState.LiveSlots = []string{"eng1", "eng2", "qa1", "super"}
	tui.openAgentsModal()
	agentsType(tui, "qa")
	agentsKey(tui, tcell.KeyDown)
	if agentsSelName(tui) != "qa1" {
		t.Fatalf("fixture: selected %s, want qa1", agentsSelName(tui))
	}
	agentsType(tui, "p")
	if _, ok := tui.layoutState.LivePinned["qa1"]; !ok {
		t.Error("p in the filtered list did not pin the selected match qa1")
	}
}

// Item 4: '/' from the list returns to the box with the text kept.
func TestAgentsSearch_SlashReturnsToTheBoxKeepingTheText(t *testing.T) {
	tui := searchTUI(t)
	agentsType(tui, "eng")
	agentsKey(tui, tcell.KeyDown)
	agentsType(tui, "/")
	if !tui.agents.searching || string(tui.agents.searchBuf) != "eng" {
		t.Errorf("/ gave searching=%v buf=%q, want the box with eng kept", tui.agents.searching, string(tui.agents.searchBuf))
	}
}

// Edge case: a grab's DESTINATION ignores the filter — a grabbed agent moves
// spatially into a group that has no matching agent.
func TestAgentsSearch_GrabMovesSpatiallyIgnoringTheFilter(t *testing.T) {
	tui := searchTUI(t)
	agentsType(tui, "eng")
	agentsKey(tui, tcell.KeyDown)
	grabbed := agentsSelName(tui)
	beforeGroup := tui.layoutState.GroupOf[grabbed]
	agentsKey(tui, tcell.KeyEnter) // grab
	agentsKey(tui, tcell.KeyRight)
	agentsKey(tui, tcell.KeyLeft)
	agentsKey(tui, tcell.KeyRight)
	if tui.layoutState.GroupOf[grabbed] == beforeGroup {
		t.Errorf("grabbed %s stayed in %q: arrows followed the filter instead of the groups", grabbed, beforeGroup)
	}
}

// Edge case: an agent whose only match is hidden is still selectable.
func TestAgentsSearch_HiddenOnlyMatchIsSelectable(t *testing.T) {
	tui := searchTUI(t)
	if tui.layoutState.Hidden == nil {
		tui.layoutState.Hidden = map[string]bool{}
	}
	tui.layoutState.Hidden["qa1"] = true
	agentsType(tui, "qa")
	agentsKey(tui, tcell.KeyDown)
	if tui.agents.searching || agentsSelName(tui) != "qa1" {
		t.Errorf("hidden-only match: searching=%v selected=%s, want the list on qa1", tui.agents.searching, agentsSelName(tui))
	}
}

// The filter is drawn while it is set, in the box and in the list, and the
// box footer names the way out.
func TestAgentsSearch_FilterStaysVisibleInTheListAndTheBoxFooterNamesDown(t *testing.T) {
	tui, _ := newTestTUIWithScreen("eng1", "eng2", "qa1", "super")
	tui.openAgentsModal()
	tui.renderAgentsGrid()
	if got := screenText(t, tui); !strings.Contains(got, "Down list") {
		t.Errorf("box footer does not name Down:\n%s", got)
	}
	agentsType(tui, "eng")
	agentsKey(tui, tcell.KeyDown)
	tui.renderAgentsGrid()
	if got := screenText(t, tui); !strings.Contains(got, "/ eng") || strings.Contains(got, "/ eng_") {
		t.Errorf("list should show the filter without the typing cursor:\n%s", got)
	}
}
