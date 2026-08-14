package tui

// canonical_identity_test.go is ini-yc03's structural guard: fleet-scoped
// structures must be keyed by the CANONICAL agent identity, never by an
// observer-relative form.
//
// It guards the CLASS, not the instance. The 6m4/xq4r/qkwc/i7fr sequence was
// four fences around four fields while the boundary spanned every field, so a
// test naming individual fields would repeat that mistake. These walk whole
// structures and whole source trees instead.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// observerAlias is the production constant, deliberately NOT re-spelled here:
// a test that defines its own copy of the thing it is guarding stops guarding
// it the moment the two drift.
const observerAlias = observerAliasPrefix

// assertNoObserverKeys fails for any map key carrying the observer alias.
func assertNoObserverKeys[V any](t *testing.T, what string, m map[string]V) {
	t.Helper()
	for k := range m {
		if strings.HasPrefix(k, observerAlias) {
			t.Errorf(`%s is keyed by an OBSERVER-RELATIVE identity: %q.

Fleet-scoped state must be keyed by the canonical agent identity, or the same
agent has one identity per observing window and the parity invariant depends on
each field remembering to translate (ini-yc03; the boundary i7fr traced).`, what, k)
		}
	}
}

// TestFleetScopedStateIsCanonicallyKeyed is the negative control. A viewer's
// panes are alias-keyed, so before the identity edge is canonical EVERY
// fleet-scoped map it builds carries observer forms.
func TestFleetScopedStateIsCanonicallyKeyed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	viewer := viewerTUI(t, root, "super", "eng1")
	viewer.ensureGroups(false)
	viewer.layoutState.Hidden = map[string]bool{}
	viewer.layoutState.Protected = map[string]bool{}
	for _, p := range viewer.panes {
		viewer.layoutState.Hidden[agentKey(p)] = false
		viewer.layoutState.Protected[agentKey(p)] = true
	}

	assertNoObserverKeys(t, "layoutState.GroupOf", viewer.layoutState.GroupOf)
	assertNoObserverKeys(t, "layoutState.Hidden", viewer.layoutState.Hidden)
	assertNoObserverKeys(t, "layoutState.Protected", viewer.layoutState.Protected)

	for _, k := range viewer.layoutState.Order {
		if strings.HasPrefix(k, observerAlias) {
			t.Errorf("layoutState.Order carries an observer-relative entry %q -- ordering "+
				"fields are fleet-scoped too (ini-yc03 AC), and this is exactly the field "+
				"ini-qkwc's fence did not cover", k)
		}
	}
}

// TestAgentKeyStripsOnlyTheObserverAlias pins the edge's exact contract. The
// window-1 alias is presentation and must go; a REAL cross-machine host prefix
// is part of the agent's identity and must survive, or two machines' agents of
// the same name collapse into one.
func TestAgentKeyStripsOnlyTheObserverAlias(t *testing.T) {
	cases := []struct {
		name string
		pane PaneView
		want string
	}{
		{"local pane", &mockPaneView{name: "eng1"}, "eng1"},
		{"viewer's remote pane", &mockPaneView{name: "eng1", host: WindowOnePeerName}, "eng1"},
		{"cross-machine pane keeps its host", &mockPaneView{name: "eng1", host: "workbench"}, "workbench:eng1"},
		{"agent whose NAME resembles the alias", &mockPaneView{name: "window1-ops"}, "window1-ops"},
		{"cross-machine agent whose name resembles the alias",
			&mockPaneView{name: "window1-ops", host: "workbench"}, "workbench:window1-ops"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := agentKey(c.pane); got != c.want {
				t.Errorf("agentKey = %q, want %q", got, c.want)
			}
		})
	}
}

// TestCanonicalIdentityCollisionsStayDistinct is the fixture corollary: values
// chosen so that truncation, collision, or a mistranslation cannot masquerade
// as success. Same agent name on two different hosts, and a name that shares
// the alias's prefix, must all remain distinguishable.
func TestCanonicalIdentityCollisionsStayDistinct(t *testing.T) {
	panes := []PaneView{
		&mockPaneView{name: "eng1"},                          // window 1's local
		&mockPaneView{name: "eng1", host: WindowOnePeerName}, // the SAME agent, as a viewer sees it
		&mockPaneView{name: "eng1", host: "workbench"},       // a DIFFERENT agent, same name
		&mockPaneView{name: "window1-ops"},                   // name that starts like the alias
		&mockPaneView{name: "ops", host: "window1-lab"},      // host that starts like the alias
	}
	got := make([]string, len(panes))
	for i, p := range panes {
		got[i] = agentKey(p)
	}

	// The first two are the same agent seen from two windows: they MUST agree,
	// which is the whole point of a canonical identity.
	if got[0] != got[1] {
		t.Errorf("the same agent has two identities across windows: %q vs %q", got[0], got[1])
	}
	// Everything else must stay distinct from it and from each other.
	seen := map[string]int{}
	for i, k := range got {
		if i == 1 {
			continue // deliberate duplicate of index 0
		}
		if prev, dup := seen[k]; dup {
			t.Errorf("distinct agents collapsed onto one identity %q (indexes %d and %d) -- "+
				"a mistranslation would masquerade as success here", k, prev, i)
		}
		seen[k] = i
	}
	if got[3] != "window1-ops" {
		t.Errorf("an agent NAMED like the alias was mangled: %q", got[3])
	}
	if got[4] != "window1-lab:ops" {
		t.Errorf("a HOST named like the alias was mangled: %q", got[4])
	}
}

// TestObserverAliasIsConstructedInOneNamedPlace is the source-level half of the
// class guard: the AC asks for ONE named translation edge, so the alias prefix
// may be built in exactly one place. A second construction site is a second
// edge, which is how per-field fences reappear.
func TestObserverAliasIsConstructedInOneNamedPlace(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var sites []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "WindowOnePeerName+\":\"") ||
				strings.Contains(line, "WindowOnePeerName + \":\"") {
				sites = append(sites, fmt.Sprintf("%s:%d", e.Name(), i+1))
			}
		}
	}
	if len(sites) > 1 {
		t.Errorf(`the observer alias is constructed in %d places: %v

ini-yc03 requires ONE named translation edge. A second construction site is a
second edge, and per-field fences reappear from there.`, len(sites), sites)
	}
}
