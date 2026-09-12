// agents_grid_test.go tests the group-seeding and persistence primitives
// behind the grouped 2-D agents modal (ini-2rc) and the search-bar
// no-matches regression the spec calls out by name as the PoC's own
// near-miss. Layout geometry lives in agents_grid_geometry_test.go and
// navigation/grab in agents_grid_transpose_test.go (ini-w771 transposed the
// grid: groups are columns now); agents_test.go / agents_search_test.go cover
// the modal's key-handling integration.
package tui

import (
	"testing"
)

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

// TestDedupeGroupLabels_CaseVariantsAreNotFolded (eng1's review of 9952b3c,
// found via a mutant folding case): the load-time repair must apply the
// SAME case-sensitive exact-match rule the creation-time check already
// pins (TestAgentsModal_CreateGroupCaseVariantIsNotADuplicate) -- "eng" and
// "ENG" are two real, distinct bands, not a duplicate of each other. This
// direction is the destructive one: creation only ever REFUSES a name,
// but the load repair DROPS an entry and LoadLayout PERSISTS the result --
// a case-folding regression here silently deletes a real band with real
// members, on disk, exactly what the AC's "never silently drop a band"
// forbids.
//
// A naive case-insensitive dedup passes when the lowercase form comes
// LAST ("Eng","eng" -> unaffected, since "eng" already seen as itself)
// but destroys the SET when the lowercase form comes FIRST -- the ordering
// eng1's demonstration used, kept here for the same reason.
func TestDedupeGroupLabels_CaseVariantsAreNotFolded(t *testing.T) {
	in := []string{"eng", "ENG", "core"}
	out, healed := dedupeGroupLabels(in)

	if healed {
		t.Errorf("dedupeGroupLabels(%v) healed=true, want false -- \"eng\" and \"ENG\" are "+
			"different labels, nothing here is a duplicate", in)
	}
	if len(out) != 3 {
		t.Fatalf("dedupeGroupLabels(%v) = %v, want all 3 labels preserved", in, out)
	}
	for _, want := range in {
		if !groupNameExists(out, want) {
			t.Errorf("dedupeGroupLabels(%v) = %v, lost %q", in, out, want)
		}
	}
}

// TestLoadLayout_CaseVariantGroupsSurviveTheRepairAndPersist is a full-path
// integration check, NOT a second cell discriminating eng1's case-folding
// mutant -- checked, not assumed: the same mutant applied to
// dedupeGroupLabels (fold the comparison key, keep the stored string exact)
// does NOT red this test. It survives because LoadLayout's own pre-existing
// dead-group rule (ini-qkwc) independently re-appends any label a surviving
// GroupOf entry still points to but the groups list lacks -- so for any
// group with real members, that rule masks a case-folding regression in
// dedupeGroupLabels before it ever reaches this test's assertions. Kept
// anyway as a normal-path integration check (case-distinct groups load,
// persist, and reload correctly); TestDedupeGroupLabels_CaseVariantsAreNotFolded
// is the cell that actually pins the mutant, by testing the function directly.
func TestLoadLayout_CaseVariantGroupsSurviveTheRepairAndPersist(t *testing.T) {
	dir := t.TempDir()
	state := LayoutState{
		Mode:     LayoutGrid,
		GridCols: 2, GridRows: 2,
		Groups:  []string{"eng", "ENG", "core"},
		GroupOf: map[string]string{"eng1": "eng", "eng2": "ENG", "super": "core"},
	}
	if err := SaveLayout(dir, state); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	loaded, ok := LoadLayout(dir, []string{"eng1", "eng2", "super"})
	if !ok {
		t.Fatal("LoadLayout returned ok=false")
	}
	if len(loaded.Groups) != 3 {
		t.Fatalf("Groups = %v, want all 3 case-distinct labels preserved", loaded.Groups)
	}
	for _, want := range []string{"eng", "ENG", "core"} {
		if !groupNameExists(loaded.Groups, want) {
			t.Errorf("Groups = %v, lost %q", loaded.Groups, want)
		}
	}
	if loaded.GroupOf["eng1"] != "eng" || loaded.GroupOf["eng2"] != "ENG" {
		t.Errorf("GroupOf = %v, want eng1=eng and eng2=ENG kept distinct", loaded.GroupOf)
	}

	// PERSISTED distinctly, not just computed distinctly in memory.
	reloaded, ok2 := LoadLayout(dir, []string{"eng1", "eng2", "super"})
	if !ok2 {
		t.Fatal("re-load returned ok=false")
	}
	if len(reloaded.Groups) != 3 {
		t.Errorf("re-loaded Groups = %v, want all 3 still present after persistence",
			reloaded.Groups)
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
	stalePerRow := agentsGridColumnsPerRow(untieredTiers(tui.layoutState.Groups), sw)
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
