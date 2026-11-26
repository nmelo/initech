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

// The message travels with the commit: a new commit whose message holds a
// term is refused even when its content is clean, --no-verify or not.
func TestHook_RefusesACommitWhoseMessageHoldsATerm(t *testing.T) {
	f := newFixture(t, wordList)
	before := f.head("main")
	f.write("e.txt", "clean\n")
	f.git("add", "e.txt")
	out, err := f.run("git", "commit", "--no-verify", "-q", "-m", "note for "+planted)
	if err == nil {
		t.Fatalf("a commit naming a term in its message landed:\n%s", out)
	}
	if !strings.Contains(out, "(message):1: pattern 4") || strings.Contains(strings.ToLower(out), planted) {
		t.Errorf("refusal does not name the message line, or prints the term:\n%s", out)
	}
	if f.head("main") != before {
		t.Error("main moved after a refused message")
	}
	// An amend that only rewrites the message is checked the same way.
	f.git("commit", "-q", "-m", "clean note")
	if out, err := f.run("git", "commit", "--amend", "--no-verify", "-q", "-m", "about "+planted); err == nil {
		t.Errorf("an amend putting a term in the message landed:\n%s", out)
	}
}

// A commit already published (on a remote-tracking branch) is not this
// checkout's to publish: bringing it into a branch, as a pull does, is never
// refused for its message, so a checkout behind main can always catch up.
func TestHook_APublishedCommitsMessageNeverBlocksAPull(t *testing.T) {
	f := newFixture(t, wordList)
	f.git("checkout", "-q", "--detach")
	f.write("g.txt", "clean\n")
	f.git("add", "g.txt")
	f.git("commit", "-q", "--no-verify", "-m", "published with "+planted) // HEAD only: no branch moves
	pub := f.head("HEAD")
	f.git("update-ref", "refs/remotes/origin/main", pub)
	f.git("checkout", "-q", "main")
	if out, err := f.run("git", "merge", "-q", "--ff-only", "origin/main"); err != nil {
		t.Fatalf("a fast-forward to a published commit was refused for its message:\n%s", out)
	}
}

// A new branch is checked on its new commits' messages too.
func TestHook_RefusesANewBranchWhoseCommitMessageHoldsATerm(t *testing.T) {
	f := newFixture(t, wordList)
	f.git("checkout", "-q", "--detach")
	f.write("h.txt", "clean\n")
	f.git("add", "h.txt")
	f.git("commit", "-q", "--no-verify", "-m", "for "+planted) // HEAD only: no branch moves
	c := f.head("HEAD")
	f.git("checkout", "-q", "main")
	if out, err := f.run("git", "branch", "msg", c); err == nil {
		t.Errorf("a new branch whose commit message holds a term was made:\n%s", out)
	}
}

// --- Only the commits this update brings that no remote holds are scanned
// (ini-y86y). A line already on GitHub is not this checkout's to publish, so
// a pull, a move back or a branch cut from origin/main never blocks on it.

// published makes a commit on a detached HEAD (no branch moves, so nothing is
// scanned), writing name=body, and returns it. The caller publishes it.
func (f *fixture) published(name, body, msg string) string {
	f.t.Helper()
	f.git("checkout", "-q", "--detach")
	f.write(name, body)
	f.git("add", name)
	f.git("commit", "-q", "-m", msg)
	c := f.head("HEAD")
	f.git("checkout", "-q", "main")
	return c
}

// publish points origin/<branch> at c, as a fetch would.
func (f *fixture) publish(branch, c string) {
	f.t.Helper()
	f.git("update-ref", "refs/remotes/origin/"+branch, c)
}

// AC1: a fast-forward pull bringing a published line that matches passes.
func TestHook_APullOfAPublishedLineWithATermPasses(t *testing.T) {
	f := newFixture(t, wordList)
	pub := f.published("p.txt", "see "+planted+"\n", "published")
	f.publish("main", pub)
	if out, err := f.run("git", "merge", "-q", "--ff-only", "origin/main"); err != nil {
		t.Fatalf("a fast-forward to a published line was refused:\n%s", out)
	}
	if f.head("main") != pub {
		t.Error("main did not move to the published commit")
	}
}

// AC2: moving a branch back over a published removal passes: in a plain
// diff the removed line reads as added.
func TestHook_MovingABranchBackPasses(t *testing.T) {
	f := newFixture(t, wordList)
	add := f.published("r.txt", "see "+planted+"\n", "adds")
	f.git("checkout", "-q", "--detach", add)
	f.write("r.txt", "clean\n")
	f.git("add", "r.txt")
	f.git("commit", "-q", "-m", "removes")
	rm := f.head("HEAD")
	f.git("checkout", "-q", "main")
	f.publish("main", rm)
	f.git("merge", "-q", "--ff-only", "origin/main")
	if out, err := f.run("git", "reset", "-q", "--hard", add); err != nil {
		t.Fatalf("git reset back over a published removal was refused:\n%s", out)
	}
	if f.head("main") != add {
		t.Error("main did not move back")
	}
	f.git("branch", "side", rm)
	if out, err := f.run("git", "branch", "-f", "side", add); err != nil {
		t.Fatalf("git branch -f back over a published removal was refused:\n%s", out)
	}
}

