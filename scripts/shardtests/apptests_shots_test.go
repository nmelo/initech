package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Pictures from an app-tests run (ini-tj3x): a run names screens, sim-test.sh
// takes a picture of each as the tests stop at it, and the job uploads them.
// None of these tests waits on a clock: every script here runs to its end, and
// a stub stands in for simctl and for gh.

// sh runs a bash script to its end and returns what it printed and its exit code.
func sh(t *testing.T, env []string, script string, args ...string) (string, string, int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the Mac job's bash scripts")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("%s %v: %v", script, args, err)
	}
	return stdout.String(), stderr.String(), code
}

func shotsScript(t *testing.T) string {
	return filepath.Join(repoRootForWorkflows(t), "app", "scripts", "shots.sh")
}

// write puts body in dir/name, runnable, and returns the path.
func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// lines is the non-empty lines of s.
func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// A clean list comes back one name per word, each once; no names is no output
// and no failure (the feed-it-nothing case: a run that names none).
func TestShots_CheckPassesACleanListAndNothingForNone(t *testing.T) {
	out, errs, code := sh(t, nil, shotsScript(t), "check", "home,pane home, blockers")
	if code != 0 || strings.TrimSpace(out) != "home pane blockers" {
		t.Errorf("check gave %q (stderr %q, exit %d), want the three names once each", out, errs, code)
	}
	out, errs, code = sh(t, nil, shotsScript(t), "check", "")
	if code != 0 || strings.TrimSpace(out) != "" || errs != "" {
		t.Errorf("check of no names gave %q, %q, exit %d; want nothing and 0", out, errs, code)
	}
}

// The repo is public and so are its artifacts: a picture comes from the test
// fixtures only. A name that is not a plain screen name, a live- screen, and
// any run paired with a live service are each refused in one line, exit 2.
func TestShots_CheckRefusesABadNameALiveScreenAndALivePairing(t *testing.T) {
	for _, c := range []struct {
		what, names string
		env         []string
		say         string
	}{
		{"a path", "home,../secrets", nil, "is not a screen name"},
		{"a capital", "Home", nil, "is not a screen name"},
		{"a glob", "*", nil, "is not a screen name"},
		{"a leading dash", "-n", nil, "is not a screen name"},
		{"a live screen", "live-blockers", nil, "test fixtures only"},
		{"a live pairing", "home", []string{"TEST_RUNNER_LIVE_PAIRING=initech://pair?host=x"}, "test fixtures only"},
		{"a live pairing, its inner name", "home", []string{"LIVE_PAIRING=initech://pair?host=x"}, "test fixtures only"},
	} {
		out, errs, code := sh(t, c.env, shotsScript(t), "check", c.names)
		if code != 2 || !strings.Contains(errs, c.say) || len(lines(errs)) != 1 || out != "" {
			t.Errorf("%s: stdout %q, stderr %q, exit %d; want one line saying %q and exit 2", c.what, out, errs, code, c.say)
		}
	}
	// The twin: a live pairing with no screen named takes no picture, so it passes.
	if _, errs, code := sh(t, []string{"LIVE_PAIRING=initech://pair?host=x"}, shotsScript(t), "check", ""); code != 0 {
		t.Errorf("a live run naming no screen was refused: %q, exit %d", errs, code)
	}
}

// fakeSimctl is a stand-in for `xcrun simctl`: it records its arguments and
// writes a picture where `io <udid> screenshot <path>` says.
func fakeSimctl(t *testing.T, dir string, ok bool) (cmd, log string) {
	log = filepath.Join(dir, "simctl.log")
	body := "#!/bin/bash\necho \"$*\" >> \"" + log + "\"\n"
	if ok {
		body += "printf PNG > \"$4\"\n"
	} else {
		body += "exit 1\n"
	}
	return "bash " + write(t, dir, "simctl.sh", body), log
}

