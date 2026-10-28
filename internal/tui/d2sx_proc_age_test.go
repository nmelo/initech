package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ini-d2sx: the background count and age come from the pane's running
// background shells (the process tree), not from how long the footer has
// listed a shell. Claude keeps a finished shell listed until it is read, so
// the footer alone reads one shell across many short jobs, aged from the
// first.

// d2sxProc is one row of a fake process table: pid, parent, age, comm.
type d2sxProc struct {
	pid, ppid int
	age       time.Duration
	comm      string
}

// d2sxTable renders rows as ps prints them (etime [[dd-]hh:]mm:ss).
func d2sxTable(rows ...d2sxProc) string {
	var b strings.Builder
	for _, r := range rows {
		s := int(r.age.Seconds())
		et := fmt.Sprintf("%02d:%02d", s/60%60, s%60)
		if s >= 3600 {
			et = fmt.Sprintf("%02d:%s", s/3600%24, et)
		}
		if s >= 86400 {
			et = fmt.Sprintf("%d-%s", s/86400, et)
		}
		fmt.Fprintf(&b, "%6d %6d %11s %s\n", r.pid, r.ppid, et, r.comm)
	}
	return b.String()
}

// d2sxSampler swaps in a sampler that reads table() synchronously at the
// test's clock, and restores the real one after.
func d2sxSampler(t *testing.T, clock *time.Time, table func() (string, error)) *treeSampler {
	t.Helper()
	old := procSampler
	s := &treeSampler{read: table, now: func() time.Time { return *clock }, run: func(f func()) { f() }}
	procSampler = s
	t.Cleanup(func() { procSampler = old })
	return s
}

