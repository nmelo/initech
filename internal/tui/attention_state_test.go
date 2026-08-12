package tui

// attention_state_test.go covers StateWaitingInput itself: how it is set, how it
// clears, and -- the part that actually bites -- how it interacts with the code
// that was already switching on activity before this state existed (ini-2x8.1).

import (
	"testing"
	"time"
)

// TestUpdateActivity_WaitingOutranksByteRecency is the load-bearing one. An open
// dialog keeps repainting, so PTY bytes keep arriving; if byte recency were
// evaluated first it would relabel the pane StateRunning on the very next tick
// and erase the state one tick after the detector set it. That is the recorded
// codex gap verbatim -- a permission dialog reading as running despite no work.
func TestUpdateActivity_WaitingOutranksByteRecency(t *testing.T) {
	p := &Pane{
		name:           "super",
		alive:          true,
		lastOutputTime: time.Now(), // bytes RIGHT NOW: the repainting dialog
	}
	p.SetWaitingInput("ship v2.6.0 now, or wait?")

	p.updateActivity()

	if got := p.Activity(); got != StateWaitingInput {
		t.Fatalf("Activity() = %v, want %v -- byte recency overwrote the waiting state, "+
			"which is exactly the failure this state exists to fix", got, StateWaitingInput)
	}
}

func TestUpdateActivity_WaitingOutranksIdleSilence(t *testing.T) {
	// The other side of the same coin: a dialog that is NOT repainting must read
	// as waiting, not as ordinary idle. "Idle" means nothing to do; this agent
	// has something to do and is blocked on a human.
	p := &Pane{
		name:           "pm",
		alive:          true,
		lastOutputTime: time.Now().Add(-10 * time.Minute),
	}
	p.SetWaitingInput("stripe or paypal first?")

	p.updateActivity()

	if got := p.Activity(); got != StateWaitingInput {
		t.Fatalf("Activity() = %v, want %v", got, StateWaitingInput)
	}
}

func TestUpdateActivity_DeadAndSuspendedStillOutrankWaiting(t *testing.T) {
	// A process that has exited is dead even if a dialog was open when it died;
	// reporting it as still awaiting the operator would put a permanently stuck
	// row in the list with no way to clear it.
	p := &Pane{name: "eng1", alive: false, lastOutputTime: time.Now()}
	p.SetWaitingInput("something")
	p.updateActivity()
	if got := p.Activity(); got != StateDead {
		t.Errorf("dead pane: Activity() = %v, want %v", got, StateDead)
	}

	p2 := &Pane{name: "eng2", alive: false, suspended: true, lastOutputTime: time.Now()}
	p2.SetWaitingInput("something")
	p2.updateActivity()
	if got := p2.Activity(); got != StateSuspended {
		t.Errorf("suspended pane: Activity() = %v, want %v", got, StateSuspended)
	}
}

func TestSetWaitingInput_DoesNotRestartTheClockOnRepeatedCalls(t *testing.T) {
	// A detector re-asserting the same open dialog every tick is the normal
	// case, not an edge case. If each call reset the clock the list's durations
	// would never advance past one tick and the 2-minute chime reminder would
	// never come due.
	p := testPane("qa1")
	p.SetWaitingInput("first")
	_, first, _ := p.WaitingInput()

	time.Sleep(5 * time.Millisecond)
	p.SetWaitingInput("first")
	_, second, _ := p.WaitingInput()

	if !first.Equal(second) {
		t.Errorf("wait clock restarted: %v then %v", first, second)
	}
}

func TestSetWaitingInput_RefreshesPreviewWithoutDisturbingTheClock(t *testing.T) {
	// A detector may learn better text after the edge signal (a screen scrape
	// landing after the notification). Upgrading the row must not cost the
	// accumulated wait.
	p := testPane("super")
	p.SetWaitingInput("dialog detected")
	_, before, _ := p.WaitingInput()

	time.Sleep(5 * time.Millisecond)
	p.SetWaitingInput("ship v2.6.0 now, or wait for the site pass?")

	waiting, after, preview := p.WaitingInput()
	if !waiting {
		t.Fatal("pane stopped waiting after a preview refresh")
	}
	if !before.Equal(after) {
		t.Errorf("preview refresh restarted the clock: %v then %v", before, after)
	}
	if preview != "ship v2.6.0 now, or wait for the site pass?" {
		t.Errorf("preview = %q, want the refreshed text", preview)
	}
}

func TestClearWaitingInput_RestoresByteRecencyDerivedState(t *testing.T) {
	p := &Pane{name: "eng1", alive: true, lastOutputTime: time.Now()}
	p.SetWaitingInput("Bash: rm -rf build/")
	p.updateActivity()
	if p.Activity() != StateWaitingInput {
		t.Fatalf("setup failed: pane is not waiting")
	}

	p.ClearWaitingInput()
	p.updateActivity()

	if got := p.Activity(); got != StateRunning {
		t.Errorf("Activity() after clear = %v, want %v (byte recency resumes)", got, StateRunning)
	}
	if waiting, since, preview := p.WaitingInput(); waiting || !since.IsZero() || preview != "" {
		t.Errorf("WaitingInput() after clear = (%v, %v, %q), want (false, zero, \"\")", waiting, since, preview)
	}
}

