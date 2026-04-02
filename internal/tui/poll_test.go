package tui

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPollQuota_SkipsBeforeDeadline(t *testing.T) {
	pane := newEmuPane("eng1", 80, 24)
	writeQuotaStatus(t, pane, 75)

	tui := &TUI{
		panes:        []PaneView{pane},
		quotaPercent: 33,
		quotaPollAt:  time.Now().Add(time.Minute),
	}

	tui.pollQuota()

	if tui.quotaPercent != 33 {
		t.Fatalf("quotaPercent = %d, want 33", tui.quotaPercent)
	}
}

func TestPollQuota_UsesFirstEligiblePaneWithQuota(t *testing.T) {
	deadPane := newEmuPane("dead", 80, 24)
	deadPane.alive = false

	suspendedPane := newEmuPane("suspended", 80, 24)
	suspendedPane.suspended = true

	remotePane := &RemotePane{alive: true}

	noQuotaPane := newEmuPane("eng1", 80, 24)
	noQuotaPane.emu.Write([]byte(fmt.Sprintf("\x1b[%d;1H%s", 24, "opus-4 │ idle │ session")))

	quotaPane := newEmuPane("eng2", 80, 24)
	writeQuotaStatus(t, quotaPane, 75)

	tui := &TUI{
		panes:       []PaneView{deadPane, suspendedPane, remotePane, noQuotaPane, quotaPane},
		quotaPollAt: time.Now().Add(-time.Second),
	}

	tui.pollQuota()

	if tui.quotaPercent != 75 {
		t.Fatalf("quotaPercent = %d, want 75", tui.quotaPercent)
	}
	if !tui.quotaPollAt.After(time.Now()) {
		t.Fatal("quotaPollAt should be moved into the future after polling")
	}
}

func TestPollQuota_KeepsLastKnownValueWhenNoPaneHasQuota(t *testing.T) {
	noQuotaPane := newEmuPane("eng1", 80, 24)
	noQuotaPane.emu.Write([]byte(fmt.Sprintf("\x1b[%d;1H%s", 24, "opus-4 │ idle │ session")))

	tui := &TUI{
		panes:        []PaneView{noQuotaPane, &RemotePane{alive: true}},
		quotaPercent: 61,
		quotaPollAt:  time.Now().Add(-time.Second),
	}

	tui.pollQuota()

	if tui.quotaPercent != 61 {
		t.Fatalf("quotaPercent = %d, want stale value 61", tui.quotaPercent)
	}
}

func TestStartBatteryPoller_NoBatteryStopsAfterInitialCheck(t *testing.T) {
	restore := stubBatteryPolling(t, func() (int, bool, bool) {
		return 0, false, false
	}, 5*time.Millisecond)
	defer restore()

	var calls atomic.Int32
	readBatteryFn = func() (int, bool, bool) {
		calls.Add(1)
		return 0, false, false
	}

	tui := &TUI{
		batteryPercent: -1,
		quitCh:         make(chan struct{}),
	}

	tui.startBatteryPoller()
	time.Sleep(25 * time.Millisecond)

	if tui.batteryPercent != -1 {
		t.Fatalf("batteryPercent = %d, want -1 when no battery exists", tui.batteryPercent)
	}
	if calls.Load() != 1 {
		t.Fatalf("readBattery calls = %d, want 1", calls.Load())
	}
	close(tui.quitCh)
}

func TestStartBatteryPoller_SeedsInitialStateAndUpdatesOnTick(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)

	restore := stubBatteryPolling(t, func() (int, bool, bool) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return 40, true, true
		}
		return 55, false, true
	}, 5*time.Millisecond)
	defer restore()

	tui := &TUI{
		batteryPercent: -1,
		quitCh:         make(chan struct{}),
	}

	tui.startBatteryPoller()

	if tui.batteryPercent != 40 || !tui.batteryCharging {
		t.Fatalf("initial battery state = (%d,%v), want (40,true)", tui.batteryPercent, tui.batteryCharging)
	}

	waitForCondition(t, func() bool {
		return tui.batteryPercent == 55 && !tui.batteryCharging
	})
	close(tui.quitCh)
}

func TestStartBatteryPoller_KeepsLastKnownValueWhenBatteryReadDropsOut(t *testing.T) {
	var calls atomic.Int32

	restore := stubBatteryPolling(t, func() (int, bool, bool) {
		if calls.Add(1) == 1 {
			return 72, false, true
		}
		return 0, false, false
	}, 5*time.Millisecond)
	defer restore()

	tui := &TUI{
		batteryPercent: -1,
		quitCh:         make(chan struct{}),
	}

	tui.startBatteryPoller()
	waitForCondition(t, func() bool { return calls.Load() >= 2 })
	close(tui.quitCh)

	if tui.batteryPercent != 72 || tui.batteryCharging {
		t.Fatalf("battery state after dropout = (%d,%v), want stale (72,false)", tui.batteryPercent, tui.batteryCharging)
	}
}

func writeQuotaStatus(t *testing.T, pane *Pane, pct int) {
	t.Helper()
	statusBar := fmt.Sprintf("opus-4 │ %d%% of limit │ session-abc", pct)
	pane.emu.Write([]byte(fmt.Sprintf("\x1b[%d;1H%s", 24, statusBar)))
}

func stubBatteryPolling(t *testing.T, readFn func() (int, bool, bool), interval time.Duration) func() {
	t.Helper()

	origRead := readBatteryFn
	origTicker := newBatteryTicker
	readBatteryFn = readFn
	newBatteryTicker = func(d time.Duration) *time.Ticker {
		return origTicker(interval)
	}

	return func() {
		readBatteryFn = origRead
		newBatteryTicker = origTicker
	}
}

func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
