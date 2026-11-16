package tui

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nmelo/initech/internal/schedule"
)

// ini-6ib: the scheduler against a fake clock, a fake host and a fake command
// runner. The send path itself is real in the host tests at the end.

type fakeScheduleHost struct {
	mu        sync.Mutex
	delivered []string // "to|from|text"
	super     []string
	refuse    map[string]string // pane -> why
}

func (h *fakeScheduleHost) deliver(to, text, from string) (bool, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if why, ok := h.refuse[to]; ok {
		return false, why
	}
	h.delivered = append(h.delivered, to+"|"+from+"|"+text)
	return true, ""
}

func (h *fakeScheduleHost) tellSuper(text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.super = append(h.super, text)
}

func (h *fakeScheduleHost) snap() ([]string, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.delivered...), append([]string(nil), h.super...)
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

func clockAt(day, hour, min int) time.Time {
	return time.Date(2026, 11, day, hour, min, 0, 0, time.Local)
}

// schedRig is a rig root holding jobs, and a scheduler started at start.
func schedRig(t *testing.T, start time.Time, jobs ...schedule.Job) (*rigScheduler, *fakeScheduleHost, *fakeClock) {
	t.Helper()
	root := t.TempDir()
	f := &schedule.File{}
	for _, j := range jobs {
		if j.Created.IsZero() {
			j.Created = start
		}
		if _, err := f.Add(j); err != nil {
			t.Fatal(err)
		}
	}
	if err := schedule.Save(schedule.Path(root), f); err != nil {
		t.Fatal(err)
	}
	host := &fakeScheduleHost{refuse: map[string]string{}}
	clk := &fakeClock{t: start}
	return newRigScheduler(root, host, clk.now), host, clk
}

// step moves the clock and runs one tick to completion.
func (s *rigScheduler) step(clk *fakeClock, to time.Time) {
	clk.set(to)
	s.tick()
	s.wg.Wait()
}

