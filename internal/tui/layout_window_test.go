package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Tests for ini-9ka.3: per-window layout persistence. layoutPath was a single
// file (.initech/layout.yaml) shared by the whole project, so two windows
// persisting layout state would clobber each other on every change -- the
// blocker ini-mcf9 found for multi-monitor. Layout is now keyed by a window
// identity, with the empty identity preserving window 1's existing file
// exactly.

// windowLayoutState builds a distinguishable LayoutState per window so a
// cross-clobber is visible in the loaded values, not just in file bytes.
//
// Callers must pick grids that HOLD windowTestPanes, because LoadLayout
// auto-expands any grid too small for the visible pane count (layout.go's
// `cols*rows < visCount` recalc). A 2x1 grid against 6 panes silently becomes
// autoGrid(6) == 3x2 — which is another window's value, so a genuine
// cross-clobber and a correct round-trip would look identical. The two grids
// used below (3x2 and 6x1) both hold exactly 6, so neither is rewritten and
// any difference in the loaded value is real.
func windowLayoutState(cols, rows int, mode LayoutMode) LayoutState {
	return LayoutState{
		Mode:     mode,
		GridCols: cols,
		GridRows: rows,
		Hidden:   map[string]bool{},
	}
}

// windowTestPanes is the pane set every test here loads against; grid choices
// above are sized to it.
var windowTestPanes = []string{"super", "eng1", "eng2", "eng3", "eng4", "eng5"}

// TestSaveLayoutForWindow_TwoIdentitiesDoNotCollide is the primary AC: two
// distinct window identities each persist and reload their own LayoutState in
// the SAME project root with no cross-clobbering.
func TestSaveLayoutForWindow_TwoIdentitiesDoNotCollide(t *testing.T) {
	root := t.TempDir()
	panes := windowTestPanes

	if err := SaveLayoutForWindow(root, "one", windowLayoutState(3, 2, LayoutGrid)); err != nil {
		t.Fatalf("save window one: %v", err)
	}
	if err := SaveLayoutForWindow(root, "two", windowLayoutState(6, 1, LayoutFocus)); err != nil {
		t.Fatalf("save window two: %v", err)
	}

	got1, ok := LoadLayoutForWindow(root, "one", panes)
	if !ok {
		t.Fatal("load window one returned false")
	}
	got2, ok := LoadLayoutForWindow(root, "two", panes)
	if !ok {
		t.Fatal("load window two returned false")
	}

	if got1.GridCols != 3 || got1.GridRows != 2 {
		t.Errorf("window one grid = %dx%d, want 3x2 (clobbered by window two?)", got1.GridCols, got1.GridRows)
	}
	if got1.Mode != LayoutGrid {
		t.Errorf("window one mode = %v, want LayoutGrid", got1.Mode)
	}
	if got2.GridCols != 6 || got2.GridRows != 1 {
		t.Errorf("window two grid = %dx%d, want 6x1 (clobbered by window one?)", got2.GridCols, got2.GridRows)
	}
	if got2.Mode != LayoutFocus {
		t.Errorf("window two mode = %v, want LayoutFocus", got2.Mode)
	}
}

// TestSaveLayout_SingleFileCollidesAcrossWindows is the NEGATIVE CONTROL the
// bead requires: it drives the pre-fix, project-scoped API with what would be
// two windows' states and asserts the collision that motivated this bead --
// the second write destroys the first, with only one file on disk. This test
// documents the OLD behavior and must keep passing; it is the baseline the
// per-window API above is measured against. Without it, the collision claim
// rests on the bead's prose rather than on an executable check.
func TestSaveLayout_SingleFileCollidesAcrossWindows(t *testing.T) {
	root := t.TempDir()
	panes := windowTestPanes

	if err := SaveLayout(root, windowLayoutState(3, 2, LayoutGrid)); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := SaveLayout(root, windowLayoutState(6, 1, LayoutFocus)); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, ok := LoadLayout(root, panes)
	if !ok {
		t.Fatal("load returned false")
	}
	if got.GridCols != 6 || got.GridRows != 1 {
		t.Errorf("grid = %dx%d, want 6x1 -- the project-scoped file should hold only the LAST write", got.GridCols, got.GridRows)
	}
	entries := layoutFilesIn(t, root)
	if len(entries) != 1 {
		t.Errorf("layout files = %v, want exactly 1 -- the project-scoped API writes a single shared file", entries)
	}
}

