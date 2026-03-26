//go:build linux

// stderr_linux.go redirects os.Stderr (fd 2) to .initech/stderr.log at the
// OS file-descriptor level. This must happen before screen.Init() puts the
// terminal into raw mode, so that cgo/native crash stack traces are written
// to a file rather than into the garbled terminal buffer.
//
// Go's own panic handler writes through os.Stderr (Go level), which also
// goes through fd 2, so this captures both Go panics and cgo crashes.
//
// Uses syscall.Dup3 instead of syscall.Dup2 because Dup2 is not available on
// linux/arm64 (the kernel exposes only dup3 on that architecture). Dup3 with
// flags=0 is semantically identical to Dup2.
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

	// Atomically point fd 2 at our log file. Use Dup3 (flags=0) instead of
	// Dup2 because Dup2 is not available on linux/arm64.
	if err := syscall.Dup3(int(f.Fd()), int(os.Stderr.Fd()), 0); err != nil {
		syscall.Close(origFd)
		f.Close()
		return func() {}
	}
	// The log file fd is now aliased via fd 2; close the original fd we got
	// from OpenFile so we don't leak it.
	f.Close()

	return func() {
		// Restore fd 2 to the saved original (terminal).
		syscall.Dup3(origFd, int(os.Stderr.Fd()), 0)
		syscall.Close(origFd)
	}
}