func reload(t *testing.T, s *rigScheduler) *schedule.File {
	t.Helper()
	f, err := schedule.Load(s.path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestScheduler_ATimeJobDeliversOnceAtItsTime is AC2's time half by effect.
func TestScheduler_ATimeJobDeliversOnceAtItsTime(t *testing.T) {
	s, host, clk := schedRig(t, clockAt(4, 9, 0), schedule.Job{Owner: "eng1", To: "qa1", At: "09:30", Message: "standup"})
	s.step(clk, clockAt(4, 9, 29))
	if d, _ := host.snap(); len(d) != 0 {
		t.Fatalf("delivered early: %v", d)
	}
	s.step(clk, clockAt(4, 9, 30))
	s.step(clk, clockAt(4, 9, 30).Add(30*time.Second))
	s.step(clk, clockAt(4, 9, 31))
	d, sup := host.snap()
	if len(d) != 1 || d[0] != "qa1|eng1|[from eng1, schedule s1] standup" {
		t.Fatalf("delivered %q, want one message from eng1 to qa1", d)
	}
	if len(sup) != 0 {
		t.Fatalf("super heard %v for a good delivery", sup)
	}
	if j := reload(t, s).Jobs[0]; j.LastRun == nil || !j.LastRun.Equal(clockAt(4, 9, 30)) || j.LastResult != "delivered" {
		t.Fatalf("the file records %+v", j)
	}
}

// TestScheduler_AnIntervalJobDeliversEachInterval is AC2's interval half.
func TestScheduler_AnIntervalJobDeliversEachInterval(t *testing.T) {
	s, host, clk := schedRig(t, clockAt(4, 9, 0), schedule.Job{Owner: "eng1", To: "qa1", Every: "10m", Message: "tick"})
	for m := 1; m <= 30; m++ {
		s.step(clk, clockAt(4, 9, m))
	}
	if d, _ := host.snap(); len(d) != 3 {
		t.Fatalf("delivered %d times in 30 minutes of a 10m job, want 3", len(d))
	}
}

// TestScheduler_ARestartInsideTheMinuteDoesNotRunItTwice: the last run is on
// disk before the delivery, and a new scheduler reads it.
func TestScheduler_ARestartInsideTheMinuteDoesNotRunItTwice(t *testing.T) {
	s, host, clk := schedRig(t, clockAt(4, 9, 0), schedule.Job{Owner: "eng1", To: "qa1", At: "09:30", Message: "once"})
	s.step(clk, clockAt(4, 9, 30))
	again := newRigScheduler(s.root, host, clk.now) // initech restarted at 09:30:20.
	again.step(clk, clockAt(4, 9, 30).Add(20*time.Second))
	again.step(clk, clockAt(4, 9, 31))
	if d, _ := host.snap(); len(d) != 1 {
		t.Fatalf("delivered %d times across a restart in the same minute, want 1", len(d))
	}
}

// TestScheduler_AJobDueWhileDownRunsOnceAndSaysSo.
func TestScheduler_AJobDueWhileDownRunsOnceAndSaysSo(t *testing.T) {
	s, host, clk := schedRig(t, clockAt(4, 8, 0), schedule.Job{Owner: "eng1", To: "qa1", At: "09:30", Message: "report"})
	clk.set(clockAt(4, 11, 0)) // Down from 08:00 to 11:00.
	back := newRigScheduler(s.root, host, clk.now)
	back.step(clk, clockAt(4, 11, 0))
	back.step(clk, clockAt(4, 11, 1))
	d, _ := host.snap()
	if len(d) != 1 || !strings.Contains(d[0], "(due 2026-11-04 09:30 while initech was down) report") {
		t.Fatalf("delivered %q, want one run saying it was missed", d)
	}
}

// TestScheduler_ACommandRunsInTheRigRootWithItsExitAndOutput is AC3.
func TestScheduler_ACommandRunsInTheRigRootWithItsExitAndOutput(t *testing.T) {
	s, host, clk := schedRig(t, clockAt(4, 9, 0), schedule.Job{Owner: "qa1", To: "qa1", Every: "1m", Command: "printf hi; exit 2"})
	var dir string
	s.runLine = func(_ context.Context, d, line string) (string, int, error) {
		dir = d
		return strings.Repeat("x", 3000), 2, nil
	}
	s.step(clk, clockAt(4, 9, 1))
	d, sup := host.snap()
	if dir != s.root {
		t.Fatalf("ran in %q, want the rig root %q", dir, s.root)
	}
	if len(d) != 1 || !strings.Contains(d[0], "qa1|qa1|[from qa1, schedule s1] $ printf hi; exit 2\nexit 2\n") {
		t.Fatalf("delivered %q", d)
	}
	if !strings.Contains(d[0], "\n"+strings.Repeat("x", scheduleOutputCut)+"\n[output cut at 2 KB]") ||
		strings.Contains(d[0], strings.Repeat("x", scheduleOutputCut+1)) {
		t.Fatal("the output was not cut at exactly 2 KB")
	}
	if len(sup) != 1 || !strings.Contains(sup[0], "exited 2") {
		t.Fatalf("super heard %q, want one line about the exit", sup)
	}
}

// TestScheduler_ACommandPastItsBoundIsStoppedAndSaid uses the real runner with
// a shortened bound: the line is stopped, the target and super hear it.
func TestScheduler_ACommandPastItsBoundIsStoppedAndSaid(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a real shell for its bound")
	}
	s, host, clk := schedRig(t, clockAt(4, 9, 0), schedule.Job{Owner: "qa1", To: "qa1", Every: "1m", Command: "sleep 30"})
	s.bound = 300 * time.Millisecond
	start := time.Now()
	s.step(clk, clockAt(4, 9, 1))
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the bound took %s", took)
	}
	d, sup := host.snap()
	if len(d) != 1 || !strings.Contains(d[0], "stopped after 300ms") {
		t.Fatalf("delivered %q, want the stop said", d)
	}
	if len(sup) != 1 || !strings.Contains(sup[0], "was stopped after 300ms") {
		t.Fatalf("super heard %q", sup)
	}
}

// TestScheduler_TheDefaultBoundIsThirtySeconds pins the bound the guide and
// cron's help state; the bound test above sets its own, so it cannot.
func TestScheduler_TheDefaultBoundIsThirtySeconds(t *testing.T) {
	s := newRigScheduler(t.TempDir(), nil, time.Now)
	if s.bound != 30*time.Second {
		t.Fatalf("a new scheduler stops a command after %s, want 30s", s.bound)
	}
}