func TestProcTree_EtimeAndCommAreReadAsPsPrintsThem(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"00:05": 5 * time.Second, "12:34": 12*time.Minute + 34*time.Second,
		"01:02:03": time.Hour + 2*time.Minute + 3*time.Second, "2-03:04:05": 51*time.Hour + 4*time.Minute + 5*time.Second,
	} {
		if got, ok := parseEtime(in); !ok || got != want {
			t.Errorf("parseEtime(%q) = %v %v, want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "5", "a:b", "1:2:3:4", "x-01:02", "-1:00"} {
		if _, ok := parseEtime(bad); ok {
			t.Errorf("parseEtime(%q) read", bad)
		}
	}
	for comm, want := range map[string][2]bool{
		"/bin/zsh": {true, false}, "-zsh": {true, false}, "bash": {true, false}, "sh": {true, false},
		"(zsh)": {false, false}, "claude": {false, true}, "/Users/x/.local/bin/claude": {false, true},
		"npm exec slack-mcp-server@1.3.0 --transport stdio": {false, false}, "caffeinate": {false, false},
		"node": {false, false}, "uv": {false, false}, "": {false, false},
	} {
		if s, c := classifyComm(comm); s != want[0] || c != want[1] {
			t.Errorf("classifyComm(%q) = shell %v claude %v, want %v", comm, s, c, want)
		}
	}
}

// The pane's background shells are the shells that are direct children of
// its claude: not the MCP servers or caffeinate, not an exiting shell, not a
// shell under a shell; claude is found under a wrapper.
func TestProcTree_TheBackgroundShellsAreClaudesShellChildren(t *testing.T) {
	now := time.Date(2026, 10, 28, 16, 0, 0, 0, time.UTC)
	tree := parseProcTable(d2sxTable(
		d2sxProc{100, 1, time.Hour, "/bin/sh"},
		d2sxProc{101, 100, time.Hour, "claude"},
		d2sxProc{110, 101, time.Hour, "uv"},
		d2sxProc{111, 110, time.Hour, "python"},
		d2sxProc{112, 101, time.Hour, "npm exec slack-mcp-server@1.3.0 --transport stdio"},
		d2sxProc{113, 101, 50 * time.Minute, "caffeinate"},
		d2sxProc{120, 101, 4 * time.Minute, "/bin/zsh"},
		d2sxProc{121, 120, 30 * time.Second, "/bin/bash"},
		d2sxProc{122, 121, 2 * time.Second, "sleep"},
		d2sxProc{130, 101, 0, "(zsh)"},
		d2sxProc{140, 101, 90 * time.Second, "-zsh"},
	), now)
	starts, ok := tree.runningShells(100)
	if !ok || len(starts) != 2 {
		t.Fatalf("shells %v (%v), want the two under claude", starts, ok)
	}
	got := map[time.Duration]bool{now.Sub(starts[0]): true, now.Sub(starts[1]): true}
	if !got[4*time.Minute] || !got[90*time.Second] {
		t.Errorf("shell ages %v, want 4m and 90s", got)
	}
	// Feed it nothing: an unknown pane, no claude, a nil tree, pid 0.
	if _, ok := tree.runningShells(999); ok {
		t.Error("a pid not in the tree read")
	}
	noClaude := parseProcTable(d2sxTable(d2sxProc{100, 1, time.Hour, "bash"}, d2sxProc{120, 100, time.Minute, "zsh"}), now)
	if _, ok := noClaude.runningShells(100); ok {
		t.Error("a tree with no claude read")
	}
	var none *procTree
	if _, ok := none.runningShells(100); ok {
		t.Error("a nil tree read")
	}
	if _, ok := tree.runningShells(0); ok {
		t.Error("pid 0 read")
	}
	// A claude with no shell children reads, with none running.
	if starts, ok := parseProcTable(d2sxTable(d2sxProc{100, 1, time.Hour, "claude"}), now).runningShells(100); !ok || len(starts) != 0 {
		t.Errorf("claude alone: %v %v", starts, ok)
	}
}

// AC1, AC2, AC3 through the real tick: a second job shows its own age, a
// finished shell the footer still lists reads idle, and the age follows the
// running shell. Red at base: the age is the first job's, and the listed
// shell keeps the pane in background.
func TestProcAge_TheSecondJobShowsItsOwnAgeAndAFinishedOneNone(t *testing.T) {
	tu, p := pi8pPane()
	p.pid = 100
	clock := time.Date(2026, 10, 28, 16, 0, 0, 0, time.UTC)
	t0 := clock
	shells := []d2sxProc{{200, 100, 0, "/bin/zsh"}}
	jobStart := map[int]time.Time{200: t0}
	d2sxSampler(t, &clock, func() (string, error) {
		rows := []d2sxProc{{100, 1, 2 * time.Hour, "claude"}, {110, 100, 2 * time.Hour, "uv"}, {113, 100, time.Hour, "caffeinate"}}
		for _, s := range shells {
			rows = append(rows, d2sxProc{s.pid, s.ppid, clock.Sub(jobStart[s.pid]), s.comm})
		}
		return d2sxTable(rows...), nil
	})
	tick := func(at time.Time) {
		clock = at
		pi8pTick(t, tu, p, "haiku310_landing_shell.txt", StateIdle, at)
	}
	tick(t0)                   // the footer shows the shell; the tree is asked for
	tick(t0.Add(time.Second))  // read
	tick(t0.Add(10 * time.Second))
	if b := paneBusy(p, clock); b == nil || b.Shells != 1 || b.SinceSec != 10 || b.SeenFor {
		t.Fatalf("first job: %+v, want 1 shell, 10s, a process age", b)
	}

	// The first job ends; ten minutes later a second starts. The footer reads
	// one shell throughout.
	shells = nil
	tick(t0.Add(11 * time.Second))
	if got := p.Activity(); got != StateIdle || paneBusy(p, clock) != nil {
		t.Errorf("finished shell still listed: %v %+v, want idle and no work", got, paneBusy(p, clock))
	}
	shells = []d2sxProc{{300, 100, 0, "/bin/zsh"}}
	jobStart[300] = t0.Add(10 * time.Minute)
	tick(t0.Add(10*time.Minute + 5*time.Second))
	b := paneBusy(p, clock)
	if b == nil || b.SinceSec != 5 || b.SeenFor || p.Activity() != StateBackground {
		t.Errorf("second job: %+v (%v), want background aged 5s, not 10m", b, p.Activity())
	}
	// Over its life the age follows the shell's start.
	tick(t0.Add(13*time.Minute + 5*time.Second))
	if b := paneBusy(p, clock); b == nil || b.SinceSec != 3*60+5 {
		t.Errorf("three minutes on: %+v, want 185s", b)
	}
	if age, ok := backgroundAge(p, clock); !ok || age != 185*time.Second || backgroundSeenFor(p) {
		t.Errorf("overlay age %v %v seen-for %v", age, ok, backgroundSeenFor(p))
	}
}

// Two jobs at once: the count is what runs, capped by the footer's kinds, and
// the age is the oldest.
func TestProcAge_TheCountIsWhatRunsAndTheAgeTheOldest(t *testing.T) {
	p := newEmuPane("eng1", 160, 45)
	now := time.Now()
	shell2 := Footer{Shells: l6omInt(2), Monitors: l6omInt(1), Tasks: l6omInt(0)}
	p.observeBackgroundTree(now, true, shell2, []time.Time{now.Add(-time.Minute), now.Add(-5 * time.Minute)}, true)
	if b := paneBusy(p, now); b == nil || b.Shells != 2 || b.Monitors != 0 || b.SinceSec != 300 {
		t.Errorf("two running of three listed: %+v, want 2 shells, 0 monitors, 5m", b)
	}
	p.observeBackgroundTree(now, true, shell2, []time.Time{now.Add(-time.Minute)}, true)
	if b := paneBusy(p, now); b == nil || b.Shells != 1 || b.Monitors != 0 || b.SinceSec != 60 {
		t.Errorf("one running: %+v", b)
	}
	// More running than listed (a foreground tool's shell): the footer's count.
	one := Footer{Shells: l6omInt(1), Monitors: l6omInt(0), Tasks: l6omInt(0)}
	p.observeBackgroundTree(now, true, one, []time.Time{now, now.Add(-time.Minute)}, true)
	if b := paneBusy(p, now); b == nil || b.Shells != 1 {
		t.Errorf("more running than listed: %+v", b)
	}
	// Monitors count once the listed shells are covered.
	mon := Footer{Shells: l6omInt(1), Monitors: l6omInt(2), Tasks: l6omInt(0)}
	p.observeBackgroundTree(now, true, mon, []time.Time{now, now, now}, true)
	if b := paneBusy(p, now); b == nil || b.Shells != 1 || b.Monitors != 2 {
		t.Errorf("1 shell 2 monitors running: %+v", b)
	}
}

// AC4: no tree (Windows, a failed read, a pane whose claude is not found, a
// stale sample) keeps the footer's reading, labelled seen for; background
// agents, which have no process, are seen for too.
func TestProcAge_WithoutTheTreeTheFootersReadingIsSeenFor(t *testing.T) {
	tu, p := pi8pPane()
	p.pid = 100
	clock := time.Date(2026, 10, 28, 16, 0, 0, 0, time.UTC)
	d2sxSampler(t, &clock, func() (string, error) { return "", errors.New("no ps") })
	for i := range 3 {
		clock = clock.Add(time.Second)
		pi8pTick(t, tu, p, "haiku310_landing_shell.txt", StateIdle, clock)
		_ = i
	}
	if b := paneBusy(p, clock); b == nil || b.Shells != 1 || !b.SeenFor || p.Activity() != StateBackground {
		t.Errorf("no tree: %+v, want the footer's shell, seen for", b)
	}
	if !backgroundSeenFor(p) {
		t.Error("the overlay would print a process age")
	}

	// Background agents only: no process to read, the footer's age, seen for.
	q := newEmuPane("eng2", 160, 45)
	now := time.Now()
	q.observeBackgroundTree(now.Add(-time.Minute), true, Footer{Shells: l6omInt(0), Monitors: l6omInt(0), Tasks: l6omInt(1)}, nil, true)
	q.observeBackgroundTree(now, true, Footer{Shells: l6omInt(0), Monitors: l6omInt(0), Tasks: l6omInt(1)}, nil, true)
	if b := paneBusy(q, now); b == nil || b.Tasks != 1 || !b.SeenFor || b.SinceSec != 60 {
		t.Errorf("a background agent: %+v, want 1 task seen for 60s", b)
	}
}

// The sampler reads only when asked, at most once a second, one read at a
// time, and a sample older than treeMaxAge is not used.
func TestProcTree_TheSamplerReadsWhenAskedAndNotTooOften(t *testing.T) {
	clock := time.Date(2026, 10, 28, 16, 0, 0, 0, time.UTC)
	reads := 0
	var pending func()
	s := &treeSampler{
		read: func() (string, error) { reads++; return d2sxTable(d2sxProc{100, 1, time.Hour, "claude"}), nil },
		now:  func() time.Time { return clock },
		run:  func(f func()) { pending = f },
	}
	if s.sample(false) != nil || pending != nil {
		t.Fatal("read while nobody asked")
	}
	s.sample(true) // t0: read
	if pending == nil {
		t.Fatal("not read when asked")
	}
	pending()
	pending = nil
	if s.sample(false) == nil || reads != 1 {
		t.Fatalf("the sample is not available (%d reads)", reads)
	}
	clock = clock.Add(500 * time.Millisecond)
	s.sample(true) // within a second of the last read
	if pending != nil {
		t.Error("read twice in one second")
	}
	clock = clock.Add(500 * time.Millisecond)
	s.sample(true) // a second on: read, and leave it running
	if pending == nil {
		t.Fatal("not read a second later")
	}
	running := pending
	pending = nil
	clock = clock.Add(time.Second)
	s.sample(true) // past the rate limit, the last read still running
	if pending != nil {
		t.Error("a second read started while one was in flight")
	}
	running()
	pending = nil
	clock = clock.Add(treeMaxAge + time.Second)
	if s.sample(false) != nil {
		t.Error("a stale sample was used")
	}
}

// The Agents overlay says "seen" before an age the tree did not give, and
// nothing before a process age.
func TestProcAge_TheOverlaySaysSeenForAFooterAge(t *testing.T) {
	for _, c := range []struct {
		treeOK bool
		want   string
	}{{true, "background 1m"}, {false, "background seen 1m"}} {
		tui, screen := newTestTUIWithScreen("eng1")
		lp := tui.panes[0].(*Pane)
		lp.mu.Lock()
		lp.activity = StateIdle
		lp.mu.Unlock()
		f := parseFooter(lvxwFixture(t, "haiku310_landing_shell.txt"), 160)
		start := time.Now().Add(-90 * time.Second)
		lp.observeBackgroundTree(start, true, f, nil, false) // first seen 90 s ago
		lp.observeBackgroundTree(time.Now(), true, f, []time.Time{start}, c.treeOK)
		tui.renderOverlay()
		screen.Show()
		w, h := screen.Size()
		found := false
		for y := 0; y < h; y++ {
			row := make([]rune, 0, w)
			for x := 0; x < w; x++ {
				ch, _, _, _ := screen.GetContent(x, y)
				row = append(row, ch)
			}
			if line := string(row); strings.Contains(line, "eng1") {
				found = true
				if !strings.Contains(line, c.want) || (c.treeOK && strings.Contains(line, "seen")) {
					t.Errorf("tree read %v: row %q, want %q", c.treeOK, strings.TrimSpace(line), c.want)
				}
			}
		}
		if !found {
			t.Fatal("no overlay row for eng1")
		}
	}
}