// AC3: a new branch cut from an origin/main that holds a published line
// passes, though no local branch holds that commit.
func TestHook_ANewBranchFromOriginMainPasses(t *testing.T) {
	f := newFixture(t, wordList)
	pub := f.published("n.txt", planted+"\n", "published")
	f.publish("main", pub)
	if out, err := f.run("git", "branch", "fresh", "origin/main"); err != nil {
		t.Fatalf("a new branch from origin/main was refused:\n%s", out)
	}
}

// AC4c: a rebase whose rewritten commit adds a term is refused at the end,
// and the branch keeps its commit.
func TestHook_RefusesARebaseWhoseRewrittenCommitAddsATerm(t *testing.T) {
	f := newFixture(t, wordList)
	f.write("s.txt", "clean\n")
	f.git("add", "s.txt")
	f.git("commit", "-q", "-m", "s")
	before := f.head("main")
	exe := "printf '%s\\n' " + planted + " >> s.txt && git add s.txt && git commit -q --amend --no-edit --no-verify"
	out, err := f.run("git", "rebase", "-q", "--exec", exe, "HEAD~1")
	if err == nil {
		t.Fatalf("a rebase adding a term finished:\n%s", out)
	}
	if !strings.Contains(out, "refused the update of refs/heads/main") || strings.Contains(strings.ToLower(out), planted) {
		t.Errorf("the refusal does not name the ref, or prints the term:\n%s", out)
	}
	f.run("git", "rebase", "--abort")
	if f.head("main") != before {
		t.Error("main moved after a refused rebase")
	}
}

// conflictWithPublished makes main and origin/side change m.txt's one line
// differently, with origin/side's line published.
func (f *fixture) conflictWithPublished(sideLine string) {
	f.t.Helper()
	f.write("m.txt", "base\n")
	f.git("add", "m.txt")
	f.git("commit", "-q", "-m", "base")
	side := f.published("m.txt", sideLine+"\n", "side")
	f.publish("side", side)
	f.write("m.txt", "ours\n")
	f.git("add", "m.txt")
	f.git("commit", "-q", "-m", "ours")
}

// AC4d: a local merge whose resolution adds a term is refused, though both
// parents are clean.
func TestHook_RefusesAMergeWhoseResolutionAddsATerm(t *testing.T) {
	f := newFixture(t, wordList)
	f.conflictWithPublished("theirs")
	before := f.head("main")
	if _, err := f.run("git", "merge", "-q", "origin/side"); err == nil {
		t.Fatal("the fixture's merge did not conflict")
	}
	f.write("m.txt", "ours and "+planted+"\n")
	f.git("add", "m.txt")
	out, err := f.run("git", "commit", "-q", "--no-edit", "--no-verify")
	if err == nil {
		t.Fatalf("a merge resolved with a term landed:\n%s", out)
	}
	if !strings.Contains(out, "m.txt:1: pattern 4") || strings.Contains(strings.ToLower(out), planted) {
		t.Errorf("the refusal does not name the line, or prints the term:\n%s", out)
	}
	if f.head("main") != before {
		t.Error("main moved after a refused merge")
	}
	// The clean twin: the same merge resolved without the term lands.
	f.write("m.txt", "ours and theirs\n")
	f.git("add", "m.txt")
	f.git("commit", "-q", "--no-edit")
	if f.head("main") == before {
		t.Error("a clean resolution did not land")
	}
}

// AC5: a local merge of a published branch that holds a term, adding none
// itself, passes: as a clean merge and as a conflict resolved to its side.
func TestHook_AMergeOfAPublishedBranchWithATermPasses(t *testing.T) {
	f := newFixture(t, wordList)
	f.write("o.txt", "local\n")
	f.git("add", "o.txt")
	f.git("commit", "-q", "-m", "local") // main is not an ancestor of the side
	pub := f.published("q.txt", "see "+planted+"\n", "published")
	f.publish("feature", pub)
	if out, err := f.run("git", "merge", "-q", "--no-ff", "--no-edit", "origin/feature"); err != nil {
		t.Fatalf("a clean merge of a published term was refused:\n%s", out)
	}

	g := newFixture(t, wordList)
	g.conflictWithPublished("theirs " + planted)
	if _, err := g.run("git", "merge", "-q", "origin/side"); err == nil {
		t.Fatal("the fixture's merge did not conflict")
	}
	g.git("checkout", "-q", "--theirs", "m.txt")
	g.git("add", "m.txt")
	if out, err := g.run("git", "commit", "-q", "--no-edit"); err != nil {
		t.Fatalf("a conflict resolved to the published side was refused:\n%s", out)
	}

	// A resolution that keeps the published side's line and adds a clean
	// one differs from both parents, so --cc shows the hunk: only the line
	// added against every parent is scanned, never the one a parent holds.
	h := newFixture(t, wordList)
	h.conflictWithPublished("theirs " + planted)
	if _, err := h.run("git", "merge", "-q", "origin/side"); err == nil {
		t.Fatal("the fixture's merge did not conflict")
	}
	h.write("m.txt", "theirs "+planted+"\nand a clean line\n")
	h.git("add", "m.txt")
	if out, err := h.run("git", "commit", "-q", "--no-edit"); err != nil {
		t.Fatalf("a resolution keeping the published line and adding a clean one was refused:\n%s", out)
	}
}

