package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// Tests for ini-9ka.10: the session-global fleet-state store.

func fleetFixture(t *testing.T) (string, *FleetState) {
	t.Helper()
	root := t.TempDir()
	fs, err := LoadFleetState(root)
	if err != nil {
		t.Fatalf("LoadFleetState: %v", err)
	}
	return root, fs
}

// ── guardrails super asked for ───────────────────────────────────────

// TestFleetState_PersistentLayoutCarriesNoGlobalFields is guardrail 1: the
// three global facts must not appear in a written layout file. Asserted
// against the SERIALIZED bytes rather than the struct, because the struct
// still declares the legacy fields (the loader must parse old files) -- what
// changed is that they are never written.
func TestFleetState_PersistentLayoutCarriesNoGlobalFields(t *testing.T) {
	root := t.TempDir()
	st := LayoutState{
		Mode: LayoutGrid, GridCols: 3, GridRows: 2,
		Hidden:     map[string]bool{"eng1": true},
		Protected:  map[string]bool{"super": true},
		LivePinned: map[string]int{"eng2": 1},
	}
	if err := SaveLayout(root, st); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(layoutPath(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"hidden:", "protected:", "live_pinned:", "pinned:"} {
		if containsSubstr(string(data), key) {
			t.Errorf("layout file contains %q; global state belongs to fleet-state.yaml:\n%s", key, data)
		}
	}
}

// TestFleetState_DirectLayoutWriteDoesNotSurviveReload is guardrail 2: writing
// the derived projection directly is not a way to change global state. The
// projection is refreshed store->memory only, so a direct write is discarded
// the moment anything re-reads -- which is what makes the projection safe to
// keep for the ~82 read sites.
func TestFleetState_DirectLayoutWriteDoesNotSurviveReload(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI(testPane("super"), testPane("eng1"))
	tui.projectRoot = root
	tui.fleetState()

	// The mistake this guards against.
	tui.layoutState.Hidden["eng1"] = true

	tui.fleet = nil // Anything that re-reads the store.
	tui.fleetState()

	if tui.layoutState.Hidden["eng1"] {
		t.Error("a direct LayoutState write survived a reload -- the projection must be refreshed from the store, never the reverse")
	}
	if fs, err := LoadFleetState(root); err != nil || fs.IsHidden("eng1") {
		t.Errorf("a direct LayoutState write reached the store (err=%v)", err)
	}
}

// ── round-trip + authority ───────────────────────────────────────────

func TestFleetState_RoundTripsAcrossRestart(t *testing.T) {
	root, fs := fleetFixture(t)
	if err := fs.SetHidden("eng1", true); err != nil {
		t.Fatal(err)
	}
	if err := fs.SetProtected("super", true); err != nil {
		t.Fatal(err)
	}
	if err := fs.SetLiveSlot("eng2", 3, true); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadFleetState(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsHidden("eng1") {
		t.Error("hidden did not survive restart")
	}
	if !reloaded.IsProtected("super") {
		t.Error("protected did not survive restart")
	}
	if slot, ok := reloaded.LiveSlot("eng2"); !ok || slot != 3 {
		t.Errorf("live slot = %d/%v, want 3/true", slot, ok)
	}
}

// TestFleetState_SlotHoldsOneAgent covers the eviction rule living in the
// store rather than being re-implemented at each call site.
func TestFleetState_SlotHoldsOneAgent(t *testing.T) {
	_, fs := fleetFixture(t)
	if err := fs.SetLiveSlot("eng1", 0, true); err != nil {
		t.Fatal(err)
	}
	if err := fs.SetLiveSlot("eng2", 0, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.LiveSlot("eng1"); ok {
		t.Error("eng1 should have been evicted from slot 0 when eng2 took it")
	}
	if slot, ok := fs.LiveSlot("eng2"); !ok || slot != 0 {
		t.Errorf("eng2 slot = %d/%v, want 0/true", slot, ok)
	}
}

// TestFleetState_OnlyWindowOneWritesDirectly is the authority AC. A secondary
// window that reaches the mutation point directly must be refused -- the
// normal path routes it through set_fleet_state instead.
func TestFleetState_OnlyWindowOneWritesDirectly(t *testing.T) {
	root := t.TempDir()
	secondary := newTestTUI(testPane("eng1"))
	secondary.projectRoot = root
	secondary.windowID = "window-2"
	secondary.fleetState()

	err := secondary.mutateFleet("hide eng1", func(fs *FleetState) error {
		return fs.SetHidden("eng1", true)
	})
	if err == nil {
		t.Fatal("a secondary window wrote fleet state directly; only window 1 may write")
	}

	// And nothing reached disk.
	if fs, lerr := LoadFleetState(root); lerr == nil && fs.IsHidden("eng1") {
		t.Error("a refused secondary write still reached the file")
	}
}

// TestFleetState_WindowOneAppliesSecondaryCommand covers the other half: the
// command a secondary sends is applied by window 1 and persists.
func TestFleetState_WindowOneAppliesSecondaryCommand(t *testing.T) {
	root := t.TempDir()
	w1 := newTestTUI(testPane("eng1"))
	w1.projectRoot = root
	w1.agentEvents = make(chan AgentEvent, 8)

	if err := w1.applyFleetStateCmd(FleetStateCmd{
		Action: "set_fleet_state", Name: "eng1", Field: "hidden", On: true,
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	reloaded, err := LoadFleetState(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsHidden("eng1") {
		t.Error("window 1 did not persist the secondary's command")
	}
}

func TestFleetState_UnknownCommandFieldRejected(t *testing.T) {
	tui := newTestTUI(testPane("eng1"))
	tui.projectRoot = t.TempDir()
	tui.agentEvents = make(chan AgentEvent, 8)
	if err := tui.applyFleetStateCmd(FleetStateCmd{Name: "eng1", Field: "colour"}); err == nil {
		t.Error("an unknown fleet-state field should be rejected, not silently ignored")
	}
}

// ── corrupt store: read-only degradation (ini-9ka.9 rules) ───────────

func TestFleetState_CorruptStoreIsAnError(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleetStatePath(root), []byte("{{{ not: [valid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFleetState(root); err == nil {
		t.Error("a corrupt store must error, not silently present as a fresh session")
	}
}

// TestFleetState_CorruptStoreDegradesReadOnlyAndNeverErases is the ini-9ka.9
// junction rule, applied here: an unreadable store yields a store that answers
// reads but refuses writes, and the operator's real bytes are left untouched.
func TestFleetState_CorruptStoreDegradesReadOnlyAndNeverErases(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0700); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("{{{ not: [valid")
	if err := os.WriteFile(fleetStatePath(root), corrupt, 0600); err != nil {
		t.Fatal(err)
	}

	tui := newTestTUI(testPane("eng1"))
	tui.projectRoot = root
	tui.agentEvents = make(chan AgentEvent, 8)

	fs := tui.fleetState()
	if !fs.readOnly {
		t.Fatal("a corrupt store must degrade to a READ-ONLY fallback, never a writable one")
	}
	if fs.IsHidden("eng1") {
		t.Error("degraded view should report nothing hidden")
	}

	if err := tui.setHidden("eng1", true); err == nil {
		t.Error("a write against a read-only fallback must be refused")
	}

	after, err := os.ReadFile(fleetStatePath(root))
	if err != nil {
		t.Fatalf("the corrupt file was removed: %v", err)
	}
	if string(after) != string(corrupt) {
		t.Error("the corrupt file was overwritten -- a recoverable parse error became silent erasure")
	}
}

// TestFleetState_RootlessTUIDoesNotWriteStrayFile guards the failure this
// nearly shipped with: fleetStatePath("") is RELATIVE, so a TUI with no
// project root would drop a .initech/fleet-state.yaml into the process's
// working directory. Writes are accepted in-session and simply not persisted.
func TestFleetState_RootlessTUIDoesNotWriteStrayFile(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	tui := newTestTUI(testPane("eng1"))
	tui.projectRoot = "" // ad-hoc / test TUI
	tui.agentEvents = make(chan AgentEvent, 8)

	if err := tui.setHidden("eng1", true); err != nil {
		t.Fatalf("a rootless TUI should accept in-session changes: %v", err)
	}
	if !tui.layoutState.Hidden["eng1"] {
		t.Error("in-session hide should still apply for a rootless TUI")
	}

	here, _ := os.Getwd()
	for _, dir := range []string{here, cwd} {
		if _, err := os.Stat(filepath.Join(dir, ".initech", "fleet-state.yaml")); err == nil {
			t.Errorf("a rootless TUI wrote a stray store into %s", dir)
		}
	}
}

// ── import-once ──────────────────────────────────────────────────────

// writeLegacyLayout writes a byte-literal PRIOR-RELEASE layout file. Literal,
// not produced by today's writer: today's writer no longer emits these fields
// at all, so a round-trip could not construct this input, and an upgrade test
// must start from what old releases actually wrote.
func writeLegacyLayout(t *testing.T, root string) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte("" +
		"grid: 3x2\n" +
		"mode: grid\n" +
		"hidden:\n" +
		"    - eng5\n" +
		"pinned:\n" +
		"    - super\n" +
		"live_pinned:\n" +
		"    eng1: 2\n")
	if err := os.WriteFile(layoutPath(root), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	return legacy
}

func TestFleetState_ImportsLegacyStateOnceAndLeavesLayoutUntouched(t *testing.T) {
	root := t.TempDir()
	legacy := writeLegacyLayout(t, root)

	fs, err := LoadFleetState(root)
	if err != nil {
		t.Fatalf("LoadFleetState: %v", err)
	}
	if !fs.IsHidden("eng5") {
		t.Error("import lost hidden")
	}
	if !fs.IsProtected("super") {
		t.Error("import lost protection migrated from the deprecated `pinned:` key")
	}
	if slot, ok := fs.LiveSlot("eng1"); !ok || slot != 2 {
		t.Errorf("import lost live pin: slot=%d ok=%v", slot, ok)
	}

	// The legacy file is never rewritten (adoption-in-place, ini-9ka.3).
	after, err := os.ReadFile(layoutPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(legacy) {
		t.Errorf("layout.yaml was rewritten during import:\nwant %q\ngot  %q", legacy, after)
	}
}

// TestFleetState_NeverReImportsOnceStoreExists is the import-ONCE half. A
// present store means the import already happened, so later edits to the dead
// legacy fields must not leak back in -- otherwise an operator's current state
// could be silently reverted to a stale copy.
func TestFleetState_NeverReImportsOnceStoreExists(t *testing.T) {
	root := t.TempDir()
	writeLegacyLayout(t, root)

	if _, err := LoadFleetState(root); err != nil { // first load imports
		t.Fatal(err)
	}
	// Operator hides something else; store now diverges from the legacy file.
	fs, err := LoadFleetState(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.SetHidden("eng5", false); err != nil {
		t.Fatal(err)
	}

	// Mutate the dead legacy fields to something loud.
	if err := os.WriteFile(layoutPath(root), []byte(""+
		"grid: 3x2\nmode: grid\nhidden:\n    - eng5\n    - eng9\n"), 0600); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadFleetState(root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.IsHidden("eng5") || reloaded.IsHidden("eng9") {
		t.Error("legacy fields were re-imported over the live store; import must happen exactly once")
	}
}

func TestFleetState_NoImportWhenLegacyHasNothing(t *testing.T) {
	root := t.TempDir()
	if err := SaveLayout(root, LayoutState{Mode: LayoutGrid, GridCols: 2, GridRows: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFleetState(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fleetStatePath(root)); err == nil {
		t.Error("an empty import created a store file; nothing to import should create nothing")
	}
}

// ── three-store pairwise independence (the 3x2 matrix) ───────────────

// TestFleetState_ThreeStoresArePairwiseIndependent asserts each store's
// round-trip touches exactly its own file. Uses an UNFILTERED listing (the
// ini-4kp lesson: a prefix-filtered listing is how "touches nothing else" goes
// blind).
func TestFleetState_ThreeStoresArePairwiseIndependent(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, root string)
		want []string
	}{
		{"layout", func(t *testing.T, root string) {
			if err := SaveLayoutForWindow(root, "two", LayoutState{Mode: LayoutGrid, GridCols: 3, GridRows: 2}); err != nil {
				t.Fatal(err)
			}
			if _, ok := LoadLayoutForWindow(root, "two", []string{"eng1"}); !ok {
				t.Fatal("layout load failed")
			}
		}, []string{"layout-two.yaml"}},

		{"assignment", func(t *testing.T, root string) {
			a, err := LoadAssignment(root, WindowOne)
			if err != nil {
				t.Fatal(err)
			}
			if err := mustAssignWriter(t, a).MoveGroup("eng", "window2"); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadAssignment(root, WindowOne); err != nil {
				t.Fatal(err)
			}
		}, []string{"assignments.yaml"}},

		{"fleet-state", func(t *testing.T, root string) {
			fs, err := LoadFleetState(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := fs.SetHidden("eng1", true); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadFleetState(root); err != nil {
				t.Fatal(err)
			}
		}, []string{"fleet-state.yaml"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.run(t, root)
			got := allInitechEntries(t, root)
			sort.Strings(got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s round-trip touched %v, want exactly %v -- the three stores must be pairwise independent",
					tc.name, got, tc.want)
			}
		})
	}
}
