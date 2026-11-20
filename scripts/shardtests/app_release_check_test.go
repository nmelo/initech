package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The app's debug launch options never ship (ini-mkg6): release-check.sh
// fails when the Release app holds an option's name, and when the Debug app
// does not (a search that cannot find a name where it is known to be proves
// nothing about where it is not). These tests drive the script on planted
// apps; the app-tests job runs it on the real builds, every run.

// plantSource writes an app source tree whose Swift names these launch options.
func plantSource(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"InitechApp", "InitechCore/Sources"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var src strings.Builder
	for _, n := range names {
		src.WriteString("let x = args.contains(\"" + n + "\")\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "InitechApp", "Hooks.swift"), []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// plantApp writes a built app holding this text in its binary.
func plantApp(t *testing.T, text string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "InitechApp.app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "InitechApp"), []byte("\x00\x01binary\x00"+text+"\x00more"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func releaseCheck(t *testing.T, src string, args ...string) (string, int) {
	t.Helper()
	_, stderr, code := sh(t, []string{"APP_DIR=" + src}, appScript(t, "release-check.sh"), args...)
	return stderr, code
}

// appScript is a script under app/scripts, from the repo's root.
func appScript(t *testing.T, name string) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "app", "scripts", name)
}

const optA, optB = "-initech-debug-unlocked", "-initech-debug-screen"

func TestReleaseCheck_ReleaseWithNoneAndDebugWithAll_Passes(t *testing.T) {
	src := plantSource(t, optA, optB)
	stderr, code := releaseCheck(t, src, "check", plantApp(t, "nothing here"), plantApp(t, optA+"\x00"+optB))
	if code != 0 {
		t.Fatalf("a clean Release beside a full Debug was refused (exit %d): %s", code, stderr)
	}
}

// The DEBUG guard removed: an option's name compiled into the build that ships.
func TestReleaseCheck_AnOptionInRelease_IsRefusedByName(t *testing.T) {
	src := plantSource(t, optA, optB)
	stderr, code := releaseCheck(t, src, "check", plantApp(t, "x"+optB+"y"), plantApp(t, optA+"\x00"+optB))
	if code != 1 || !strings.Contains(stderr, "Release app holds "+optB) {
		t.Fatalf("an option in the Release app: exit %d, said %q; want exit 1 naming %s", code, stderr, optB)
	}
	if strings.Contains(stderr, "holds "+optA) {
		t.Fatalf("the option that is not in Release was named too: %q", stderr)
	}
}

// The twin: a name the search cannot find in Debug says nothing about Release.
func TestReleaseCheck_AnOptionMissingFromDebug_IsRefused(t *testing.T) {
	src := plantSource(t, optA, optB)
	stderr, code := releaseCheck(t, src, "check", plantApp(t, "nothing"), plantApp(t, optA))
	if code != 1 || !strings.Contains(stderr, "Debug app does not hold "+optB) {
		t.Fatalf("a name missing from Debug: exit %d, said %q; want exit 1 naming %s", code, stderr, optB)
	}
}

// Feed it nothing to check with, and it refuses instead of passing.
func TestReleaseCheck_NothingToCheckWith_IsRefused(t *testing.T) {
	full := plantApp(t, optA+"\x00"+optB)
	empty := filepath.Join(t.TempDir(), "Empty.app")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		why  string
		src  string
		args []string
		say  string
	}{
		{"no option name in the source", plantSource(t), []string{"check", plantApp(t, "x"), full}, "nothing to check with"},
		{"a Release app that is not there", plantSource(t, optA, optB), []string{"check", filepath.Join(t.TempDir(), "none.app"), full}, "not an app"},
		{"an empty Release app", plantSource(t, optA, optB), []string{"check", empty, full}, "not an app"},
		{"an empty Debug app", plantSource(t, optA, optB), []string{"check", plantApp(t, "x"), empty}, "not an app"},
		{"one app only", plantSource(t, optA, optB), []string{"check", full}, "takes the Release app and the Debug app"},
		{"no mode", plantSource(t, optA, optB), nil, "check <Release .app>"},
	} {
		stderr, code := releaseCheck(t, c.src, c.args...)
		if code != 2 || !strings.Contains(stderr, c.say) {
			t.Errorf("%s: exit %d, said %q; want exit 2 saying %q", c.why, code, stderr, c.say)
		}
	}
}

// A short name is kept inside the instructions of an optimised build, not as
// text: the check passed with "-debug-focus" compiled into Release on purpose.
// So a name of 15 bytes or fewer is refused before any search.
func TestReleaseCheck_AShortOptionName_IsRefused(t *testing.T) {
	short := "-debug-focus"
	stderr, code := releaseCheck(t, plantSource(t, optA, short), "check", plantApp(t, "x"), plantApp(t, optA+"\x00"+short))
	if code != 2 || !strings.Contains(stderr, "the option name "+short) {
		t.Fatalf("a 12-byte option name: exit %d, said %q; want exit 2 naming it", code, stderr)
	}
}

// The check runs on every app-tests run, before the simulator tests.
func TestAppTestsWorkflow_RunsTheReleaseCheck(t *testing.T) {
	wf, err := os.ReadFile("../../.github/workflows/app-tests.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(wf)
	step := strings.Index(text, `run: app/scripts/release-check.sh build "$RUNNER_TEMP/release-check"`)
	tests := strings.Index(text, "- name: Test on a fresh simulator")
	if step < 0 || tests < 0 || step > tests {
		t.Fatalf("app-tests.yml does not run release-check.sh build before the simulator tests (step at %d, tests at %d)", step, tests)
	}
	before := text[:step]
	name := strings.LastIndex(before, "- name:")
	if strings.Contains(before[name:], "if:") {
		t.Fatal("the release check step carries a condition: it must run every time")
	}
}
