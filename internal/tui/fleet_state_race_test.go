package tui

// Composed test for ini-8od: a secondary window's set_fleet_state arriving on
// the daemon's connection goroutine, WHILE window 1 mutates the same fleet
// state from its main loop.
//
// This is the pair nobody was driving. Each side was correct alone and tested
// alone, which is why `go test -race` was clean for months: the race needs both
// halves running at once, and no test composed them. Same shape as the rest of
// the ini-9ka seam findings (ini-3vi) -- the bug lives in the join, so a test
// per side cannot see it.
//
// It asserts the FINAL STATE, not merely the absence of a panic. mutate() is a
// read-modify-write over the whole document: it snapshots the maps for
// rollback, applies, then saves. The interesting failure is therefore not a
// crash but one side's change silently vanishing under the other side's
// snapshot -- and a no-panic assertion would pass on the broken code most of
// the time, which is a guard that cannot fail for the reason it exists.

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fleetRaceTUI builds a window-1 TUI with a real on-disk fleet store and a
// goroutine standing in for the main event loop, draining ipcCh the way Run()
// does. Returns the TUI, a Daemon wired to it exactly as startWindowServer
// wires one, and a stop func.
func fleetRaceTUI(t *testing.T) (*TUI, *Daemon, func()) {
	t.Helper()
	root := t.TempDir()

	tui := &TUI{
		projectRoot: root,
		windowID:    WindowOne, // window 1 is the fleet authority
		quitCh:      make(chan struct{}),
		ipcCh:       make(chan ipcAction, 64),
	}
	// Populate the store and the projection the renderer reads.
	tui.fleetState()

	// The simulated main loop. Every runOnMain op executes here, on ONE
	// goroutine -- which is the property the fix relies on.
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		for {
			select {
			case op := <-tui.ipcCh:
				op.fn()
				close(op.done)
			case <-tui.quitCh:
				return
			}
		}
	}()

	d := &Daemon{onFleetState: tui.applyFleetStateCmd}

	return tui, d, func() {
		close(tui.quitCh)
		<-loopDone
	}
}

// TestFleetState_SecondaryCommandRacesWindowOneKeypress is the ini-8od
// regression: run both writers at once, under -race, and require every update
// to survive.
//
// Run with -race; without it this still catches lost updates, which is the
// half a race detector cannot see.
func TestFleetState_SecondaryCommandRacesWindowOneKeypress(t *testing.T) {
	tui, d, stop := fleetRaceTUI(t)
	defer stop()

	const n = 40
	var wg sync.WaitGroup

	// SIDE B -- the secondary window, entering through the REAL wire handler on
	// what is, in production, a per-connection goroutine.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload, err := json.Marshal(FleetStateCmd{
				Action: "set_fleet_state",
				Name:   fmt.Sprintf("secondary-%d", i),
				Field:  "hidden",
				On:     true,
			})
			if err != nil {
				t.Errorf("marshal: %v", err)
				return
			}
			if resp := d.handleSetFleetState(payload); !resp.OK {
				t.Errorf("secondary set_fleet_state %d refused: %s", i, resp.Error)
			}
		}(i)
	}

	// SIDE A -- window 1's own keybinding. Its mutations run ON the main loop,
	// which is what the input handler does, so this goes through runOnMain too
	// rather than pretending a keypress happens on a random goroutine.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("local-%d", i)
			tui.runOnMain(func() {
				if err := tui.setProtected(key, true); err != nil {
					t.Errorf("local setProtected %d: %v", i, err)
				}
			})
		}(i)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("fleet mutations did not complete; a marshalled command is blocked (deadlock between the daemon goroutine and the main loop?)")
	}

	// NO LOST UPDATES. Every key from both sides must be present: mutate() is a
	// read-modify-write over the whole document, so an unsynchronised pair
	// loses entries to each other's snapshots without ever crashing.
	var missingHidden, missingProtected []string
	tui.runOnMain(func() {
		fs := tui.fleetState()
		for i := 0; i < n; i++ {
			if !fs.IsHidden(fmt.Sprintf("secondary-%d", i)) {
				missingHidden = append(missingHidden, fmt.Sprintf("secondary-%d", i))
			}
			if !fs.IsProtected(fmt.Sprintf("local-%d", i)) {
				missingProtected = append(missingProtected, fmt.Sprintf("local-%d", i))
			}
		}
	})
	if len(missingHidden) > 0 {
		t.Errorf("%d/%d secondary-window updates were LOST: %v",
			len(missingHidden), n, truncateKeys(missingHidden))
	}
	if len(missingProtected) > 0 {
		t.Errorf("%d/%d window-1 updates were LOST: %v",
			len(missingProtected), n, truncateKeys(missingProtected))
	}

	// The projection the renderer reads must agree with the store EXACTLY --
	// cardinality AND membership, not either alone. This test has already
	// proven that shape twice over: its original count assertion caught the
	// translation loop inserting into the map it was ranging over (Go may
	// visit keys added mid-iteration, so aliases were re-aliased into
	// nondeterministic "window1:window1:x" junk -- 92 entries where 80
	// belonged), and the interim presence-only weakening would have let that
	// junk ship.
	//
	// SINCE ini-yc03 THE EXPECTED SET IS CANONICAL-ONLY. It used to be each
	// key in its store form PLUS one "window1:" alias, because lookups were
	// keyed by the observer form. Identity is canonical at the point it is
	// computed now, so the alias is not merely unnecessary -- its presence
	// would mean the doubling has grown back, and this assertion is the thing
	// that would catch it. The contract is unchanged and strictly stronger.
	tui.runOnMain(func() {
		wantHidden := make(map[string]bool, n)
		wantProtected := make(map[string]bool, n)
		for i := 0; i < n; i++ {
			wantHidden[fmt.Sprintf("secondary-%d", i)] = true
			wantProtected[fmt.Sprintf("local-%d", i)] = true
		}
		for k := range wantHidden {
			if !tui.layoutState.Hidden[k] {
				t.Errorf("projected Hidden missing %q -- the renderer would show a different "+
					"fleet than the store holds", k)
			}
		}
		for k := range tui.layoutState.Hidden {
			if !wantHidden[k] {
				t.Errorf("projected Hidden carries unexpected key %q (alias junk or field crossover)", k)
			}
		}
		for k := range wantProtected {
			if !tui.layoutState.Protected[k] {
				t.Errorf("projected Protected missing %q", k)
			}
		}
		for k := range tui.layoutState.Protected {
			if !wantProtected[k] {
				t.Errorf("projected Protected carries unexpected key %q", k)
			}
		}
	})
}

// TestFleetState_CommandDuringShutdownReportsFailure pins the other half of the
// marshalling contract: when the main loop is gone, the command CANNOT run, and
// saying "ok" would tell a secondary window its change applied when nothing was
// written.
func TestFleetState_CommandDuringShutdownReportsFailure(t *testing.T) {
	tui, d, stop := fleetRaceTUI(t)
	stop() // main loop gone, quitCh closed

	payload, err := json.Marshal(FleetStateCmd{
		Action: "set_fleet_state", Name: "eng1", Field: "hidden", On: true,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp := d.handleSetFleetState(payload)
	if resp.OK {
		t.Fatal("a set_fleet_state during shutdown reported OK. The mutation never ran, and a " +
			"secondary window told 'applied' for a change that was silently dropped is worse " +
			"than one told 'refused'")
	}
	if tui.fleetState().IsHidden("eng1") {
		t.Error("the change was applied despite the session shutting down")
	}
}

func truncateKeys(keys []string) []string {
	if len(keys) > 6 {
		return append(keys[:6:6], "...")
	}
	return keys
}
