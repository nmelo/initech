// Package exec wraps os/exec with consistent error handling for external
// command execution. Every initech package that shells out to git, bd,
// or other tools uses the Runner interface from this package instead of calling
// os/exec directly.
//
// This indirection is the primary testing seam for the entire project. Tests
// swap in a fake Runner to verify command invocations without requiring real
// git or bd installations. The DefaultRunner implementation shells out
// to real binaries via os/exec.
//
// All methods return combined stdout+stderr output as a trimmed string.
// Errors wrap the underlying exec error with the command name and captured
// stderr for diagnostics.
package exec

import (
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes external commands. Implementations must be safe for
// concurrent use if callers invoke methods from multiple goroutines.
type Runner interface {
	// Run executes a command in the caller's working directory.
	// Returns combined stdout as a trimmed string, or an error with
	// command name and stderr context.
	Run(name string, args ...string) (string, error)

	// RunInDir executes a command in the specified directory.
	// The directory must exist; no creation or validation is performed.
	RunInDir(dir, name string, args ...string) (string, error)
}

// DefaultRunner shells out to real binaries via os/exec.
// Safe for concurrent use (each call creates an independent exec.Cmd).
type DefaultRunner struct{}

// Run executes a command in the caller's working directory.
func (r *DefaultRunner) Run(name string, args ...string) (string, error) {
	return r.RunInDir("", name, args...)
}

// RunInDir executes a command in the specified directory.
// If dir is empty, the caller's working directory is used.
func (r *DefaultRunner) RunInDir(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}

	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if err != nil {
		return output, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, output)
	}

	return output, nil
}
