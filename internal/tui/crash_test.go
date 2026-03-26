package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCrashLogWritesReport(t *testing.T) {
	dir := t.TempDir()
	report := crashLog(dir, "v0.1.0-test", "nil pointer dereference")

	// Verify report content.
	if !strings.Contains(report, "INITECH CRASH") {
		t.Error("report missing header")
	}
	if !strings.Contains(report, "v0.1.0-test") {
		t.Error("report missing version")
	}
	if !strings.Contains(report, "nil pointer dereference") {
		t.Error("report missing panic value")
	}
	if !strings.Contains(report, "goroutine") {
		t.Error("report missing stack trace")
	}

	// Verify file was written.
	path := filepath.Join(dir, ".initech", "crash.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("crash.log not written: %v", err)
	}
	if string(data) != report {
		t.Error("file content doesn't match returned report")
	}
}

func TestCrashLogAppends(t *testing.T) {
	dir := t.TempDir()
	crashLog(dir, "v1", "first panic")
	crashLog(dir, "v1", "second panic")

	path := filepath.Join(dir, ".initech", "crash.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	count := strings.Count(string(data), "INITECH CRASH")
	if count != 2 {
		t.Errorf("expected 2 crash entries, got %d", count)
	}
}

func TestCrashLogNoProjectRoot(t *testing.T) {
	// Empty projectRoot: should still return a report, just not write a file.
	report := crashLog("", "v1", "panic")
	if !strings.Contains(report, "panic") {
		t.Error("report should still be generated without projectRoot")
	}
}
