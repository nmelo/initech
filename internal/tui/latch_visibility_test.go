package tui

import (
	"strings"
	"testing"
	"time"
)

// A deferral a sender cannot act on is why the live incident ran for hours:
// "deferred" alone does not separate a dialog opened a second ago from one
// holding four messages since this morning.
func TestDeferredMsg_CarriesAgeAndDepth(t *testing.T) {
	got := deferredMsg("super", 47*time.Minute, 4)
	for _, want := range []string{"super", "47m", "4 queued"} {
		if !strings.Contains(got, want) {
			t.Errorf("deferral does not say %q: %q", want, got)
		}
	}
}

// A latch just raised has no age yet; the message must still be well formed
// rather than claiming "0s".
func TestDeferredMsg_FreshLatchOmitsAMisleadingAge(t *testing.T) {
	got := deferredMsg("super", 0, 1)
	if strings.Contains(got, "0s") || strings.Contains(got, "0m") {
		t.Errorf("a just-raised latch reported a fake age: %q", got)
	}
	if !strings.Contains(got, "1 queued") {
		t.Errorf("depth missing: %q", got)
	}
}

func TestLatchAge_ReportsNothingWhenNoLatchIsUp(t *testing.T) {
	p := &Pane{name: "eng1"}
	if age, latched := p.latchAge(time.Now()); latched || age != 0 {
		t.Errorf("an unlatched pane reported latched=%v age=%v", latched, age)
	}
}

func TestLatchAge_MeasuresFromWhenTheLatchRose(t *testing.T) {
	p := latchedPane(t) // raised two hours ago
	age, latched := p.latchAge(time.Now())
	if !latched {
		t.Fatal("latched pane reported no latch")
	}
	if age < 90*time.Minute {
		t.Errorf("age %v does not reflect a latch raised two hours ago", age)
	}
}
