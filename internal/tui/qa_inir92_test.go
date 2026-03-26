// QA tests for ini-r92: Crash diagnostics — signal handling, stderr capture, PID file.
// Covers the three new files: pid.go, signals.go, stderr_unix.go.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// PID file is written on startup and reads back as the current process's PID.
func TestQAPIDFile_WrittenAtStartup(t *testing.T) {
	dir := t.TempDir()
	cleanup := writePIDFile(dir)
	defer cleanup()

	path := filepath.Join(dir, ".initech", pidFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("PID file not created: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("PID file content not an integer: %q", string(data))
	}
	if pid != os.Getpid() {
		t.Errorf("PID file = %d, want %d", pid, os.Getpid())
	}
}

// PID file is removed on clean exit (deferred cleanup runs).
func TestQAPIDFile_RemovedOnCleanExit(t *testing.T) {
	dir := t.TempDir()
	cleanup := writePIDFile(dir)

	path := filepath.Join(dir, ".initech", pidFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("PID file should exist before cleanup: %v", err)
	}

	cleanup()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("PID file must be removed after clean exit (cleanup ran)")
	}
}

// PID file is NOT removed when cleanup is never called — this simulates
// what happens when os.Exit(2) is called by the signal handler. The next
// launch should detect this stale file.
func TestQAPIDFile_LeftBehindIfCleanupSkipped(t *testing.T) {
	dir := t.TempDir()
	_ = writePIDFile(dir) // return value (cleanup) intentionally discarded

	path := filepath.Join(dir, ".initech", pidFileName)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("PID file must remain when cleanup is not called: %v", err)
	}
}

// Stale PID file (dead process) is removed by checkPreviousCrash.
// This simulates detecting an unclean exit on the next launch.
func TestQACheckPreviousCrash_DetectsDeadProcess(t *testing.T) {
	dir := t.TempDir()
	initechDir := filepath.Join(dir, ".initech")
	os.MkdirAll(initechDir, 0755)
	pidPath := filepath.Join(initechDir, pidFileName)

	// 999999999 is guaranteed not to be a running process.
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", 999999999)), 0644)

	checkPreviousCrash(dir)

	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("stale PID file for dead process must be removed by checkPreviousCrash")
	}
}

// Live PID file is left alone by checkPreviousCrash — the process exists.
func TestQACheckPreviousCrash_IgnoresLiveProcess(t *testing.T) {
	dir := t.TempDir()
	initechDir := filepath.Join(dir, ".initech")
	os.MkdirAll(initechDir, 0755)
	pidPath := filepath.Join(initechDir, pidFileName)

	// Our own PID is guaranteed to be alive.
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)

	checkPreviousCrash(dir)

	if _, err := os.Stat(pidPath); err != nil {
		t.Error("PID file for live process must not be removed by checkPreviousCrash")
	}
}

// redirectStderr creates .initech/stderr.log at the OS fd level.
// The file must exist immediately after the call.
func TestQARedirectStderr_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	cleanup := redirectStderr(dir)
	defer cleanup()

	path := filepath.Join(dir, ".initech", "stderr.log")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("stderr.log not created: %v", err)
	}
}

// redirectStderr is a no-op when projectRoot is empty.
func TestQARedirectStderr_NoopOnEmptyRoot(t *testing.T) {
	cleanup := redirectStderr("")
	cleanup() // must not panic
}
