package tui

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
