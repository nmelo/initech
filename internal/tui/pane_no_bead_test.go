package tui

import (
	"os"
	"testing"
	"time"
)

// ini-wra.3: a pane working fifteen minutes with no bead reads "no bead".

// workFor ticks p's unattributed-work count once a second for d, starting at
// from, and returns the time it reached.
func workFor(p *Pane, from time.Time, d time.Duration) time.Time {
	now := from
	for end := from.Add(d); !now.After(end); now = now.Add(time.Second) {
		p.noteNoBeadWork(now)
	}
	return now.Add(-time.Second)
}

func setNoBeadActivity(p *Pane, st ActivityState) {
	p.mu.Lock()
	p.activity = st
	p.mu.Unlock()
}

func unattributedPane(t *testing.T) *Pane {
	t.Helper()
	tui, _ := newTestTUIWithScreen("eng1")
	p := tui.panes[0].(*Pane)
	setNoBeadActivity(p, StateRunning)
	return p
}

// AC1: fifteen minutes of work with no bead turns the marker on, in the
// ribbon and the list reply; at fourteen it is off. Red at base: no marker.
func TestNoBead_FifteenMinutesOfWorkShowsNoBead(t *testing.T) {
	p := unattributedPane(t)
	now := workFor(p, t0, 14*time.Minute)
	if p.Unattributed() || unattributedOf(p) {
		t.Fatal("14 minutes of work: the marker is on, want off")
	}
	if look, _ := p.chromeLook(false, 1); look.bead != "" {
		t.Errorf("14 minutes: ribbon bead %q, want blank", look.bead)
	}
	workFor(p, now.Add(time.Second), time.Minute)
	if !p.Unattributed() || !unattributedOf(p) {
		t.Fatal("15 minutes of work: the marker is off, want on")
	}
	if look, _ := p.chromeLook(false, 1); look.bead != noBeadLabel {
		t.Errorf("15 minutes: ribbon bead %q, want %q", look.bead, noBeadLabel)
	}
}

// AC2: idle time does not count toward the fifteen (it pauses the count), and
// a bead, set or derived, clears the marker at once and resets the count.
func TestNoBead_IdlePausesAndABeadClearsAndResets(t *testing.T) {
	p := unattributedPane(t)
	now := workFor(p, t0, 10*time.Minute)
	setNoBeadActivity(p, StateIdle)
	now = workFor(p, now.Add(time.Second), 30*time.Minute)
	if p.Unattributed() {
		t.Fatal("10 minutes of work and 30 idle turned the marker on: idle counted")
	}
	setNoBeadActivity(p, StateBackground)
	if !paneIsWorking(p) {
		t.Skip("fixture: background did not read as working")
	}
	now = workFor(p, now.Add(time.Second), 5*time.Minute)
	if !p.Unattributed() {
		t.Fatal("10 + 5 minutes of work around an idle stretch: marker off, want on (idle pauses, it does not reset)")
	}

	p.SetBead("ini-x", "")
	if p.Unattributed() {
		t.Error("an initech bead did not clear the marker at once")
	}
	p.noteNoBeadWork(now.Add(time.Second))
	p.SetBead("", "")
	now = workFor(p, now.Add(2*time.Second), time.Minute)
	if p.Unattributed() {
		t.Error("a bead did not reset the count: one more minute without it turned the marker back on")
	}

	// A derived bead clears it too.
	now = workFor(p, now.Add(time.Second), 15*time.Minute)
	if !p.Unattributed() {
		t.Fatal("fixture: 15 more minutes did not turn the marker on")
	}
	p.applyDerivedBeads([]bdBead{bdAt("ini-d", "eng1", t0)}, true, now)
	if p.Unattributed() {
		t.Error("a derived bead did not clear the marker")
	}
}

// AC4 feed it nothing: a pane that never worked, a pane that starts with a
// bead, a suspended pane, and the no-bead words of initech assign -m.
func TestNoBead_NotShownForIdleBeadedSuspendedOrDispatchedPanes(t *testing.T) {
	idle := unattributedPane(t)
	setNoBeadActivity(idle, StateIdle)
	workFor(idle, t0, 20*time.Minute)
	if idle.Unattributed() {
		t.Error("a pane that never worked shows the marker")
	}

	beaded := unattributedPane(t)
	beaded.SetBead("ini-b", "")
	workFor(beaded, t0, 20*time.Minute)
	if beaded.Unattributed() {
		t.Error("a pane with a bead shows the marker")
	}

	suspended := unattributedPane(t)
	suspended.mu.Lock()
	suspended.alive, suspended.suspended, suspended.activity = false, true, StateSuspended
	suspended.mu.Unlock()
	workFor(suspended, t0, 20*time.Minute)
	if suspended.Unattributed() {
		t.Error("a suspended pane shows the marker")
	}

	dispatched := unattributedPane(t)
	dispatched.SetNoBeadWork("look into the flake")
	workFor(dispatched, t0, 20*time.Minute)
	if dispatched.Unattributed() {
		t.Error("work dispatched with no bead (assign -m) counted as unattributed")
	}
}

// A stalled tick (the machine asleep) does not count as fifteen minutes.
func TestNoBead_AStalledTickIsNotCountedAsWork(t *testing.T) {
	p := unattributedPane(t)
	p.noteNoBeadWork(t0)
	p.noteNoBeadWork(t0.Add(time.Hour))
	if p.Unattributed() {
		t.Error("one tick an hour after the last counted the hour as work")
	}
}

// AC3: the marker writes nothing to the pane's PTY. The pane's terminal is a
// pipe; after fifteen minutes of ticks, a draw and every read, the pipe holds
// zero bytes. The twin: a send into the same pipe is seen, so the pipe is
// being read.
func TestNoBead_WritesNothingToThePane(t *testing.T) {
	p, r := paneWithPipe(t)
	p.name = "eng1"
	p.alive = true
	setNoBeadActivity(p, StateRunning)
	workFor(p, t0, 16*time.Minute)
	if !p.Unattributed() {
		t.Fatal("fixture: the marker did not turn on")
	}
	_ = unattributedOf(p)

	if n := pendingBytes(t, r); n != 0 {
		t.Fatalf("the no-bead marker wrote %d bytes to the agent's pane, want 0", n)
	}
	p.SendPaste(true)
	if n := pendingBytes(t, r); n == 0 {
		t.Fatal("twin: a write to the pane was not seen; the check cannot see bytes")
	}
}

// pendingBytes reads what the pipe holds now, without blocking.
func pendingBytes(t *testing.T, r *os.File) int {
	t.Helper()
	r.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	return n
}

// Reachability: the count runs from the real once-a-second tick,
// maintainModal, which both the TUI and the daemon run for their own panes.
func TestNoBead_TheMaintenanceTickCountsTheWork(t *testing.T) {
	p := unattributedPane(t)
	var last time.Time
	maintainModal([]PaneView{p}, t0, &last)
	maintainModal([]PaneView{p}, t0.Add(2*time.Second), &last)
	p.mu.Lock()
	counted := p.noBeadFor
	p.mu.Unlock()
	if counted <= 0 {
		t.Fatalf("two maintenance ticks two seconds apart counted %v of work, want > 0", counted)
	}
}
