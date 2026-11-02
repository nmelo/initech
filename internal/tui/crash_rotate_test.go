package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// ini-7rd part 3: a frozen loop dumps once, then at most every five minutes,
// and crash.log stops growing at its cap. These run the real renderWatchdog in
// a synctest bubble, so eleven minutes of freeze take no wall time.

// watchdogDumps returns the "Stale for" value of each dump in crash.log.
func watchdogDumps(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".initech", "crash.log"))
	if err != nil {
		t.Fatalf("no crash.log: %v", err)
	}
	var stale []string
	for _, m := range regexp.MustCompile(`(?m)^Stale for: (\S+)$`).FindAllSubmatch(data, -1) {
		stale = append(stale, string(m[1]))
	}
	return stale
}

// TestWatchdog_OneFreezeDumpsOnceThenEveryFiveMinutes is the 1.6 MB a minute by
// effect: base dumped every fifteen seconds of freeze, and reset the clock each
// time, so every dump said 15s however long the freeze had lasted.
func TestWatchdog_OneFreezeDumpsOnceThenEveryFiveMinutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		var lastRender atomic.Int64
		lastRender.Store(time.Now().UnixNano())
		quit := make(chan struct{})
		go renderWatchdog(&lastRender, 10*time.Second, dir, "test", quit)

		time.Sleep(11 * time.Minute)
		close(quit)
		synctest.Wait()

		got := strings.Join(watchdogDumps(t, dir), " ")
		if want := "15s 5m15s 10m15s"; got != want {
			t.Fatalf("eleven minutes of one freeze dumped at stale %q, want %q: one dump past the "+
				"timeout, then one every five minutes, each naming the real length", got, want)
		}
	})
}

// TestWatchdog_ARenderEndsTheFreeze: the five minutes are per freeze. After a
// render the next freeze is a new one and gets its own first dump at once.
func TestWatchdog_ARenderEndsTheFreeze(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		var lastRender atomic.Int64
		lastRender.Store(time.Now().UnixNano())
		quit := make(chan struct{})
		go renderWatchdog(&lastRender, 10*time.Second, dir, "test", quit)

		time.Sleep(17 * time.Second) // First freeze, dumped at 15s.
		lastRender.Store(time.Now().UnixNano())
		time.Sleep(20 * time.Second) // Second freeze, past the timeout.
		close(quit)
		synctest.Wait()

		if got := watchdogDumps(t, dir); len(got) != 2 {
			t.Fatalf("two freezes dumped %d times (%v), want one each", len(got), got)
		}
	})
}

// TestWatchdog_NoRenderYetIsNotAFreeze: a clock at zero means startup.
func TestWatchdog_NoRenderYetIsNotAFreeze(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		var lastRender atomic.Int64
		quit := make(chan struct{})
		go renderWatchdog(&lastRender, 10*time.Second, dir, "test", quit)
		time.Sleep(time.Minute)
		close(quit)
		synctest.Wait()
		if _, err := os.Stat(filepath.Join(dir, ".initech", "crash.log")); !os.IsNotExist(err) {
			t.Fatalf("a watchdog with no render yet wrote a dump (stat err %v)", err)
		}
	})
}

// sizedCrashLog writes marker at the start of a crash.log of exactly size bytes
// and returns its path. The rest is a hole, so a 20 MB file costs no disk.
func sizedCrashLog(t *testing.T, dir string, size int64, marker string) string {
	t.Helper()
	root := filepath.Join(dir, ".initech")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "crash.log")
	if err := os.WriteFile(path, []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
	return path
}

func fileHas(t *testing.T, path, want string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Contains(string(data), want)
}

func TestCrashLog_PastTheCapMovesToDotOneAndStartsNew(t *testing.T) {
	dir := t.TempDir()
	path := sizedCrashLog(t, dir, crashLogCap+1, "OLDDUMP")

	if got := appendCrashLog(dir, "NEWDUMP\n"); got != path {
		t.Fatalf("appendCrashLog returned %q, want %q", got, path)
	}
	if !fileHas(t, path+".1", "OLDDUMP") {
		t.Fatal("crash.log.1 does not hold the old log")
	}
	if data, _ := os.ReadFile(path); string(data) != "NEWDUMP\n" {
		t.Fatalf("the new crash.log holds %d bytes, want only the new report", len(data))
	}
}

func TestCrashLog_AtTheCapIsNotRotated(t *testing.T) {
	dir := t.TempDir()
	path := sizedCrashLog(t, dir, crashLogCap, "OLDDUMP")

	appendCrashLog(dir, "NEWDUMP\n")
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("a log exactly at the cap was rotated (stat err %v)", err)
	}
	if !fileHas(t, path, "OLDDUMP") || !fileHas(t, path, "NEWDUMP") {
		t.Fatal("a log at the cap should have kept its content and taken the report")
	}
}

// TestCrashLog_KeepsOnePreviousFile: a second rotation replaces crash.log.1, so
// the log never takes more than the two files.
func TestCrashLog_KeepsOnePreviousFile(t *testing.T) {
	dir := t.TempDir()
	path := sizedCrashLog(t, dir, crashLogCap+1, "SECOND")
	if err := os.WriteFile(path+".1", []byte("FIRST"), 0600); err != nil {
		t.Fatal(err)
	}

	appendCrashLog(dir, "THIRD\n")
	if fileHas(t, path+".1", "FIRST") || !fileHas(t, path+".1", "SECOND") {
		t.Fatal("crash.log.1 should hold the file just rotated and nothing older")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("the folder holds %d files, want crash.log and crash.log.1", len(entries))
	}
}

// TestCrashLog_APanicAndADumpBothRotate: both writers go through the cap.
func TestCrashLog_APanicAndADumpBothRotate(t *testing.T) {
	dir := t.TempDir()
	path := sizedCrashLog(t, dir, crashLogCap+1, "OLDDUMP")
	crashLog(dir, "test", "boom")
	if !fileHas(t, path+".1", "OLDDUMP") || !fileHas(t, path, "INITECH CRASH") {
		t.Fatal("the panic path did not rotate")
	}

	dir = t.TempDir()
	path = sizedCrashLog(t, dir, crashLogCap+1, "OLDDUMP")
	dumpWatchdogStacks(dir, "test", 15*time.Second)
	if !fileHas(t, path+".1", "OLDDUMP") || !fileHas(t, path, "RENDER WATCHDOG") {
		t.Fatal("the watchdog dump did not rotate")
	}
}

// TestCrashLog_AFailedRotationStillWritesAndSaysSo: a report matters more than
// the cap, and the failure is named where the reader will look.
func TestCrashLog_AFailedRotationStillWritesAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	path := sizedCrashLog(t, dir, crashLogCap+1, "OLDDUMP")
	// A directory in crash.log.1's place makes the rename fail.
	if err := os.Mkdir(path+".1", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path+".1", "x"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	if got := appendCrashLog(dir, "NEWDUMP\n"); got != path {
		t.Fatalf("appendCrashLog returned %q, want the report written to %q", got, path)
	}
	if !fileHas(t, path, "crash.log not rotated") || !fileHas(t, path, "NEWDUMP") {
		t.Fatal("the report or the rotation failure is missing from crash.log")
	}
}

func TestCrashLog_NothingToWriteToReturnsNoPath(t *testing.T) {
	if got := appendCrashLog("", "report"); got != "" {
		t.Fatalf("no project root returned %q", got)
	}
	// .initech is a file, so the log cannot be opened.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".initech"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if got := appendCrashLog(dir, "report"); got != "" {
		t.Fatalf("an unwritable folder returned %q", got)
	}
}
