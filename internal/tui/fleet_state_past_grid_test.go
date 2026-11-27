package tui

import (
	"os"
	"strings"
	"testing"
)

// ini-dc33 AC6: a pin stored past the grid (a Focus Live pin) loads, and a
// later save keeps it with its number.
func TestFleetStatePastGrid_APastGridPinLoadsAndSurvivesASave(t *testing.T) {
	root := tempProjectRoot(t)
	if err := os.WriteFile(fleetStatePath(root), []byte("live_pinned:\n    eng1: 0\n    eng2: 1\n    eng3: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, err := LoadFleetState(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := fs.LivePinnedMap(); got["eng3"] != 2 || len(got) != 3 {
		t.Fatalf("loaded %v, want eng3 at 2 with the others", got)
	}
	if err := fs.SetHidden("eng4", true); err != nil { // any save
		t.Fatal(err)
	}
	b, err := os.ReadFile(fleetStatePath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "eng3: 2") {
		t.Errorf("after a save the file reads:\n%s\nwant eng3: 2 kept", b)
	}
}