// TestSaveLayoutForWindow_EmptyIdentityUsesLegacyPath is the single-window
// regression the AC calls for explicitly: the empty window identity must
// resolve to the exact pre-existing .initech/layout.yaml path, so an existing
// operator's layout file is adopted in place on upgrade with no migration,
// rename, or fallback read.
func TestSaveLayoutForWindow_EmptyIdentityUsesLegacyPath(t *testing.T) {
	root := t.TempDir()
	if err := SaveLayoutForWindow(root, "", windowLayoutState(4, 2, LayoutGrid)); err != nil {
		t.Fatalf("save: %v", err)
	}
	legacy := filepath.Join(root, ".initech", "layout.yaml")
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("empty window identity must write the legacy path %s: %v", legacy, err)
	}
	entries := layoutFilesIn(t, root)
	if len(entries) != 1 || entries[0] != "layout.yaml" {
		t.Errorf("layout files = %v, want exactly [layout.yaml]", entries)
	}
}

// TestLoadLayout_PriorReleaseFileLoadsUnchangedAfterUpgrade is the upgrade-
// compatibility AC (pm's appended clause): an existing project's layout.yaml
// from ANY prior release must load unchanged after upgrade. The fixture is a
// byte-literal file, deliberately NOT produced by today's writer -- a
// round-trip through current code would only prove today's writer agrees with
// today's reader, which is not what an upgrade is. It also carries the
// deprecated `pinned:` key that older releases actually wrote (LoadLayout
// still has the migration shim for it), so this exercises a genuinely old
// on-disk shape rather than a recent one.
//
// The design meets this by construction rather than by migration: window 1's
// identity is the empty string, which resolves to the same .initech/layout.yaml
// path as before, so the operator's file is adopted in place. Nothing is
// renamed, copied, or rewritten at upgrade time, so there is no partial-
// migration window in which the layout could be lost.
func TestLoadLayout_PriorReleaseFileLoadsUnchangedAfterUpgrade(t *testing.T) {
	root := t.TempDir()
	panes := windowTestPanes

	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0700); err != nil {
		t.Fatal(err)
	}
	priorRelease := "" +
		"grid: 3x2\n" +
		"grid_explicit: true\n" +
		"hidden:\n" +
		"    - eng5\n" +
		"pinned:\n" +
		"    - super\n" +
		"mode: grid\n"
	if err := os.WriteFile(filepath.Join(root, ".initech", "layout.yaml"), []byte(priorRelease), 0600); err != nil {
		t.Fatal(err)
	}

	viaLegacy, ok := LoadLayout(root, panes)
	if !ok {
		t.Fatal("LoadLayout returned false for a prior-release layout.yaml -- upgrade would silently lose the operator's layout")
	}
	viaWindow, ok := LoadLayoutForWindow(root, "", panes)
	if !ok {
		t.Fatal("LoadLayoutForWindow(\"\") returned false for a prior-release layout.yaml")
	}

	for label, got := range map[string]LayoutState{"legacy": viaLegacy, "window": viaWindow} {
		if got.GridCols != 3 || got.GridRows != 2 {
			t.Errorf("%s: grid = %dx%d, want 3x2", label, got.GridCols, got.GridRows)
		}
		if !got.GridExplicit {
			t.Errorf("%s: gridExplicit = false, want true", label)
		}
		if got.Mode != LayoutGrid {
			t.Errorf("%s: mode = %v, want LayoutGrid", label, got.Mode)
		}
		// hidden/protected are NO LONGER layout fields (ini-9ka.10): they are
		// session-global and migrate into fleet-state.yaml. Asserted below,
		// end-to-end, which is a stronger upgrade claim than this was --
		// it proves the state moved, not merely that it was re-read.
		if len(got.Hidden) != 0 || len(got.Protected) != 0 {
			t.Errorf("%s: layout carried hidden=%v protected=%v; both belong to fleet state now",
				label, got.Hidden, got.Protected)
		}
	}

	// The one-time import lifts the legacy global fields into fleet state --
	// including the doubly-deprecated `pinned:` key, so an upgrade from two
	// formats back does not lose protection.
	fs, err := LoadFleetState(root)
	if err != nil {
		t.Fatalf("LoadFleetState after upgrade: %v", err)
	}
	if !fs.IsHidden("eng5") {
		t.Error("upgrade lost the hidden flag: eng5 should be hidden in fleet state")
	}
	if !fs.IsProtected("super") {
		t.Error("upgrade lost protection migrated from the legacy `pinned:` key")
	}

	// Upgrade must not rewrite or relocate the LAYOUT file.
	entries := layoutFilesIn(t, root)
	if len(entries) != 1 || entries[0] != "layout.yaml" {
		t.Errorf("layout files after an upgrade read = %v, want exactly [layout.yaml] -- nothing may be migrated or renamed", entries)
	}
}

