package tui

// i7fr_harness_test.go is TEMPORARY INVESTIGATION INSTRUMENTATION (ini-i7fr),
// branch investigate/i7fr only. It never merges to main.
//
// It drives the REAL load/derive path with the FROZEN evidence stores, once as
// window 1 (local panes, bare pane keys) and once as window 2 (a viewer, whose
// panes are remote and therefore keyed "window1:<name>"), and prints what each
// window derives. The point is to see WHERE the two derivations part company
// using the shipped functions, not a re-implementation of them.
//
// The frozen stores are copied into a t.TempDir() per run; the evidence
// directory itself is opened read-only and never written.

import (
	"os"
	"path/filepath"
	"testing"
)

// Absolute, via env, so the harness cannot silently SKIP on a path slip and
// be mistaken for a run that found nothing.
var i7frEvidenceDir = os.Getenv("I7FR_EVIDENCE")

// i7frAgents is the fleet as the frozen layout.yaml describes it (8 agents).
var i7frAgents = []string{"super", "pm", "eng1", "eng2", "qa1", "pmm", "shipper", "growth"}

// i7frSeedProject copies the frozen stores into a scratch project root.
func i7frSeedProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, f := range []string{"layout.yaml", "assignments.yaml", "fleet-state.yaml"} {
		// Unset means "this investigation run was not requested" -- skip, so
		// the harness never breaks the suite for anyone else. Set-but-broken
		// means a path slip, which must FAIL: a silent skip there would read
		// as "measured nothing" when it actually measured nowhere.
		if i7frEvidenceDir == "" {
			t.Skip("set I7FR_EVIDENCE to the frozen evidence directory to run the ini-i7fr harness")
		}
		data, err := os.ReadFile(filepath.Join(i7frEvidenceDir, f))
		if err != nil {
			t.Fatalf("I7FR_EVIDENCE is set but %s is not readable: %v", f, err)
		}
		if err := os.WriteFile(filepath.Join(root, ".initech", f), data, 0o600); err != nil {
			t.Fatalf("seed %s: %v", f, err)
		}
	}
	return root
}

// i7frPanes builds the pane set a window would hold. host=="" is window 1
// (local panes, bare keys); host=="window1" is a viewer's remote panes.
func i7frPanes(host string) []PaneView {
	var out []PaneView
	for _, name := range i7frAgents {
		out = append(out, &mockPaneView{name: name, host: host, alive: true})
	}
	return out
}

// TestI7FR_DerivationByWindowRole runs the real path for both window roles.
func TestI7FR_DerivationByWindowRole(t *testing.T) {
	for _, role := range []struct {
		name string
		host string
	}{
		{"window1-local-panes", ""},
		{"window2-viewer-remote-panes", "window1"},
	} {
		t.Run(role.name, func(t *testing.T) {
			t.Setenv("INITECH_I7FR_WHO", role.name)
			root := i7frSeedProject(t)
			panes := i7frPanes(role.host)

			keys := make([]string, len(panes))
			for i, p := range panes {
				keys[i] = paneKey(p)
			}
			t.Logf("PANE KEYS: %v", keys)

			// The real load path, with this window's own pane keys.
			state, ok := LoadLayout(root, keys)
			t.Logf("LoadLayout ok=%v groups=%v group_of=%v order=%v",
				ok, state.Groups, state.GroupOf, state.Order)

			tui := &TUI{projectRoot: root, layoutState: state, panes: panes}
			// persist=false: the investigation must not write stores.
			tui.ensureGroups(false)
			t.Logf("AFTER ensureGroups: groups=%v group_of=%v", tui.layoutState.Groups, tui.layoutState.GroupOf)

			members := tui.agentsGroupMembers()
			for label, idx := range members {
				names := make([]string, 0, len(idx))
				for _, i := range idx {
					names = append(names, panes[i].Name())
				}
				t.Logf("MEMBERS[%s] = %v", label, names)
			}

			assign, err := LoadAssignment(root)
			if err != nil {
				t.Fatalf("LoadAssignment: %v", err)
			}
			// Tiers forced ACTIVE to isolate the universe/assignment question
			// from the separate gate question; the gate is traced separately.
			tiers := tui.agentsTierGroups(assign, true)
			for _, tg := range tiers {
				t.Logf("TIER %s -> %v", tg.windowID, tg.groups)
			}

			var unrendered []string
			inTier := map[string]bool{}
			for _, tg := range tiers {
				for _, g := range tg.groups {
					inTier[g] = true
				}
			}
			for label := range members {
				if !inTier[label] {
					unrendered = append(unrendered, label)
				}
			}
			t.Logf("BANDS WITH MEMBERS BUT NO TIER: %v", unrendered)
		})
	}
}

