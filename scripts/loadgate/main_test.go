package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

var gateTZ = func() *time.Location {
	l, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return l
}()

func at(hhmm string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04", "2026-11-02 "+hhmm, gateTZ)
	return t
}

func gate() config {
	from, to, _ := parseWindow("11:45-12:15")
	return config{maxLoad: 6, skipFrom: from, skipTo: to, maxWait: 20 * time.Minute, every: 15 * time.Second}
}

// Each condition holds the job, and its absence lets it go.
func TestWhy_EachConditionHoldsTheJob(t *testing.T) {
	c := gate()
	for name, cs := range map[string]struct {
		r    reading
		sims bool
		want string
	}{
		"quiet":                  {reading{3.1, at("10:00"), 0}, false, ""},
		"load at the limit runs": {reading{6.0, at("10:00"), 0}, false, ""},
		"load over":              {reading{6.01, at("10:00"), 0}, false, "the 5-minute load is 6.01, over 6.00"},
		"window start":           {reading{1, at("11:45"), 0}, false, "11:45 is inside the 11:45 to 12:15 window"},
		"window inside":          {reading{1, at("12:14"), 0}, false, "12:14 is inside the 11:45 to 12:15 window"},
		"window end runs":        {reading{1, at("12:15"), 0}, false, ""},
		"before the window runs": {reading{1, at("11:44"), 0}, false, ""},
		"a simulator, not asked": {reading{1, at("10:00"), 1}, false, ""},
		"a simulator, asked":     {reading{1, at("10:00"), 1}, true, "1 iOS simulator(s) booted"},
		"no simulator, asked":    {reading{1, at("10:00"), 0}, true, ""},
	} {
		c.simulators = cs.sims
		if got := why(c, cs.r); got != cs.want {
			t.Errorf("%s: %q, want %q", name, got, cs.want)
		}
	}
	c.simulators, c.skipFrom, c.skipTo = false, 0, 0
	if got := why(c, reading{1, at("12:00"), 0}); got != "" {
		t.Errorf("no window: %q", got)
	}
}

// The wait: it looks again every interval, says why each time, and goes once
// the host is quiet; past the cap it fails with a GitHub error.
func TestRun_WaitsThenGoesOrGivesUpAtTheCap(t *testing.T) {
	c := gate()
	loads := []float64{9, 7.5, 5}
	var elapsed time.Duration
	var out bytes.Buffer
	i := 0
	waited, gaveUp := run(c, func() (reading, error) {
		r := reading{loads[i], at("10:00"), 0}
		i++
		return r, nil
	}, func(d time.Duration) { elapsed += d }, func() time.Duration { return elapsed }, &out)
	if gaveUp || waited != 30*time.Second || strings.Count(out.String(), "load gate: waiting, the 5-minute load is") != 2 || !strings.Contains(out.String(), "load gate: go (5-minute load 5.00") {
		t.Errorf("waited %v, gave up %v:\n%s", waited, gaveUp, out.String())
	}
	elapsed, out = 0, bytes.Buffer{}
	waited, gaveUp = run(c, func() (reading, error) { return reading{40, at("10:00"), 0}, nil },
		func(d time.Duration) { elapsed += d }, func() time.Duration { return elapsed }, &out)
	// ini-r0d8 AC1: at the cap the gate fails, naming the load, the line and
	// the wait, so the runner frees; it never runs the job on a loaded host.
	if !gaveUp || waited != 20*time.Minute || !strings.Contains(out.String(), "::error::load gate: the 5-minute load is 40.00, over 6.00, for 20m0s; failing so the runner frees; rerun when the host is quiet") || strings.Contains(out.String(), "running the job anyway") {
		t.Errorf("at the cap: waited %v, gave up %v:\n%s", waited, gaveUp, out.String()[max(0, out.Len()-300):])
	}
	// A host it cannot read never holds a job.
	out = bytes.Buffer{}
	if _, gaveUp := run(c, func() (reading, error) { return reading{}, errors.New("no sysctl") }, func(time.Duration) { t.Fatal("slept") }, func() time.Duration { return 0 }, &out); gaveUp || !strings.Contains(out.String(), "cannot read the host") {
		t.Errorf("unreadable host: %q", out.String())
	}
}

