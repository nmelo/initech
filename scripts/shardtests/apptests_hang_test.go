package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ini-qiv AC3: the bounds stay as they are. The first boot's alarm is seven
// minutes and the tests' is eighteen; a hang is found and fixed, never waited
// out with a wider bound.
func TestSimTest_TheBoundsAreSevenAndEighteenMinutes(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRootForWorkflows(t), "app", "scripts", "sim-test.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range regexp.MustCompile(`alarm (\d+);`).FindAllSubmatch(src, -1) {
		got = append(got, string(m[1]))
	}
	if strings.Join(got, ",") != "420,1080" {
		t.Errorf("sim-test.sh alarms %v, want the boot's 420 and the tests' 1080 seconds", got)
	}
}

// probeRun starts hang-probe.sh against a stand-in xcodebuild (a sleep) and
// the log at path, quiet after one second, looking every second. It returns
// the out file and a stop func that ends both and gives the probe's stdout.
func probeRun(t *testing.T, log string) (string, func() string) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("hang-probe.sh reads BSD stat and sample: macOS only")
	}
	fake := exec.Command("sleep", "60")
	if err := fake.Start(); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "x.log.hang")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	probe := exec.CommandContext(ctx, "bash", filepath.Join(repoRootForWorkflows(t), "app", "scripts", "hang-probe.sh"),
		strconv.Itoa(fake.Process.Pid), log, "FAKE-UDID", out)
	probe.Env = append(os.Environ(), "HANG_QUIET=1", "HANG_EVERY=1", "HANG_SIMCTL=false", "HANG_DERIVED="+t.TempDir())
	var stdout bytes.Buffer
	probe.Stdout = &stdout
	if err := probe.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() string {
		if !stopped {
			stopped = true
			_ = fake.Process.Kill()
			_, _ = fake.Process.Wait()
			_ = probe.Wait()
			cancel()
		}
		return stdout.String()
	}
	t.Cleanup(func() { stop() })
	return out, stop
}

// writeOldLog writes body to a log whose last write was a minute ago.
func writeOldLog(t *testing.T, body string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "x.log")
	if err := os.WriteFile(log, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(log, old, old); err != nil {
		t.Fatal(err)
	}
	return log
}

func waitFile(path string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// ini-qiv AC1's instrument: a log silent right after a suite line gets one
// record of what xcodebuild was doing: its process tree by command name, and
// one line on the step log; a second quiet spell writes nothing more.
func TestHangProbe_ASuiteGoneQuietIsRecordedOnce(t *testing.T) {
	log := writeOldLog(t, "Test Suite 'All tests' passed at 2026-11-04 18:23:38.943.\n\t Executed 23 tests, with 0 failures (0 unexpected) in 497.542 (497.580) seconds\n")
	out, stop := probeRun(t, log)
	if !waitFile(out, 15*time.Second) {
		t.Fatalf("no record after the log went quiet; probe said %q", stop())
	}
	time.Sleep(1500 * time.Millisecond) // the record is written whole before the line
	b, _ := os.ReadFile(out)
	got := string(b)
	for _, want := range []string{"hang probe at ", "the log silent ", "## process tree under xcodebuild", "## in the device FAKE-UDID", "## newest result bundle"} {
		if !strings.Contains(got, want) {
			t.Errorf("record lacks %q:\n%s", want, got)
		}
	}
	var tree []string
	for _, l := range strings.Split(got, "\n") {
		if strings.HasSuffix(strings.TrimSpace(l), "sleep") {
			tree = append(tree, l)
		}
	}
	if len(tree) != 1 {
		t.Errorf("the tree does not name the stand-in by command alone:\n%s", got)
	}
	if strings.Contains(got, "sleep 60") {
		t.Errorf("the record carries a command line:\n%s", got)
	}
	first, _ := os.Stat(out)
	old := time.Now().Add(-time.Minute)
	_ = os.Chtimes(log, old, old)
	time.Sleep(3 * time.Second)
	again, _ := os.Stat(out)
	said := stop()
	if !again.ModTime().Equal(first.ModTime()) {
		t.Error("the record was written a second time")
	}
	if strings.Count(said, "xcodebuild silent ") != 1 {
		t.Errorf("the step log line %d times, want once: %q", strings.Count(said, "xcodebuild silent "), said)
	}
}

// Feed it nothing: a log that ends mid-test, however old, and a log still
// being written after a suite, record nothing. The test above is the twin.
func TestHangProbe_ASlowTestOrAMovingLogRecordsNothing(t *testing.T) {
	midTest := writeOldLog(t, "    t =    12.32s Tap \"Enter\" Button\n    t =    12.35s     Check for interrupting elements\n")
	out, stop := probeRun(t, midTest)
	time.Sleep(4 * time.Second)
	if said := stop(); waitFile(out, 0) || said != "" {
		t.Errorf("a slow test was recorded as a hang: %q", said)
	}

	moving := filepath.Join(t.TempDir(), "y.log")
	if err := os.WriteFile(moving, []byte("Test Suite 'All tests' passed at 2026-11-04 18:23:38.943.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out2, stop2 := probeRun(t, moving)
	for i := 0; i < 8; i++ {
		now := time.Now()
		_ = os.Chtimes(moving, now, now) // still being written
		time.Sleep(500 * time.Millisecond)
	}
	if said := stop2(); waitFile(out2, 0) || said != "" {
		t.Errorf("a log still moving was recorded as a hang: %q", said)
	}
}

// sim-test.sh changes into the app folder before the tests, so the probe is
// started by a path read before that cd; a path from $0 then names no file
// (run 41952270959: "app/scripts/hang-probe.sh: No such file or directory").
func TestSimTest_TheProbeIsStartedByAPathReadBeforeTheCd(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRootForWorkflows(t), "app", "scripts", "sim-test.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	here := strings.Index(s, `here=$(cd "$(dirname "$0")" && pwd)`)
	cd := strings.Index(s, `cd "$app" || exit 2`)
	start := strings.Index(s, `"$here/hang-probe.sh" "$xb"`)
	if here < 0 || cd < 0 || start < 0 || !(here < cd && cd < start) {
		t.Errorf("want here= (at %d) before the cd (at %d) before the probe's start by $here (at %d)", here, cd, start)
	}
	if strings.Contains(s, `"$(dirname "$0")/hang-probe.sh"`) {
		t.Error("the probe is started by a path from $0, which names no file after the cd")
	}
}
