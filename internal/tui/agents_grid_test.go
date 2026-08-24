// agents_grid_test.go tests the grid layout, navigation, group-seeding, and
// persistence primitives behind the grouped 2-D agents modal (ini-2rc).
// agents_test.go / agents_search_test.go cover the modal's key-handling
// integration; this file covers the underlying math and data model the
// spec's own AC list names explicitly: band wrap/width at 1..23 agents,
// nearest-column vertical nav across wrapped bands, cross-band grab splice,
// group seed rule, persistence round-trip, and the search-bar no-matches
// regression the spec calls out by name as the PoC's own near-miss.
package tui

import (
	"fmt"
	"testing"
)

// ---------- band wrap / width math, 1..23 agents ----------

// TestAgentsGridPerRow_CapsAndWraps_1To23Agents is the spec's explicit AC:
// "band wrap/width math at 1..23 agents". A single band of n agents should
// use perRow = min(n, gridMaxPerRow), and agentsGridLayoutCells should
// produce ceil(n/perRow) rows with every cell inside [0, perRow) columns.
func TestAgentsGridPerRow_CapsAndWraps_1To23Agents(t *testing.T) {
	for n := 1; n <= 23; n++ {
		n := n
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			members := map[string][]int{"band": make([]int, n)}
			for i := range members["band"] {
				members["band"][i] = i
			}
			groups := []string{"band"}
			wantPerRow := n
			if wantPerRow > gridMaxPerRow {
				wantPerRow = gridMaxPerRow
			}
			perRow := agentsGridPerRow(members, groups, 1000) // wide terminal, no width shrink
			if perRow != wantPerRow {
				t.Fatalf("perRow = %d, want %d", perRow, wantPerRow)
			}

			cells := agentsGridLayoutCells(members, groups, 0, 0, perRow)
			if len(cells) != n {
				t.Fatalf("got %d cells, want %d", len(cells), n)
			}
			wantRows := (n + perRow - 1) / perRow
			maxLine := -1
			for _, c := range cells {
				col := (c.x - 0) / gridCellW
				if col < 0 || col >= perRow {
					t.Errorf("cell x=%d implies column %d, out of [0,%d)", c.x, col, perRow)
				}
				if c.line > maxLine {
					maxLine = c.line
				}
			}
			if maxLine+1 != wantRows {
				t.Errorf("max line index %d implies %d rows, want %d", maxLine, maxLine+1, wantRows)
			}
		})
	}
}

// TestAgentsGridPerRow_ShrinksForNarrowTerminal: content-sizing caps at 6,
// but a genuinely narrow terminal shrinks further (spec: fallback only,
// content decides first).
func TestAgentsGridPerRow_ShrinksForNarrowTerminal(t *testing.T) {
	members := map[string][]int{"band": {0, 1, 2, 3, 4, 5}}
	groups := []string{"band"}

	wide := agentsGridPerRow(members, groups, 1000)
	if wide != 6 {
		t.Fatalf("wide terminal: perRow = %d, want 6", wide)
	}
	narrow := agentsGridPerRow(members, groups, 40) // fits ~2 cells
	if narrow >= wide {
		t.Errorf("narrow terminal should shrink perRow below the content cap: got %d, wide was %d", narrow, wide)
	}
	if narrow < 1 {
		t.Errorf("perRow should never go below 1, got %d", narrow)
	}
}

// ---------- nearest-column vertical nav across wrapped bands ----------