// sysctl's figure, ps's simulators and the window flag are read as written.
func TestParsers_ReadTheirInputs(t *testing.T) {
	if l, err := parseLoad("{ 1.85 107.18 2.16 }\n"); err != nil || l != 107.18 {
		t.Errorf("load %v %v", l, err)
	}
	for _, bad := range []string{"", "{ }", "{ 1.0 }", "{ 1 x 2 }"} {
		if _, err := parseLoad(bad); err == nil {
			t.Errorf("load %q accepted", bad)
		}
	}
	ps := "/sbin/launchd\n/usr/libexec/launchd_sim\nlaunchd_sim\n/Applications/Simulator.app/Contents/MacOS/Simulator\n"
	if n := countSimulators(ps); n != 2 {
		t.Errorf("simulators %d, want 2", n)
	}
	if from, to, err := parseWindow("11:45-12:15"); err != nil || from != 705 || to != 735 {
		t.Errorf("window %d %d %v", from, to, err)
	}
	for _, bad := range []string{"11:45", "12:15-11:45", "1145-1215", "11:45-11:45"} {
		if _, _, err := parseWindow(bad); err == nil {
			t.Errorf("window %q accepted", bad)
		}
	}
	if from, to, err := parseWindow(""); err != nil || from != 0 || to != 0 {
		t.Errorf("no window: %d %d %v", from, to, err)
	}
}

// The default limit is the operator's 24, twice the host's 12 cores: a load
// our own rigs run makes (about 20) does not hold a push, and one past 24 does.
func TestDefaultMaxLoad_IsTheOperatorsTwentyFour(t *testing.T) {
	if defaultMaxLoad != 24 {
		t.Fatalf("defaultMaxLoad is %v, want 24", float64(defaultMaxLoad))
	}
	c := gate()
	c.maxLoad = defaultMaxLoad
	if got := why(c, reading{19.78, at("10:00"), 0}); got != "" {
		t.Errorf("a rigs run's own load held the job: %q", got)
	}
	if got := why(c, reading{24.01, at("10:00"), 0}); got != "the 5-minute load is 24.01, over 24.00" {
		t.Errorf("past the limit: %q", got)
	}
}

// fakeHost is a look that always reads r, with a clock the sleeps advance.
func fakeHost(r reading) (func() (reading, error), func(time.Duration), func() time.Duration) {
	var elapsed time.Duration
	return func() (reading, error) { return r, nil },
		func(d time.Duration) { elapsed += d },
		func() time.Duration { return elapsed }
}

// ini-r0d8 AC2: the failure reaches the exit code, through the same flags the
// action passes; the twins exit 0.
func TestGate_ExitsNonZeroAtTheLimitAndZeroOtherwise(t *testing.T) {
	for _, c := range []struct {
		name string
		r    reading
		err  error
		want int
		says string
	}{
		{"over the line past the limit", reading{101.2, at("10:00"), 0}, nil, 1, "::error::load gate: the 5-minute load is 101.20, over 24.00, for 20m0s"},
		{"under the line", reading{3, at("10:00"), 0}, nil, 0, "load gate: go (5-minute load 3.00"},
		{"host cannot be read", reading{}, errors.New("no sysctl"), 0, "cannot read the host (no sysctl): running the job"},
	} {
		look, sleep, since := fakeHost(c.r)
		if c.err != nil {
			look = func() (reading, error) { return reading{}, c.err }
		}
		var out bytes.Buffer
		if got := gateExit([]string{"-tz", "UTC"}, look, sleep, since, &out); got != c.want || !strings.Contains(out.String(), c.says) {
			t.Errorf("%s: exit %d, want %d; output:\n%s", c.name, got, c.want, out.String()[max(0, out.Len()-300):])
		}
	}
	var out bytes.Buffer
	if got := gateExit([]string{"-skip", "nonsense"}, nil, nil, nil, &out); got != 2 {
		t.Errorf("a bad flag: exit %d, want 2", got)
	}
}

// The skip window and the simulators wait fail at the same limit, each naming
// its own reason.
func TestRun_EveryWaitFailsAtTheLimit(t *testing.T) {
	c := gate()
	c.simulators = true
	for _, r := range []struct {
		reading
		says string
	}{
		{reading{1, at("12:00"), 0}, "12:00 is inside the 11:45 to 12:15 window, for 20m0s; failing so the runner frees"},
		{reading{1, at("10:00"), 2}, "2 iOS simulator(s) booted, for 20m0s; failing so the runner frees"},
	} {
		look, sleep, since := fakeHost(r.reading)
		var out bytes.Buffer
		if _, failed := run(c, look, sleep, since, &out); !failed || !strings.Contains(out.String(), "::error::load gate: "+r.says) {
			t.Errorf("%v: failed %v:\n%s", r.reading, failed, out.String()[max(0, out.Len()-200):])
		}
	}
}
