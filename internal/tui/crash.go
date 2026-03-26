package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

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
		os.MkdirAll(dir, 0755)
		path := filepath.Join(dir, "crash.log")
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
			f.WriteString(report)
			f.Close()
		}
	}

	return report
}
