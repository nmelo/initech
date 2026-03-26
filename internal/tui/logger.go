// logger.go provides structured application logging for the TUI.
// Writes to .initech/initech.log with automatic rotation at 10MB.
// Uses log/slog for leveled, structured output.
package tui

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

const (
	logFileName    = "initech.log"
	logMaxBytes    = 10 * 1024 * 1024 // 10MB before rotation.
	logBackupCount = 1                // Keep 1 rotated file (.1).
)

// appLogger is the package-level logger. Nil until InitLogger is called.
var appLogger struct {
	mu     sync.Mutex
	logger *slog.Logger
	file   *os.File
	path   string
}

// InitLogger sets up the application logger writing to .initech/initech.log.
// level is the minimum severity (slog.LevelDebug for verbose, slog.LevelInfo
// for normal). Safe to call multiple times; subsequent calls replace the logger.
// Returns a cleanup function that closes the log file.
func InitLogger(projectRoot string, level slog.Level) func() {
	if projectRoot == "" {
		return func() {}
	}

	dir := filepath.Join(projectRoot, ".initech")
	os.MkdirAll(dir, 0700)

	logPath := filepath.Join(dir, logFileName)
	rotateIfNeeded(logPath)

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		// Can't open log file. Use a discard logger so callers don't nil-check.
		appLogger.mu.Lock()
		appLogger.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
		appLogger.mu.Unlock()
		return func() {}
	}

	handler := slog.NewTextHandler(f, &slog.HandlerOptions{
		Level: level,
	})

	appLogger.mu.Lock()
	// Close previous file if replacing.
	if appLogger.file != nil {
		appLogger.file.Close()
	}
	appLogger.logger = slog.New(handler)
	appLogger.file = f
	appLogger.path = logPath
	appLogger.mu.Unlock()

	return func() {
		appLogger.mu.Lock()
		defer appLogger.mu.Unlock()
		if appLogger.file != nil {
			appLogger.file.Close()
			appLogger.file = nil
		}
	}
}

// rotateIfNeeded renames the log file to .1 if it exceeds logMaxBytes.
func rotateIfNeeded(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < logMaxBytes {
		return
	}
	backup := path + ".1"
	os.Remove(backup)
	os.Rename(path, backup)
}

// getLogger returns the current logger, or nil if not initialized.
func getLogger() *slog.Logger {
	appLogger.mu.Lock()
	defer appLogger.mu.Unlock()
	return appLogger.logger
}

// LogDebug logs at DEBUG level with a component tag.
func LogDebug(component string, msg string, args ...any) {
	if l := getLogger(); l != nil {
		l.Debug(fmt.Sprintf("[%s] %s", component, msg), args...)
	}
}

// LogInfo logs at INFO level with a component tag.
func LogInfo(component string, msg string, args ...any) {
	if l := getLogger(); l != nil {
		l.Info(fmt.Sprintf("[%s] %s", component, msg), args...)
	}
}

// LogWarn logs at WARN level with a component tag.
func LogWarn(component string, msg string, args ...any) {
	if l := getLogger(); l != nil {
		l.Warn(fmt.Sprintf("[%s] %s", component, msg), args...)
	}
}

// LogError logs at ERROR level with a component tag.
func LogError(component string, msg string, args ...any) {
	if l := getLogger(); l != nil {
		l.Error(fmt.Sprintf("[%s] %s", component, msg), args...)
	}
}