// A stop a test reached gets its picture when the run named it, and is let go
// either way: an unnamed stop must never hold a test. A stop already let go is
// not pictured twice.
func TestShots_PassPicturesANamedStopAndReleasesEveryStop(t *testing.T) {
	dir := t.TempDir()
	simctl, log := fakeSimctl(t, t.TempDir(), true)
	for _, n := range []string{"home.ready", "pane.ready", "old.ready", "old.done"} {
		write(t, dir, n, "")
	}
	_, errs, code := sh(t, []string{"SHOT_SIMCTL=" + simctl, "SHOT_SETTLE=0"}, shotsScript(t), "pass", dir, "UDID-1", "home old")
	if code != 0 {
		t.Fatalf("pass exit %d: %s", code, errs)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "home.png")); string(b) != "PNG" {
		t.Errorf("home.png holds %q, want the picture simctl wrote", b)
	}
	if exists(filepath.Join(dir, "pane.png")) {
		t.Error("pane was pictured though the run did not name it")
	}
	for _, n := range []string{"home.done", "pane.done"} {
		if !exists(filepath.Join(dir, n)) {
			t.Errorf("%s is missing: the test at that stop would wait a minute and fail", n)
		}
	}
	calls, _ := os.ReadFile(log)
	if got := lines(string(calls)); len(got) != 1 || !strings.HasPrefix(got[0], "io UDID-1 screenshot ") {
		t.Errorf("simctl was called %q, want once, for home on UDID-1 (old was already let go)", got)
	}
}

// A capture that fails still lets the test go, and leaves a mark so the run
// can say the screen was reached but not pictured.
func TestShots_AFailedCaptureIsMarkedAndTheStopIsStillReleased(t *testing.T) {
	dir := t.TempDir()
	simctl, _ := fakeSimctl(t, t.TempDir(), false)
	write(t, dir, "home.ready", "")
	sh(t, []string{"SHOT_SIMCTL=" + simctl, "SHOT_SETTLE=0"}, shotsScript(t), "pass", dir, "UDID-1", "home")
	if !exists(filepath.Join(dir, "home.done")) || !exists(filepath.Join(dir, "home.failed")) {
		t.Error("want home.done and home.failed after a failed capture")
	}
	if exists(filepath.Join(dir, "home.png")) {
		t.Error("a failed capture left a home.png")
	}
	_, errs, code := sh(t, nil, shotsScript(t), "collect", dir, t.TempDir(), "home")
	if code != 10 || !strings.Contains(errs, "home was reached, but its picture could not be taken") {
		t.Errorf("collect said %q, exit %d; want exit 10 naming the failed picture", errs, code)
	}
}

// A missing picture never passes silently: collect copies what is there, names
// each screen that is not, and exits 10. With every picture there it exits 0
// and lists each screen with its file. With no names it does nothing.
func TestShots_CollectFailsNamingAScreenThatWasNotReached(t *testing.T) {
	dir, out := t.TempDir(), filepath.Join(t.TempDir(), "shots")
	write(t, dir, "home.png", "PNG")
	write(t, dir, "empty.png", "")
	stdout, errs, code := sh(t, nil, shotsScript(t), "collect", dir, out, "home nosuch empty")
	if code != 10 {
		t.Errorf("collect exit %d, want 10", code)
	}
	for _, want := range []string{"screen nosuch was not reached", "screen empty was not reached"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr %q does not say %q", errs, want)
		}
	}
	if !strings.Contains(stdout, "screen home: home.png") || !exists(filepath.Join(out, "home.png")) {
		t.Errorf("the picture that was there was not kept: stdout %q", stdout)
	}

	out2 := filepath.Join(t.TempDir(), "shots")
	stdout, errs, code = sh(t, nil, shotsScript(t), "collect", dir, out2, "home")
	if code != 0 || errs != "" || strings.TrimSpace(stdout) != "shots: screen home: home.png" {
		t.Errorf("collect of a pictured screen: %q, %q, exit %d", stdout, errs, code)
	}

	out3 := filepath.Join(t.TempDir(), "shots")
	stdout, errs, code = sh(t, nil, shotsScript(t), "collect", dir, out3, "")
	if code != 0 || stdout != "" || errs != "" || exists(out3) {
		t.Errorf("collect of no names: %q, %q, exit %d, out made %v; want nothing at all", stdout, errs, code, exists(out3))
	}
}

