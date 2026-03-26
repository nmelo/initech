//go:build !windows

// stderr_unix.go redirects os.Stderr (fd 2) to .initech/stderr.log at the
// OS file-descriptor level. This must happen before screen.Init() puts the
// terminal into raw mode, so that cgo/native crash stack traces are written
// to a file rather than into the garbled terminal buffer.
//
// Go's own panic handler writes through os.Stderr (Go level), which also
// goes through fd 2, so this captures both Go panics and cgo crashes.
package tui

import (
	"os"
	"path/filepath"
	"syscall"
)

// redirectStderr opens .initech/stderr.log for append and redirects fd 2 to
// it. Returns a cleanup func that restores the original stderr fd. Cleanup
// runs after screen.Fini() so error messages printed by cobra/main after the
// TUI exits still reach the terminal.
//
// The original stderr fd is preserved via dup so the cleanup can restore it.
// No-op (returns empty cleanup) when projectRoot is empty or if any step fails.
func redirectStderr(projectRoot string) func() {
	if projectRoot == "" {
		return func() {}
	}
	dir := filepath.Join(projectRoot, ".initech")
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, "stderr.log")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return func() {}
	}

	// Save the original stderr fd so we can restore it on cleanup.
	origFd, err := syscall.Dup(int(os.Stderr.Fd()))
	if err != nil {
		f.Close()
		return func() {}
	}

	// Atomically point fd 2 at our log file.
	if err := syscall.Dup2(int(f.Fd()), int(os.Stderr.Fd())); err != nil {
		syscall.Close(origFd)
		f.Close()
		return func() {}
	}
	// The log file fd is now aliased via fd 2; close the original fd we got
	// from OpenFile so we don't leak it.
	f.Close()

	return func() {
		// Restore fd 2 to the saved original (terminal).
		syscall.Dup2(origFd, int(os.Stderr.Fd()))
		syscall.Close(origFd)
	}
}
