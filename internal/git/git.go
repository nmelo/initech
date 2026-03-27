// Package git owns git CLI interaction for initech project bootstrap.
// It handles repo initialization, submodule management, and commits.
//
// All operations take an exec.Runner, making the package fully testable
// without a real git installation. This package does not know about config
// or scaffold.
package git

import (
	"fmt"
	"os"
	"path/filepath"

	iexec "github.com/nmelo/initech/internal/exec"
)

// Init runs git init in the given directory. If the directory already
// contains a .git directory, it's a no-op and returns nil.
func Init(runner iexec.Runner, dir string) error {
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		return nil // already a git repo
	}

	_, err := runner.RunInDir(dir, "git", "init")
	if err != nil {
		return fmt.Errorf("git init: %w", err)
	}
	return nil
}

// AddSubmodule adds a git submodule at the specified path within the repo.
// The path is relative to the repo root (e.g., "eng1/src").
func AddSubmodule(runner iexec.Runner, repoDir, url, subPath string) error {
	_, err := runner.RunInDir(repoDir, "git", "submodule", "add", url, subPath)
	if err != nil {
		return fmt.Errorf("git submodule add %s: %w", subPath, err)
	}
	return nil
}

// CommitAll stages all files and creates a commit with the given message.
// Returns an error if staging or commit fails.
func CommitAll(runner iexec.Runner, dir, message string) error {
	if _, err := runner.RunInDir(dir, "git", "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	if _, err := runner.RunInDir(dir, "git", "commit", "-m", message); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}