// TestSaveLayoutForWindow_RestartRoundTripPerWindow covers the restart AC:
// after the whole session goes away (simulated by discarding all in-memory
// state and re-reading from disk), each window's own layout restores
// independently.
func TestSaveLayoutForWindow_RestartRoundTripPerWindow(t *testing.T) {
	root := t.TempDir()
	panes := windowTestPanes

	w1 := windowLayoutState(3, 2, LayoutGrid)
	w1.LiveAuto = true
	w2 := windowLayoutState(6, 1, Layout2Col)
	w2.GridExplicit = true

	if err := SaveLayoutForWindow(root, "", w1); err != nil {
		t.Fatalf("save window 1: %v", err)
	}
	if err := SaveLayoutForWindow(root, "two", w2); err != nil {
		t.Fatalf("save window 2: %v", err)
	}

	got1, ok := LoadLayoutForWindow(root, "", panes)
	if !ok {
		t.Fatal("restart: window 1 load returned false")
	}
	got2, ok := LoadLayoutForWindow(root, "two", panes)
	if !ok {
		t.Fatal("restart: window 2 load returned false")
	}

	if got1.Mode != LayoutGrid || !got1.LiveAuto {
		t.Errorf("window 1 after restart: mode=%v liveAuto=%v, want LayoutGrid/true", got1.Mode, got1.LiveAuto)
	}
	if got2.Mode != Layout2Col || !got2.GridExplicit {
		t.Errorf("window 2 after restart: mode=%v gridExplicit=%v, want Layout2Col/true", got2.Mode, got2.GridExplicit)
	}
}