// TestScheduler_ARepeatedDeliveryFailureIsSaidOnceUntilItDelivers is pm's
// ruling on qa1's finding: an interval job to a missing pane told super every
// interval. Said once; a delivery re-arms it; another reason is said; and a
// restart on the same file says nothing more.
func TestScheduler_ARepeatedDeliveryFailureIsSaidOnceUntilItDelivers(t *testing.T) {
	s, host, clk := schedRig(t, clockAt(4, 9, 0), schedule.Job{Owner: "eng7", To: "qa1", Every: "1m", Message: "hi"})
	supers := func() int { _, sup := host.snap(); return len(sup) }
	host.refuse["qa1"] = "qa1 is not running"
	s.step(clk, clockAt(4, 9, 1))
	s.step(clk, clockAt(4, 9, 2))
	s.step(clk, clockAt(4, 9, 3))
	if n := supers(); n != 1 {
		t.Fatalf("three failures for one reason told super %d times, want 1", n)
	}
	if j := reload(t, s).Jobs[0]; j.Told != "qa1 is not running" {
		t.Fatalf("the file holds told %q", j.Told)
	}
	// A restart: a new scheduler on the same file says nothing more.
	s2 := newRigScheduler(s.root, host, clk.now)
	s2.step(clk, clockAt(4, 9, 4))
	if n := supers(); n != 1 {
		t.Fatalf("after a restart super was told %d times, want still 1", n)
	}
	// Another reason is a new failure.
	host.refuse["qa1"] = "a dialog is open"
	s2.step(clk, clockAt(4, 9, 5))
	if n := supers(); n != 2 {
		t.Fatalf("a new reason told super %d times in all, want 2", n)
	}
	// A delivery re-arms it.
	delete(host.refuse, "qa1")
	s2.step(clk, clockAt(4, 9, 6))
	if j := reload(t, s2).Jobs[0]; j.Told != "" || j.LastResult != "delivered" {
		t.Fatalf("after a delivery the file holds told %q, result %q", j.Told, j.LastResult)
	}
	host.refuse["qa1"] = "a dialog is open"
	s2.step(clk, clockAt(4, 9, 7))
	if n := supers(); n != 3 {
		t.Fatalf("a failure after a delivery told super %d times in all, want 3", n)
	}
}

// TestScheduler_AFailedDeliveryTellsSuperOnce is AC4: nothing vanishes.
func TestScheduler_AFailedDeliveryTellsSuperOnce(t *testing.T) {
	s, host, clk := schedRig(t, clockAt(4, 9, 0), schedule.Job{Owner: "eng7", To: "ghost", At: "09:30", Message: "hello"})
	host.refuse["ghost"] = `there is no pane named "ghost" in window 1`
	s.step(clk, clockAt(4, 9, 30))
	s.step(clk, clockAt(4, 9, 31))
	_, sup := host.snap()
	if len(sup) != 1 || !strings.Contains(sup[0], "schedule s1 (owner eng7, to ghost) did not deliver") ||
		!strings.Contains(sup[0], "no pane named") {
		t.Fatalf("super heard %q, want one line naming the job, the owner and why", sup)
	}
	if j := reload(t, s).Jobs[0]; !strings.HasPrefix(j.LastResult, "not delivered") {
		t.Fatalf("the file records %q", j.LastResult)
	}
}

