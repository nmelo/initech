package progress

import (
	"testing"
	"time"
)

// FamilyMoved is the one walk: any depth, newest of the family, safe on a
// cycle bd should never hold.
func TestFamilyStale_FamilyMovedWalksEveryDepthAndStopsOnACycle(t *testing.T) {
	t0 := famNow.Add(-time.Hour)
	parent := map[string]string{"b": "a", "c": "b", "x": "y", "y": "x"}
	moved := map[string]time.Time{"a": t0, "b": t0, "c": famNow, "x": t0, "y": t0.Add(time.Minute)}
	got := FamilyMoved(parent, moved)
	for id, want := range map[string]time.Time{"a": famNow, "b": famNow, "c": famNow, "x": t0.Add(time.Minute), "y": t0.Add(time.Minute)} {
		if !got[id].Equal(want) {
			t.Errorf("%s: %v, want %v", id, got[id], want)
		}
	}
}
