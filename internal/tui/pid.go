// pid.go manages the .initech/initech.pid file and post-mortem crash detection.
//
// On startup: write current PID. On clean exit: delete it.
// If the file exists at startup, the previous run exited uncleanly (signal,
// OOM, cgo crash). We log a warning and query the macOS system log and
// DiagnosticReports for evidence of what happened.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const pidFileName = "initech.pid"

// writePIDFile writes the current PID to .initech/initech.pid and returns a
// cleanup func that removes the file. The cleanup func is called on any clean
// exit (normal quit, returned error). Signal handlers and os.Exit paths bypass
// deferred cleanup, which is intentional: a missing cleanup means unclean exit.
func writePIDFile(projectRoot string) func() {
	if projectRoot == "" {
		return func() {}
	}
	dir := filepath.Join(projectRoot, ".initech")
	os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, pidFileName)
	content := fmt.Sprintf("%d\n", os.Getpid())
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		LogWarn("pid", "failed to write PID file", "path", path, "err", err)
		return func() {}
	}
	LogInfo("pid", "wrote PID file", "pid", os.Getpid(), "path", path)
	return func() {
		if err := os.Remove(path); err == nil {
			LogInfo("pid", "removed PID file (clean exit)", "pid", os.Getpid())
		}
	}
}

// checkPreviousCrash reads the PID file from the last run and detects whether
// that run ended uncleanly. Called at startup before the TUI takes over the
// terminal. Logs warnings to initech.log; never returns an error (best-effort).
func checkPreviousCrash(projectRoot string) {
	if projectRoot == "" {
		return
	}
	path := filepath.Join(projectRoot, ".initech", pidFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return // No PID file: either first launch or previous run exited cleanly.
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		os.Remove(path)
		return
	}

	// Signal 0 checks existence without sending a real signal.
	// ESRCH = process not found; any other error means it exists (or EPERM).
	proc, err := os.FindProcess(pid)
	if err != nil {
		// On Unix, FindProcess never errors. Treat as missing.
		LogWarn("pid", "previous instance exited without clean shutdown", "pid", pid)
		os.Remove(path)
		querySystemLog(pid)
		return
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		// Process is gone and didn't clean up the PID file → unclean exit.
		LogWarn("pid", "previous instance exited without clean shutdown", "pid", pid)
		os.Remove(path)
		querySystemLog(pid)
		return
	}
	// Signal(0) succeeded — a process with this PID exists. Verify it's
	// actually initech: on high-churn systems (Linux 32-bit PID space), the
	// original process could have died and an unrelated process reused the PID.
	if pid == os.Getpid() {
		return // Our own PID — we are initech, definitely alive.
	}
	if !isInitechProcess(pid) {
		// PID belongs to a different process — initech crashed, PID was reused.
		LogWarn("pid", "previous PID reused by unrelated process — treating as crash", "pid", pid)
		os.Remove(path)
		querySystemLog(pid)
	}
}

// isInitechProcess returns true if the process with the given PID has a name
// that contains "initech". Uses ps(1) which is available on macOS and Linux.
// Returns false on any error (process gone between Signal(0) and ps check).
func isInitechProcess(pid int) bool {
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false // Process exited between Signal(0) and ps check — treat as crash.
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(string(out))), "initech")
}

// disableSystemLog skips the slow `log show` call in tests.
var disableSystemLog bool

// querySystemLog queries the macOS unified log and DiagnosticReports for crash
// evidence related to the given PID. Best-effort: all errors are silently ignored.
func querySystemLog(pid int) {
	if disableSystemLog {
		return
	}
	// macOS unified log: look for kill/crash entries in the last 10 minutes.
	predicate := fmt.Sprintf(`eventMessage contains "%d"`, pid)
	out, err := exec.Command("log", "show",
		"--predicate", predicate,
		"--last", "10m",
		"--style", "compact",
	).Output()
	if err == nil && len(out) > 0 {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lower := strings.ToLower(line)
			if strings.Contains(lower, "kill") ||
				strings.Contains(lower, "crash") ||
				strings.Contains(lower, "signal") ||
				strings.Contains(lower, "initech") {
				LogWarn("pid", "system log entry for crashed PID", "pid", pid, "entry", line)
			}
		}
	}

	// macOS DiagnosticReports: crash files written by the OS for abnormal exits.
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	diagDir := filepath.Join(home, "Library", "Logs", "DiagnosticReports")
	entries, err := os.ReadDir(diagDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "initech") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) < 10*time.Minute {
			LogWarn("pid", "crash report found in DiagnosticReports",
				"file", filepath.Join(diagDir, e.Name()),
				"modified", info.ModTime().Format(time.RFC3339),
			)
		}
	}
}
