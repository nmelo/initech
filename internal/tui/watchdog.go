// watchdog.go implements a render watchdog that detects when the TUI main loop
// stops rendering (deadlock, infinite loop, blocked syscall). When no render
// completes within the timeout, the watchdog dumps all goroutine stacks to
// .initech/crash.log so the blocking path can be identified post-mortem.
package tui

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"time"
)

const (
	// watchdogCheckInterval is how often the watchdog reads the render clock.
	watchdogCheckInterval = 5 * time.Second
	// watchdogRepeat is the least time between two dumps of one freeze. The
	// first dump names the blocked path; a frozen loop stays frozen, so dumping
	// every timeout only filled the disk (ini-7rd: 1.6 MB a minute, 750 MB).
	watchdogRepeat = 5 * time.Minute
)

// watchdogCadence decides when a freeze is dumped: once at the first check
// past timeout, then at most once every repeat, until a render ends the freeze.
type watchdogCadence struct {
	timeout, repeat time.Duration
	lastDump        time.Time // Zero when no dump has been written for this freeze.
}

// step takes the time of the check and of the last completed render, and
// returns how long the loop has been frozen and whether a dump is due now.
func (c *watchdogCadence) step(now, lastRender time.Time) (time.Duration, bool) {
	stale := now.Sub(lastRender)
	if stale <= c.timeout {
		c.lastDump = time.Time{} // Rendering: the next freeze is a new one.
		return stale, false
	}
	if !c.lastDump.IsZero() && now.Sub(c.lastDump) < c.repeat {
		return stale, false
	}
	c.lastDump = now
	return stale, true
}

// renderWatchdog monitors the TUI render loop. It checks every
// watchdogCheckInterval whether the last render completion timestamp has
// advanced. If no render completes within timeout, it dumps all goroutine
// stacks to crash.log, at the cadence watchdogCadence states. It runs on its
// own goroutine, so a dump never touches the main loop, and it only reads
// lastRender, so each dump names how long the freeze has really lasted.
//
// The watchdog exits when quit is closed.
func renderWatchdog(lastRender *atomic.Int64, timeout time.Duration, projectRoot, version string, quit chan struct{}) {
	ticker := time.NewTicker(watchdogCheckInterval)
	defer ticker.Stop()
	cadence := watchdogCadence{timeout: timeout, repeat: watchdogRepeat}

	for {
		select {
		case <-ticker.C:
			last := lastRender.Load()
			if last == 0 {
				continue // No render has completed yet (startup).
			}
			if stale, due := cadence.step(time.Now(), time.Unix(0, last)); due {
				dumpWatchdogStacks(projectRoot, version, stale)
			}
		case <-quit:
			return
		}
	}
}

// dumpWatchdogStacks captures ALL goroutine stacks and writes them to
// .initech/crash.log. This is the key diagnostic for silent freezes:
// the stacks show exactly which goroutine is blocked and on what.
func dumpWatchdogStacks(projectRoot, version string, stale time.Duration) {
	// Capture all goroutine stacks (true = all goroutines, not just current).
	buf := make([]byte, 1024*1024) // 1MB should cover even large stack dumps.
	n := runtime.Stack(buf, true)
	buf = buf[:n]

	report := fmt.Sprintf(
		"=== RENDER WATCHDOG ===\nTime:      %s\nVersion:   %s\nGo:        %s\nStale for: %s\nDiagnosis: No render completed in %s. Main loop is likely blocked.\n\n--- All Goroutine Stacks ---\n%s\n",
		time.Now().Format(time.RFC3339),
		version,
		runtime.Version(),
		stale.Round(time.Second),
		stale.Round(time.Second),
		buf,
	)

	LogError("watchdog", "render stalled, dumping goroutine stacks",
		"stale", stale.Round(time.Second), "goroutines", runtime.NumGoroutine())

	if path := appendCrashLog(projectRoot, report); path != "" {
		LogError("watchdog", "goroutine stacks written", "path", path)
	}
}
