// Package main is the efficiency-sprint lab rig (ini-pqdy.4): it runs initech
// in a PTY it owns, with N panes that replay recordings, and measures frame
// cost, keystroke echo and message delivery into one table per sha.
//
// NO REAL CLAUDE, EVER. The rig writes its own initech.yaml in which every
// role is agent_type generic with a sh or bash command, refuses to run if any
// role could start anything else (checkRoles), and counts claude processes
// before, during and after every run; one seen fails the run.
//
// This file holds the parts that need no PTY, so they are unit-tested on
// every platform. The driver is main.go.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/recording"
)

// dist summarises samples: nearest-rank percentiles and the max.
type dist struct {
	n                  int
	p50, p95, p99, max time.Duration
}

func summarize(xs []time.Duration) dist {
	if len(xs) == 0 {
		return dist{}
	}
	s := append([]time.Duration(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	at := func(q float64) time.Duration {
		r := int(float64(len(s))*q + 0.999999)
		if r < 1 {
			r = 1
		}
		if r > len(s) {
			r = len(s)
		}
		return s[r-1]
	}
	return dist{n: len(s), p50: at(0.50), p95: at(0.95), p99: at(0.99), max: s[len(s)-1]}
}

func (d dist) String() string {
	if d.n == 0 {
		return "n/a"
	}
	ms := func(x time.Duration) string { return strconv.FormatFloat(float64(x)/1e6, 'f', 1, 64) }
	return fmt.Sprintf("%s / %s / %s / %s (n=%d)", ms(d.p50), ms(d.p95), ms(d.p99), ms(d.max), d.n)
}

// rigSpec is one run's fixture.
type rigSpec struct {
	root, bin, rec       string
	panes                int // total panes: 1 focused shell + panes-1 replayers
	idle, replay, recDur time.Duration
	burst                bool // every replayer at offset 0 (worst case)
}

// startFile is the file the rig creates once every pane is awake.
func (s rigSpec) startFile() string { return filepath.Join(s.root, ".rig-start") }

// replayOffset staggers replayer i across the recording, or 0 in a burst.
func (s rigSpec) replayOffset(i int) time.Duration {
	replayers := s.panes - 1
	if s.burst || replayers <= 0 || s.recDur <= 0 {
		return 0
	}
	return time.Duration(int64(s.recDur) * int64(i) / int64(replayers))
}

// genConfig writes the run's initech.yaml. Every role is generic with a sh or
// bash command; the operator's config is never read.
func genConfig(s rigSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project: iqrig\nroot: %s\nroles:\n    - shell\n", s.root)
	for i := 1; i < s.panes; i++ {
		fmt.Fprintf(&b, "    - r%03d\n", i)
	}
	b.WriteString("role_overrides:\n    shell:\n        agent_type: generic\n        command: [bash, --norc, --noprofile]\n        no_bracketed_paste: true\n")
	for i := 1; i < s.panes; i++ {
		// Every replayer waits for the rig's start file, so all N begin in
		// step once the whole fleet is awake -- initech's startup stagger
		// brings panes up one at a time, and a timer from each pane's own
		// spawn would start them minutes apart.
		cmd := fmt.Sprintf("while [ ! -e %s ]; do sleep 0.2; done; sleep %d; exec %s replay %s --offset %s --loop --stop-after %s",
			s.startFile(), int(s.idle.Seconds()), s.bin, s.rec, s.replayOffset(i-1), s.replay+10*time.Second)
		fmt.Fprintf(&b, "    r%03d:\n        agent_type: generic\n        command: [sh, -c, %q]\n        no_bracketed_paste: true\n", i, cmd)
	}
	return b.String()
}

var commandLineRe = regexp.MustCompile(`(?m)^\s+command: \[(\S+?),`)

// checkRoles refuses a config in which any role could start anything but sh
// or bash, or that mentions claude at all. Structural: run before every run,
// on the exact text that is about to be written.
func checkRoles(yaml string) error {
	if strings.Contains(strings.ToLower(yaml), "claude") {
		return fmt.Errorf("rig config mentions claude; refusing to run")
	}
	cmds := commandLineRe.FindAllStringSubmatch(yaml, -1)
	if len(cmds) == 0 {
		return fmt.Errorf("rig config has no role commands; refusing to run")
	}
	for _, m := range cmds {
		if m[1] != "sh" && m[1] != "bash" {
			return fmt.Errorf("a role runs %q, not sh or bash; refusing to run", m[1])
		}
	}
	if strings.Count(yaml, "agent_type: generic") != len(cmds) {
		return fmt.Errorf("a role is not agent_type generic; refusing to run")
	}
	return nil
}

// writeSynthetic writes a deterministic recording: green lines at a fixed
// rate with a periodic line redraw, shape-only. For local proof runs only; a
// real run takes shape-only copies of the operator's recordings.
func writeSynthetic(path string, dur time.Duration, cols, rows int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := recording.NewWriter(f, recording.Header{Version: recording.Version, Pane: "synthetic", Start: time.Unix(0, 0), Cols: cols, Rows: rows, ShapeOnly: true})
	if err != nil {
		return err
	}
	line := "\x1b[32m" + strings.Repeat(string(recording.ShapeFiller), cols/2) + "\x1b[0m\r\n"
	i := 0
	for at := time.Duration(0); at < dur; at += 50 * time.Millisecond {
		data := line
		if i%10 == 9 {
			data = "\x1b[2K\r" + strings.Repeat(string(recording.ShapeFiller), cols/3)
		}
		if err := w.Output(at, []byte(data)); err != nil {
			return err
		}
		i++
	}
	return w.Trailer(dur, 0)
}

// recordingLength returns the time of the recording's last record.
func recordingLength(path string) (time.Duration, error) {
	r, err := recording.Open(path)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	var last time.Duration
	for {
		rec, err := r.Next()
		if err != nil {
			break
		}
		last = rec.At
	}
	if last <= 0 {
		return 0, fmt.Errorf("%s holds no timed records", filepath.Base(path))
	}
	return last, nil
}

// ── reading initech.log ─────────────────────────────────────────────

const logTimeLayout = "2006-01-02T15:04:05.000Z07:00"

var logTimeRe = regexp.MustCompile(`^time=(\S+) `)
var kvRe = regexp.MustCompile(`(\w+)=("[^"]*"|\S+)`)

// perfMinute is one '[perf] minute' line.
type perfMinute struct {
	at                 time.Time
	frames, missed     int
	readBytes          int64 // PTY bytes all local panes read that minute: proof the load ran
	activePanes        int   // panes that read anything that minute (pane_io entries)
	p50, p95, p99, max time.Duration
}

// markerStamps is one IQPERF token's three in-app stamps.
type markerStamps struct{ accepted, written, echoed time.Time }

func parseLog(path string) ([]perfMinute, map[string]*markerStamps, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	var mins []perfMinute
	marks := map[string]*markerStamps{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		tm := logTimeRe.FindStringSubmatch(line)
		if tm == nil {
			continue
		}
		at, err := time.Parse(logTimeLayout, tm[1])
		if err != nil {
			continue
		}
		kv := map[string]string{}
		for _, m := range kvRe.FindAllStringSubmatch(line, -1) {
			kv[m[1]] = strings.Trim(m[2], `"`)
		}
		us := func(k string) time.Duration { v, _ := strconv.Atoi(kv[k]); return time.Duration(v) * time.Microsecond }
		num := func(k string) int { v, _ := strconv.Atoi(kv[k]); return v }
		switch {
		case strings.Contains(line, `msg="[perf] minute"`):
			rb, _ := strconv.ParseInt(kv["read_bytes"], 10, 64)
			active := 0
			if io := kv["pane_io"]; io != "" {
				active = len(strings.Split(io, ","))
			}
			mins = append(mins, perfMinute{at: at, frames: num("frames"), missed: num("missed_ticks"), readBytes: rb, activePanes: active,
				p50: us("p50_us"), p95: us("p95_us"), p99: us("p99_us"), max: us("max_us")})
		case strings.Contains(line, `msg="[perf] marker `):
			tok := kv["marker"]
			if tok == "" {
				continue
			}
			m := marks[tok]
			if m == nil {
				m = &markerStamps{}
				marks[tok] = m
			}
			switch {
			case strings.Contains(line, "marker accepted"):
				m.accepted = at
			case strings.Contains(line, "marker written"):
				m.written = at
			case strings.Contains(line, "marker echoed"):
				m.echoed = at
			}
		}
	}
	return mins, marks, sc.Err()
}

// ── the process census ──────────────────────────────────────────────

// countNamed counts processes whose command NAME (never the command line) is
// name, from 'ps -axo comm=' output. A path's last element is the name.
func countNamed(psComm, name string) int {
	n := 0
	for _, l := range strings.Split(psComm, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && filepath.Base(l) == name {
			n++
		}
	}
	return n
}

// ── the table ───────────────────────────────────────────────────────

type tableMeta struct {
	sha, host, recSet string
	idle, replay      time.Duration
	cols, rows        int
	profiles          string
}

type tableRow struct {
	n                      int
	mode, phase, verdict   string
	p99s                   []time.Duration
	frameMax               time.Duration
	missed                 int
	readKBMin              float64 // mean PTY KB read per minute in this phase
	activeMax              int     // most panes that read anything in one minute of this phase
	keys, ext, accW, wEcho dist
	keyMiss, extMiss       int
	census                 string
	runnerBusy             bool
}

// renderTable is one markdown table for one sha. Every row names its fixture
// limits through the header, which is printed with the table.
func renderTable(m tableMeta, rows []tableRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Efficiency baseline at %s\n\n", m.sha)
	fmt.Fprintf(&b, "- host: %s\n- recording set: %s\n- phases: %s idle, then %s replay; TUI %dx%d in a rig-owned PTY\n", m.host, m.recSet, m.idle, m.replay, m.cols, m.rows)
	b.WriteString("- active panes is the most panes that read any PTY input in one minute (initech's own pane_io): with every replayer running it is N in the replay phase. The rig wakes the whole fleet in parallel before measuring, because initech's startup stagger brings generic panes up about one per 20 s (one line on ini-pqdy.4).\n")
	b.WriteString("- read KB/min is the PTY input initech itself counted (ini-pqdy.1's read_bytes): it shows the replay load actually arrived, so a flat frame cost cannot be an idle phase measured twice.\n")
	b.WriteString("- fixture limits: N panes = 1 focused bash shell + N-1 replayers (`initech replay`, generic roles, never claude). Keystroke echo and delivery go to the bash shell, so they measure initech's own path, not an agent's. Frame cost is ini-pqdy.1's per-minute line (p99 per minute listed; buckets are 41% wide). ms throughout: p50 / p95 / p99 / max (n).\n")
	fmt.Fprintf(&b, "- CPU profiles (replay phase): %s\n\n", m.profiles)
	b.WriteString("| N | mode | phase | active panes | read KB/min | frame p99 per minute | frame max | missed ticks | key echo | delivery, external | in-app accepted->written | in-app written->echoed | claude before/during/after | runner job | verdict |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	ms := func(d time.Duration) string { return strconv.FormatFloat(float64(d)/1e6, 'f', 1, 64) }
	for _, r := range rows {
		var p []string
		for _, x := range r.p99s {
			p = append(p, ms(x))
		}
		pp := strings.Join(p, ", ")
		if pp == "" {
			pp = "n/a"
		}
		key, ext := r.keys.String(), r.ext.String()
		if r.keyMiss > 0 {
			key += fmt.Sprintf(" +%d missed", r.keyMiss)
		}
		if r.extMiss > 0 {
			ext += fmt.Sprintf(" +%d missed", r.extMiss)
		}
		runner := "none active"
		if r.runnerBusy {
			runner = "ACTIVE"
		}
		kb := "n/a"
		if len(r.p99s) > 0 {
			kb = strconv.FormatFloat(r.readKBMin, 'f', 1, 64)
		}
		active := "n/a"
		if len(r.p99s) > 0 {
			active = strconv.Itoa(r.activeMax)
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s | %s | %d | %s | %s | %s | %s | %s | %s | %s |\n",
			r.n, r.mode, r.phase, active, kb, pp, ms(r.frameMax), r.missed, key, ext, r.accW, r.wEcho, r.census, runner, r.verdict)
	}
	return b.String()
}
