package tui

// Regression tests for ini-qkwc (mixed identity families in layout group_of)
// and the absorbed ini-m495 (heals must persist once). The xq4r class one
// store over: bare keys and window1:-prefixed keys for the SAME agents with
// conflicting values, splitting the fleet per window -- window 1 read one
// family, window 2 the other, and agents whose prefixed keys pointed at a
// group missing from the groups list vanished from the viewer's modal.
//
// Per the jhm6 layer-masking lesson, the read heal and the write guard each
// get the assertion only IT can answer, designed up front: the heal is
// proven on a dirty FILE (and the guard cannot help, since nothing saves),
// the guard on a dirty in-memory MAP (and the heal cannot help, since the
// dirty state never touched a loader).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// qkwcSeed is the EXACT mixed map from the operator's live layout.yaml
// (2026-08-13 ~21:16): bare family says qa for all three; window1: family
// says eng for eng1/eng2 -- and "eng" is absent from groups.
const qkwcSeed = `grid: 2x2
mode: grid
groups:
    - core
    - qa
group_of:
    eng1: qa
    eng2: qa
    qa1: qa
    "window1:eng1": eng
    "window1:eng2": eng
    "window1:qa1": qa
`

func writeQkwcLayout(t *testing.T, windowID, content string) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	os.WriteFile(layoutPath(root), []byte(content), 0o644)
	return root
}

// TestLoadLayout_HealsMixedIdentityFamilies is the read heal on the live
// seed: one family survives (bare), bare wins the conflict, and nothing is
// dropped -- the same membership regardless of which window loads it.
func TestLoadLayout_HealsMixedIdentityFamilies(t *testing.T) {
	root := writeQkwcLayout(t, "", qkwcSeed)
	panes := []string{"eng1", "eng2", "qa1"}

	st, ok := LoadLayout(root, panes)
	if !ok {
		t.Fatal("seeded layout failed to load")
	}
	for _, agent := range panes {
		if got := st.GroupOf[agent]; got != "qa" {
			t.Errorf("GroupOf[%s] = %q, want qa (bare wins the conflict; the aliased eng value "+
				"is the viewer-written family)", agent, got)
		}
	}
	for key := range st.GroupOf {
		if strings.Contains(key, ":") {
			t.Errorf("normalized GroupOf still carries aliased key %q; per-window resolution "+
				"of two families is the fleet-splitting bug itself", key)
		}
	}
}

// TestLoadLayout_PrefixedOnlyAgentSurvives pins the never-dropped edge: an
// agent present ONLY under the alias family keeps its group under the bare
// name. Dropping it re-creates the vanishing-agent symptom via the heal.
func TestLoadLayout_PrefixedOnlyAgentSurvives(t *testing.T) {
	root := writeQkwcLayout(t, "", `grid: 2x2
mode: grid
groups:
    - core
group_of:
    "window1:growth": marketing
`)
	st, ok := LoadLayout(root, []string{"growth"})
	if !ok {
		t.Fatal("layout failed to load")
	}
	if got := st.GroupOf["growth"]; got != "marketing" {
		t.Fatalf("prefixed-only agent's group = %q, want marketing under the bare key -- the "+
			"heal dropped an agent that had no bare entry to win", got)
	}
	// And the dead-group rule: marketing was not in groups; it must surface.
	found := false
	for _, g := range st.Groups {
		if g == "marketing" {
			found = true
		}
	}
	if !found {
		t.Error("group referenced only by a healed entry was not surfaced in Groups; its " +
			"agents render nowhere in the modal")
	}
}

// TestLoadLayout_CrossMachineGroupKeysDroppedWhole pins BOTH halves of the
// family boundary as it stands after the remote-group rule (2026-08-15,
// support:pm rendered nowhere):
//
//  1. A cross-machine group_of entry is DROPPED — remote panes never carry a
//     persisted group, because a group routes them to a window that has no
//     stream for them. (This replaced the earlier "passes through untouched"
//     doctrine; the operator approved remote agents displaying on window 1
//     only, in their machine section.)
//  2. It is dropped WHOLE, never normalized onto the local name — the
//     over-broad strip-any-colon mutation must still die here (the
//     fleetStoreKey lesson): workbench:eng1 is a DISTINCT agent, and its
//     group must not leak onto a local eng1.
func TestLoadLayout_CrossMachineGroupKeysDroppedWhole(t *testing.T) {
	root := writeQkwcLayout(t, "", `grid: 2x2
mode: grid
groups:
    - core
group_of:
    "workbench:eng1": core
    eng1: core
`)
	st, ok := LoadLayout(root, []string{"workbench:eng1", "eng1"})
	if !ok {
		t.Fatal("layout failed to load")
	}
	if got, has := st.GroupOf["workbench:eng1"]; has {
		t.Fatalf("cross-machine group entry survived the load (GroupOf[workbench:eng1] = %q); "+
			"a persisted group routes a remote pane to a window that cannot draw it", got)
	}
	// The identity tripwire: dropping must not have become renaming. The
	// local eng1 keeps ITS OWN entry and only its own.
	if got := st.GroupOf["eng1"]; got != "core" {
		t.Fatalf("local eng1's group entry damaged by the cross-machine drop: %q", got)
	}
}