// TestAgentsMoveV_NearestColumnAcrossWrappedBand: an 8-agent band wraps to
// two rows (6 + 2). Moving down from column 5 of row 1 must land on the
// NEAREST available column in row 2 (which only has columns 0-1), not fail
// or wrap around.
func TestAgentsMoveV_NearestColumnAcrossWrappedBand(t *testing.T) {
	names := make([]string, 8)
	for i := range names {
		names[i] = fmt.Sprintf("eng%d", i+1) // all "eng" family -> one band, wraps at 6
	}
	tui, _ := newTestTUIWithScreen(names...)
	tui.openAgentsModal()

	tui.agents.selected = 5 // eng6: row 0, column 5 (last column of the first row)
	cells := tui.agentsCurrentCells()
	sel := agentsCellForPane(cells, tui.agents.selected)
	if sel == nil || sel.line != 0 {
		t.Fatalf("precondition: eng6 should be on line 0, got %+v", sel)
	}

	tui.agentsMoveV(cells, 1)

	got := agentsCellForPane(tui.agentsCurrentCells(), tui.agents.selected)
	if got == nil || got.line != 1 {
		t.Fatalf("selection should move to line 1 (the wrapped row), got %+v", got)
	}
	name := tui.panes[tui.agents.selected].Name()
	if name != "eng7" && name != "eng8" {
		t.Errorf("selection should land on the nearest column of the wrapped row (eng7 or eng8), got %q", name)
	}
}

// TestAgentsMoveV_AcrossBands_NearestColumn: core (3 members) sits above eng
// (6 members, single row). Moving down from core's last (rightmost) member
// should land on eng's nearest column by x-distance, not always column 0.
func TestAgentsMoveV_AcrossBands_NearestColumn(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "pm", "shipper", "eng1", "eng2", "eng3", "eng4", "eng5", "eng6")
	tui.openAgentsModal()

	// Select the rightmost core member (3rd cell in that band).
	tui.agents.selected = 2 // shipper, 3rd position in core
	cells := tui.agentsCurrentCells()
	cur := agentsCellForPane(cells, tui.agents.selected)
	if cur == nil {
		t.Fatal("could not locate shipper's cell")
	}

	tui.agentsMoveV(cells, 1)

	got := agentsCellForPane(tui.agentsCurrentCells(), tui.agents.selected)
	if got == nil {
		t.Fatal("selection lost after moving into eng band")
	}
	if got.group != "eng" {
		t.Errorf("selection should have moved into the eng band, landed in %q", got.group)
	}
	// Nearest-column: shipper was the 3rd (index 2) cell in core; eng's 3rd
	// cell (eng3) should be closer in x than eng's 1st (eng1) or 6th (eng6).
	if name := tui.panes[tui.agents.selected].Name(); name != "eng3" {
		t.Errorf("nearest-column landing = %q, want eng3 (3rd column, matching shipper's column)", name)
	}
}

// ---------- cross-band grab splice ----------

// TestAgentsMoveV_GrabSpliceIntoWrappedBand: grabbing an agent and carrying
// it down into a band that itself wraps must splice it into the correct
// row/position, not just append it.
func TestAgentsMoveV_GrabSpliceIntoWrappedBand(t *testing.T) {
	names := []string{"super"}
	for i := 1; i <= 8; i++ {
		names = append(names, fmt.Sprintf("eng%d", i)) // wraps at 6
	}
	tui, _ := newTestTUIWithScreen(names...)
	tui.openAgentsModal()
	if got := tui.layoutState.GroupOf["super"]; got != "core" {
		t.Fatalf("precondition: super in core, got %q", got)
	}

	tui.agents.moving = true
	cells := tui.agentsCurrentCells()
	tui.agentsMoveV(cells, 1) // carry super down into eng's first row

	if got := tui.layoutState.GroupOf["super"]; got != "eng" {
		t.Errorf("super's group after grab-splice = %q, want eng", got)
	}
	// super must appear exactly once in t.panes, not duplicated or dropped.
	count := 0
	for _, p := range tui.panes {
		if p.Name() == "super" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("super appears %d times in t.panes after splice, want exactly 1", count)
	}
	if len(tui.panes) != len(names) {
		t.Errorf("pane count = %d after splice, want %d (no panes lost or duplicated)", len(tui.panes), len(names))
	}
}