// TestSaveLayoutForWindow_ConcurrentWritesDoNotCorrupt covers the atomic-write
// AC per-peer. The trap this guards is the staging file: if the temp file were
// still derived from the shared project path rather than the per-window path,
// two windows saving at once would stage through the SAME .tmp and rename over
// each other, corrupting or cross-writing final files that look independent.
// Run with -race.
func TestSaveLayoutForWindow_ConcurrentWritesDoNotCorrupt(t *testing.T) {
	root := t.TempDir()
	panes := windowTestPanes

	// ROUNDS, not one burst (ini-g9x). This test went green on Windows for
	// months and then failed the one run that gated a release: a single burst
	// of concurrent writes is a single roll of the dice, so a real race shows
	// up as "flaky CI" rather than as a bug. Re-running the burst and
	// re-asserting after each round turns one roll into many, which is what
	// makes the failure reproducible enough to be fixed rather than re-run.
	// Cheap: the whole test is milliseconds.
	for round := 0; round < 25; round++ {
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(2)
			go func() { defer wg.Done(); SaveLayoutForWindow(root, "one", windowLayoutState(3, 2, LayoutGrid)) }()
			go func() { defer wg.Done(); SaveLayoutForWindow(root, "two", windowLayoutState(6, 1, LayoutFocus)) }()
		}
		wg.Wait()

		// Assert INSIDE the loop: a corruption that a later round overwrites
		// with a good file would otherwise pass unnoticed.
		if _, ok := LoadLayoutForWindow(root, "one", panes); !ok {
			t.Fatalf("round %d: window one failed to load after concurrent writes (corrupt or missing file)", round)
		}
		if _, ok := LoadLayoutForWindow(root, "two", panes); !ok {
			t.Fatalf("round %d: window two failed to load after concurrent writes (corrupt or missing file)", round)
		}
	}

	got1, ok := LoadLayoutForWindow(root, "one", panes)
	if !ok {
		t.Fatal("window one failed to load after concurrent writes (corrupt file?)")
	}
	got2, ok := LoadLayoutForWindow(root, "two", panes)
	if !ok {
		t.Fatal("window two failed to load after concurrent writes (corrupt file?)")
	}
	if got1.GridCols != 3 || got1.GridRows != 2 {
		t.Errorf("window one grid = %dx%d, want 3x2", got1.GridCols, got1.GridRows)
	}
	if got2.GridCols != 6 || got2.GridRows != 1 {
		t.Errorf("window two grid = %dx%d, want 6x1", got2.GridCols, got2.GridRows)
	}
	for _, name := range layoutFilesIn(t, root) {
		if strings.HasSuffix(name, ".tmp") {
			t.Errorf("staging file %q survived; atomic write leaked a temp file", name)
		}
	}
}

// TestSaveLayoutForWindow_RejectsUnsafeIdentity covers path-traversal safety:
// the window identity becomes part of a filesystem path, so an identity
// containing separators or ".." must be rejected outright rather than writing
// outside .initech.
func TestSaveLayoutForWindow_RejectsUnsafeIdentity(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"../escape", "a/b", "a:b", "..", ".", "with space", "dot.name", ""} {
		if bad == "" {
			continue // empty is the legitimate window-1 identity, covered above
		}
		if err := SaveLayoutForWindow(root, bad, windowLayoutState(2, 1, LayoutGrid)); err == nil {
			t.Errorf("SaveLayoutForWindow(%q) should reject an unsafe window identity", bad)
		}
		if _, ok := LoadLayoutForWindow(root, bad, []string{"super"}); ok {
			t.Errorf("LoadLayoutForWindow(%q) should return false for an unsafe window identity", bad)
		}
	}
	// Nothing may have been created outside .initech, or inside it.
	if entries := layoutFilesIn(t, root); len(entries) != 0 {
		t.Errorf("rejected identities wrote files: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); err == nil {
		t.Error("a traversal identity escaped the project root")
	}
}