// AC4a on a new branch, and its by-effect twin: a new branch at a local
// commit that adds a term is refused; a new branch at a commit another local
// branch already holds adds nothing, even when that commit carries a term
// listed after it was made.
func TestHook_ANewBranchIsScannedOnlyForCommitsNoBranchHolds(t *testing.T) {
	f := newFixture(t, wordList)
	f.git("checkout", "-q", "-b", "work")
	f.write("w.txt", planted+"\n")
	f.git("add", "w.txt")
	f.git("-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", "before the list named it")
	f.git("checkout", "-q", "main")
	if out, err := f.run("git", "branch", "copy", "work"); err != nil {
		t.Fatalf("a new branch at a commit another branch holds was refused:\n%s", out)
	}
	f.git("checkout", "-q", "--detach")
	f.write("x.txt", planted+"\n")
	f.git("add", "x.txt")
	f.git("commit", "-q", "-m", "x")
	bad := f.head("HEAD")
	f.git("checkout", "-q", "main")
	if out, err := f.run("git", "branch", "copy2", bad); err == nil {
		t.Errorf("a new branch at a local commit adding a term was made:\n%s", out)
	}
	if _, err := f.run("git", "rev-parse", "-q", "--verify", "refs/heads/copy2"); err == nil {
		t.Error("the refused branch exists")
	}
}

// Feed it nothing: an update that brings no commit scans no line. With a
// list that matches every line, main moves among published commits freely;
// the positive twin, a local commit under the same list, is refused.
func TestHook_AnUpdateBringingNoCommitScansNothing(t *testing.T) {
	f := newFixture(t, "\\S\n")
	a := f.published("a.txt", "one\n", "a")
	f.git("checkout", "-q", "--detach", a)
	f.write("a.txt", "two\n")
	f.git("add", "a.txt")
	f.git("commit", "-q", "-m", "b")
	b := f.head("HEAD")
	f.git("checkout", "-q", "main")
	f.publish("main", b)
	for _, to := range []string{b, a, b} {
		if out, err := f.run("git", "update-ref", "refs/heads/main", to); err != nil {
			t.Fatalf("moving main to published %s was refused:\n%s", to[:7], out)
		}
	}
	f.git("reset", "-q", "--hard")
	f.write("l.txt", "local\n")
	f.git("add", "l.txt")
	if out, err := f.run("git", "commit", "-q", "-m", "local"); err == nil {
		t.Fatalf("a local commit under a list matching every line landed:\n%s", out)
	}
}

// No list still refuses an update that brings no commit: a scan with nothing
// to look for must not pass, whatever the update brings.
func TestHook_WithNoListRefusesAMoveToAPublishedCommit(t *testing.T) {
	f := newFixture(t, wordList)
	pub := f.published("k.txt", "clean\n", "published")
	f.publish("main", pub)
	os.Remove(f.list)
	out, err := f.run("git", "merge", "-q", "--ff-only", "origin/main")
	if err == nil || !strings.Contains(out, "is missing") {
		t.Errorf("a move with no list: err %v\n%s", err, out)
	}
}

// A read of the commits that fails is a scan that did not happen: the hook
// refuses the update instead of passing an empty patch to the scanner. The
// commit's blob is gone from the object store, so its patch cannot be read;
// its content is clean, so only the failed read can refuse it.
func TestHook_RefusesWhenTheCommitsCannotBeRead(t *testing.T) {
	f := newFixture(t, wordList)
	before := f.head("main")
	f.git("checkout", "-q", "--detach")
	f.write("u.txt", "clean\n")
	f.git("add", "u.txt")
	f.git("commit", "-q", "-m", "u") // HEAD only: no branch moves
	c := f.head("HEAD")
	blob := f.head("HEAD:u.txt")
	if err := os.Remove(filepath.Join(f.dir, ".git", "objects", blob[:2], blob[2:])); err != nil {
		t.Fatal(err)
	}
	out, err := f.run("git", "update-ref", "refs/heads/main", c)
	if err == nil || !strings.Contains(out, "cannot read the commits of refs/heads/main") {
		t.Errorf("a failed read passed: err %v\n%s", err, out)
	}
	if f.head("main") != before {
		t.Error("main moved after a failed read")
	}
}