// TestAgentsMoveV_GrabIntoFreshlyCreatedEmptyGroup is the qa1 regression:
// grabbing an agent and carrying it onto a just-created (empty) group's
// reserved line must populate that group, not silently no-op. This is the
// spec's own stated mechanism for reaching a custom grouping ("g" then
// grab) -- the PoC never implemented group creation at all, so this
// interaction was never prototyped before this bead, and this test is the
// only thing establishing it works.
func TestAgentsMoveV_GrabIntoFreshlyCreatedEmptyGroup(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "pmm")
	tui.openAgentsModal()
	if got := tui.layoutState.GroupOf["pmm"]; got != "core" {
		t.Fatalf("precondition: pmm should seed to core, got %q", got)
	}

	// Select pmm, create "mkt" right after core (pmm's own band).
	for i, p := range tui.panes {
		if p.Name() == "pmm" {
			tui.agents.selected = i
		}
	}
	tui.agentsCreateGroup("mkt")
	want := []string{"core", "mkt"}
	if got := tui.layoutState.Groups; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("precondition: groups = %v, want %v", got, want)
	}

	// Grab pmm and carry it down onto mkt's (empty) line.
	tui.agents.moving = true
	cells := tui.agentsCurrentCells()
	tui.agentsMoveV(cells, 1)

	if got := tui.layoutState.GroupOf["pmm"]; got != "mkt" {
		t.Fatalf("pmm's group after grab into the empty band = %q, want mkt -- the grab silently no-op'd", got)
	}
	if !tui.agents.moving {
		t.Error("moving should still be true after a successful splice (drop is a separate Enter press)")
	}
	// pmm must appear exactly once, nothing lost or duplicated.
	count := 0
	for _, p := range tui.panes {
		if p.Name() == "pmm" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("pmm appears %d times in t.panes after the splice, want exactly 1", count)
	}
	if len(tui.panes) != 2 {
		t.Errorf("pane count = %d after splice, want 2", len(tui.panes))
	}
	// Confirm via the actual members query too, not just GroupOf.
	members := tui.agentsGroupMembers()
	if len(members["mkt"]) != 1 || tui.panes[members["mkt"][0]].Name() != "pmm" {
		t.Errorf("mkt's members = %v, want exactly [pmm]", members["mkt"])
	}
}

// TestAgentsMoveV_PlainNavOntoEmptyBandStaysPut is the other half of qa1's
// ask: non-grab arrow navigation onto an empty band's line has no agent to
// select there, so the selection must stay put rather than crash, jump
// somewhere arbitrary, or silently select the wrong pane.
func TestAgentsMoveV_PlainNavOntoEmptyBandStaysPut(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "pmm")
	tui.openAgentsModal()
	for i, p := range tui.panes {
		if p.Name() == "pmm" {
			tui.agents.selected = i
		}
	}
	tui.agentsCreateGroup("mkt")

	before := tui.agents.selected
	tui.agents.moving = false // plain navigation, not grabbing
	cells := tui.agentsCurrentCells()
	tui.agentsMoveV(cells, 1) // toward mkt's empty line

	if tui.agents.selected != before {
		t.Errorf("plain nav onto an empty band's line changed selection from %d to %d, want unchanged", before, tui.agents.selected)
	}
	// The group must still be empty -- plain nav must never mutate GroupOf.
	if got := tui.layoutState.GroupOf["pmm"]; got != "core" {
		t.Errorf("plain nav mutated pmm's group to %q, want unchanged (core)", got)
	}
}

// ---------- group seed rule ----------

