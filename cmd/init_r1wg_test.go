package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/scaffold"
	"github.com/spf13/cobra"
)

// ini-r1wg: a failed clone must never delete a <role>/src that was already
// there. Before the fix, every failed submodule add ran CleanFailedSubmodule,
// whose first step is os.RemoveAll(<role>/src): a re-run of initech init over
// a plain source tree (no .git) deleted it. Temp dirs and a stubbed clone only.

// runInitWithFailingClone runs initech init for one eng1 role in a temp
// project whose clone always fails. prepare shapes the tree before the run;
// addCalled reports whether a clone was attempted. Returns the root and output.
func runInitWithFailingClone(t *testing.T, prepare func(root string), onAdd func(root string)) (root, out string, addCalled bool) {
	t.Helper()
	root = t.TempDir()
	cfg := &config.Project{
		Name:  "demo",
		Root:  root,
		Roles: []string{"eng1"},
		Repos: []config.Repo{{URL: "git@github.com:test/unreachable.git", Name: "repo"}},
		Beads: config.BeadsConfig{Prefix: "dem"},
	}
	if err := config.Write(filepath.Join(root, "initech.yaml"), cfg); err != nil {
		t.Fatalf("config.Write: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "index.lock"), []byte("lock"), 0o644); err != nil {
		t.Fatal(err)
	}
	if prepare != nil {
		prepare(root)
	}

	restoreWD := chdirForTest(t, root)
	t.Cleanup(restoreWD)
	t.Cleanup(stubInitRunner(t, &fakeMultiRunner{responses: []fakeResponse{
		{output: "", err: nil},                               // git config --remove-section
		{output: "", err: fmt.Errorf("which bd: not found")}, // which bd
	}}))
	t.Cleanup(stubScaffoldRun(t, func(p *config.Project, opts scaffold.Options) ([]string, error) {
		return []string{"eng1/CLAUDE.md"}, nil
	}))

	origInit, origAdd, origCommit := gitInit, gitAddSubmodule, gitCommitAll
	t.Cleanup(func() { gitInit, gitAddSubmodule, gitCommitAll = origInit, origAdd, origCommit })
	gitInit = func(r iexec.Runner, root string) error { return nil }
	gitCommitAll = func(r iexec.Runner, root, message string) error { return nil }
	gitAddSubmodule = func(r iexec.Runner, repoRoot, repoURL, subPath string) error {
		addCalled = true
		if onAdd != nil {
			onAdd(repoRoot)
		}
		return fmt.Errorf("git submodule add %s: fatal: '%s' already exists and is not a valid git repo", subPath, subPath)
	}

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit returned %v; a failed clone keeps exit status 0 (pm ruling)", err)
	}
	return root, buf.String(), addCalled
}

func indexLockGone(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, ".git", "index.lock")); !os.IsNotExist(err) {
		t.Error("index.lock was not cleared after the failed clone; the next clone would fail on it")
	}
}

// AC 1: a pre-existing plain src survives a failed clone, and the summary
// says so.
func TestRunInit_FailedCloneLeavesAnExistingSrcUntouched(t *testing.T) {
	root, out, _ := runInitWithFailingClone(t, func(root string) {
		src := filepath.Join(root, "eng1", "src")
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "keep.txt"), []byte("my work"), 0o644); err != nil {
			t.Fatal(err)
		}
	}, nil)

	b, err := os.ReadFile(filepath.Join(root, "eng1", "src", "keep.txt"))
	if err != nil || string(b) != "my work" {
		t.Fatalf("eng1/src/keep.txt after a failed clone: %q, %v; init must never delete an existing src", b, err)
	}
	if !strings.Contains(out, "clone failed for eng1") {
		t.Errorf("output does not report the clone failure:\n%s", out)
	}
	if !strings.Contains(out, "eng1/src exists and is not a git checkout; left untouched") {
		t.Errorf("output does not say the existing src was left untouched:\n%s", out)
	}
	indexLockGone(t, root)
}

// AC 2: a clone that fails with no src before it still removes what the
// failed clone left behind.
func TestRunInit_FailedCloneStillRemovesItsOwnPartialSrc(t *testing.T) {
	root, out, _ := runInitWithFailingClone(t, nil, func(repoRoot string) {
		// What a clone that died part way leaves.
		partial := filepath.Join(repoRoot, "eng1", "src")
		os.MkdirAll(partial, 0o755)
		os.WriteFile(filepath.Join(partial, "half-cloned"), nil, 0o644)
	})

	if _, err := os.Stat(filepath.Join(root, "eng1", "src")); !os.IsNotExist(err) {
		t.Errorf("the failed clone's own partial eng1/src was not cleaned up (err=%v)", err)
	}
	if strings.Contains(out, "left untouched") {
		t.Errorf("a src init created itself was reported as left untouched:\n%s", out)
	}
	indexLockGone(t, root)
}

// AC 3: an existing git checkout is not cloned over at all.
func TestRunInit_ExistingGitCheckoutIsNotCloned(t *testing.T) {
	root, _, addCalled := runInitWithFailingClone(t, func(root string) {
		os.MkdirAll(filepath.Join(root, "eng1", "src", ".git"), 0o755)
		os.WriteFile(filepath.Join(root, "eng1", "src", "keep.txt"), []byte("x"), 0o644)
	}, nil)

	if addCalled {
		t.Error("a src with .git was queued for cloning")
	}
	if _, err := os.Stat(filepath.Join(root, "eng1", "src", "keep.txt")); err != nil {
		t.Errorf("existing checkout changed: %v", err)
	}
}

// Feed it nothing: no role directory at all behaves as today (a clone is
// attempted, its failure reported, nothing called untouched).
func TestRunInit_NoRoleDirectoryBehavesAsBefore(t *testing.T) {
	root, out, addCalled := runInitWithFailingClone(t, nil, nil)

	if !addCalled {
		t.Fatal("no clone was attempted for a role with no directory")
	}
	if !strings.Contains(out, "clone failed for eng1") || strings.Contains(out, "left untouched") {
		t.Errorf("unexpected output for a role with no directory:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "eng1", "src")); !os.IsNotExist(err) {
		t.Errorf("eng1/src exists after a failed clone of a role that had none (err=%v)", err)
	}
	indexLockGone(t, root)
}