// sim-test.sh checks the names before it takes the lock or makes a simulator.
// A script that does not check must never get as far as a simulator from this
// test, so two things stand in its way. The lock is held by this test's own
// process: a holder the script can signal, so it waits and gives up (a holder
// it may not signal, such as pid 1, reads as gone, and the script takes the
// lock and boots a device). And xcrun is a stand-in that only fails.
func TestSimTest_ScreensAreCheckedBeforeTheLock(t *testing.T) {
	root := repoRootForWorkflows(t)
	script := filepath.Join(root, "app", "scripts", "sim-test.sh")
	noXcrun := t.TempDir()
	write(t, noXcrun, "xcrun", "#!/bin/bash\necho \"xcrun $*: refused, this is a test\" >&2\nexit 1\n")
	path := "PATH=" + noXcrun + string(os.PathListSeparator) + os.Getenv("PATH")
	for _, c := range []struct {
		what string
		env  []string
		say  string
	}{
		{"a live screen", []string{"SHOTS=live-blockers", "SHOT_OUT=" + t.TempDir()}, "test fixtures only"},
		{"no place for the pictures", []string{"SHOTS=home"}, "SHOT_OUT"},
	} {
		lock := filepath.Join(t.TempDir(), "sim.lock")
		if err := os.Mkdir(lock, 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, lock, "pid", strconv.Itoa(os.Getpid())+"\n")
		env := append([]string{path, "SIM_LOCK=" + lock, "SIM_LOCK_WAIT=0", "SHOT_OUT="}, c.env...)
		stdout, errs, code := sh(t, env, script, "iphone", filepath.Join(t.TempDir(), "x.log"))
		if code != 2 || !strings.Contains(errs, c.say) {
			t.Errorf("%s: stderr %q, exit %d; want exit 2 saying %q", c.what, errs, code, c.say)
		}
		if strings.Contains(stdout+errs, "simulator lock") {
			t.Errorf("%s: the script reached the lock before checking the screens: %q %q", c.what, stdout, errs)
		}
		if strings.Contains(stdout+errs, "xcrun") {
			t.Errorf("%s: the script went past the lock, to a simulator: %q %q", c.what, stdout, errs)
		}
	}
}