// TestLoadLayoutForWindow_IndependentOfAssignmentState is the grooming AC,
// hardened per ini-4kp after ini-9ka.4 landed.
//
// Form (a) corrupts the assignment store and confirms the layout round-trip
// is unaffected. When first written this was forward-guessing -- .4 did not
// exist, so the path was a literal guess. It now resolves the store through
// .4's own assignmentPath helper, so if that store is ever renamed this test
// follows it instead of silently reverting to guessing at a path nothing
// writes (which would make the guard quietly vacuous again).
//
// Form (b) is the structural claim: a layout round-trip touches its own file
// and NOTHING else. It lists the directory unfiltered -- the earlier version
// filtered to a "layout" prefix, which meant a stray write of any
// differently-named file (assignments.yaml being the obvious one) was
// invisible to an assertion whose whole point is "nothing else".
func TestLoadLayoutForWindow_IndependentOfAssignmentState(t *testing.T) {
	panes := windowTestPanes
	want := windowLayoutState(3, 2, LayoutGrid)

	// (a) Round-trip is byte-identical with a corrupt assignment-store
	// present and with no such file at all.
	var results []LayoutState
	for _, withCorruptStore := range []bool{false, true} {
		root := t.TempDir()
		if withCorruptStore {
			if err := os.MkdirAll(filepath.Join(root, ".initech"), 0700); err != nil {
				t.Fatal(err)
			}
			// The REAL store path, via .4's helper -- not a literal.
			if err := os.WriteFile(assignmentPath(root), []byte("{{{ not: [valid yaml"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := SaveLayoutForWindow(root, "two", want); err != nil {
			t.Fatalf("save (corruptStore=%v): %v", withCorruptStore, err)
		}
		got, ok := LoadLayoutForWindow(root, "two", panes)
		if !ok {
			t.Fatalf("load returned false (corruptStore=%v)", withCorruptStore)
		}
		results = append(results, got)
	}
	if results[0].GridCols != results[1].GridCols || results[0].GridRows != results[1].GridRows || results[0].Mode != results[1].Mode {
		t.Errorf("layout round-trip differed with a corrupt assignment store present: %+v vs %+v", results[0], results[1])
	}

	// (b) A full round-trip touches exactly one file -- asserted against an
	// UNFILTERED listing, so any extra write of any name is caught.
	root := t.TempDir()
	if err := SaveLayoutForWindow(root, "two", want); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, ok := LoadLayoutForWindow(root, "two", panes); !ok {
		t.Fatal("load returned false")
	}
	entries := allInitechEntries(t, root)
	if len(entries) != 1 || entries[0] != "layout-two.yaml" {
		t.Errorf("round-trip touched %v, want exactly [layout-two.yaml] -- layout persistence must read and write no other state", entries)
	}
}

// allInitechEntries lists EVERY entry under .initech, unfiltered and sorted.
// Deliberately not layoutFilesIn: that one filters to a "layout" prefix, which
// is right for tests asking "which layout files exist" but wrong for any test
// asserting that nothing else was written (ini-4kp).
func allInitechEntries(t *testing.T, root string) []string {
	t.Helper()
	des, err := os.ReadDir(filepath.Join(root, ".initech"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read .initech: %v", err)
	}
	var out []string
	for _, de := range des {
		out = append(out, de.Name())
	}
	sort.Strings(out)
	return out
}

// TestDeleteLayoutForWindow_RemovesOnlyThatWindow confirms reset for one
// window leaves other windows' layouts intact.
func TestDeleteLayoutForWindow_RemovesOnlyThatWindow(t *testing.T) {
	root := t.TempDir()
	if err := SaveLayoutForWindow(root, "", windowLayoutState(3, 2, LayoutGrid)); err != nil {
		t.Fatal(err)
	}
	if err := SaveLayoutForWindow(root, "two", windowLayoutState(6, 1, LayoutFocus)); err != nil {
		t.Fatal(err)
	}
	if err := DeleteLayoutForWindow(root, "two"); err != nil {
		t.Fatalf("delete window two: %v", err)
	}
	entries := layoutFilesIn(t, root)
	if len(entries) != 1 || entries[0] != "layout.yaml" {
		t.Errorf("layout files after deleting window two = %v, want exactly [layout.yaml]", entries)
	}
}

// layoutFilesIn lists the .initech entries that look like layout files (plus
// any leaked staging files), sorted for stable assertions.
func layoutFilesIn(t *testing.T, root string) []string {
	t.Helper()
	dir := filepath.Join(root, ".initech")
	des, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read .initech: %v", err)
	}
	var out []string
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, "layout") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
