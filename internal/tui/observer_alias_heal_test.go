package tui

// observer_alias_heal_test.go covers ini-yc03's store heal: layout files
// written before the identity edge became canonical carry window-1 observer
// aliases, and canonical identity makes those entries UNREACHABLE rather than
// merely wrong -- so without the heal an upgraded fleet silently loses its
// saved arrangement and band membership for every aliased agent.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aliasedStore is a pre-yc03 layout file, in the exact shape ini-i7fr froze
// from the operator's fleet: order entirely window1:-prefixed (written by a
// viewer, then immortal because the staleness filter keeps any key with a
// colon), group_of canonical, and a cross-machine entry that must SURVIVE.
const aliasedStore = `grid: 4x2
mode: grid
order:
    - window1:super
    - window1:eng1
    - workbench:eng1
groups:
    - core
    - eng
group_of:
    window1:super: core
    eng1: eng
    workbench:eng1: eng
`

func writeStore(t *testing.T, root, body string) string {
	t.Helper()
	dir := filepath.Join(root, ".initech")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, "layout.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestHealStripsObserverAliasesAndKeepsCrossMachineIdentity(t *testing.T) {
	root := t.TempDir()
	writeStore(t, root, aliasedStore)

	groups, groupOf, ok := LoadFleetScopedLayout(root)
	if !ok {
		t.Fatal("LoadFleetScopedLayout !ok")
	}
	_ = groups

	if _, aliased := groupOf["window1:super"]; aliased {
		t.Error("group_of still carries an observer alias after load; canonical lookups miss it, " +
			"so the agent silently loses its band")
	}
	if got := groupOf["super"]; got != "core" {
		t.Errorf("group_of[super] = %q, want core -- the aliased entry must be HEALED, not dropped", got)
	}
	// The fixture corollary: a cross-machine agent shares the bare name "eng1"
	// with a window-1 agent. If the heal stripped host prefixes indiscriminately
	// they would collapse onto one identity and the mistranslation would look
	// like success.
	if got := groupOf["workbench:eng1"]; got != "eng" {
		t.Errorf("cross-machine identity workbench:eng1 was mangled by the heal (got band %q); "+
			"a real host prefix is identity, not observer decoration", got)
	}
	if got := groupOf["eng1"]; got != "eng" {
		t.Errorf("window 1's own eng1 lost its band (%q) -- it must stay DISTINCT from "+
			"workbench:eng1 rather than collapsing onto it", got)
	}
}

func TestHealCollapsesBothFormsOfOneAgentInOrder(t *testing.T) {
	root := t.TempDir()
	writeStore(t, root, "grid: 2x1\nmode: grid\norder:\n    - window1:eng1\n    - eng1\n    - workbench:eng1\n")

	state, ok := LoadLayout(root, []string{"eng1", "workbench:eng1"})
	if !ok {
		t.Fatal("LoadLayout !ok")
	}
	var bare, cross int
	for _, k := range state.Order {
		switch k {
		case "eng1":
			bare++
		case "workbench:eng1":
			cross++
		default:
			t.Errorf("order carries an unexpected key %q after heal", k)
		}
	}
	if bare != 1 {
		t.Errorf("the two forms of one agent produced %d order entries, want 1 -- a duplicate "+
			"would give the same agent two slots in the arrangement", bare)
	}
	if cross != 1 {
		t.Errorf("cross-machine eng1 appears %d times, want 1 (it is a different agent)", cross)
	}
}

// TestHealIsIdempotentAndSilentOnSecondLoad is pm's heal-once discipline: a
// second load performs ZERO heal actions, and the silence is the proof the
// first one persisted rather than being re-applied every time.
func TestHealIsIdempotentAndSilentOnSecondLoad(t *testing.T) {
	root := t.TempDir()
	writeStore(t, root, aliasedStore)

	_, first, ok := LoadFleetScopedLayout(root)
	if !ok {
		t.Fatal("first load !ok")
	}
	// Feed the healed result back through the heal: a second pass must be a
	// no-op. Comparing the maps IS the silence assertion -- any further change
	// means the heal is not converged and would keep rewriting forever.
	pl := PersistentLayout{GroupOf: first}
	for k := range first {
		pl.Order = append(pl.Order, k)
	}
	before := len(pl.Order)
	normalizePersistedIdentities(&pl)

	if len(pl.Order) != before {
		t.Errorf("second heal changed order length %d -> %d; the heal is not converged", before, len(pl.Order))
	}
	for k, v := range first {
		if pl.GroupOf[k] != v {
			t.Errorf("second heal altered group_of[%q]: %q -> %q", k, v, pl.GroupOf[k])
		}
	}
	if len(pl.GroupOf) != len(first) {
		t.Errorf("second heal changed group_of size %d -> %d", len(first), len(pl.GroupOf))
	}
}

// TestFollowerReadPathNeverPersistsTheHeal pins the seam between two rules that
// pull opposite ways, so neither quietly wins.
//
// ini-m495 says a heal must PERSIST or it re-heals forever, and
// LoadLayoutForWindow does exactly that. ini-la97 says a viewer never writes
// project-root state. LoadFleetScopedLayout is the FOLLOWER's read path, so it
// normalizes in memory and leaves the file alone; convergence is the
// authority's job on its own load. Getting this backwards would put a store
// write on the one path la97 exists to keep read-only.
func TestFollowerReadPathNeverPersistsTheHeal(t *testing.T) {
	root := t.TempDir()
	p := writeStore(t, root, aliasedStore)
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	groups, groupOf, ok := LoadFleetScopedLayout(root)
	if !ok {
		t.Fatal("load !ok")
	}
	_ = groups
	if _, aliased := groupOf["window1:super"]; aliased {
		t.Error("the follower read path returned an un-normalized alias")
	}

	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("the FOLLOWER read path wrote the store:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestAuthorityLoadPersistsTheHealOnce is the other side of that seam, and it
// is ini-m495's rule restated for the fields yc03 added: the authority's load
// converges the file so the next load heals nothing.
func TestAuthorityLoadPersistsTheHealOnce(t *testing.T) {
	root := t.TempDir()
	p := writeStore(t, root, aliasedStore)

	if _, ok := LoadLayout(root, []string{"super", "eng1", "workbench:eng1"}); !ok {
		t.Fatal("first load !ok")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "window1:") {
		t.Fatalf("the store still carries the alias family after the authority's healing load:\n%s\n"+
			"an in-memory-only heal re-heals and re-logs forever (ini-m495; measured on the "+
			"assignment store in ini-i7fr, re-firing every two seconds for hours)", data)
	}
	if !strings.Contains(string(data), "workbench:eng1") {
		t.Errorf("the heal removed a CROSS-MACHINE identity from disk:\n%s", data)
	}
}
