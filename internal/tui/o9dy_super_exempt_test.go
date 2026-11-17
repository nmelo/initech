package tui

import (
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ini-o9dy: on a restart at 09:59 the ceiling refused every agent, super
// included, at load 30 of 30 with 0 sessions awake. The coordinator (the
// role named super) is never refused; every other agent is held as before.

// AC1: at load 30/30 super starts and eng1 is refused, with the load named.
func TestCeilingO9dy_SuperStartsAndEng1IsRefusedAtLoad30(t *testing.T) {
	tui := ceilingTUI(t, ceilingAt(t, 30), time.Now())
	if err := tui.admitAgent(nil, "super", "boot"); err != nil {
		t.Fatalf("super refused at load 30/30: %v", err)
	}
	err := tui.admitAgent(nil, "eng1", "boot")
	var e *ErrAtCeiling
	if !errors.As(err, &e) || !strings.HasPrefix(e.Words, "not started: the Mac is at its ceiling: load 30 at or over 30 (") {
		t.Fatalf("eng1 at load 30/30: %v, want refused naming the load", err)
	}
}

// AC2: every path: start, resume, restart and the wake a send causes; and
// with memory over instead of load.
func TestCeilingO9dy_EveryPathLetsSuperThrough(t *testing.T) {
	over := ceilingAt(t, 99)
	dead := testPane("super")
	dead.alive = false
	tui := ceilingTUI(t, over, time.Now(), dead)
	var spawned atomic.Int32
	tui.paneSpawner = fakePaneSpawner(t, &spawned)
	if resp := ipcCall(t, func(c net.Conn) { tui.handleIPCStart(c, IPCRequest{Target: "super"}) }); strings.Contains(resp.Error, "at its ceiling") {
		t.Errorf("start of super refused: %+v", resp)
	}

	parked := NewParkedPane(PaneConfig{Name: "super"}, 10, 40)
	tui = ceilingTUI(t, over, time.Now(), parked)
	tui.paneSpawner = fakePaneSpawner(t, &spawned)
	if resp := ipcCall(t, func(c net.Conn) { tui.handleIPCResume(c, IPCRequest{Target: "super"}) }); !resp.OK {
		t.Errorf("resume of super refused: %+v", resp)
	} else {
		np := tui.panes[0].(*Pane)
		t.Cleanup(func() { np.Close() })
	}

	running := testPane("super")
	tui = ceilingTUI(t, over, time.Now(), running)
	tui.paneSpawner = fakePaneSpawner(t, &spawned)
	if resp := ipcCall(t, func(c net.Conn) { tui.handleIPCRestart(c, IPCRequest{Target: "super"}) }); strings.Contains(resp.Error, "at its ceiling") {
		t.Errorf("restart of super refused: %+v", resp)
	}

	waking := NewParkedPane(PaneConfig{Name: "super"}, 10, 40)
	tui = ceilingTUI(t, over, time.Now(), waking)
	tui.paneSpawner = fakePaneSpawner(t, &spawned)
	if err := tui.resumePane(waking, "queued message"); isAtCeiling(err) {
		t.Errorf("the wake a send causes refused super: %v", err)
	} else if np, ok := tui.panes[0].(*Pane); ok && np != waking {
		t.Cleanup(func() { np.Close() })
	}

	lowMem := &fakeMachine{dir: t.TempDir(), freeKB: 4 * 1024 * 1024, load: 5}
	tui = ceilingTUI(t, lowMem, time.Now())
	if err := tui.admitAgent(nil, "super", "add"); err != nil {
		t.Errorf("super refused with memory under the floor: %v", err)
	}
	var e *ErrAtCeiling
	if err := tui.admitAgent(nil, "eng1", "add"); !errors.As(err, &e) || !strings.Contains(e.Words, "GB free at or under 8 GB") {
		t.Errorf("eng1 with memory under the floor: %v", err)
	}
}

// Rule 4: each exempt start is logged once, with what was over.
func TestCeilingO9dy_AnExemptStartIsLogged(t *testing.T) {
	logs := captureLogs(t)
	tui := ceilingTUI(t, ceilingAt(t, 30), time.Now())
	if err := tui.admitAgent(nil, "super", "boot"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(logs.String(), "[ceiling] super started over the ceiling (load 30/30)"); got != 1 {
		t.Fatalf("exempt start logged %d times, want 1; log:\n%s", got, logs.String())
	}
	under := ceilingTUI(t, ceilingAt(t, 5), time.Now())
	under.admitAgent(nil, "super", "boot")
	if got := strings.Count(logs.String(), "super started over the ceiling"); got != 1 {
		t.Fatalf("a start under the ceiling was logged as exempt; log:\n%s", logs.String())
	}
}

// Feed it nothing: a rig with no role named super starts nothing over the
// ceiling, and only the exact name is exempt.
func TestCeilingO9dy_OnlyTheRoleNamedSuperIsExempt(t *testing.T) {
	tui := ceilingTUI(t, ceilingAt(t, 30), time.Now())
	for _, name := range []string{"eng1", "pm", "shipper", "super2", "Super", "supervisor", ""} {
		if err := tui.admitAgent(nil, name, "boot"); !isAtCeiling(err) {
			t.Errorf("%q at load 30/30: %v, want refused", name, err)
		}
	}
}
