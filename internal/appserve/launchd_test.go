package appserve

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// ini-r0q.12: the launchd job, the unclean-exit marker and the notify guard.

// AC1, AC2: the job has exactly the listed keys: KeepAlive true and a ten
// second throttle are the watchdog.
func TestPlist_HasExactlyTheListedKeys(t *testing.T) {
	args := []string{"/opt/homebrew/bin/initech", "app-serve", "--listen", "tailnet", "--port", "7392", "--projects", "/p/a b", "--notify", `/n "x" & <y>`}
	got := string(RenderPlist("/home/u", "/opt/homebrew/bin:/usr/bin:/a&b", args))
	keys := regexp.MustCompile(`<key>([^<]*)</key>`).FindAllStringSubmatch(got, -1)
	var names []string
	for _, k := range keys {
		names = append(names, k[1])
	}
	want := "Label ProgramArguments EnvironmentVariables PATH KeepAlive ThrottleInterval StandardOutPath StandardErrorPath"
	if strings.Join(names, " ") != want {
		t.Errorf("keys %v, want %s", names, want)
	}
	for _, s := range []string{
		"<key>Label</key>\n\t<string>com.initech.app-serve</string>",
		"<key>KeepAlive</key>\n\t<true/>",
		"<key>EnvironmentVariables</key>\n\t<dict>\n\t\t<key>PATH</key>\n\t\t<string>/opt/homebrew/bin:/usr/bin:/a&amp;b</string>\n\t</dict>",
		"<key>ThrottleInterval</key>\n\t<integer>10</integer>",
		"<key>StandardOutPath</key>\n\t<string>/home/u/Library/Logs/initech/app-serve.log</string>",
		"<key>StandardErrorPath</key>\n\t<string>/home/u/Library/Logs/initech/app-serve.log</string>",
		"<string>/opt/homebrew/bin/initech</string>\n\t\t<string>app-serve</string>",
		"<string>/p/a b</string>",
		"<string>/n &#34;x&#34; &amp; &lt;y&gt;</string>",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("the job lacks %q:\n%s", s, got)
		}
	}
	if PlistPath("/home/u") != "/home/u/Library/LaunchAgents/com.initech.app-serve.plist" {
		t.Errorf("job path %s", PlistPath("/home/u"))
	}
}

// markerRig is a state dir and a --notify command that records each run's
// arguments, one line a run.
type markerRig struct {
	dir, notify, ran string
	now              time.Time
	log              bytes.Buffer
}

func newMarkerRig(t *testing.T) *markerRig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows: --notify runs under /bin/sh; launchd is macOS")
	}
	r := &markerRig{dir: t.TempDir(), now: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)}
	r.ran = filepath.Join(r.dir, "notify.ran")
	script := filepath.Join(t.TempDir(), "notify.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$*\" >> "+r.ran+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r.notify = script
	return r
}

func (r *markerRig) clock() time.Time { return r.now }

func (r *markerRig) plantMarker(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, MarkerFile), []byte("pid 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r *markerRig) markerThere() bool {
	_, err := os.Stat(filepath.Join(r.dir, MarkerFile))
	return err == nil
}

