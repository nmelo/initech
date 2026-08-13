package git

// Invocation probe for ini-3nzc, per the acceptance rule that presence is not
// the assertion -- INVOCATION is. An installed-but-never-invoked hook is the
// presence-trap in git plumbing costume: it passes every ls, wires every
// config bit, and blocks nothing. So this proves, with real git in a scratch
// repo, that the core.hooksPath mechanism initech relies on actually FIRES:
// a failing hook must block a commit, and the repo's real versioned hook
// shape must be reachable through the same wiring.
//
// Real git rather than the FakeRunner deliberately: the claim under test is
// git's behaviour, not ours -- a fake proving we set a config bit says
// nothing about whether git honours it.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitEnv is os.Environ() with git's hook-execution plumbing removed. When a
// test suite runs FROM a git hook, GIT_DIR/GIT_INDEX_FILE point at the hooking
// repo, and a nested git in a scratch dir would operate on THAT repo instead
// -- this probe discovered it by blocking the commit that introduced it. The
// versioned hook scrubs too; the test scrubs for itself so it does not depend
// on every caller's hygiene.
func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_PREFIX",
			"GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY":
			continue
		}
		env = append(env, kv)
	}
	return env
}

func scratchRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "probe@initech.test"},
		{"config", "user.name", "initech probe"},
		{"commit", "--allow-empty", "-q", "-m", "root"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// TestHooksPath_FailingHookBlocksCommit is eng1's per-checkout probe in
// committed form: point core.hooksPath at a hook that exits 1 and prove an
// empty commit is BLOCKED. If this fails, every wiring check in the Makefile
// is asserting a mechanism that does not work.
func TestHooksPath_FailingHookBlocksCommit(t *testing.T) {
	dir := scratchRepo(t)
	hooks := filepath.Join(dir, "refusing-hooks")
	os.MkdirAll(hooks, 0o755)
	os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755)

	cfg := exec.Command("git", "config", "core.hooksPath", "refusing-hooks")
	cfg.Dir = dir
	cfg.Env = gitEnv()
	if out, err := cfg.CombinedOutput(); err != nil {
		t.Fatalf("set hooksPath: %v\n%s", err, out)
	}

	commit := exec.Command("git", "commit", "--allow-empty", "-q", "-m", "must be blocked")
	commit.Dir = dir
	commit.Env = gitEnv()
	if err := commit.Run(); err == nil {
		t.Fatal("a commit succeeded with a failing pre-commit hook wired via core.hooksPath. " +
			"The entire ini-3nzc design rests on this mechanism; if git is not invoking the " +
			"hook, wiring checks are asserting presence of something that never runs")
	}
}

// TestHooksPath_PassingHookAllowsCommit is the other direction, so the probe
// cannot pass by commits being broken for an unrelated reason.
func TestHooksPath_PassingHookAllowsCommit(t *testing.T) {
	dir := scratchRepo(t)
	hooks := filepath.Join(dir, "scripts", "hooks")
	os.MkdirAll(hooks, 0o755)
	// The same shape as the repo's versioned hook: a script that runs and
	// succeeds. (Running the real `make check` here would be a test of the
	// whole build, not of hook invocation.)
	os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 0\n"), 0o755)

	cfg := exec.Command("git", "config", "core.hooksPath", "scripts/hooks")
	cfg.Dir = dir
	cfg.Env = gitEnv()
	if out, err := cfg.CombinedOutput(); err != nil {
		t.Fatalf("set hooksPath: %v\n%s", err, out)
	}

	commit := exec.Command("git", "commit", "--allow-empty", "-q", "-m", "must pass")
	commit.Dir = dir
	commit.Env = gitEnv()
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("a commit was blocked with a PASSING hook at the repo's real hooks path: %v\n%s"+
			"\nthe relative core.hooksPath is not resolving from the worktree root", err, out)
	}
}
