package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	iexec "github.com/nmelo/initech/internal/exec"
)

func TestInit_NewRepo(t *testing.T) {
	fake := &iexec.FakeRunner{}
	dir := t.TempDir()

	if err := Init(fake, dir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.Calls))
	}
	if !strings.Contains(fake.Calls[0], "git init") {
		t.Errorf("expected git init call, got %q", fake.Calls[0])
	}
}

func TestInit_ExistingRepo(t *testing.T) {
	fake := &iexec.FakeRunner{}
	dir := t.TempDir()

	// Create .git directory to simulate existing repo
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := Init(fake, dir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if len(fake.Calls) != 0 {
		t.Errorf("expected 0 calls for existing repo, got %d: %v", len(fake.Calls), fake.Calls)
	}
}

func TestInit_Error(t *testing.T) {
	fake := &iexec.FakeRunner{Err: errors.New("git failed")}
	dir := t.TempDir()

	err := Init(fake, dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "git init") {
		t.Errorf("error should mention git init: %v", err)
	}
}

func TestAddSubmodule(t *testing.T) {
	fake := &iexec.FakeRunner{}

	err := AddSubmodule(fake, "/project", "git@github.com:user/repo.git", "eng1/src")
	if err != nil {
		t.Fatalf("AddSubmodule: %v", err)
	}

	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.Calls))
	}
	call := fake.Calls[0]
	if !strings.Contains(call, "git submodule add") {
		t.Errorf("expected git submodule add, got %q", call)
	}
	if !strings.Contains(call, "eng1/src") {
		t.Errorf("expected subpath in call, got %q", call)
	}
	if !strings.HasPrefix(call, "/project|") {
		t.Errorf("expected dir /project, got %q", call)
	}
}

func TestAddSubmodule_Error(t *testing.T) {
	fake := &iexec.FakeRunner{Err: errors.New("submodule failed")}

	err := AddSubmodule(fake, "/project", "url", "path")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "git submodule add") {
		t.Errorf("error should mention submodule: %v", err)
	}
}

func TestCommitAll(t *testing.T) {
	fake := &iexec.FakeRunner{}

	err := CommitAll(fake, "/project", "initial commit")
	if err != nil {
		t.Fatalf("CommitAll: %v", err)
	}

	if len(fake.Calls) != 2 {
		t.Fatalf("expected 2 calls (add + commit), got %d", len(fake.Calls))
	}

	if !strings.Contains(fake.Calls[0], "git add -A") {
		t.Errorf("first call should be git add: %q", fake.Calls[0])
	}
	if !strings.Contains(fake.Calls[1], "git commit") {
		t.Errorf("second call should be git commit: %q", fake.Calls[1])
	}
	if !strings.Contains(fake.Calls[1], "initial commit") {
		t.Errorf("commit message missing: %q", fake.Calls[1])
	}
}

func TestCommitAll_AddError(t *testing.T) {
	fake := &iexec.FakeRunner{Err: errors.New("add failed")}

	err := CommitAll(fake, "/project", "msg")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "git add") {
		t.Errorf("error should mention git add: %v", err)
	}
}

func TestNormalizeRepoURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"github.com/nmelo/initech", "git@github.com:nmelo/initech.git"},
		{"github.com/user/repo", "git@github.com:user/repo.git"},
		{"gitlab.com/org/project", "git@gitlab.com:org/project.git"},
		{"github.com/nmelo/initech.git", "git@github.com:nmelo/initech.git"},
		{"git@github.com:nmelo/initech.git", "git@github.com:nmelo/initech.git"},
		{"https://github.com/nmelo/initech.git", "https://github.com/nmelo/initech.git"},
		{"https://github.com/nmelo/initech", "https://github.com/nmelo/initech"},
		{"http://github.com/nmelo/initech.git", "http://github.com/nmelo/initech.git"},
		{"ssh://git@github.com/nmelo/initech.git", "ssh://git@github.com/nmelo/initech.git"},
		{"", ""},
		{"localhost", "localhost"},
	}
	for _, tc := range tests {
		got := NormalizeRepoURL(tc.input)
		if got != tc.want {
			t.Errorf("NormalizeRepoURL(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestAddSubmodule_NormalizesURL(t *testing.T) {
	fake := &iexec.FakeRunner{}

	err := AddSubmodule(fake, "/project", "github.com/nmelo/initech", "eng1/src")
	if err != nil {
		t.Fatalf("AddSubmodule: %v", err)
	}

	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.Calls))
	}
	call := fake.Calls[0]
	if !strings.Contains(call, "git@github.com:nmelo/initech.git") {
		t.Errorf("expected normalized URL in call, got %q", call)
	}
}

func TestIsEmptyRepoError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"generic error", errors.New("connection refused"), false},
		{"branch yet to be born", errors.New("fatal: You are on a branch yet to be born"), true},
		{"wrapped yet to be born", errors.New("git submodule add: fatal: You are on a branch yet to be born"), true},
		{"nonexistent ref", errors.New("fatal: remote HEAD refers to nonexistent ref"), true},
		{"did not match", errors.New("fatal: pathspec 'HEAD' did not match any file"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsEmptyRepoError(tc.err); got != tc.want {
				t.Errorf("IsEmptyRepoError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestCleanFailedSubmodule(t *testing.T) {
	repoDir := t.TempDir()

	// Create the artifacts that a failed git submodule add leaves behind.
	subPath := "eng1/src"
	os.MkdirAll(filepath.Join(repoDir, subPath), 0755)
	os.MkdirAll(filepath.Join(repoDir, ".git", "modules", subPath), 0755)
	lockPath := filepath.Join(repoDir, ".git", "index.lock")
	os.WriteFile(lockPath, []byte("lock"), 0644)

	fake := &iexec.FakeRunner{}
	CleanFailedSubmodule(fake, repoDir, subPath)

	// Partial checkout directory should be removed.
	if _, err := os.Stat(filepath.Join(repoDir, subPath)); !os.IsNotExist(err) {
		t.Error("partial checkout dir should be removed")
	}

	// .git/modules/<subPath> should be removed.
	if _, err := os.Stat(filepath.Join(repoDir, ".git", "modules", subPath)); !os.IsNotExist(err) {
		t.Error(".git/modules dir should be removed")
	}

	// index.lock should be removed.
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("index.lock should be removed")
	}

	// Should have called git config to remove .gitmodules section.
	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d: %v", len(fake.Calls), fake.Calls)
	}
	if !strings.Contains(fake.Calls[0], "--remove-section") || !strings.Contains(fake.Calls[0], "submodule.eng1/src") {
		t.Errorf("expected gitmodules remove-section call, got %q", fake.Calls[0])
	}
}

func TestCleanFailedSubmodule_NoArtifacts(t *testing.T) {
	// Safe to call when no artifacts exist (nothing to clean up).
	repoDir := t.TempDir()
	os.MkdirAll(filepath.Join(repoDir, ".git"), 0755)

	fake := &iexec.FakeRunner{}
	CleanFailedSubmodule(fake, repoDir, "eng1/src")

	// Should not panic or error. The git config call happens regardless
	// (best-effort), so we just verify no crash.
}
