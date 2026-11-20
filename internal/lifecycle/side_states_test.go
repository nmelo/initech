package lifecycle

import (
	"slices"
	"testing"
)

// ini-ylx9: in a rig whose status.custom has no qa_passed, waiting_for_release
// is a side state by name, so it is never work (Working) and never stuck; and
// the chain deliver walks is unchanged.
func TestSideStates_WaitingForReleaseIsNeverWork(t *testing.T) {
	l := Parse("waiting_on_nelson,waiting,waiting_for_release")
	for _, s := range []string{"waiting_for_release", "waiting", "waiting_on_nelson", "deferred"} {
		if slices.Contains(l.Working(), s) {
			t.Errorf("%s is in Working %v", s, l.Working())
		}
	}
	if !slices.Contains(l.Working(), "in_progress") { // the twin: work is still work
		t.Errorf("in_progress missing from Working %v", l.Working())
	}
}

// The chain initech deliver walks (cmd/deliver.go reads Chain) does not move:
// naming a side state changes Working only.
func TestSideStates_DeliversChainIsUnchanged(t *testing.T) {
	want := []string{"open", "in_progress", "waiting_on_nelson", "waiting", "waiting_for_release", "closed"}
	if got := Parse("waiting_on_nelson,waiting,waiting_for_release").Chain; !slices.Equal(got, want) {
		t.Errorf("Chain %v, want %v", got, want)
	}
}