// A lock holder the script may not signal is alive, not gone (2026-11-19: a
// lock held by pid 1 was read as abandoned, taken over, and a simulator was
// booted; on a Linux runner, ps could not see pid 1 at all). The holder
// planted here is pid 1, which no test owns; xcrun is a
// stand-in that only fails, so a script that takes the lock over stops there
// and never makes a device. The twin, a holder that is really gone, is taken
// over: that is the rule the lock always had.
func TestSimTest_AHolderItMayNotSignalIsAliveAndKeepsTheLock(t *testing.T) {
	script := filepath.Join(repoRootForWorkflows(t), "app", "scripts", "sim-test.sh")
	noXcrun := t.TempDir()
	write(t, noXcrun, "xcrun", "#!/bin/bash\necho \"xcrun $*: refused, this is a test\" >&2\nexit 1\n")
	path := "PATH=" + noXcrun + string(os.PathListSeparator) + os.Getenv("PATH")
	plant := func(pid string) string {
		lock := filepath.Join(t.TempDir(), "sim.lock")
		if err := os.Mkdir(lock, 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, lock, "pid", pid+"\n")
		write(t, lock, "owner", "another rig's run\n")
		return lock
	}

	lock := plant("1")
	stdout, errs, code := sh(t, []string{path, "SIM_LOCK=" + lock, "SIM_LOCK_WAIT=0"}, script, "iphone", filepath.Join(t.TempDir(), "x.log"))
	if code != 2 || !strings.Contains(errs, "still held after 0 minutes by another rig's run, pid 1; refused") {
		t.Errorf("a holder owned by someone else: exit %d, stdout %q, stderr %q; want the lock's refusal naming pid 1", code, stdout, errs)
	}
	if strings.Contains(stdout+errs, "xcrun") {
		t.Errorf("the script took the lock from a live holder and went on to a simulator: %q %q", stdout, errs)
	}
	if got, err := os.ReadFile(filepath.Join(lock, "pid")); err != nil || strings.TrimSpace(string(got)) != "1" {
		t.Errorf("the holder's lock was disturbed: pid file %q, %v", got, err)
	}

	// The twin runs the script past the lock, which only macOS can follow:
	// GNU mktemp refuses the script's -t template before xcrun is reached.
	if runtime.GOOS != "darwin" {
		return
	}
	// A pid no process has: started, waited for, so it is gone for certain.
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Skip("no true to run")
	}
	gone := plant(strconv.Itoa(dead.Process.Pid))
	stdout, errs, _ = sh(t, []string{path, "SIM_LOCK=" + gone, "SIM_LOCK_WAIT=0"}, script, "iphone", filepath.Join(t.TempDir(), "x.log"))
	if strings.Contains(errs, "still held") || !strings.Contains(errs, "xcrun simctl create") {
		t.Errorf("a holder that is gone was not taken over: stdout %q, stderr %q; want the script past the lock, at the stand-in xcrun", stdout, errs)
	}
}

// The order in sim-test.sh: the pictures are taken while xcodebuild runs and
// counted after it ends, and a missing one turns a passing run into exit 10.
func TestSimTest_PicturesAreTakenInTheRelayAndCountedAfterTheTests(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRootForWorkflows(t), "app", "scripts", "sim-test.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	relay := strings.Index(s, `while kill -0 "$xb"`)
	pass := strings.Index(s, `"$here/shots.sh" pass "$shotdir" "$udid" "$shots"`)
	done := strings.Index(s, `wait "$xb"; rc=$?`)
	collect := strings.Index(s, `"$here/shots.sh" collect "$shotdir" "$SHOT_OUT" "$shots" || { [ "$rc" -eq 0 ] && rc=10; }`)
	if relay < 0 || pass < 0 || done < 0 || collect < 0 || !(relay < pass && pass < done && done < collect) {
		t.Errorf("want the relay loop (at %d), pass in it (at %d), the wait (at %d), then collect (at %d)", relay, pass, done, collect)
	}
	if !strings.Contains(s, `export TEST_RUNNER_SHOT_DIR="$shotdir"`) {
		t.Error("the tests are never told where to leave their stops")
	}
}

// fakeGh is a stand-in for gh: it records every call, says the Mac runner is
// there and the switch is on, and answers the commit questions as told.
func fakeGh(t *testing.T) (bin, log string) {
	bin = t.TempDir()
	log = filepath.Join(t.TempDir(), "gh.log")
	write(t, bin, "gh", `#!/bin/bash
echo "$*" >> "`+log+`"
case "$*" in
  "variable get MAC_RUNNER"*) echo macrunner ;;
  "variable get APP_TESTS"*) echo on ;;
  "api repos/"*"/actions/runners"*) echo 1 ;;
  "api repos/"*"/branches/"*) case " $GH_BRANCHES " in *" ${2##*/} "*) exit 0 ;; *) exit 1 ;; esac ;;
  "api repos/"*"/commits/"*) [ -n "$GH_FULL" ] || exit 1; echo "$GH_FULL" ;;
  "api repos/"*"/compare/"*) echo "$GH_COMPARE" ;;
  "workflow run"*) if [ "${GH_RUN_RC:-0}" != 0 ]; then echo "could not create workflow dispatch event: HTTP 422: No ref found" >&2; echo "(https://api.github.com/x)" >&2; exit 1; fi ;;
  "run list"*) echo "app-tests-run: started run 1 (queued): https://example.invalid/1" ;;
esac
`)
	return bin, log
}

