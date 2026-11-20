// Package privatescan tests scripts/private-scan.sh and the versioned
// scripts/hooks/reference-transaction hook that runs it on every branch
// update, with real git in scratch repositories and a test-only list.
package privatescan

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// planted is the test-only private term; the list matches it as a word.
const planted = "zqplanted77"

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(wd)) // scripts/privatescan -> repo root
}

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the scanner and hook are POSIX sh, run by git on macOS and Linux")
	}
}

// fixture is a scratch repo wired as a fleet checkout is: core.hooksPath ->
// scripts/hooks, with this tree's scanner and hook copied in, and a list.
type fixture struct {
	t    *testing.T
	dir  string
	list string
	env  []string
}

func newFixture(t *testing.T, list string) *fixture {
	t.Helper()
	skipOnWindows(t)
	root := repoRoot(t)
	dir := t.TempDir()
	f := &fixture{t: t, dir: dir, list: filepath.Join(t.TempDir(), "private-terms")}
	if err := os.WriteFile(f.list, []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	f.env = append(os.Environ(),
		"INITECH_PRIVATE_TERMS="+f.list,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.org",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.org")
	for _, c := range []struct{ from, to string }{
		{"scripts/private-scan.sh", "scripts/private-scan.sh"},
		{"scripts/hooks/reference-transaction", "scripts/hooks/reference-transaction"},
	} {
		b, err := os.ReadFile(filepath.Join(root, c.from))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, c.to)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, c.to), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.git("init", "-q", "-b", "main")
	f.git("add", "scripts")
	f.git("-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", "tools")
	f.git("config", "core.hooksPath", "scripts/hooks")
	return f
}

func (f *fixture) run(name string, args ...string) (string, error) {
	f.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = f.dir, f.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	out, err := f.run("git", args...)
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}

func (f *fixture) write(name, body string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) head(ref string) string {
	f.t.Helper()
	return strings.TrimSpace(f.git("rev-parse", ref))
}

// scan runs the scanner in a mode, with a diff on stdin for --diff.
func (f *fixture) scan(mode, stdin string) (string, int) {
	f.t.Helper()
	cmd := exec.Command("sh", filepath.Join(f.dir, "scripts/private-scan.sh"), mode)
	cmd.Dir, cmd.Env, cmd.Stdin = f.dir, f.env, strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		f.t.Fatal(err)
	}
	return string(out), code
}

const wordList = "# test-only list\n\nnot-this-one-xyz\n\\b" + planted + "\\b\n"

// The positive twin of every clean result: a planted term is found in both
// modes, reported by path, line and the pattern's number, never by the text.
// The pattern uses \b, which git grep -E silently ignored here before the
// scanner moved to one dialect, Perl's.
func TestScan_FindsAPlantedWordBoundedTerm_InBothModes_WithoutPrintingIt(t *testing.T) {
	f := newFixture(t, wordList)
	f.write("notes.txt", "line one\nwe met "+strings.ToUpper(planted)+" today\n")
	f.git("add", "notes.txt")
	diff := f.git("diff", "--cached", "--unified=0")
	for _, c := range []struct{ mode, in string }{{"--diff", diff}, {"--tree", ""}} {
		out, code := f.scan(c.mode, c.in)
		if code != 1 || !strings.Contains(out, "notes.txt:2: pattern 4") {
			t.Errorf("%s: exit %d, want 1 and notes.txt:2: pattern 4:\n%s", c.mode, code, out)
		}
		if strings.Contains(strings.ToLower(out), planted) {
			t.Errorf("%s printed the term:\n%s", c.mode, out)
		}
	}
	// Its negative twin: the same term inside a longer word is no hit.
	f.write("notes.txt", "line one\nan x"+planted+"x word\n")
	f.git("add", "notes.txt")
	if out, code := f.scan("--diff", f.git("diff", "--cached", "--unified=0")); code != 0 {
		t.Errorf("a word-bounded pattern matched inside a word: exit %d\n%s", code, out)
	}
}

// Nothing to look for passes nothing: a missing list, a list of only
// comments and blanks, and a pattern that does not compile each exit 1 with
// one line, and the bad pattern is never echoed.
func TestScan_NoListAnEmptyListOrABadPattern_Refuses(t *testing.T) {
	f := newFixture(t, wordList)
	cases := map[string]func(){
		"missing": func() { os.Remove(f.list) },
		"empty":   func() { os.WriteFile(f.list, []byte("# nothing\n\n   \n"), 0o600) },
		"bad":     func() { os.WriteFile(f.list, []byte("zqbad(unclosed\n"), 0o600) },
	}
	for name, prep := range cases {
		prep()
		out, code := f.scan("--tree", "")
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if code != 1 || len(lines) != 1 || !strings.Contains(out, f.list) {
			t.Errorf("%s: exit %d, want 1 and one line naming %s:\n%s", name, code, f.list, out)
		}
		if strings.Contains(out, "zqbad") {
			t.Errorf("%s: the pattern was printed: %s", name, out)
		}
	}
}

