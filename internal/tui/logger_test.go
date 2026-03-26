package tui

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitLogger_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	cleanup := InitLogger(dir, slog.LevelInfo)
	defer cleanup()

	LogInfo("test", "hello world")

	path := filepath.Join(dir, ".initech", logFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("log file not created: %v", err)
	}
	if !strings.Contains(string(data), "hello world") {
		t.Errorf("log file missing expected content: %s", data)
	}
	if !strings.Contains(string(data), "[test]") {
		t.Errorf("log file missing component tag: %s", data)
	}
}

func TestInitLogger_LevelFiltering(t *testing.T) {
	dir := t.TempDir()
	cleanup := InitLogger(dir, slog.LevelWarn)
	defer cleanup()

	LogDebug("test", "debug msg")
	LogInfo("test", "info msg")
	LogWarn("test", "warn msg")
	LogError("test", "error msg")

	path := filepath.Join(dir, ".initech", logFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("log file not created: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "debug msg") {
		t.Error("DEBUG message should be filtered at WARN level")
	}
	if strings.Contains(content, "info msg") {
		t.Error("INFO message should be filtered at WARN level")
	}
	if !strings.Contains(content, "warn msg") {
		t.Error("WARN message should be present")
	}
	if !strings.Contains(content, "error msg") {
		t.Error("ERROR message should be present")
	}
}

func TestInitLogger_VerboseEnablesDebug(t *testing.T) {
	dir := t.TempDir()
	cleanup := InitLogger(dir, slog.LevelDebug)
	defer cleanup()

	LogDebug("test", "debug visible")

	path := filepath.Join(dir, ".initech", logFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("log file not created: %v", err)
	}
	if !strings.Contains(string(data), "debug visible") {
		t.Error("DEBUG message should be present at DEBUG level")
	}
}

func TestInitLogger_EmptyProjectRoot(t *testing.T) {
	cleanup := InitLogger("", slog.LevelInfo)
	defer cleanup()
	// Should not panic.
	LogInfo("test", "no-op")
}

func TestInitLogger_Rotation(t *testing.T) {
	dir := t.TempDir()
	initechDir := filepath.Join(dir, ".initech")
	os.MkdirAll(initechDir, 0755)

	logPath := filepath.Join(initechDir, logFileName)
	// Create an oversized log file.
	bigData := make([]byte, logMaxBytes+1)
	for i := range bigData {
		bigData[i] = 'x'
	}
	os.WriteFile(logPath, bigData, 0644)

	cleanup := InitLogger(dir, slog.LevelInfo)
	defer cleanup()

	// The old file should have been rotated.
	backup := logPath + ".1"
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		t.Error("backup file should exist after rotation")
	}

	// New log file should exist and be small (just the new entries).
	LogInfo("test", "after rotation")
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("new log file not created: %v", err)
	}
	if info.Size() > logMaxBytes {
		t.Errorf("new log file should be small after rotation, got %d bytes", info.Size())
	}
}

func TestInitLogger_StructuredArgs(t *testing.T) {
	dir := t.TempDir()
	cleanup := InitLogger(dir, slog.LevelInfo)
	defer cleanup()

	LogInfo("ipc", "send timeout", "target", "eng1", "elapsed", "1.1s")

	path := filepath.Join(dir, ".initech", logFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("log file not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "target=eng1") {
		t.Errorf("structured arg 'target' missing: %s", content)
	}
	if !strings.Contains(content, "elapsed=1.1s") {
		t.Errorf("structured arg 'elapsed' missing: %s", content)
	}
}
