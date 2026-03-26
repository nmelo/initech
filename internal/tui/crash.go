package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// safeGo launches fn in a goroutine with panic recovery. On panic, it writes
// a crash report to .initech/crash.log, prints to stderr, restores the
// terminal via screen.Fini(), and signals the TUI to shut down via quitCh.
//
// Every goroutine spawned by the TUI or its panes should use this instead of
// bare "go" to ensure panics never silently kill the process.
func (t *TUI) safeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				report := crashLog(t.projectRoot, t.version, r)
				fmt.Fprint(os.Stderr, report)
				if t.screen != nil {
					t.screen.Fini()
				}
				// Signal the main loop to exit. Close is safe even if
				// already closed (we recover from that too).
				func() {
					defer func() { recover() }()
					close(t.quitCh)
				}()
			}
		}()
		fn()
	}()
}

// crashLog appends a panic report to the crash log file. The report includes
// a timestamp, Go version, the panic value, and a full stack trace. Returns
// the formatted report for re-printing to stderr.
func crashLog(projectRoot string, version string, panicVal any) string {
	stack := make([]byte, 64*1024)
	n := runtime.Stack(stack, false)
	stack = stack[:n]

	report := fmt.Sprintf(
		"=== INITECH CRASH ===\nTime:    %s\nVersion: %s\nGo:      %s\nPanic:   %v\n\n%s\n",
		time.Now().Format(time.RFC3339),
		version,
		runtime.Version(),
		panicVal,
		stack,
	)

	// Write to .initech/crash.log (best effort).
	if projectRoot != "" {
		dir := filepath.Join(projectRoot, ".initech")
		os.MkdirAll(dir, 0700)
		path := filepath.Join(dir, "crash.log")
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
			f.WriteString(report)
			f.Close()
		}
	}

	return report
}