func TestGroupFor_SeedRule(t *testing.T) {
	cases := map[string]string{
		"eng1": "eng", "eng6": "eng", "eng42": "eng",
		"qa1": "qa", "qa12": "qa", "qa99": "qa",
		"super": "core", "pm": "core", "shipper": "core",
		"pmm": "core", "growth": "core", "intern": "core",
		"totally-custom-role": "core",
	}
	for name, want := range cases {
		if got := groupFor(name); got != want {
			t.Errorf("groupFor(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestEnsureGroups_SeedsOnceThenPreservesManualEdits: ensureGroups must not
// overwrite a pane's group once it has one -- otherwise a manual grab (e.g.
// moving pmm into a hand-created "mkt" band) would be silently undone the
// next time the modal opens.
func TestEnsureGroups_SeedsOnceThenPreservesManualEdits(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "pmm", "eng1")
	tui.ensureGroups(false)
	if got := tui.layoutState.GroupOf["pmm"]; got != "core" {
		t.Fatalf("precondition: pmm should seed to core, got %q", got)
	}

	// Manual edit: move pmm into a hand-created "mkt" band.
	tui.layoutState.Groups = append(tui.layoutState.Groups, "mkt")
	tui.layoutState.GroupOf["pmm"] = "mkt"

	// Re-running ensureGroups (as every modal open does) must not revert it.
	tui.ensureGroups(false)
	if got := tui.layoutState.GroupOf["pmm"]; got != "mkt" {
		t.Errorf("ensureGroups overwrote a manual edit: pmm = %q, want mkt", got)
	}
}

// TestEnsureGroups_BandOrderIsFirstSeenOverPanes: band order should be
// deterministic (first-seen over t.panes), not Go's unspecified map order.
func TestEnsureGroups_BandOrderIsFirstSeenOverPanes(t *testing.T) {
	tui, _ := newTestTUIWithScreen("super", "eng1", "qa1", "eng2")
	tui.ensureGroups(false)

	want := []string{"core", "eng", "qa"}
	if len(tui.layoutState.Groups) != len(want) {
		t.Fatalf("groups = %v, want %v", tui.layoutState.Groups, want)
	}
	for i, g := range want {
		if tui.layoutState.Groups[i] != g {
			t.Errorf("groups[%d] = %q, want %q (full: %v)", i, tui.layoutState.Groups[i], g, tui.layoutState.Groups)
		}
	}
}

// ---------- persistence round-trip ----------

func TestLayoutPersistence_GroupsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 2, GridRows: 2,
		Groups:  []string{"core", "eng"},
		GroupOf: map[string]string{"super": "core", "eng1": "eng", "eng2": "eng"},
	}
	if err := SaveLayout(dir, state); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	loaded, ok := LoadLayout(dir, []string{"super", "eng1", "eng2"})
	if !ok {
		t.Fatal("LoadLayout returned ok=false")
	}
	if len(loaded.Groups) != 2 || loaded.Groups[0] != "core" || loaded.Groups[1] != "eng" {
		t.Errorf("Groups after round-trip = %v, want [core eng]", loaded.Groups)
	}
	for name, want := range state.GroupOf {
		if got := loaded.GroupOf[name]; got != want {
			t.Errorf("GroupOf[%q] = %q, want %q", name, got, want)
		}
	}
}

// TestLayoutPersistence_PrunesGroupEmptiedByStaleKeyFilter: a group whose
// only member is a pane key no longer in the current fleet (removed since
// last save) must not surface as an empty band on load -- the same
// no-empty-bands invariant the modal enforces on close, applied at load
// time too (a stale-key filter is the other way a band can end up empty).
func TestLayoutPersistence_PrunesGroupEmptiedByStaleKeyFilter(t *testing.T) {
	dir := t.TempDir()
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 1, GridRows: 1,
		Groups:  []string{"core", "mkt"},
		GroupOf: map[string]string{"super": "core", "pmm": "mkt"},
	}
	if err := SaveLayout(dir, state); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	// pmm is gone from the current fleet; mkt would be empty after filtering.
	loaded, ok := LoadLayout(dir, []string{"super"})
	if !ok {
		t.Fatal("LoadLayout returned ok=false")
	}
	for _, g := range loaded.Groups {
		if g == "mkt" {
			t.Errorf("mkt should be pruned (emptied by stale-key filter), groups = %v", loaded.Groups)
		}
	}
	if len(loaded.Groups) != 1 || loaded.Groups[0] != "core" {
		t.Errorf("Groups after round-trip = %v, want [core]", loaded.Groups)
	}
}

