package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// The watchdog tests run in a synctest bubble: renderWatchdog's 5s ticker
// and the tests' own sleeps share the bubble's clock, so they take no wall
// time.

func TestRenderWatchdog_DumpsOnStale(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow watchdog test in short mode")
	}
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		var lastRender atomic.Int64
		quit := make(chan struct{})

		// Set the last render to 20s ago so the watchdog fires immediately.
		lastRender.Store(time.Now().Add(-20 * time.Second).UnixNano())

		// Start watchdog with a very short timeout and check interval.
		go renderWatchdog(&lastRender, 1*time.Second, dir, "test", quit)

		// Wait for the watchdog to fire and write the crash log.
		deadline := time.Now().Add(10 * time.Second)
		crashPath := filepath.Join(dir, ".initech", "crash.log")
		for time.Now().Before(deadline) {
			if data, err := os.ReadFile(crashPath); err == nil && len(data) > 0 {
				content := string(data)
				if !strings.Contains(content, "RENDER WATCHDOG") {
					t.Errorf("crash.log missing RENDER WATCHDOG header")
				}
				if !strings.Contains(content, "goroutine") {
					t.Errorf("crash.log missing goroutine stacks")
				}
				close(quit)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		close(quit)
		t.Fatal("watchdog did not dump stacks within 10s")
	})
}

// watchdogStampRun runs renderWatchdog (2s timeout) beside a goroutine that
// stamps a fresh render every 100ms for stampFor, then waits 11s: past
// renderWatchdog's 5s check interval twice. It reports whether a crash log
// was written. Call it inside a synctest bubble.
func watchdogStampRun(t *testing.T, stampFor time.Duration) bool {
	t.Helper()
	dir := t.TempDir()
	var lastRender atomic.Int64
	quit := make(chan struct{})

	lastRender.Store(time.Now().UnixNano())
	stampDone := make(chan struct{})
	go func() {
		defer close(stampDone)
		stop := time.After(stampFor)
		for {
			select {
			case <-quit:
				return
			case <-stop:
				return
			case <-time.After(100 * time.Millisecond):
				lastRender.Store(time.Now().UnixNano())
			}
		}
	}()

	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		renderWatchdog(&lastRender, 2*time.Second, dir, "test", quit)
	}()

	time.Sleep(11 * time.Second)
	close(quit)
	<-stampDone
	<-watchdogDone

	_, err := os.Stat(filepath.Join(dir, ".initech", "crash.log"))
	return err == nil
}

// TestRenderWatchdog_NoFalsePositive keeps renders fresh across two
// watchdog checks (5s and 10s) and asserts no dump. The wait used to be 3s,
// shorter than the first check, so the watchdog never looked and a watchdog
// that always fires passed.
func TestRenderWatchdog_NoFalsePositive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow watchdog test in short mode")
	}
	synctest.Test(t, func(t *testing.T) {
		if watchdogStampRun(t, time.Hour) {
			t.Error("watchdog fired a false positive (crash.log exists)")
		}
	})
}

// TestRenderWatchdog_FiresWhenStampsStopInTheNoFalsePositiveFixture is
// NoFalsePositive's positive twin: the same fixture, but renders stop after
// 1s. The watchdog must dump, which proves the fixture's wait reaches a real
// check rather than ending before the watchdog looks.
func TestRenderWatchdog_FiresWhenStampsStopInTheNoFalsePositiveFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow watchdog test in short mode")
	}
	synctest.Test(t, func(t *testing.T) {
		if !watchdogStampRun(t, time.Second) {
			t.Error("watchdog did not dump after renders stopped; the fixture never reaches a watchdog check")
		}
	})
}
