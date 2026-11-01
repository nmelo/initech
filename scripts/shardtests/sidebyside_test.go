package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The side-by-side step in build-and-test (ini-c5a.7), run for real: its own
// script, taken from ci.yml, under GitHub's default shell for a Mac runner
// (bash -e), with a fake make and go on PATH. The two release suites that run
// on Linux and the census start together and each exit code gates on its own: a red in any one
// fails the step, every log still prints, and a pull request skips the census.

const sideBySideStep = "Release suites and the census, side by side"

// sideBySide is the step's run script and env, read from ci.yml.
func sideBySide(t *testing.T) (string, map[string]string) {
	t.Helper()
	for _, s := range readCI(t).Jobs["build-and-test"].Steps {
		if s.Name == sideBySideStep {
			return s.Run, s.Env
		}
	}
	t.Fatalf("build-and-test has no step named %q: this test is not reading it", sideBySideStep)
	return "", nil
}

// The fakes. Each participant (a make target, or "census" for the census's
// -list) marks itself started and then waits for all $EXPECT participants, so
// a step that ran them one after another fails here: the first would wait
// alone. Each prints a line naming itself into its log and exits with
// FAKE_RC_<participant>, 0 when unset. go records each call's arguments and
// INITECH_CENSUS.
const fakeMake = `#!/bin/bash
p="$1"
touch "$BARRIER/$p"
for i in $(seq 1 100); do
  [ "$(ls "$BARRIER" | wc -l | tr -d ' ')" -ge "$EXPECT" ] && break
  sleep 0.05
done
[ "$(ls "$BARRIER" | wc -l | tr -d ' ')" -ge "$EXPECT" ] || { echo "$p ran alone"; exit 97; }
echo "log of $p"
v="FAKE_RC_${p//-/_}"
exit "${!v:-0}"
`

const fakeGo = `#!/bin/bash
echo "go $* INITECH_CENSUS=$INITECH_CENSUS" >> "$CALLS"
case " $* " in
*" -list "*)
  touch "$BARRIER/census"
  for i in $(seq 1 100); do
    [ "$(ls "$BARRIER" | wc -l | tr -d ' ')" -ge "$EXPECT" ] && break
    sleep 0.05
  done
  [ "$(ls "$BARRIER" | wc -l | tr -d ' ')" -ge "$EXPECT" ] || { echo "census ran alone"; exit 97; }
  echo "log of census list"
  exit "${FAKE_RC_census_list:-0}" ;;
*" -run "*)
  echo "log of census run"
  exit "${FAKE_RC_census_run:-0}" ;;
esac
exit 99
`

type stepResult struct {
	rc    int
	out   string
	calls string
}

