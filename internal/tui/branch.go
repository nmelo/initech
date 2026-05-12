// Package tui — git branch detection for the status bar.
//
// readBranch resolves the current branch of a repo (or git worktree) by
// reading .git/HEAD directly, avoiding a fork+exec on every poll. Returns
// "" when the directory is not a git repo or HEAD cannot be parsed.
package tui

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// branchPollInterval throttles HEAD reads from the render tick.
const branchPollInterval = 2 * time.Second

// findGitDir walks upward from dir looking for a .git entry. Returns the
// path to .git and its FileInfo, or "" if no repo is found.
func findGitDir(dir string) (string, os.FileInfo) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", nil
	}
	for {
		p := filepath.Join(d, ".git")
		if info, err := os.Stat(p); err == nil {
			return p, info
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", nil
		}
		d = parent
	}
}

// readBranch returns the branch name for the repo containing dir, or a short
// sha for a detached HEAD, or "" if no enclosing git repo is found.
// Walks upward from dir until a .git entry is found or the filesystem root
// is reached.
func readBranch(dir string) string {
	if dir == "" {
		return ""
	}
	gitPath, info := findGitDir(dir)
	if gitPath == "" {
		return ""
	}

	gitDir := gitPath
	if !info.IsDir() {
		// Worktree: .git is a file with "gitdir: <path>".
		data, err := os.ReadFile(gitPath)
		if err != nil {
			return ""
		}
		line := strings.TrimSpace(string(data))
		if !strings.HasPrefix(line, "gitdir:") {
			return ""
		}
		gd := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
		if !filepath.IsAbs(gd) {
			// Relative gitdir is relative to the directory containing .git,
			// not to the caller's dir (.git may have been found upward).
			gd = filepath.Join(filepath.Dir(gitPath), gd)
		}
		gitDir = gd
	}

	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(head))
	if ref, ok := strings.CutPrefix(s, "ref: refs/heads/"); ok {
		return ref
	}
	// Not on a branch (detached HEAD, tag checkout, etc.) — nothing to show.
	return ""
}

// pollBranch refreshes t.branch at most every branchPollInterval. Called
// from the render tick.
func (t *TUI) pollBranch() {
	if time.Now().Before(t.branchPollAt) {
		return
	}
	t.branchPollAt = time.Now().Add(branchPollInterval)
	t.branch = readBranch(t.projectRoot)
}

// truncateRunes returns s clipped to max runes, appending an ellipsis when
// it had to drop characters. Counts runes (not bytes) so multi-byte names
// keep a stable display width.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}