// TestScheduler_AFileThatDoesNotParseKeepsTheLastGoodScheduleRunning is AC1's
// second half: refused with the line named, and the jobs still run.
func TestScheduler_AFileThatDoesNotParseKeepsTheLastGoodScheduleRunning(t *testing.T) {
	s, host, clk := schedRig(t, clockAt(4, 9, 0), schedule.Job{Owner: "eng1", To: "qa1", Every: "5m", Message: "still here"})
	s.step(clk, clockAt(4, 9, 1)) // A good read first: that is the last good schedule.
	if err := os.WriteFile(s.path, []byte("jobs:\n  - id: s1\n    to: [broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(s.path, future, future) // A new mtime the reader will see.
	s.step(clk, clockAt(4, 9, 5))
	s.step(clk, clockAt(4, 9, 6))
	time.Sleep(50 * time.Millisecond) // The super line is sent off the tick.
	d, sup := host.snap()
	if len(d) != 1 {
		t.Fatalf("the last good schedule did not run: %v", d)
	}
	if len(sup) != 1 || !strings.Contains(sup[0], "does not parse") || !strings.Contains(sup[0], "line ") {
		t.Fatalf("super heard %q, want one line naming the line", sup)
	}
	if err := s.change(func(*schedule.File) error { return nil }); err == nil {
		t.Fatal("a command changed a schedule whose file does not parse")
	}
}

// TestScheduler_NothingToRun: no file, an empty file.
func TestScheduler_NothingToRun(t *testing.T) {
	host := &fakeScheduleHost{}
	clk := &fakeClock{t: clockAt(4, 9, 0)}
	s := newRigScheduler(t.TempDir(), host, clk.now)
	s.step(clk, clockAt(4, 10, 0))
	if d, sup := host.snap(); len(d)+len(sup) != 0 {
		t.Fatalf("an empty rig did something: %v %v", d, sup)
	}
	if len(s.snapshot()) != 0 {
		t.Fatal("an empty rig lists a job")
	}
}

// TestScheduler_KickNeverWaitsAndRunsAtMostOnceASecond: the tick's own work,
// reading the file and checking owners, happens off the caller, which is the
// main goroutine.
func TestScheduler_KickNeverWaitsAndRunsAtMostOnceASecond(t *testing.T) {
	s, _, _ := schedRig(t, clockAt(4, 9, 0), schedule.Job{Owner: "eng1", To: "qa1", Every: "5m", Message: "x"})
	block := make(chan struct{})
	s.isAgent = func(string) bool { <-block; return true }
	start := time.Now()
	for i := 0; i < 100; i++ {
		s.kick()
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Fatalf("100 kicks took %s with the tick blocked", took)
	}
	close(block)
}

// TestScheduler_ARestartWhileADeliveryIsInFlightDoesNotRunItTwice: the claim is
// on disk before the delivery starts.
func TestScheduler_ARestartWhileADeliveryIsInFlightDoesNotRunItTwice(t *testing.T) {
	s, _, clk := schedRig(t, clockAt(4, 9, 0), schedule.Job{Owner: "eng1", To: "qa1", At: "09:30", Message: "once"})
	block := make(chan struct{})
	s.host = &blockingHost{block: block}
	clk.set(clockAt(4, 9, 30))
	s.tick() // The delivery is now stuck in flight.
	host := &fakeScheduleHost{refuse: map[string]string{}}
	again := newRigScheduler(s.root, host, clk.now)
	again.step(clk, clockAt(4, 9, 30).Add(10*time.Second))
	close(block)
	s.wg.Wait()
	if d, _ := host.snap(); len(d) != 0 {
		t.Fatalf("the restarted scheduler ran the in-flight job again: %q", d)
	}
}

type blockingHost struct{ block chan struct{} }

func (b *blockingHost) deliver(string, string, string) (bool, string) { <-b.block; return true, "" }
func (b *blockingHost) tellSuper(string)                              { <-b.block }

// TestScheduler_AnOwnerThatIsNotAnAgentIsRefusedAtLoad: a hand edit must say who
// it is from. The refusal names the line, and the last good schedule runs on.
func TestScheduler_AnOwnerThatIsNotAnAgentIsRefusedAtLoad(t *testing.T) {
	s, host, clk := schedRig(t, clockAt(4, 9, 0), schedule.Job{Owner: "eng1", To: "qa1", Every: "5m", Message: "good"})
	s.isAgent = func(n string) bool { return n == "eng1" || n == "qa1" || n == "super" }
	s.step(clk, clockAt(4, 9, 1)) // First read: eng1 is an agent.
	f, _ := schedule.Load(s.path)
	f.Jobs = append(f.Jobs, schedule.Job{ID: "s9", Owner: "mallory", To: "qa1", Every: "5m", Command: "id", Created: clockAt(4, 9, 0)})
	if err := schedule.Save(s.path, f); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(s.path, future, future)
	s.step(clk, clockAt(4, 9, 5))
	time.Sleep(50 * time.Millisecond)
	d, sup := host.snap()
	if len(sup) != 1 || !strings.Contains(sup[0], `owner "mallory" is not an agent`) || !strings.Contains(sup[0], "line ") {
		t.Fatalf("super heard %q, want the refusal with the line", sup)
	}
	for _, m := range d {
		if strings.Contains(m, "mallory") {
			t.Fatalf("a job of an unknown owner ran: %q", m)
		}
	}
	if len(d) != 1 || !strings.Contains(d[0], "good") {
		t.Fatalf("the last good schedule did not run: %q", d)
	}
}

// TestScheduler_AJobOfARemovedAgentKeepsRunning: an owner already held stays
// valid after the agent is gone, until super removes the job.
func TestScheduler_AJobOfARemovedAgentKeepsRunning(t *testing.T) {
	s, host, clk := schedRig(t, clockAt(4, 9, 0), schedule.Job{Owner: "eng7", To: "qa1", Every: "5m", Message: "still mine"})
	agents := map[string]bool{"eng7": true}
	s.isAgent = func(n string) bool { return agents[n] }
	s.step(clk, clockAt(4, 9, 1))
	delete(agents, "eng7") // eng7 leaves the rig.
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(s.path, future, future) // The file is read again.
	s.step(clk, clockAt(4, 9, 5))
	if d, sup := host.snap(); len(d) != 1 || len(sup) != 0 {
		t.Fatalf("a removed agent's job: delivered %q, super heard %q", d, sup)
	}
}

// TestScheduler_ControlCharactersNeverReachThePane: a command's output and a
// message from the file are cleaned by the send path before they are typed
// (cleanMessage, ini-47vy), so neither can end the paste early or repaint the
// screen. The fake host records what the scheduler hands the send path; the
// real path is TestCleanPath_AScheduleMessageTypesTheCleanBody.
func TestScheduler_ControlCharactersNeverReachThePane(t *testing.T) {
	s, host, clk := schedRig(t, clockAt(4, 9, 0),
		schedule.Job{Owner: "qa1", To: "qa1", Every: "1m", Command: "printf x"},
		schedule.Job{Owner: "qa1", To: "qa1", Every: "1m", Message: "hi\x1b[201~rm -rf x\r\x07"})
	s.runLine = func(context.Context, string, string) (string, int, error) {
		return "ok\x1b[201~\x1b[2Jboom\x00\x9b\ttab\nline2", 0, nil
	}
	s.step(clk, clockAt(4, 9, 1))
	d, _ := host.snap()
	if len(d) != 2 {
		t.Fatalf("delivered %d", len(d))
	}
	for i, m := range d {
		d[i], _ = cleanMessage(m)
		m = d[i]
		if strings.ContainsAny(m, "\x1b\x00\r\x07\u009b") {
			t.Fatalf("a control character reached the pane: %q", m)
		}
	}
	joined := strings.Join(d, "|")
	if !strings.Contains(joined, "okboom\ttab\nline2") || !strings.Contains(joined, "hirm -rf x") {
		t.Fatalf("the text around the controls was lost: %q", joined)
	}
}

// TestScheduler_TheCutLandsOnARuneBoundary.
func TestScheduler_TheCutLandsOnARuneBoundary(t *testing.T) {
	text := strings.Repeat("é", 2000) // Two bytes each.
	got := cutAtRune(text, scheduleOutputCut+1)
	body := strings.TrimSuffix(got, "\n[output cut at 2 KB]")
	if !utf8.ValidString(body) || len(body) > scheduleOutputCut+1 {
		t.Fatalf("cut to %d bytes, valid %v", len(body), utf8.ValidString(body))
	}
}

// ── ini-7k3n: a quiet command job ──────────────────────────────────────

// quietRun runs one interval tick of a job on the real command runner unless
// runLine is given, and returns what was delivered, what super heard and the
// job's last result.
func quietRun(t *testing.T, job schedule.Job, runLine func(context.Context, string, string) (string, int, error), bound *time.Duration) ([]string, []string, string) {
	t.Helper()
	s, host, clk := schedRig(t, clockAt(4, 9, 0), job)
	if runLine != nil {
		s.runLine = runLine
	}
	if bound != nil {
		s.bound = *bound
	}
	s.step(clk, clockAt(4, 9, 1))
	d, sup := host.snap()
	return d, sup, reload(t, s).Jobs[0].LastResult
}

// TestQuietCron_AQuietRunDeliversNothing is AC1 red-first: a quiet job whose
// command prints nothing, or only whitespace, and exits 0 makes no deliver
// call and tells super nothing, and the run is recorded as quiet.
func TestQuietCron_AQuietRunDeliversNothing(t *testing.T) {
	for name, line := range map[string]string{
		"nothing":         "true",
		"only whitespace": `printf '\n \t\n\n'`,
	} {
		t.Run(name, func(t *testing.T) {
			d, sup, last := quietRun(t, schedule.Job{Owner: "qa1", To: "eng2", Every: "1m", Command: line, Quiet: true}, nil, nil)
			if len(d) != 0 || len(sup) != 0 {
				t.Fatalf("a quiet run delivered %q and told super %q", d, sup)
			}
			if last != "quiet: exit 0, no output" {
				t.Fatalf("last result %q", last)
			}
		})
	}
}

// TestQuietCron_EverythingElseDeliversAsToday is AC2: the same quiet job
// delivers today's exact text when it has output, exits non-zero, is stopped
// by the bound or does not run; and a job without quiet delivers its empty run.
func TestQuietCron_EverythingElseDeliversAsToday(t *testing.T) {
	zero := time.Duration(0) // Already past: the stop path with no clock.
	cases := []struct {
		name    string
		job     schedule.Job
		run     func(context.Context, string, string) (string, int, error)
		bound   *time.Duration
		text    string
		toSuper string
	}{
		{name: "output", job: schedule.Job{Command: "echo found", Quiet: true},
			text: "eng2|qa1|[from qa1, schedule s1] $ echo found\nexit 0\nfound"},
		{name: "exit 1 with no output", job: schedule.Job{Command: "exit 1", Quiet: true},
			text: "eng2|qa1|[from qa1, schedule s1] $ exit 1\nexit 1", toSuper: "exited 1"},
		{name: "stopped by the bound", job: schedule.Job{Command: "sleep 9", Quiet: true},
			run:   func(context.Context, string, string) (string, int, error) { return "", 0, nil },
			bound: &zero, text: "eng2|qa1|[from qa1, schedule s1] $ sleep 9\nstopped after 0s", toSuper: "was stopped after 0s"},
		{name: "did not run", job: schedule.Job{Command: "true", Quiet: true},
			run:  func(context.Context, string, string) (string, int, error) { return "", 0, errors.New("no shell") },
			text: "eng2|qa1|[from qa1, schedule s1] $ true\ndid not run: no shell", toSuper: "did not run: no shell"},
		{name: "not quiet, empty run", job: schedule.Job{Command: "true"},
			text: "eng2|qa1|[from qa1, schedule s1] $ true\nexit 0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.job.Owner, c.job.To, c.job.Every = "qa1", "eng2", "1m"
			d, sup, last := quietRun(t, c.job, c.run, c.bound)
			if len(d) != 1 || d[0] != c.text {
				t.Fatalf("delivered %q, want exactly %q", d, c.text)
			}
			if c.toSuper == "" && len(sup) != 0 || c.toSuper != "" && (len(sup) != 1 || !strings.Contains(sup[0], c.toSuper)) {
				t.Fatalf("super heard %q, want %q", sup, c.toSuper)
			}
			if strings.HasPrefix(last, "quiet") {
				t.Fatalf("a delivered run recorded %q", last)
			}
		})
	}
}

// TestQuietCron_AMissedRunFollowsTheSameRule: a quiet time job due while
// initech was down delivers nothing when quiet, and with its note otherwise.
func TestQuietCron_AMissedRunFollowsTheSameRule(t *testing.T) {
	for _, c := range []struct{ line, want string }{
		{"true", ""},
		{"echo found", "(due 2026-11-04 09:30 while initech was down) $ echo found\nexit 0\nfound"},
	} {
		s, host, clk := schedRig(t, clockAt(4, 8, 0), schedule.Job{Owner: "eng1", To: "qa1", At: "09:30", Command: c.line, Quiet: true})
		clk.set(clockAt(4, 11, 0))
		back := newRigScheduler(s.root, host, clk.now)
		back.step(clk, clockAt(4, 11, 0))
		d, _ := host.snap()
		switch {
		case c.want == "" && len(d) != 0:
			t.Fatalf("%s: a missed quiet run delivered %q", c.line, d)
		case c.want != "" && (len(d) != 1 || !strings.Contains(d[0], c.want)):
			t.Fatalf("%s: delivered %q, want the note and the output", c.line, d)
		}
	}
}

// TestQuietCron_AQuietRunIsOneInfoLineInTheLog (pm, ini-7k3n): a quiet run is
// told nowhere but its last result and one info line naming the job, with no
// command output in it; a delivered run writes no such line.
func TestQuietCron_AQuietRunIsOneInfoLineInTheLog(t *testing.T) {
	logs := captureLogs(t)
	quietRun(t, schedule.Job{Owner: "qa1", To: "eng2", Every: "1m", Command: `printf '\n'`, Quiet: true}, nil, nil)
	out := logs.String()
	want := `level=INFO msg="[schedule] schedule s1: quiet run, exit 0, no output"`
	if n := strings.Count(out, "quiet run"); n != 1 || !strings.Contains(out, want) {
		t.Fatalf("the log holds %d quiet-run lines, want one %s:\n%s", n, want, out)
	}
	logs2 := captureLogs(t)
	quietRun(t, schedule.Job{Owner: "qa1", To: "eng2", Every: "1m", Command: "echo found", Quiet: true}, nil, nil)
	if strings.Contains(logs2.String(), "quiet run") {
		t.Fatalf("a delivered run logged a quiet run:\n%s", logs2.String())
	}
}
