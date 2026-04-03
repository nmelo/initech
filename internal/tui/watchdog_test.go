package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRenderWatchdog_DumpsOnStale(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow watchdog test in short mode")
	}
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
}

func TestRenderWatchdog_NoFalsePositive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow watchdog test in short mode")
	}
	dir := t.TempDir()
	var lastRender atomic.Int64
	quit := make(chan struct{})

	// Keep stamping fresh renders so watchdog never fires.
	lastRender.Store(time.Now().UnixNano())
	go func() {
		for {
			select {
			case <-quit:
				return
			case <-time.After(100 * time.Millisecond):
				lastRender.Store(time.Now().UnixNano())
			}
		}
	}()

	go renderWatchdog(&lastRender, 2*time.Second, dir, "test", quit)

	// Wait 3s (longer than the watchdog timeout) and verify no crash log.
	time.Sleep(3 * time.Second)
	close(quit)

	crashPath := filepath.Join(dir, ".initech", "crash.log")
	if _, err := os.Stat(crashPath); err == nil {
		t.Error("watchdog fired a false positive (crash.log exists)")
	}
}