const fullSha = "b38a5987e6b191f41c19fdf98df7556be9dfbd1a"

// runAppTests runs app-tests-run.sh with the stand-in gh first on PATH.
func runAppTests(t *testing.T, env []string, args ...string) (stderr string, code int, calls string) {
	t.Helper()
	bin, log := fakeGh(t)
	// GH_TOKEN is a dud on purpose: if the stand-in were ever not the gh
	// found, the real one could start nothing.
	env = append([]string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "GH_TOKEN=not-a-token-this-is-a-test", "APP_TESTS_RUN_SETTLE=0", "REPO=nmelo/initech"}, env...)
	_, stderr, code = sh(t, env, filepath.Join(repoRootForWorkflows(t), "app", "scripts", "app-tests-run.sh"), args...)
	b, _ := os.ReadFile(log)
	return stderr, code, string(b)
}

// A commit that main contains starts a run at main, carrying its full id for
// the job to check out. A branch starts as it always did, with no id.
func TestAppTestsRun_ALandedCommitStartsAtMainWithItsFullId(t *testing.T) {
	for _, status := range []string{"ahead", "identical"} {
		errs, code, calls := runAppTests(t, []string{"GH_FULL=" + fullSha, "GH_COMPARE=" + status}, "b38a598", "iphone")
		want := "workflow run app-tests.yml --repo nmelo/initech --ref main -f device=iphone -f sha=" + fullSha
		if code != 0 || !strings.Contains(calls, want) {
			t.Errorf("main %s of the commit: exit %d, stderr %q, calls:\n%s\nwant %q", status, code, errs, calls, want)
		}
	}
	errs, code, calls := runAppTests(t, []string{"GH_BRANCHES=main ios-look"}, "ios-look", "ipad")
	if code != 0 || !strings.Contains(calls, "workflow run app-tests.yml --repo nmelo/initech --ref ios-look -f device=ipad\n") {
		t.Errorf("a branch: exit %d, stderr %q, calls:\n%s", code, errs, calls)
	}
}

// A command that cannot do all it was asked fails: a commit main does not
// contain, and an id GitHub does not know, are each refused in one line with
// exit 3 and no run started. GitHub's own refusal is one line and exit 4.
func TestAppTestsRun_ACommitNotOnMainIsRefusedAndNothingStarts(t *testing.T) {
	for _, c := range []struct {
		what string
		env  []string
		code int
		say  string
	}{
		{"a commit beside main", []string{"GH_FULL=" + fullSha, "GH_COMPARE=diverged"}, 3, "is not on main"},
		{"a commit ahead of main", []string{"GH_FULL=" + fullSha, "GH_COMPARE=behind"}, 3, "is not on main"},
		{"no answer about main", []string{"GH_FULL=" + fullSha, "GH_COMPARE="}, 3, "is not on main"},
		{"an unknown id", nil, 3, "no commit"},
	} {
		errs, code, calls := runAppTests(t, c.env, "b38a598", "iphone")
		if code != c.code || !strings.Contains(errs, c.say) || len(lines(errs)) != 1 || !strings.Contains(errs, "othing started") {
			t.Errorf("%s: exit %d, stderr %q; want exit %d and one line saying %q and that nothing started", c.what, code, errs, c.code, c.say)
		}
		if strings.Contains(calls, "workflow run") {
			t.Errorf("%s: a run was started:\n%s", c.what, calls)
		}
	}
	errs, code, _ := runAppTests(t, []string{"GH_BRANCHES=main", "GH_RUN_RC=1"}, "main", "iphone")
	if code != 4 || len(lines(errs)) != 1 || !strings.Contains(errs, "othing started") {
		t.Errorf("GitHub's refusal: exit %d, stderr %q; want exit 4 and one line", code, errs)
	}
}

