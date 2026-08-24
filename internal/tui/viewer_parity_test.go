package tui

// The two ini-6m4 guards the first delivery DESCRIBED but never committed
// (qa1's narrow FAIL): the tiers gate on a viewer, and the follower's
// fleet-state re-read on modal open. Landed here with their mutations re-run
// against these committed forms -- a described mutation result transfers to
// nothing.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

// TestAgentsTiersActive_ViewerRendersTiers pins the tier gate (ini-6m4
// symptom 1). The old predicate asked WindowListen != "" -- but viewerProject
// deliberately CLEARS WindowListen, because a viewer serves nothing. So a
// secondary window could never render monitor tiers BY CONSTRUCTION, while
// window 1 rendered them from the same assignment data. The question the gate
// must ask is "is this fleet multi-window?", which is true for the process
// that serves it AND for the process that is one.
func TestAgentsTiersActive_ViewerRendersTiers(t *testing.T) {
	cases := []struct {
		name string
		proj *config.Project
		want bool
	}{
		{"window 1 of a multi-window fleet (serves)", &config.Project{WindowListen: ":7500"}, true},
		{"secondary window (is one; WindowListen deliberately empty)", &config.Project{PeerName: "window-2"}, true},
		{"ordinary single-window session", &config.Project{}, false},
		{"nil project", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tui := newTestTUI()
			tui.project = tc.proj
			if got := tui.agentsTiersActive(); got != tc.want {
				t.Errorf("agentsTiersActive = %v, want %v -- a viewer whose gate reads "+
					"WindowListen shows a flat modal while window 1 shows tiers from the "+
					"same assignment data", got, tc.want)
			}
		})
	}
}

// TestOpenAgentsModal_AuthorityKeepsItsOwnState pins the other half: window 1
// OWNS fleet state, its in-memory copy IS the truth, and re-reading the file
// on modal open would discard any not-yet-persisted state and turn every
// modal open into a needless disk read. The refresh is follower-only.
func TestOpenAgentsModal_AuthorityKeepsItsOwnState(t *testing.T) {
	root := t.TempDir()
	tui := newTestTUI(testPane("pmm"))
	tui.projectRoot = root
	tui.windowID = WindowOne // the authority
	before := tui.fleetState()

	tui.openAgentsModal()

	if tui.fleet != before {
		t.Fatal("window 1 dropped its fleet store on modal open; the authority's memory is " +
			"the truth and must not be replaced by a file re-read")
	}
}

// TestFleetProjection_TranslatesKeysForFollowerLookups pins the committed
// rig's catch: the fleet store keys agents the way WINDOW 1 names them (plain
// names), while a follower's panes key as "window1:<name>" -- so the follower
// re-read the store correctly and then missed on every lookup, which is the
// operator's all-[x] modal by a second road. The projection carries both key
// forms; the write path normalizes back to the store's form.
func TestFleetProjection_TranslatesKeysForFollowerLookups(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".initech"), 0o755)
	os.WriteFile(filepath.Join(root, ".initech", "fleet-state.yaml"),
		[]byte("hidden:\n    - pmm\n"), 0o644)

	tui := newTestTUI()
	tui.projectRoot = root
	tui.windowID = "window-2"
	tui.fleetState()

	// Since ini-yc03 the projection is CANONICAL-ONLY. The follower used to
	// need a "window1:pmm" alias here because its lookups were keyed by the
	// observer form; now every lookup goes through agentKey, so the canonical
	// key is the only one that should exist -- and the alias must be ABSENT,
	// or the doubling this bead removed has grown back.
	if !tui.layoutState.Hidden["pmm"] {
		t.Fatal("window 1's hide is missing from the follower's projection under the agent's " +
			"canonical identity, so the follower cannot see it at all")
	}
	if _, aliased := tui.layoutState.Hidden["window1:pmm"]; aliased {
		t.Fatal("the projection carries an observer-relative alias again; canonical identity " +
			"makes it unnecessary, and the doubling that produced it was itself a defect source")
	}
}

// TestFollowerWritesReachTheStoreCanonically pins the reverse direction, which
// this suite has always asserted: a follower toggling an agent must reach the
// store under the agent's canonical identity, or the store grows one entry per
// window identity for the same agent.
//
// The contract is unchanged; its OWNER moved. fleetStoreKey normalized one
// store's keys at the write site (a per-field fence); since ini-yc03 the
// identity is canonical where it is computed, so the same guarantee is a
// property of agentKey and holds for every fleet-scoped field at once.
func TestFollowerWritesReachTheStoreCanonically(t *testing.T) {
	// The same agent, as window 1 sees it and as a follower sees it.
	windowOneView := &mockPaneView{name: "pmm"}
	followerView := &mockPaneView{name: "pmm", host: WindowOnePeerName}
	if got, want := agentKey(followerView), agentKey(windowOneView); got != want {
		t.Errorf("follower resolves pmm to %q, window 1 to %q -- the store would grow one "+
			"entry per window identity for one agent", got, want)
	}
	if got := agentKey(windowOneView); got != "pmm" {
		t.Errorf("window 1's own key = %q, want pmm (it passes through unchanged)", got)
	}
	// Cross-machine host prefixes are IDENTITY, not observer decoration.
	crossMachine := &mockPaneView{name: "eng1", host: "workbench"}
	if got := agentKey(crossMachine); got != "workbench:eng1" {
		t.Errorf("agentKey(workbench/eng1) = %q; cross-machine keys are NOT window-1 aliases "+
			"and must survive untouched", got)
	}
}