// TestLoadLayout_DuplicateGroupNamesAreRepairedAndAgentsPreserved (ini-9y3s):
// a layout.yaml already holding two "eng" entries -- the operator has some
// today -- must load to a UNIQUE name list with every agent still present.
// GroupOf has no field distinguishing which physical band an agent was
// dropped into, so both eng1 and eng2 already resolve to "eng" before and
// after the repair; what changes is the duplicate LIST ENTRY, which is
// dropped (super's call: an empty phantom "eng (2)" nobody could ever
// populate is worse than naming the merge and removing it).
func TestLoadLayout_DuplicateGroupNamesAreRepairedAndAgentsPreserved(t *testing.T) {
	dir := t.TempDir()
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 2, GridRows: 2,
		Groups:  []string{"core", "eng", "eng"},
		GroupOf: map[string]string{"super": "core", "eng1": "eng", "eng2": "eng"},
	}
	if err := SaveLayout(dir, state); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	loaded, ok := LoadLayout(dir, []string{"super", "eng1", "eng2"})
	if !ok {
		t.Fatal("LoadLayout returned ok=false")
	}

	count := 0
	for _, g := range loaded.Groups {
		if g == "eng" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Groups = %v, want exactly one \"eng\" after repair", loaded.Groups)
	}
	if len(loaded.Groups) != 2 {
		t.Errorf("Groups = %v, want length 2 (core, eng) -- no phantom renamed entry", loaded.Groups)
	}

	// NO AGENT LOST: the count before/after check the AC names explicitly.
	for _, name := range []string{"super", "eng1", "eng2"} {
		if _, ok := loaded.GroupOf[name]; !ok {
			t.Errorf("GroupOf lost %q after duplicate-group repair: %v", name, loaded.GroupOf)
		}
	}
	if loaded.GroupOf["eng1"] != "eng" || loaded.GroupOf["eng2"] != "eng" {
		t.Errorf("GroupOf after repair = %v, want eng1 and eng2 both still \"eng\"", loaded.GroupOf)
	}

	// PERSIST THE HEAL ONCE: a second load of the same directory must find
	// an already-clean file, not re-discover the same duplicate every time.
	reloaded, ok2 := LoadLayout(dir, []string{"super", "eng1", "eng2"})
	if !ok2 {
		t.Fatal("re-load after the persisted heal returned ok=false")
	}
	count2 := 0
	for _, g := range reloaded.Groups {
		if g == "eng" {
			count2++
		}
	}
	if count2 != 1 || len(reloaded.Groups) != 2 {
		t.Errorf("re-loaded Groups = %v, want the heal to have persisted (still [core eng])", reloaded.Groups)
	}
}

// TestLoadFleetScopedLayout_DuplicateGroupNamesAreRepairedInMemory
// (ini-9y3s): window 2's read-only loader must not diverge from window 1's
// after a repair. Since LoadFleetScopedLayout never persists (that store's
// own documented contract -- "convergence is the authority's job, on its
// own load"), this proves the in-memory repair alone, independent of
// whether window 1 has healed the file on disk yet: dedupeGroupLabels is a
// deterministic function of input order, so both loaders converge on the
// same result without coordinating.
func TestLoadFleetScopedLayout_DuplicateGroupNamesAreRepairedInMemory(t *testing.T) {
	dir := t.TempDir()
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 2, GridRows: 2,
		Groups:  []string{"core", "eng", "eng"},
		GroupOf: map[string]string{"super": "core", "eng1": "eng", "eng2": "eng"},
	}
	if err := SaveLayout(dir, state); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	groups, groupOf, ok := LoadFleetScopedLayout(dir)
	if !ok {
		t.Fatal("LoadFleetScopedLayout returned ok=false")
	}
	count := 0
	for _, g := range groups {
		if g == "eng" {
			count++
		}
	}
	if count != 1 || len(groups) != 2 {
		t.Errorf("groups = %v, want exactly [core eng] (deduped in memory)", groups)
	}
	if groupOf["eng1"] != "eng" || groupOf["eng2"] != "eng" {
		t.Errorf("groupOf = %v, want eng1 and eng2 both still \"eng\"", groupOf)
	}
}

// ---------- the PoC's own near-miss: no-matches must read the CURRENT frame ----------