// Screens and test names ride along to the job; a bad screen name is refused
// here, before a runner is spent on it.
func TestAppTestsRun_ScreensAndTestNamesReachTheJob(t *testing.T) {
	errs, code, calls := runAppTests(t, []string{"GH_BRANCHES=main"}, "main", "iphone", "home,pane", "InitechAppUITests/ScreenshotTests")
	want := "--ref main -f device=iphone -f screens=home,pane -f only=InitechAppUITests/ScreenshotTests\n"
	if code != 0 || !strings.Contains(calls, want) {
		t.Errorf("exit %d, stderr %q, calls:\n%s\nwant %q", code, errs, calls, want)
	}
	errs, code, calls = runAppTests(t, []string{"GH_BRANCHES=main"}, "main", "iphone", "live-blockers")
	if code != 2 || !strings.Contains(errs, "test fixtures only") || strings.Contains(calls, "workflow run") {
		t.Errorf("a live screen: exit %d, stderr %q, calls:\n%s", code, errs, calls)
	}
}

// appTestsStep is one step of the app-tests job, by name.
func appTestsStep(t *testing.T, name string) (run string, with map[string]string, cond string) {
	t.Helper()
	for _, s := range readWorkflow(t, "app-tests.yml").Jobs["app-tests"].Steps {
		if s.Name == name {
			return s.Run, s.With, s.If
		}
	}
	t.Fatalf("app-tests.yml has no step named %q: this guard is reading nothing", name)
	return "", nil, ""
}

// What a person types into a run (device, screens, sha, only) reaches a script
// through the environment only, never pasted into its text, where it would run.
func TestAppTests_InputsReachScriptsOnlyThroughTheEnvironment(t *testing.T) {
	steps := readWorkflow(t, "app-tests.yml").Jobs["app-tests"].Steps
	carried := map[string]bool{}
	for _, s := range steps {
		if strings.Contains(s.Run, "${{") {
			t.Errorf("step %q pastes an expression into its script:\n%s", s.Name, s.Run)
		}
		for _, v := range s.Env {
			for _, in := range []string{"device", "screens", "sha", "only"} {
				if strings.Contains(v, "github.event.inputs."+in) {
					carried[in] = true
				}
			}
		}
	}
	for _, in := range []string{"device", "screens", "sha", "only"} {
		if !carried[in] {
			t.Errorf("no step takes the input %q through env: this guard is reading nothing for it", in)
		}
	}
}

// The repo is public, so the pictures are kept the shortest time GitHub
// allows, one day, and are uploaded only when the run named screens.
func TestAppTests_ThePicturesAreOneArtifactKeptOneDay(t *testing.T) {
	_, with, cond := appTestsStep(t, "Upload the pictures")
	if with["retention-days"] != "1" {
		t.Errorf("retention-days %q, want 1", with["retention-days"])
	}
	if !strings.HasPrefix(with["name"], "app-screens-") {
		t.Errorf("artifact name %q, want app-screens-<run id>", with["name"])
	}
	if !strings.Contains(cond, "always()") || !strings.Contains(cond, "github.event.inputs.screens != ''") {
		t.Errorf("if: %q, want always() and only when screens were named", cond)
	}
}

