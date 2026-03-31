// watchdog.go implements a render watchdog that detects when the TUI main loop
// stops rendering (deadlock, infinite loop, blocked syscall). When no render
// completes within the timeout, the watchdog dumps all goroutine stacks to
// .initech/crash.log so the blocking path can be identified post-mortem.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"
)

// renderWatchdog monitors the TUI render loop. It checks every checkInterval
// whether the last render completion timestamp has advanced. If no render
// completes within timeout, it dumps all goroutine stacks to crash.log.
//
// The watchdog exits when quit is closed.
func renderWatchdog(lastRender *atomic.Int64, timeout time.Duration, projectRoot, version string, quit chan struct{}) {
	const checkInterval = 5 * time.Second
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			last := lastRender.Load()
			if last == 0 {
				continue // No render has completed yet (startup).
			}
			elapsed := time.Since(time.Unix(0, last))
			if elapsed > timeout {
				dumpWatchdogStacks(projectRoot, version, elapsed)
				// Reset the timestamp so we don't dump repeatedly.
				lastRender.Store(time.Now().UnixNano())
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

	if projectRoot != "" {
		dir := filepath.Join(projectRoot, ".initech")
		os.MkdirAll(dir, 0700)
		path := filepath.Join(dir, "crash.log")
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
			f.WriteString(report)
			f.Close()
			LogError("watchdog", "goroutine stacks written", "path", path)
		}
	}
}