// runSideBySide runs the step's script once with the given event and fake exit
// codes, in a fresh folder, bounded to 30 seconds.
func runSideBySide(t *testing.T, event string, rcs map[string]string) stepResult {
	t.Helper()
	script, env := sideBySide(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	barrier := filepath.Join(dir, "barrier")
	for _, d := range []string{bin, barrier} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, src := range map[string]string{"make": fakeMake, "go": fakeGo} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(src), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	scriptPath := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	expect := "3"
	if event == "pull_request" {
		expect = "2"
	}
	calls := filepath.Join(dir, "calls")
	cmd := exec.Command("bash", "-e", scriptPath)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + bin + ":/usr/bin:/bin",
		"GITHUB_EVENT_NAME=" + event,
		"BARRIER=" + barrier,
		"EXPECT=" + expect,
		"CALLS=" + calls,
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	for k, v := range rcs {
		cmd.Env = append(cmd.Env, "FAKE_RC_"+k+"="+v)
	}
	done := make(chan struct{})
	var out []byte
	var err error
	go func() { out, err = cmd.CombinedOutput(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("the step did not finish in 30s:\n%s", out)
	}
	rc := 0
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	c, _ := os.ReadFile(calls)
	return stepResult{rc, string(out), string(c)}
}

var participants = map[string]string{
	"release_install_test": "Install step tests",
	"release_publish_test": "Publish step tests",
}

// Every log prints, inside its group, whatever the outcome.
func assertEveryLog(t *testing.T, name string, r stepResult, census bool) {
	t.Helper()
	for _, l := range []string{"log of release-install-test", "log of release-publish-test"} {
		if !strings.Contains(r.out, l) {
			t.Errorf("%s: %q missing from the step's output:\n%s", name, l, r.out)
		}
	}
	if census && !strings.Contains(r.out, "log of census list") {
		t.Errorf("%s: the census log is missing:\n%s", name, r.out)
	}
}

func TestSideBySide_AllGreenPassesWithEveryLogAndTheCensusUnderItsGate(t *testing.T) {
	r := runSideBySide(t, "push", nil)
	if r.rc != 0 || strings.Contains(r.out, "::error::") {
		t.Fatalf("all green: exit %d\n%s", r.rc, r.out)
	}
	assertEveryLog(t, "all green", r, true)
	want := "go test ./internal/tui/ -list TestCensus_|TestRolesLessBoot_|TestWindowCLI_ INITECH_CENSUS=1\n" +
		"go test ./internal/tui/ -count=1 -timeout 900s -run TestCensus_|TestRolesLessBoot_|TestWindowCLI_ INITECH_CENSUS=1\n"
	if r.calls != want {
		t.Errorf("go was called\n%s\nwant\n%s", r.calls, want)
	}
	for _, g := range []string{"::group::Install step tests (exit 0)", "::group::Publish step tests (exit 0)", "::group::Census and window-CLI integration (exit 0)"} {
		if !strings.Contains(r.out, g) {
			t.Errorf("no %q in the output:\n%s", g, r.out)
		}
	}
}

// A red in any one participant fails the step and names it, and the others'
// logs still print: no exit code hides behind another's.
func TestSideBySide_EachRedFailsTheStepAlone(t *testing.T) {
	cases := map[string]string{"census_list": "Census and window-CLI integration", "census_run": "Census and window-CLI integration"}
	for k, v := range participants {
		cases[k] = v
	}
	for key, name := range cases {
		r := runSideBySide(t, "push", map[string]string{key: "3"})
		if r.rc == 0 {
			t.Errorf("%s red: the step passed\n%s", key, r.out)
		}
		if n := strings.Count(r.out, "::error::"); n != 1 || !strings.Contains(r.out, "::error::"+name+" failed (exit 3)") {
			t.Errorf("%s red: want one ::error:: naming %s, got %d:\n%s", key, name, n, r.out)
		}
		assertEveryLog(t, key+" red", r, true)
		if key == "census_list" && strings.Contains(r.calls, " -run ") {
			t.Errorf("the census ran its tests after its -list failed:\n%s", r.calls)
		}
	}
	r := runSideBySide(t, "push", map[string]string{"release_install_test": "2", "census_run": "1"})
	if r.rc == 0 || strings.Count(r.out, "::error::") != 2 {
		t.Errorf("two reds: exit %d, want both named:\n%s", r.rc, r.out)
	}
}

// On a pull request the census does not run, as census-leg did not, and the
// release suites still gate.
func TestSideBySide_PullRequestSkipsOnlyTheCensus(t *testing.T) {
	r := runSideBySide(t, "pull_request", nil)
	if r.rc != 0 || r.calls != "" || !strings.Contains(r.out, "skipped on a pull request") {
		t.Errorf("pull request: exit %d, go calls %q:\n%s", r.rc, r.calls, r.out)
	}
	assertEveryLog(t, "pull request", r, false)
	r = runSideBySide(t, "pull_request", map[string]string{"release_publish_test": "5"})
	if r.rc == 0 || !strings.Contains(r.out, "::error::Publish step tests failed (exit 5)") {
		t.Errorf("pull request, publish red: exit %d:\n%s", r.rc, r.out)
	}
}

// A feed-it-nothing twin: a step with no participants started cannot pass the
// barrier, so the barrier is what proves they run together.
func TestSideBySide_TheBarrierFailsAParticipantThatRunsAlone(t *testing.T) {
	dir := t.TempDir()
	barrier := filepath.Join(dir, "b")
	if err := os.Mkdir(barrier, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "make"), []byte(fakeMake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(dir, "make"), "release-install-test")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "BARRIER=" + barrier, "EXPECT=2"}
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 97 || !strings.Contains(string(out), "ran alone") {
		t.Errorf("a participant alone: %v %q, want exit 97 and 'ran alone'", err, out)
	}
}