// stepRun runs one step's script the way the runner does, in dir.
func stepRun(t *testing.T, script, dir string, env []string) (string, string, int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the Mac job's bash steps")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

// The job counts the pictures itself, whatever the checked-out sim-test.sh
// did: a run at an older commit, whose sim-test.sh knows nothing of screens,
// must fail too. Each named screen with no picture is named and the step
// fails; with all there, the run's summary lists each screen with its file.
func TestAppTests_TheJobFailsNamingEachScreenWithNoPicture(t *testing.T) {
	run, _, cond := appTestsStep(t, "Every named screen has its picture")
	if !strings.Contains(cond, "always()") {
		t.Errorf("if: %q, want always(): a failed test run must still say which pictures are missing", cond)
	}
	out := t.TempDir()
	write(t, out, "home.png", "PNG")
	summary := filepath.Join(t.TempDir(), "summary.md")
	env := []string{"SHOT_OUT=" + out, "GITHUB_STEP_SUMMARY=" + summary, "GITHUB_RUN_ID=7"}
	_, errs, code := stepRun(t, run, t.TempDir(), append(env, "SHOTS=home, pane"))
	if code == 0 || !strings.Contains(errs, "no picture of pane") || strings.Contains(errs, "no picture of home") {
		t.Errorf("a run missing pane: exit %d, stderr %q; want a failure naming pane alone", code, errs)
	}
	write(t, out, "pane.png", "PNG")
	_, errs, code = stepRun(t, run, t.TempDir(), append(env, "SHOTS=home, pane"))
	if code != 0 {
		t.Fatalf("a run with both pictures: exit %d, stderr %q", code, errs)
	}
	b, _ := os.ReadFile(summary)
	for _, want := range []string{"home.png", "pane.png", "app-screens-7"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the summary does not name %s:\n%s", want, b)
		}
	}
	// "-n" is the name echo would swallow, leaving no screen to count.
	for _, bad := range []string{"../home", "-n"} {
		if _, errs, code = stepRun(t, run, t.TempDir(), append(env, "SHOTS="+bad)); code == 0 || !strings.Contains(errs, "not a screen name") {
			t.Errorf("%q as a screen: exit %d, stderr %q; want a refusal", bad, code, errs)
		}
	}
}

// git runs git in dir with a fixed identity and returns its trimmed output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A run by commit id tests a commit of the branch it was started on, and no
// other: a commit that branch does not contain (a fork's, reachable by id on
// GitHub) is refused and the checkout stays where it was. The step is run
// here, in a repo with a main line and a commit beside it.
func TestAppTests_ACommitIsCheckedOutOnlyWhenTheBranchContainsIt(t *testing.T) {
	run, _, cond := appTestsStep(t, "Check out the named commit")
	if !strings.Contains(cond, "github.event.inputs.sha != ''") {
		t.Errorf("if: %q, want only when a commit was named", cond)
	}
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "one")
	first := git(t, repo, "rev-parse", "HEAD")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "two")
	tip := git(t, repo, "rev-parse", "HEAD")
	git(t, repo, "checkout", "-q", "--orphan", "beside")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "beside")
	beside := git(t, repo, "rev-parse", "HEAD")
	git(t, repo, "checkout", "-q", "main")

	env := []string{"GITHUB_REF_NAME=main"}
	if _, errs, code := stepRun(t, run, repo, append(env, "SHA="+first)); code != 0 || git(t, repo, "rev-parse", "HEAD") != first {
		t.Fatalf("a commit main contains: exit %d, stderr %q, HEAD %s; want HEAD at %s", code, errs, git(t, repo, "rev-parse", "HEAD"), first)
	}
	git(t, repo, "checkout", "-q", "main")
	for what, sha := range map[string]string{
		"a commit beside the branch": beside,
		"a short id":                 first[:7],
		"words":                      "main; echo run",
		"a branch name":              "beside",
	} {
		_, errs, code := stepRun(t, run, repo, append(env, "SHA="+sha))
		if code == 0 || !strings.Contains(errs, "refused") {
			t.Errorf("%s: exit %d, stderr %q; want a refusal", what, code, errs)
		}
		if got := git(t, repo, "rev-parse", "HEAD"); got != tip {
			t.Errorf("%s: the checkout moved to %s", what, got)
		}
	}
}
