package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWritePIDFile_WritesCurrentPID(t *testing.T) {
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
		t.Fatalf("PID file content not a number: %q", string(data))
	}
	if pid != os.Getpid() {
		t.Errorf("PID file contains %d, want %d", pid, os.Getpid())
	}
}

func TestWritePIDFile_CleanupRemovesFile(t *testing.T) {
	dir := t.TempDir()
	cleanup := writePIDFile(dir)

	path := filepath.Join(dir, ".initech", pidFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("PID file should exist before cleanup: %v", err)
	}

	cleanup()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("PID file should be removed after cleanup")
	}
}

func TestWritePIDFile_NoopOnEmptyRoot(t *testing.T) {
	// Should not panic or create files with empty projectRoot.
	cleanup := writePIDFile("")
	cleanup() // should not panic
}

func TestCheckPreviousCrash_NoFileNoOp(t *testing.T) {
	dir := t.TempDir()
	// No PID file exists: should not panic or log anything unexpected.
	checkPreviousCrash(dir) // must not panic
}

func TestCheckPreviousCrash_NoopOnEmptyRoot(t *testing.T) {
	checkPreviousCrash("") // must not panic
}

func TestCheckPreviousCrash_StaleDeadPIDRemovesFile(t *testing.T) {
	dir := t.TempDir()
	initechDir := filepath.Join(dir, ".initech")
	os.MkdirAll(initechDir, 0755)
	pidPath := filepath.Join(initechDir, pidFileName)

	// PID 1 always exists on Unix (init/launchd), but PID 999999999 should
	// never be a real process. Use a guaranteed-dead PID.
	deadPID := 999999999
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", deadPID)), 0644)

	checkPreviousCrash(dir)

	// The stale PID file should be removed after detection.
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("stale PID file should be removed after crash detection")
	}
}

func TestCheckPreviousCrash_LivePIDLeavesFile(t *testing.T) {
	dir := t.TempDir()
	initechDir := filepath.Join(dir, ".initech")
	os.MkdirAll(initechDir, 0755)
	pidPath := filepath.Join(initechDir, pidFileName)

	// Write our own PID — we know we're alive.
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)

	checkPreviousCrash(dir)

	// Live process: file should NOT be removed (we're that process).
	if _, err := os.Stat(pidPath); err != nil {
		t.Error("PID file for live process should not be removed")
	}
}

func TestCheckPreviousCrash_InvalidPIDContentNoOp(t *testing.T) {
	dir := t.TempDir()
	initechDir := filepath.Join(dir, ".initech")
	os.MkdirAll(initechDir, 0755)
	pidPath := filepath.Join(initechDir, pidFileName)

	os.WriteFile(pidPath, []byte("not-a-number\n"), 0644)

	checkPreviousCrash(dir) // must not panic

	// Invalid content: file should be removed (can't make sense of it).
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("PID file with invalid content should be removed")
	}
}
