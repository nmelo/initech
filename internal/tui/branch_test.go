package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadBranch_NoRepo(t *testing.T) {
	dir := t.TempDir()
	if got := readBranch(dir); got != "" {
		t.Errorf("readBranch(non-repo) = %q, want empty", got)
	}
}

func TestReadBranch_EmptyDir(t *testing.T) {
	if got := readBranch(""); got != "" {
		t.Errorf("readBranch(\"\") = %q, want empty", got)
	}
}

func TestReadBranch_NormalRepo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
	if got := readBranch(dir); got != "main" {
		t.Errorf("readBranch = %q, want main", got)
	}
}

func TestReadBranch_FromSubdir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/feature/foo\n")
	sub := filepath.Join(root, "deep", "nested", "dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := readBranch(sub); got != "feature/foo" {
		t.Errorf("readBranch(subdir) = %q, want feature/foo", got)
	}
}

func TestReadBranch_DetachedHEAD(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "deadbeefcafe1234567890abcdef0123456789ab\n")
	if got := readBranch(dir); got != "" {
		t.Errorf("readBranch(detached) = %q, want empty (not on a branch)", got)
	}
}

func TestReadBranch_NonBranchRef(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/tags/v1.0\n")
	if got := readBranch(dir); got != "" {
		t.Errorf("readBranch(tag ref) = %q, want empty", got)
	}
}

func TestReadBranch_WorktreeAbsoluteGitdir(t *testing.T) {
	main := t.TempDir()
	wt := t.TempDir()
	gitDir := filepath.Join(main, ".git", "worktrees", "wt")
	writeFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/wt-branch\n")
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+gitDir+"\n")
	if got := readBranch(wt); got != "wt-branch" {
		t.Errorf("readBranch(worktree abs) = %q, want wt-branch", got)
	}
}

func TestReadBranch_WorktreeRelativeGitdir(t *testing.T) {
	// Relative gitdir resolves from the directory containing the .git file,
	// not from the caller's dir.
	root := t.TempDir()
	wt := filepath.Join(root, "wt")
	gitDir := filepath.Join(root, ".git", "worktrees", "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/rel\n")
	rel, err := filepath.Rel(wt, gitDir)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+rel+"\n")

	// Call from a subdirectory of wt to make sure findGitDir walks up
	// before resolving the relative gitdir.
	sub := filepath.Join(wt, "sub", "dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := readBranch(sub); got != "rel" {
		t.Errorf("readBranch(worktree rel from subdir) = %q, want rel", got)
	}
}

func TestReadBranch_MalformedGitFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".git"), "not a gitdir line\n")
	if got := readBranch(dir); got != "" {
		t.Errorf("readBranch(malformed) = %q, want empty", got)
	}
}

func TestReadBranch_MissingHEAD(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := readBranch(dir); got != "" {
		t.Errorf("readBranch(no HEAD) = %q, want empty", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"main", 25, "main"},
		{"feat/short", 25, "feat/short"},
		{"feat/exactly-twenty-five", 25, "feat/exactly-twenty-five"},
		{"feat/this-is-a-very-long-branch-name", 25, "feat/this-is-a-very-long…"},
		{"αβγδε", 4, "αβγ…"},
		{"abc", 0, ""},
		{"abc", 1, "…"},
		{"", 25, ""},
	}
	for _, c := range cases {
		if got := truncateRunes(c.in, c.max); got != c.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}