// The hook is the commit guard: --no-verify cannot skip it.
func TestHook_RefusesACommitAddingATerm_EvenWithNoVerify(t *testing.T) {
	f := newFixture(t, wordList)
	before := f.head("main")
	f.write("a.txt", "talk to "+planted+" later\n")
	f.git("add", "a.txt")
	out, err := f.run("git", "commit", "--no-verify", "-q", "-m", "add a")
	if err == nil {
		t.Fatalf("the commit landed:\n%s", out)
	}
	if !strings.Contains(out, "a.txt:1: pattern 4") || !strings.Contains(out, "refused the update of refs/heads/main") {
		t.Errorf("refusal does not name the line and the ref:\n%s", out)
	}
	if strings.Contains(strings.ToLower(out), planted) {
		t.Errorf("the refusal printed the term:\n%s", out)
	}
	if got := f.head("main"); got != before {
		t.Errorf("main moved to %s after a refusal", got)
	}
	// The clean twin lands, also with --no-verify.
	f.write("a.txt", "talk to the vendor later\n")
	f.git("add", "a.txt")
	f.git("commit", "--no-verify", "-q", "-m", "add a")
	if f.head("main") == before {
		t.Error("a clean commit did not land")
	}
}

func TestHook_RefusesAnAmendThatAddsATerm(t *testing.T) {
	f := newFixture(t, wordList)
	f.write("b.txt", "clean\n")
	f.git("add", "b.txt")
	f.git("commit", "-q", "-m", "b")
	before := f.head("main")
	f.write("b.txt", "clean\n"+planted+"\n")
	f.git("add", "b.txt")
	if out, err := f.run("git", "commit", "--amend", "--no-verify", "-q", "--no-edit"); err == nil {
		t.Fatalf("the amend landed:\n%s", out)
	}
	if f.head("main") != before {
		t.Error("main moved after a refused amend")
	}
}

// A new branch is checked on the commits no other branch has, and a
// deletion adds nothing.
func TestHook_RefusesANewBranchCarryingATerm_AndLetsADeletionThrough(t *testing.T) {
	f := newFixture(t, wordList)
	// A commit on no branch (made on a detached HEAD), then a branch at it.
	f.git("checkout", "-q", "--detach")
	f.write("c.txt", planted+"\n")
	f.git("add", "c.txt")
	f.git("commit", "-q", "-m", "c") // HEAD only: no branch moves, nothing scanned
	bad := f.head("HEAD")
	f.git("checkout", "-q", "main")
	if out, err := f.run("git", "branch", "copy", bad); err == nil {
		t.Errorf("a new branch carrying a term was made:\n%s", out)
	}
	// A new branch at a commit already on a branch adds nothing new, and a
	// deletion is never refused.
	f.git("branch", "plain", "main")
	f.git("branch", "-D", "plain")
}

// Only what an update adds is read: a term already in the tree, in a file
// the commit does not touch, never blocks it.
func TestHook_AnUntouchedOldTermDoesNotBlockACommit(t *testing.T) {
	f := newFixture(t, wordList)
	f.write("old.txt", planted+"\n")
	f.git("add", "old.txt")
	f.git("-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", "old")
	f.write("new.txt", "unrelated\n")
	f.git("add", "new.txt")
	if out, err := f.run("git", "commit", "-q", "-m", "new"); err != nil {
		t.Fatalf("a commit not touching old.txt was refused:\n%s", out)
	}
	// The whole-tree report still sees the old line.
	if out, code := f.scan("--tree", ""); code != 1 || !strings.Contains(out, "old.txt:1: pattern 4") {
		t.Errorf("--tree missed the old line: exit %d\n%s", code, out)
	}
}

// With no list the hook refuses every branch update: a scan with nothing to
// look for must not pass.
func TestHook_WithNoListRefusesEveryUpdate(t *testing.T) {
	f := newFixture(t, wordList)
	os.Remove(f.list)
	f.write("d.txt", "clean\n")
	f.git("add", "d.txt")
	out, err := f.run("git", "commit", "-q", "-m", "d")
	if err == nil || !strings.Contains(out, f.list+", one Perl-compatible regex per line, mode 0600) is missing") {
		t.Errorf("a commit with no list: err %v\n%s", err, out)
	}
}

// A git grep that cannot run (a git built without PCRE, a broken repo) is a
// scan that did not happen: --tree refuses instead of reading it as clean.
func TestScan_TreeRefusesWhenGitGrepCannotRun(t *testing.T) {
	f := newFixture(t, wordList)
	shim := t.TempDir()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = grep ]; then echo 'fatal: cannot use Perl-compatible regexes' >&2; exit 128; fi\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	f.env = append(f.env, "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, code := f.scan("--tree", "")
	if code != 1 || !strings.Contains(out, "git grep could not run pattern") {
		t.Errorf("a failed git grep read as clean: exit %d\n%s", code, out)
	}
}