func (r *markerRig) runs() []string {
	raw, err := os.ReadFile(r.ran)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// AC3: a start with the marker runs notify once with the fixed line and
// clears the marker. The clock moves past the guard before the second look,
// so only a cleared marker keeps it from running twice.
func TestMarker_AStartAfterAnUncleanExitNotifiesOnce(t *testing.T) {
	r := newMarkerRig(t)
	r.plantMarker(t)
	checkUncleanExit(r.dir, r.notify, &r.log, r.clock)
	if got := r.runs(); len(got) != 1 || got[0] != NotifyLine {
		t.Fatalf("notify ran %q, want once with %q", got, NotifyLine)
	}
	if r.markerThere() {
		t.Error("the marker was not cleared")
	}
	r.now = r.now.Add(2 * notifyEvery)
	checkUncleanExit(r.dir, r.notify, &r.log, r.clock)
	if got := r.runs(); len(got) != 1 {
		t.Errorf("notify ran %d times, want once", len(got))
	}
	if NotifyLine != "initech app-serve restarted after an unclean exit" {
		t.Errorf("the fixed line is %q", NotifyLine)
	}
}

// AC3: no marker, nothing runs and nothing is logged.
func TestMarker_AStartWithNoMarkerRunsNothing(t *testing.T) {
	r := newMarkerRig(t)
	checkUncleanExit(r.dir, r.notify, &r.log, r.clock)
	if len(r.runs()) != 0 || r.log.Len() != 0 {
		t.Errorf("ran %q, logged %q", r.runs(), r.log.String())
	}
}

// AC3: --notify unset runs nothing and still clears the marker and logs the
// restart; it stamps nothing, so a later --notify is not held back.
func TestMarker_NoNotifyStillClearsTheMarker(t *testing.T) {
	r := newMarkerRig(t)
	r.plantMarker(t)
	checkUncleanExit(r.dir, "", &r.log, r.clock)
	if r.markerThere() || !strings.Contains(r.log.String(), NotifyLine) {
		t.Errorf("marker there %v, log %q", r.markerThere(), r.log.String())
	}
	if _, err := os.Stat(filepath.Join(r.dir, NotifiedAtFile)); err == nil {
		t.Error("a stamp was written with no notify")
	}
}

// AC3, G3: a crash loop notifies once an hour; the marker is cleared and the
// restart logged every time.
func TestMarker_ACrashLoopNotifiesOncePerHour(t *testing.T) {
	r := newMarkerRig(t)
	for i := 0; i < 5; i++ {
		r.plantMarker(t)
		checkUncleanExit(r.dir, r.notify, &r.log, r.clock)
		if r.markerThere() {
			t.Fatalf("restart %d left the marker", i)
		}
		r.now = r.now.Add(10 * time.Second)
	}
	if got := r.runs(); len(got) != 1 {
		t.Errorf("notify ran %d times in a minute, want once", len(got))
	}
	if n := strings.Count(r.log.String(), NotifyLine); n != 5 {
		t.Errorf("the log records %d restarts, want 5", n)
	}
	// One second short of the hour since the first: still held.
	r.now = time.Date(2026, 11, 1, 10, 59, 59, 0, time.UTC)
	r.plantMarker(t)
	checkUncleanExit(r.dir, r.notify, &r.log, r.clock)
	if got := r.runs(); len(got) != 1 {
		t.Errorf("notify ran %d times before the hour, want once", len(got))
	}
	// The hour is up: it runs again.
	r.now = time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC)
	r.plantMarker(t)
	checkUncleanExit(r.dir, r.notify, &r.log, r.clock)
	if got := r.runs(); len(got) != 2 {
		t.Errorf("notify ran %d times after the hour, want twice", len(got))
	}
}

