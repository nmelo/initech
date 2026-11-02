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
	appendCrashLog(projectRoot, report)

	return report
}

// crashLogCap is the size past which crash.log is rotated. One previous file
// is kept, so the two together hold a little over 40 MB at most.
const crashLogCap = 20 * 1024 * 1024

// appendCrashLog appends report to .initech/crash.log and returns the path, or
// "" when nothing was written. Past crashLogCap the file first moves to
// crash.log.1, replacing the previous one, and a new file starts. Every
// crash.log write goes through here. It is best effort and logs nothing: it
// runs while a panic is being handled, when the logger's lock may be held, so a
// rotation that fails is reported in the file itself and the write goes on.
func appendCrashLog(projectRoot, report string) string {
	if projectRoot == "" {
		return ""
	}
	dir := filepath.Join(projectRoot, ".initech")
	os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, "crash.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > crashLogCap {
		if err := os.Rename(path, path+".1"); err != nil {
			report = fmt.Sprintf("crash.log not rotated: %v\n", err) + report
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.WriteString(report); err != nil {
		return ""
	}
	return path
}
