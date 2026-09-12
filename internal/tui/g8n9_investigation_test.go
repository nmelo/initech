package tui

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// g8n9Viewer exercises the production loaders, follower projection, served
// ownership and applyLayout using the captured hover stores. RemotePane
// objects stand in only for already-arrived streams: no transport, terminal
// timing or emulator behavior is claimed by this planning instrument.
func g8n9Viewer(t *testing.T, historicalEight bool) *TUI {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"assignments.yaml", "layout.yaml", "fleet-state.yaml"} {
		data, err := os.ReadFile(filepath.Join("testdata", "g8n9", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".initech", name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, ".initech", "layout.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var saved PersistentLayout
	if err := yaml.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Order) != 41 {
		t.Fatalf("snapshot fleet size = %d, want 41", len(saved.Order))
	}
	a, err := LoadAssignment(root, "window-2")
	if err != nil {
		t.Fatal(err)
	}
	_, groupOf, ok := LoadFleetScopedLayout(root)
	if !ok {
		t.Fatal("fleet membership did not load")
	}
	if historicalEight {
		// A controlled variant matching the logged ownership SET, not a claim
		// that the current files are an exact snapshot of the earlier event.
		groupOf["pm"], groupOf["super"] = "core", "core"
	}
	u := newTestTUI()
	u.windowID, u.projectRoot, u.assignment = "window-2", root, a
	u.fleetState()
	for _, name := range saved.Order {
		u.panes = append(u.panes, &RemotePane{name: name, host: "window1", alive: true})
	}
	u.refreshMembershipIfFollower()
	u.applyServedPaneOwnership(computePaneOwnership(u.panes, a, groupOf,
		map[string]bool{"window-2": true}))
	return u
}

func TestG8N9_HiddenProjectionExplainsOwnedButUnplanned(t *testing.T) {
	for _, tc := range []struct {
		name            string
		historicalEight bool
		owned           string
	}{
		{"captured_stores", false, "eng1,eng2,eng3,eng4,eng5,pm,qa1,qa2,qa3,super"},
		{"logged_eight_ownership_control", true, "eng1,eng2,eng3,eng4,eng5,qa1,qa2,qa3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := g8n9Viewer(t, tc.historicalEight)
			owned := ownershipKeysFor(u.paneOwnership, u.windowID)
			if got := strings.Join(owned, ","); got != tc.owned {
				t.Fatalf("owned = %s, want %s", got, tc.owned)
			}
			if got := paneNameSet(u.visiblePanesForWindow()); got != tc.owned {
				t.Fatalf("ownership filter lost streams: %s", got)
			}
			for _, key := range owned {
				if !u.layoutState.Hidden[key] {
					t.Fatalf("owned agent %s is not hidden by captured fleet state", key)
				}
			}
			if len(u.plan.Panes) != 0 {
				t.Fatalf("all-hidden plan = %s, want empty", planPaneSet(u.plan))
			}
			t.Logf("streams=%d owned=%s hidden_owned=%d plan=%d", len(u.panes), tc.owned, len(owned), len(u.plan.Panes))

			// Change ONLY hidden state in the isolated store, then run the
			// same follower refresh and planner. Proves the planner reaches the
			// owned streams; blank cannot be blamed on missing streams, grid
			// dimensions, membership aliases, or ownership arrival order.
			before := maps.Clone(u.paneOwnership)
			if err := u.fleet.SetHidden("eng1", false); err != nil {
				t.Fatal(err)
			}
			u.refreshFleetIfFollower()
			u.recalcGrid(false)
			if got := planPaneSet(u.plan); got != "eng1" {
				t.Fatalf("unhide eng1 plan = %q, want eng1", got)
			}
			if !reflect.DeepEqual(before, u.paneOwnership) {
				t.Fatal("unhide unexpectedly changed ownership")
			}
			if err := u.fleet.ClearHidden(); err != nil {
				t.Fatal(err)
			}
			u.refreshFleetIfFollower()
			u.recalcGrid(false)
			if got := planPaneSet(u.plan); got != tc.owned {
				t.Fatalf("clear hidden plan = %q, want %s", got, tc.owned)
			}
			t.Logf("controls: unhide eng1 -> eng1; clear hidden -> %s", planPaneSet(u.plan))
		})
	}
}