// A stamp that cannot be read does not hold notify back.
func TestMarker_ADamagedStampAllowsTheNotify(t *testing.T) {
	r := newMarkerRig(t)
	if err := os.WriteFile(filepath.Join(r.dir, NotifiedAtFile), []byte("not a time\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.plantMarker(t)
	checkUncleanExit(r.dir, r.notify, &r.log, r.clock)
	if len(r.runs()) != 1 {
		t.Errorf("notify ran %d times", len(r.runs()))
	}
}

// AC5: a notify that fails is logged once, the marker is still cleared, the
// service still starts. One that hangs is bounded.
func TestMarker_AFailingNotifyDoesNotStopTheService(t *testing.T) {
	r := newMarkerRig(t)
	r.plantMarker(t)
	served := false
	err := runMarked(r.dir, "/bin/sh -c 'exit 3'", &r.log, r.clock, func() error { served = true; return errors.New("done") })
	if !served || err == nil || err.Error() != "done" {
		t.Errorf("served %v, err %v", served, err)
	}
	if n := strings.Count(r.log.String(), "the --notify command failed"); n != 1 {
		t.Errorf("logged %d failures, want one: %q", n, r.log.String())
	}
	old := notifyBound
	notifyBound = 200 * time.Millisecond
	t.Cleanup(func() { notifyBound = old })
	h := newMarkerRig(t)
	h.plantMarker(t)
	start := time.Now()
	checkUncleanExit(h.dir, "/bin/sleep 30 #", &h.log, h.clock)
	if time.Since(start) > 5*time.Second || !strings.Contains(h.log.String(), "did not finish") || h.markerThere() {
		t.Errorf("after %v, log %q, marker there %v", time.Since(start), h.log.String(), h.markerThere())
	}
}

// AC3: the marker is written for the run (mode 0600, no token), and a serve
// that returns an error keeps it, so the next start reports it.
func TestRunMarked_AServeErrorKeepsTheMarker(t *testing.T) {
	r := newMarkerRig(t)
	var during bool
	var mode os.FileMode
	boom := errors.New("listener died")
	err := runMarked(r.dir, r.notify, &r.log, r.clock, func() error {
		during = r.markerThere()
		if fi, err := os.Stat(filepath.Join(r.dir, MarkerFile)); err == nil {
			mode = fi.Mode().Perm()
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("err %v", err)
	}
	if !during || !r.markerThere() || mode != 0o600 {
		t.Errorf("marker during %v, after %v, mode %o", during, r.markerThere(), mode)
	}
	if len(r.runs()) != 0 {
		t.Errorf("a first start notified: %q", r.runs())
	}
}

// AC3: SIGTERM and SIGINT are clean: the marker goes, nil comes back, nothing
// notifies at the next start.
func TestRunMarked_ASignalIsACleanExit(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		r := newMarkerRig(t)
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		done := make(chan error, 1)
		go func() {
			done <- runMarked(r.dir, r.notify, &r.log, r.clock, func() error {
				self, err := os.FindProcess(os.Getpid())
				if err != nil {
					t.Error(err)
				} else if err := self.Signal(sig); err != nil {
					t.Error(err)
				}
				<-release
				return nil
			})
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%v: err %v", sig, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%v: RunMarked did not return", sig)
		}
		if r.markerThere() {
			t.Errorf("%v left the marker", sig)
		}
		checkUncleanExit(r.dir, r.notify, &r.log, r.clock)
		if len(r.runs()) != 0 {
			t.Errorf("%v: the next start notified %q", sig, r.runs())
		}
	}
}

// AC3: the token is in neither the notify line, the log, the marker nor the
// stamp.
func TestMarker_TheTokenAppearsNowhere(t *testing.T) {
	r := newMarkerRig(t)
	token, err := IssueToken(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	r.plantMarker(t)
	err = runMarked(r.dir, r.notify, &r.log, r.clock, func() error { return errors.New("died") })
	if err == nil {
		t.Fatal("want the serve error")
	}
	places := map[string]string{"the log": r.log.String(), "the notify line": strings.Join(r.runs(), "\n")}
	for _, f := range []string{MarkerFile, NotifiedAtFile} {
		raw, err := os.ReadFile(filepath.Join(r.dir, f))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		places[f] = string(raw)
	}
	if places["the notify line"] == "" {
		t.Fatal("notify did not run, so this proves nothing")
	}
	for where, text := range places {
		if strings.Contains(text, token) {
			t.Errorf("the token is in %s", where)
		}
	}
}

// A folder that does not answer or is refused is reported: the log names the
// folder and why, notify gets the line.
func TestReportFolder_NamesTheFolderAndWhy(t *testing.T) {
	r := newMarkerRig(t)
	ReportFolder(r.dir, r.notify, &r.log, "/some/rigs", "it has not answered")
	want := "initech app-serve cannot read /some/rigs: it has not answered"
	if got := r.runs(); len(got) != 1 || got[0] != want {
		t.Errorf("notify ran %q, want once with %q", got, want)
	}
	if r.log.String() != want+"\n" {
		t.Errorf("the log says %q", r.log.String())
	}
}

// The macOS hint is given only for a folder under ~/Desktop, ~/Documents or
// ~/Downloads; a missing folder elsewhere, or one merely named like them, is
// not blamed on macOS.
func TestStuckFolderLine_BlamesMacOSOnlyUnderAGuardedFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	hint := "; macOS guards ~/Desktop, ~/Documents and ~/Downloads for launchd jobs until the binary is allowed"
	for _, name := range []string{"Desktop", "Documents", "Downloads"} {
		dir := filepath.Join(home, name, "Projects")
		if got := StuckFolderLine(dir, "it has not answered"); got != "initech app-serve cannot read "+dir+": it has not answered"+hint {
			t.Errorf("%s: %q", name, got)
		}
	}
	for _, dir := range []string{"/srv/rigs", filepath.Join(home, "Desktopish"), filepath.Join(home, "Projects")} {
		why := "open " + dir + ": no such file or directory"
		if got := StuckFolderLine(dir, why); got != "initech app-serve cannot read "+dir+": "+why {
			t.Errorf("%s: %q", dir, got)
		}
	}
	t.Setenv("HOME", "")
	if got := StuckFolderLine(filepath.Join(home, "Desktop"), "x"); strings.Contains(got, "macOS") {
		t.Errorf("no HOME: %q", got)
	}
}

// Within the hour a second folder is logged and not notified; after it, it is.
// With no --notify it is logged and stamps nothing.
func TestReportFolder_IsGuardedPerHour(t *testing.T) {
	r := newMarkerRig(t)
	ReportFolder(r.dir, r.notify, &r.log, "/one", "held")
	ReportFolder(r.dir, r.notify, &r.log, "/two", "held")
	if got := r.runs(); len(got) != 1 {
		t.Errorf("notify ran %d times within the hour, want once: %q", len(got), got)
	}
	if !strings.Contains(r.log.String(), "/two") {
		t.Errorf("the log lacks the second folder: %q", r.log.String())
	}
	old := time.Now().UTC().Add(-2 * notifyEvery).Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(r.dir, FolderNotifiedAtFile), []byte(old+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ReportFolder(r.dir, r.notify, &r.log, "/three", "held")
	if got := r.runs(); len(got) != 2 {
		t.Errorf("notify ran %d times after the hour, want twice: %q", len(got), got)
	}
	n := newMarkerRig(t)
	ReportFolder(n.dir, "", &n.log, "/one", "held")
	if _, err := os.Stat(filepath.Join(n.dir, FolderNotifiedAtFile)); err == nil || !strings.Contains(n.log.String(), "/one") {
		t.Errorf("no notify: stamp err %v, log %q", err, n.log.String())
	}
}

// pm's ruling: the guard is per kind of message: the crash notice at a restart
// never silences the folder notice a second later, which is the actionable one.
func TestReportFolder_ARestartDoesNotSilenceIt(t *testing.T) {
	r := newMarkerRig(t)
	r.now = time.Now()
	r.plantMarker(t)
	checkUncleanExit(r.dir, r.notify, &r.log, r.clock)
	ReportFolder(r.dir, r.notify, &r.log, "/rigs", "held")
	got := strings.Join(r.runs(), "\n")
	for _, want := range []string{NotifyLine, StuckFolderLine("/rigs", "held")} {
		if !strings.Contains(got, want) {
			t.Errorf("notify ran %q, lacks %q", got, want)
		}
	}
	if n := len(r.runs()); n != 2 {
		t.Errorf("notify ran %d times, want 2: %q", n, got)
	}
}

// The token is in neither the folder's log line nor its notice.
func TestReportFolder_NeverTheToken(t *testing.T) {
	r := newMarkerRig(t)
	token, err := IssueToken(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	ReportFolder(r.dir, r.notify, &r.log, "/some/rigs", "refused")
	if strings.Contains(r.log.String()+strings.Join(r.runs(), "\n"), token) {
		t.Error("the token is in the log or the notice")
	}
	if len(r.runs()) != 1 {
		t.Fatal("notify did not run, so this proves nothing")
	}
}

// Two folders reporting at once notify once.
func TestReportFolder_TwoAtOnceNotifyOnce(t *testing.T) {
	r := newMarkerRig(t)
	var wg sync.WaitGroup
	var log lockedLog
	for _, dir := range []string{"/a", "/b", "/c", "/d"} {
		wg.Add(1)
		go func(dir string) {
			defer wg.Done()
			ReportFolder(r.dir, r.notify, &log, dir, "held")
		}(dir)
	}
	wg.Wait()
	if got := r.runs(); len(got) != 1 {
		t.Errorf("notify ran %d times, want once: %q", len(got), got)
	}
}

// lockedLog is a log several goroutines may write.
type lockedLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// A serve that stops by itself with no error, as the service's own signal
// handling does, is a clean exit: no marker, no report at the next start.
func TestRunMarked_AServeThatReturnsNilIsClean(t *testing.T) {
	r := newMarkerRig(t)
	if err := runMarked(r.dir, r.notify, &r.log, r.clock, func() error { return nil }); err != nil {
		t.Errorf("err %v", err)
	}
	if r.markerThere() {
		t.Error("a clean return left the marker")
	}
	checkUncleanExit(r.dir, r.notify, &r.log, r.clock)
	if len(r.runs()) != 0 {
		t.Errorf("the next start notified %q", r.runs())
	}
}

// A planned restart (the bound address moved, so the service exits for launchd
// to start it on the new one) is no crash: the marker goes and nothing is told,
// while the error still reaches the caller for its exit code and the log. The
// twin: any other error keeps the marker.
func TestRunMarked_APlannedRestartIsNotACrash(t *testing.T) {
	r := newMarkerRig(t)
	moved := fmt.Errorf("the address went away: %w", ErrPlannedRestart)
	err := runMarked(r.dir, r.notify, &r.log, r.clock, func() error { return moved })
	if !errors.Is(err, ErrPlannedRestart) || r.markerThere() {
		t.Errorf("err %v, marker there %v", err, r.markerThere())
	}
	checkUncleanExit(r.dir, r.notify, &r.log, r.clock)
	if len(r.runs()) != 0 {
		t.Errorf("a planned restart notified %q", r.runs())
	}
	if err := runMarked(r.dir, r.notify, &r.log, r.clock, func() error { return errors.New("died") }); err == nil || !r.markerThere() {
		t.Errorf("another error: err %v, marker there %v", err, r.markerThere())
	}
}
