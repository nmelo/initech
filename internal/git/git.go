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
	"strings"

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
// The path is relative to the repo root (e.g., "eng1/src"). The URL is
// normalized before use (bare hostnames get git@ SSH prefix).
func AddSubmodule(runner iexec.Runner, repoDir, url, subPath string) error {
	url = normalizeRepoURL(url)
	_, err := runner.RunInDir(repoDir, "git", "submodule", "add", url, subPath)
	if err != nil {
		return fmt.Errorf("git submodule add %s: %w", subPath, err)
	}
	return nil
}

// normalizeRepoURL converts bare repository references like
// "github.com/user/repo" into proper git URLs. If the URL already has a
// recognized protocol prefix (https://, http://, git@, ssh://), it is
// returned unchanged. Otherwise, the first "/" after the host is converted
// to ":" and "git@" is prepended, producing SSH URLs like
// "git@github.com:user/repo.git".
func normalizeRepoURL(url string) string {
	if url == "" {
		return url
	}
	// Already has a protocol prefix: leave it alone.
	for _, prefix := range []string{"https://", "http://", "ssh://", "git@"} {
		if strings.HasPrefix(url, prefix) {
			return url
		}
	}
	// Bare hostname: github.com/user/repo -> git@github.com:user/repo.git
	if idx := strings.Index(url, "/"); idx > 0 {
		host := url[:idx]
		path := url[idx+1:]
		if !strings.HasSuffix(path, ".git") {
			path += ".git"
		}
		return "git@" + host + ":" + path
	}
	return url
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

// IsEmptyRepoError reports whether the error indicates the remote repository
// has no commits. This happens when git submodule add clones a freshly created
// repo (e.g., a new GitHub repo with no initial commit). Git fails with
// "You are on a branch yet to be born" because there is no HEAD to check out.
func IsEmptyRepoError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "yet to be born") ||
		strings.Contains(msg, "did not match any file") ||
		strings.Contains(msg, "remote HEAD refers to nonexistent ref")
}

// CleanFailedSubmodule removes leftover artifacts from a failed
// git submodule add. This prevents cascading failures: a stale index.lock
// blocks all subsequent git operations, and partial checkout directories
// cause "does not have a commit checked out" errors during git add.
//
// Cleanup is best-effort; errors are silently ignored because the artifacts
// may not all exist depending on where the submodule add failed.
func CleanFailedSubmodule(runner iexec.Runner, repoDir, subPath string) {
	// Remove partial checkout directory (fixes "does not have a commit checked out")
	os.RemoveAll(filepath.Join(repoDir, subPath))

	// Remove cached module clone
	os.RemoveAll(filepath.Join(repoDir, ".git", "modules", subPath))

	// Remove .gitmodules entry if it was partially written
	runner.RunInDir(repoDir, "git", "config", "-f", ".gitmodules",
		"--remove-section", "submodule."+subPath)

	// Remove index.lock (fixes "Unable to create index.lock: File exists")
	os.Remove(filepath.Join(repoDir, ".git", "index.lock"))
}