func TestClearWaitingInput_SafeWhenNotWaiting(t *testing.T) {
	// lint:test-name-allow verifies the idempotent-clear contract with assertions below
	p := testPane("pm")
	p.ClearWaitingInput()
	if waiting, _, _ := p.WaitingInput(); waiting {
		t.Error("pane reports waiting after a clear that had nothing to clear")
	}
}

// TestUpdateActivity_WaitingSuppressesIdleWithBead guards against notifying
// twice for one condition. Before StateWaitingInput this could not happen by
// accident -- an open dialog repaints, so byte recency kept resetting the
// idle-with-bead flag. Now that waiting no longer reads as running, the
// suppression has to be explicit, and this is the test that says so.
func TestUpdateActivity_WaitingSuppressesIdleWithBead(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "eng1",
		alive:                 true,
		activity:              StateRunning,
		beadIDs:               []string{"ini-abc"},
		beadAssignedAt:        time.Now().Add(-2 * time.Hour),
		eventCh:               ch,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-65 * time.Second), // would fire idle-with-bead
	}
	p.SetWaitingInput("Do you want to proceed?")

	p.updateActivity()

	if evs := drainEvents(ch); len(evs) != 0 {
		t.Errorf("expected no idle-with-bead while blocked on the operator, got %d events: %+v", len(evs), evs)
	}
}

// TestUpdateActivity_IdleWithBeadStillFiresWhenNotWaiting is the negative
// control for the suppression above: it must not have silently disabled the
// existing notification for every agent.
func TestUpdateActivity_IdleWithBeadStillFiresWhenNotWaiting(t *testing.T) {
	ch := makeEventCh()
	p := &Pane{
		name:                  "eng1",
		alive:                 true,
		activity:              StateRunning,
		beadIDs:               []string{"ini-abc"},
		beadAssignedAt:        time.Now().Add(-2 * time.Hour),
		eventCh:               ch,
		idleWithBeadThreshold: defaultIdleWithBeadThreshold,
		lastOutputTime:        time.Now().Add(-65 * time.Second),
	}

	p.updateActivity()

	if evs := drainEvents(ch); len(evs) != 1 {
		t.Fatalf("expected the existing idle-with-bead notification to still fire, got %d events", len(evs))
	}
}

// TestActivityState_WaitingHasItsOwnLabelAndValue pins the enum: appended, so
// the pre-existing constants keep their values, and with a distinct label.
func TestActivityState_WaitingHasItsOwnLabelAndValue(t *testing.T) {
	if StateRunning != 0 || StateIdle != 1 || StateDead != 2 || StateSuspended != 3 {
		t.Errorf("existing ActivityState values shifted: running=%d idle=%d dead=%d suspended=%d",
			StateRunning, StateIdle, StateDead, StateSuspended)
	}
	if StateWaitingInput == StateIdle || StateWaitingInput == StateRunning {
		t.Error("StateWaitingInput must be a distinct state, not an alias of idle or running")
	}
	if got := StateWaitingInput.String(); got != "waiting" {
		t.Errorf("StateWaitingInput.String() = %q, want %q", got, "waiting")
	}
}

// ── The existing activity switch sites, asserted rather than assumed ──

// TestSuspendEligible_NeverSuspendsAnAgentWaitingOnTheOperator is the
// auto-suspend switch site. Killing an agent the operator is about to answer
// would take the question down with the process -- a new bug of exactly the kind
// this feature exists to prevent.
func TestSuspendEligible_NeverSuspendsAnAgentWaitingOnTheOperator(t *testing.T) {
	if suspendEligible(true, StateWaitingInput, "", false, false, false) {
		t.Error("an agent blocked on the operator was eligible for auto-suspension")
	}
}

// TestSuspendEligible_StillSuspendsAGenuinelyIdleAgent is the negative control
// for the assertion above: the guard must not have disabled auto-suspension
// wholesale.
func TestSuspendEligible_StillSuspendsAGenuinelyIdleAgent(t *testing.T) {
	if !suspendEligible(true, StateIdle, "", false, false, false) {
		t.Error("a genuinely idle, unbeaded, unpinned, unfocused agent should stay eligible")
	}
}

func TestSuspendEligible_PreservesEveryPreexistingExclusion(t *testing.T) {
	cases := []struct {
		name     string
		alive    bool
		activity ActivityState
		bead     string
		pinned   bool
		focused  bool
		inGrace  bool
	}{
		{name: "dead", alive: false, activity: StateIdle},
		{name: "already suspended", alive: true, activity: StateSuspended},
		{name: "state dead", alive: true, activity: StateDead},
		{name: "running", alive: true, activity: StateRunning},
		{name: "has a bead", alive: true, activity: StateIdle, bead: "ini-abc"},
		{name: "pinned", alive: true, activity: StateIdle, pinned: true},
		{name: "focused", alive: true, activity: StateIdle, focused: true},
		{name: "in resume grace", alive: true, activity: StateIdle, inGrace: true},
	}
	for _, c := range cases {
		if suspendEligible(c.alive, c.activity, c.bead, c.pinned, c.focused, c.inGrace) {
			t.Errorf("%s: should not be eligible for auto-suspension", c.name)
		}
	}
}