// TestAgentsSearch_NoMatchesReadsCurrentFrame_NotStaleFrame is the spec's
// named hardest test: "the search bar's no-matches verdict must be computed
// AFTER layout for the current frame, not from the previous frame's cells."
// This is red-first against the OBVIOUS wrong implementation: compute the
// cell list once, then reuse that same (now stale) list across a structural
// change instead of recomputing. Demonstrated directly: a query that
// matches nothing YET, then a matching pane is hot-added (t.panes mutated,
// exactly as a live :add would), all without any render() call in between --
// the naive stale-cells check would still report zero matches for a pane
// that provably now exists and matches; the correct (fresh, current-frame)
// check does not.
func TestAgentsSearch_NoMatchesReadsCurrentFrame_NotStaleFrame(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1")
	tui.openAgentsModal()
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("eng2")

	sw, _ := s.Size()
	staleMembers := tui.agentsGroupMembers()
	stalePerRow := agentsGridPerRow(staleMembers, tui.layoutState.Groups, sw)
	staleCells := agentsGridLayoutCells(staleMembers, tui.layoutState.Groups, 4, 0, stalePerRow)
	if len(tui.agentsMatchCells(staleCells)) != 0 {
		t.Fatal("precondition: 'eng2' should match nothing before eng2 exists")
	}

	// Structural change to the CURRENT frame: eng2 is hot-added. No render()
	// call happens in between -- this models exactly the timing the PoC's
	// comment describes (a frame where the grid's true content changed but
	// nothing forced a fresh layout computation yet).
	tui.panes = append(tui.panes, testPane("eng2"))
	tui.ensureGroups(false)

	// THE BUG, demonstrated: reusing staleCells (the PREVIOUS frame's
	// layout, which was computed before eng2's pane existed at all) cannot
	// see eng2 no matter how matching logic evolves -- staleCells simply
	// has no cell for it. This is exactly the failure shape: a "no matches"
	// verdict that is actually just stale, not true.
	if got := len(tui.agentsMatchCells(staleCells)); got != 0 {
		t.Fatalf("this branch demonstrates the stale-frame problem structurally (stale cells cannot contain eng2's cell) -- got %d, want 0", got)
	}

	// THE FIX, verified: the production path (agentsCurrentCells, called
	// fresh by both render and every nav/search key handler -- see its own
	// doc comment) recomputes cells for the CURRENT frame and correctly
	// finds the match.
	freshCells := tui.agentsCurrentCells()
	freshMatches := tui.agentsMatchCells(freshCells)
	if len(freshMatches) != 1 {
		t.Fatalf("fresh (current-frame) cells should show 1 match for 'eng2' now that it exists, got %d", len(freshMatches))
	}
	if got := tui.panes[freshCells[freshMatches[0]].paneIdx].Name(); got != "eng2" {
		t.Errorf("the fresh match should be eng2, got %q", got)
	}

	// End-to-end: renderAgentsGrid itself (not a hand-rolled fresh call)
	// must show the corrected state, not "no matches", once actually drawn.
	tui.render()
	sw2, sh2 := s.Size()
	text := readScreenRect(s, 0, 0, sw2, sh2)
	if containsStr(text, "no matches") {
		t.Error("renderAgentsGrid still shows 'no matches' after eng2 was added and matches -- it read a stale frame")
	}
}

// TestAgentsSearch_NoMatchesTextAppearsWhenTrulyZero is the positive control
// for the test above: confirms the "no matches" indicator DOES appear when
// the current frame genuinely has zero matches, so the previous test isn't
// passing merely because the indicator never renders at all.
func TestAgentsSearch_NoMatchesTextAppearsWhenTrulyZero(t *testing.T) {
	tui, s := newTestTUIWithScreen("eng1", "eng2")
	tui.openAgentsModal()
	tui.agents.searching = true
	tui.agents.searchBuf = []rune("zzz")
	tui.render()

	sw, sh := s.Size()
	text := readScreenRect(s, 0, 0, sw, sh)
	if !containsStr(text, "no matches") {
		t.Error("'no matches' should appear for a query that genuinely matches nothing")
	}
}
