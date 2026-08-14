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
	viaWindow, ok := LoadLayout(root, panes)
	if !ok {
		t.Fatal("LoadLayout returned false for a prior-release layout.yaml")
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

// TestSaveLayout_ConcurrentWritesDoNotCorrupt covers the atomic-write AC on
// the ONE project layout file.
//
// Until ini-qodm this drove two window identities writing two files, because
// the trap it guarded was staging: a temp path derived from the shared project
// path rather than the per-window path would have two windows renaming over
// each other. That per-window surface is retired -- it was never written or
// read in any release -- but the staging guarantee it depended on is NOT
// retired, and it still has to hold for concurrent writers to the single file.
// So the test keeps its shape and drops the identities.
//
// ROUNDS, not one burst (ini-g9x). This test went green on Windows for months
// and then failed the one run that gated a release: a single burst of
// concurrent writes is a single roll of the dice, so a real race shows up as
// "flaky CI" rather than as a bug. Re-running the burst and re-asserting after
// each round turns one roll into many, which is what makes the failure
// reproducible enough to be fixed rather than re-run. Cheap: milliseconds.
// Run with -race.
func TestSaveLayout_ConcurrentWritesDoNotCorrupt(t *testing.T) {
	root := t.TempDir()
	panes := windowTestPanes

	for round := 0; round < 25; round++ {
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(2)
			go func() { defer wg.Done(); SaveLayout(root, windowLayoutState(3, 2, LayoutGrid)) }()
			go func() { defer wg.Done(); SaveLayout(root, windowLayoutState(6, 1, LayoutFocus)) }()
		}
		wg.Wait()

		// Assert INSIDE the loop: a corruption that a later round overwrites
		// with a good file would otherwise pass unnoticed.
		st, ok := LoadLayout(root, panes)
		if !ok {
			t.Fatalf("round %d: layout failed to load after concurrent writes (corrupt or missing file)", round)
		}
		// Whichever writer won, the file must be ONE of the two coherent
		// states -- never a blend, which is what a shared staging path
		// produces and what "it still parses" would not catch.
		switch {
		case st.Mode == LayoutGrid && st.GridCols == 3 && st.GridRows == 2:
		case st.Mode == LayoutFocus && st.GridCols == 6 && st.GridRows == 1:
		default:
			t.Fatalf("round %d: layout is a BLEND of two concurrent writers "+
				"(mode=%v cols=%d rows=%d); staging is not isolating writers",
				round, st.Mode, st.GridCols, st.GridRows)
		}
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
