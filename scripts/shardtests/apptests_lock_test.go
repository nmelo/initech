package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// firstInt returns the first group of re in the file at path as a number.
func firstInt(t *testing.T, path, re string) int {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(re).FindSubmatch(src)
	if m == nil {
		t.Fatalf("%s: no match for %s: this guard is reading nothing", path, re)
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestAppTests_TheJobOutlastsEveryWait adds up the waits an app-tests job can
// spend. The job's timeout has to hold all of them, or a long wait ends at the
// job timeout with nothing said, and the lock's named failure never prints.
func TestAppTests_TheJobOutlastsEveryWait(t *testing.T) {
	root := repoRootForWorkflows(t)
	const setupAndCleanupMinutes = 5
	gate := firstInt(t, filepath.Join(root, "scripts", "loadgate", "main.go"), `"max-wait", (\d+)\*time\.Minute`)
	script := filepath.Join(root, "app", "scripts", "sim-test.sh")
	lock := firstInt(t, script, `SIM_LOCK_WAIT:-(\d+)`)
	src, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	var run int // every alarm in the script: the first boot, then the tests
	alarms := regexp.MustCompile(`alarm (\d+);`).FindAllSubmatch(src, -1)
	for _, m := range alarms {
		n, err := strconv.Atoi(string(m[1]))
		if err != nil {
			t.Fatal(err)
		}
		run += n
	}
	if len(alarms) < 2 {
		t.Fatalf("sim-test.sh has %d alarms, want the boot's and the tests': this guard is reading nothing", len(alarms))
	}
	timeout := firstInt(t, filepath.Join(root, ".github", "workflows", "app-tests.yml"), `timeout-minutes: (\d+)`)
	need := gate + lock/60 + run/60 + setupAndCleanupMinutes
	if timeout < need {
		t.Errorf("app-tests timeout-minutes %d, want at least %d: gate %dm, lock %dm, run %dm, setup %dm", timeout, need, gate, lock/60, run/60, setupAndCleanupMinutes)
	}
}

// TestSimTest_ALockHeldPastTheBoundFailsNamingItsHolder plants a live holder
// and shrinks the bound. The script must say who holds the simulator, fail,
// and leave the holder's lock alone. It stops before any simulator is touched.
func TestSimTest_ALockHeldPastTheBoundFailsNamingItsHolder(t *testing.T) {
	root := repoRootForWorkflows(t)
	lock := filepath.Join(t.TempDir(), "sim.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	const owner = "run 41371587308 on eng4-branch (iphone)"
	pid := strconv.Itoa(os.Getpid()) // alive, so the takeover rule leaves it
	for name, body := range map[string]string{"pid": pid, "owner": owner} {
		if err := os.WriteFile(filepath.Join(lock, name), []byte(body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(root, "app", "scripts", "sim-test.sh"), "iphone", filepath.Join(t.TempDir(), "x.log"))
	cmd.Env = append(os.Environ(), "SIM_LOCK="+lock, "SIM_LOCK_WAIT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("sim-test exit %v, want 2\nstdout %s\nstderr %s", err, &stdout, &stderr)
	}
	wait := fmt.Sprintf("waiting for the simulator lock, 0 of 0 minutes so far, held by %s, pid %s", owner, pid)
	if !strings.Contains(stdout.String(), wait) {
		t.Errorf("stdout %q, want the waiting line %q", &stdout, wait)
	}
	refused := fmt.Sprintf("still held after 0 minutes by %s, pid %s; refused", owner, pid)
	if !strings.Contains(stderr.String(), refused) {
		t.Errorf("stderr %q, want the failure %q", &stderr, refused)
	}
	if got, err := os.ReadFile(filepath.Join(lock, "pid")); err != nil || strings.TrimSpace(string(got)) != pid {
		t.Errorf("the holder's lock was disturbed: pid file %q, %v", got, err)
	}
}