// TestLoadLayout_HealPersistsOnce is ini-m495's rule on this store: the
// second load finds a clean file -- proven on the DISK BYTES, not on a flag
// the heal sets (a flag is the heal grading its own homework).
func TestLoadLayout_HealPersistsOnce(t *testing.T) {
	root := writeQkwcLayout(t, "", qkwcSeed)
	panes := []string{"eng1", "eng2", "qa1"}

	if _, ok := LoadLayout(root, panes); !ok {
		t.Fatal("first load failed")
	}
	data, err := os.ReadFile(layoutPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "window1:") {
		t.Fatalf("disk still carries the alias family after the healing load:\n%s\n"+
			"the heal is in-memory only -- ini-m495's exact disease, re-healing and "+
			"re-logging on every load forever", data)
	}
}

// TestSaveLayout_WriteGuardNormalizes is the write guard's OWN assertion (the
// jhm6 rule): a dirty in-memory map -- which the read heal never saw -- must
// not persist aliased keys. Deleting the read heal entirely leaves this test
// green and vice versa; each layer answers for itself.
func TestSaveLayout_WriteGuardNormalizes(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	st := DefaultLayoutState([]string{"eng1"})
	st.Groups = []string{"qa"}
	st.GroupOf = map[string]string{"window1:eng1": "qa"} // dirty, in memory only

	if err := SaveLayout(root, st); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(layoutPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "window1:") {
		t.Fatalf("the save path persisted an aliased key:\n%s\nread-side healing without a "+
			"write guard is a leak with a mop -- a viewer re-contaminates its file on every save", data)
	}
	if !strings.Contains(string(data), "eng1: qa") {
		t.Fatalf("the write guard dropped the entry instead of normalizing it:\n%s", data)
	}
}

// TestLoadAssignment_HealPersistsOnAuthorityOnly is the absorbed ini-m495:
// the authority persists the heal once (second load: clean disk, no re-heal);
// a VIEWER's load leaves the file byte-identical, because the civ rule
// forbids a viewer writing shared session state.
func TestLoadAssignment_HealPersistsOnAuthorityOnly(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	legacy := "group_window:\n    eng: window2\n"
	path := filepath.Join(root, ".initech", "assignments.yaml")
	os.WriteFile(path, []byte(legacy), 0o644)

	// A viewer loads: heal in memory, file untouched.
	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.WindowOfGroup("eng"); got != "window-2" {
		t.Fatalf("in-memory heal missing: %q", got)
	}
	if b, _ := os.ReadFile(path); string(b) != legacy {
		t.Fatal("a plain LoadAssignment mutated the shared store; viewers call this and the " +
			"civ rule forbids their writing shared session state")
	}

	// The authority persists.
	a.PersistHealIfNeeded()
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "window2") && !strings.Contains(string(b), "window-2") {
		t.Fatalf("authority persist left the legacy form on disk:\n%s", b)
	}

	// Idempotence: the next load has nothing to heal.
	a2, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	if a2.healed {
		t.Fatal("second load still healing; the persist did not converge the disk form -- " +
			"the every-reconnect log noise ini-m495 observed live for hours")
	}
}

// TestPersistHealIfNeeded_Refusals pins the function's own guards: a
// read-only fallback store never persists (the operator's real state is
// still in the unreadable file -- writing would convert a loud parse error
// into silent erasure), and an unhealed store never writes (a spurious save
// on every startup is churn the atomic writer should not be asked to hide).
// The authority-vs-viewer gating lives at the call site (only window 1
// calls); the civ half proven here is that plain LoadAssignment never
// writes, in TestLoadAssignment_HealPersistsOnAuthorityOnly.
func TestPersistHealIfNeeded_Refusals(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	path := filepath.Join(root, ".initech", "assignments.yaml")

	// Unhealed store: no write at all.
	os.WriteFile(path, []byte("group_window:\n    qa: window-2\n"), 0o644)
	a, err := LoadAssignment(root, WindowOne)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	a.PersistHealIfNeeded()
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("an unhealed store was rewritten on PersistHealIfNeeded; every startup would " +
			"churn the file for nothing")
	}

	// Read-only fallback: must refuse even when marked healed.
	ro := newFallbackAssignment(root)
	ro.healed = true
	ro.PersistHealIfNeeded()
	if ro.healed == false && !ro.readOnly {
		t.Error("a read-only fallback store persisted; the operator's real state is still in " +
			"the unreadable file and was just silently erased")
	}
}