// TestI7FR_Window1LoadOrderingChangesTheUniverse asks whether WHEN window 1
// loads the layout (before its panes exist, or after) changes the group
// universe it ends up with. The cold derivation above does not reproduce the
// observed window-1 modal, and per the bead that gap is a finding to chase,
// not to explain away.
func TestI7FR_Window1LoadOrderingChangesTheUniverse(t *testing.T) {
	for _, c := range []struct {
		name       string
		keysAtLoad func(panes []PaneView) []string
	}{
		{"load AFTER panes exist (keys known)", func(p []PaneView) []string {
			keys := make([]string, len(p))
			for i, pv := range p {
				keys[i] = paneKey(pv)
			}
			return keys
		}},
		{"load BEFORE panes exist (no keys)", func(p []PaneView) []string { return nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("INITECH_I7FR_WHO", "window1/"+c.name)
			root := i7frSeedProject(t)
			panes := i7frPanes("") // window 1: local panes, bare keys

			state, ok := LoadLayout(root, c.keysAtLoad(panes))
			t.Logf("LoadLayout ok=%v groups=%v group_of_size=%d", ok, state.Groups, len(state.GroupOf))

			tui := &TUI{projectRoot: root, layoutState: state, panes: panes}
			tui.ensureGroups(false)
			t.Logf("AFTER ensureGroups: groups=%v", tui.layoutState.Groups)

			assign, _ := LoadAssignment(root)
			t.Logf("UNTIERED (gate off) would render: %v", tui.agentsTierGroups(assign, false))
			t.Logf("TIERED   (gate on)  would render: %v", tui.agentsTierGroups(assign, true))
		})
	}
}

// TestI7FR_ViewerWritesSharedStores is the WRITE-PATH evidence (bead step 3):
// which FILE does a viewer's save actually land in, and in which key space?
// A logged write closes this; reading the code does not.
func TestI7FR_ViewerWritesSharedStores(t *testing.T) {
	t.Setenv("INITECH_I7FR_WHO", "window2-viewer")
	root := i7frSeedProject(t)
	shared := filepath.Join(root, ".initech", "layout.yaml")
	perWindow := filepath.Join(root, ".initech", "layout-window-2.yaml")

	before, err := os.ReadFile(shared)
	if err != nil {
		t.Fatalf("read shared: %v", err)
	}

	// A viewer: remote panes hosted by window1, and a window identity that
	// says it is a secondary.
	panes := i7frPanes("window1")
	state, _ := LoadLayout(root, nil) // viewer has no local roles -> no keys
	tui := &TUI{projectRoot: root, layoutState: state, panes: panes, windowID: "window-2"}
	tui.ensureGroups(false)

	// The exact call every layout mutation funnels through.
	tui.saveLayoutIfConfigured()

	after, err := os.ReadFile(shared)
	if err != nil {
		t.Fatalf("read shared after: %v", err)
	}
	if string(before) == string(after) {
		t.Error("viewer save did NOT touch the shared layout.yaml (expected it to, given " +
			"saveLayoutIfConfigured -> SaveLayout -> windowID \"\")")
	} else {
		t.Logf("VIEWER WROTE THE SHARED window-1 FILE %s", shared)
		t.Logf("SHARED FILE AFTER VIEWER SAVE:\n%s", string(after))
	}
	if _, err := os.Stat(perWindow); err == nil {
		t.Logf("per-window file also exists: %s", perWindow)
	} else {
		t.Logf("PER-WINDOW FILE NEVER WRITTEN: %s (%v)", perWindow, err)
	}

	// And the assignment store: a viewer's group move writes the shared file
	// that window 1 caches at startup and, by design, never reloads.
	assign, err := LoadAssignment(root)
	if err != nil {
		t.Fatalf("LoadAssignment: %v", err)
	}
	if err := assign.MoveGroup("qa", "window-2"); err != nil {
		t.Fatalf("MoveGroup: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".initech", "assignments.yaml"))
	t.Logf("ASSIGNMENTS AFTER VIEWER MoveGroup:\n%s", string(data))
}
