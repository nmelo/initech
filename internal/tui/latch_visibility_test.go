package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
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

// TestAllPanes_ReportsTheLatchOnAStatusListing closes a reachability gap:
// deferredMsg and latchAge are tested directly above, but nothing drove
// TUI.AllPanes() itself -- the function `initech status` actually calls --
// with a latched pane. A wiring bug here (the ipc.go:124 type assertion, or
// the age/queued-count writes inside it) would be invisible to every cell
// that only exercises the two primitives in isolation, the same shape as
// ini-1z6i's PopQueuedMessage/drainModalQueue gap on this same bead's
// sibling.
func TestAllPanes_ReportsTheLatchOnAStatusListing(t *testing.T) {
	p := latchedPane(t) // raised two hours ago, corroborated
	p.EnqueueMessage("held while the dialog is up", true)
	p.EnqueueMessage("second held message", true)

	tui := &TUI{panes: []PaneView{p}}
	infos, ok := tui.AllPanes()
	if !ok {
		t.Fatal("AllPanes returned ok=false")
	}
	if len(infos) != 1 {
		t.Fatalf("AllPanes returned %d entries, want 1", len(infos))
	}

	info := infos[0]
	if !info.ModalLatched {
		t.Error("ModalLatched = false for a corroborated, latched pane")
	}
	if info.ModalAgeSec < 90*60 {
		t.Errorf("ModalAgeSec = %d, want at least 5400 (90m) for a latch raised two hours ago",
			info.ModalAgeSec)
	}
	if info.QueuedCount != 2 {
		t.Errorf("QueuedCount = %d, want 2", info.QueuedCount)
	}
}

// TestAllPanes_OmitsLatchFieldsWhenNoLatchIsUp is the positive control for
// the cell above: without it, a mutant that always sets ModalLatched=true
// would pass a check that only ever looks at the latched case.
func TestAllPanes_OmitsLatchFieldsWhenNoLatchIsUp(t *testing.T) {
	p := &Pane{name: "eng1", emu: vt.NewSafeEmulator(80, 24), alive: true}
	tui := &TUI{panes: []PaneView{p}}

	infos, ok := tui.AllPanes()
	if !ok {
		t.Fatal("AllPanes returned ok=false")
	}
	if len(infos) != 1 {
		t.Fatalf("AllPanes returned %d entries, want 1", len(infos))
	}
	info := infos[0]
	if info.ModalLatched || info.ModalAgeSec != 0 || info.QueuedCount != 0 {
		t.Errorf("an unlatched pane reported latch fields: %+v", info)
	}
}
