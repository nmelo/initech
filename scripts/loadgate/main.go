// loadgate holds a CI job on the shared Mac runner host (192.168.1.207) until
// the host can take it, so initech's jobs do not pile onto another project's
// nightly build (ini-c5a.7, the operator's conditions). It runs once, at the
// start of each first-wave job, right after checkout:
//
//	go run ./scripts/loadgate [-max-load 24] [-skip 11:45-12:15] [-tz America/New_York] [-max-wait 20m] [-simulators]
//
// It waits while the host's 5-minute load average is over -max-load, while the
// local time is inside -skip (the other project's nightly window), and, with
// -simulators, while any iOS simulator is booted on the host, by any user
// (simctl only lists the calling user's own devices, so the gate counts
// launchd_sim processes). It reads every 15 seconds and says why it waits.
// After -max-wait it fails the job with one GitHub error line naming what it
// waited on (the measured load and the line, the window or the simulators) and
// for how long, so the runner frees and the rerun is a choice; a job never
// starts on a host that is still loaded. A host it cannot read never holds or
// fails a job: it runs, and says so.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// reading is what the gate sees at one look.
type reading struct {
	load5      float64
	now        time.Time // in the gate's time zone
	simulators int
}

// defaultMaxLoad is the operator's limit on .207's 5-minute load: twice its 12
// cores. A limit of 6 held every push behind initech's own rigs run, which
// alone puts the host near 20.
const defaultMaxLoad = 24

// config is the gate's limits.
type config struct {
	maxLoad    float64
	skipFrom   int // minutes after midnight, inclusive
	skipTo     int // minutes after midnight, exclusive
	maxWait    time.Duration
	simulators bool
	every      time.Duration
}

// why is the reason r must wait under c, or "" when the job may run.
func why(c config, r reading) string {
	m := r.now.Hour()*60 + r.now.Minute()
	switch {
	case c.skipFrom != c.skipTo && m >= c.skipFrom && m < c.skipTo:
		return fmt.Sprintf("%s is inside the %s to %s window", r.now.Format("15:04"), hhmm(c.skipFrom), hhmm(c.skipTo))
	case r.load5 > c.maxLoad:
		return fmt.Sprintf("the 5-minute load is %.2f, over %.2f", r.load5, c.maxLoad)
	case c.simulators && r.simulators > 0:
		return fmt.Sprintf("%d iOS simulator(s) booted", r.simulators)
	}
	return ""
}

func hhmm(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// run waits until look says the job may go or maxWait has passed, writing one
// line a look to w. It returns how long it waited and whether it failed: at
// maxWait the job must not run on a host that is still loaded.
func run(c config, look func() (reading, error), sleep func(time.Duration), since func() time.Duration, w io.Writer) (time.Duration, bool) {
	for {
		r, err := look()
		if err != nil {
			fmt.Fprintf(w, "load gate: cannot read the host (%v): running the job\n", err)
			return since(), false
		}
		reason := why(c, r)
		if reason == "" {
			fmt.Fprintf(w, "load gate: go (5-minute load %.2f, %s, %d simulator(s)) after %s\n", r.load5, r.now.Format("15:04 MST"), r.simulators, since().Round(time.Second))
			return since(), false
		}
		if since() >= c.maxWait {
			fmt.Fprintf(w, "::error::load gate: %s, for %s; failing so the runner frees; rerun when the host is quiet\n", reason, c.maxWait)
			return since(), true
		}
		fmt.Fprintf(w, "load gate: waiting, %s\n", reason)
		sleep(c.every)
	}
}

// parseLoad is the 5-minute figure of sysctl vm.loadavg's "{ 1.85 1.94 2.16 }".
func parseLoad(s string) (float64, error) {
	f := strings.Fields(strings.Trim(strings.TrimSpace(s), "{}"))
	if len(f) < 2 {
		return 0, fmt.Errorf("unexpected vm.loadavg %q", s)
	}
	return strconv.ParseFloat(f[1], 64)
}

// countSimulators is how many launchd_sim processes ps lists, one per booted
// simulator, any user.
func countSimulators(psComm string) int {
	n := 0
	for _, l := range strings.Split(psComm, "\n") {
		if strings.HasSuffix(strings.TrimSpace(l), "launchd_sim") {
			n++
		}
	}
	return n
}

// parseWindow is "11:45-12:15" as minutes after midnight; "" is no window.
func parseWindow(s string) (int, int, error) {
	if s == "" {
		return 0, 0, nil
	}
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, fmt.Errorf("window %q is not HH:MM-HH:MM", s)
	}
	parse := func(t string) (int, error) {
		v, err := time.Parse("15:04", strings.TrimSpace(t))
		if err != nil {
			return 0, fmt.Errorf("window %q: %v", s, err)
		}
		return v.Hour()*60 + v.Minute(), nil
	}
	from, err := parse(a)
	if err != nil {
		return 0, 0, err
	}
	to, err := parse(b)
	if err != nil {
		return 0, 0, err
	}
	if to <= from {
		return 0, 0, fmt.Errorf("window %q ends before it starts", s)
	}
	return from, to, nil
}

func main() {
	os.Exit(gateExit(os.Args[1:], nil, time.Sleep, nil, os.Stdout))
}

// gateExit parses args as the action passes them, runs the gate and returns
// the process's exit code. A nil look reads the real host; a nil since is the
// time since the call.
func gateExit(args []string, look func() (reading, error), sleep func(time.Duration), since func() time.Duration, w io.Writer) int {
	fs := flag.NewFlagSet("loadgate", flag.ContinueOnError)
	fs.SetOutput(w)
	maxLoad := fs.Float64("max-load", defaultMaxLoad, "wait while the 5-minute load average is over this")
	skip := fs.String("skip", "11:45-12:15", "wait while the local time is inside HH:MM-HH:MM (empty: never)")
	tz := fs.String("tz", "America/New_York", "the time zone of -skip")
	maxWait := fs.Duration("max-wait", 20*time.Minute, "fail the job after waiting this long")
	sims := fs.Bool("simulators", false, "also wait while any iOS simulator is booted, by any user")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	from, to, err := parseWindow(*skip)
	if err != nil {
		fmt.Fprintln(w, "load gate:", err)
		return 2
	}
	loc, err := time.LoadLocation(*tz)
	if err != nil {
		fmt.Fprintln(w, "load gate:", err)
		return 2
	}
	c := config{maxLoad: *maxLoad, skipFrom: from, skipTo: to, maxWait: *maxWait, simulators: *sims, every: 15 * time.Second}
	if since == nil {
		start := time.Now()
		since = func() time.Duration { return time.Since(start) }
	}
	if look == nil {
		look = func() (reading, error) {
			out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
			if err != nil {
				return reading{}, err
			}
			l, err := parseLoad(string(out))
			if err != nil {
				return reading{}, err
			}
			r := reading{load5: l, now: time.Now().In(loc)}
			if c.simulators {
				ps, err := exec.Command("ps", "-A", "-o", "comm=").Output()
				if err != nil {
					return reading{}, err
				}
				r.simulators = countSimulators(string(ps))
			}
			return r, nil
		}
	}
	if _, failed := run(c, look, sleep, since, w); failed {
		return 1
	}
	return 0
}
