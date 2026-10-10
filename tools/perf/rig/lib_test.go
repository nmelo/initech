package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSummarize_NearestRank(t *testing.T) {
	var xs []time.Duration
	for i := 1; i <= 100; i++ {
		xs = append(xs, time.Duration(i)*time.Millisecond)
	}
	d := summarize(xs)
	if d.p50 != 50*time.Millisecond || d.p95 != 95*time.Millisecond || d.p99 != 99*time.Millisecond || d.max != 100*time.Millisecond || d.n != 100 {
		t.Errorf("summarize(1..100ms) = %+v", d)
	}
	if summarize(nil).String() != "n/a" {
		t.Error("no samples must render n/a, not zeros")
	}
}

func TestReplayOffset_StaggersAcrossTheRecordingOrBurstsAtZero(t *testing.T) {
	s := rigSpec{panes: 5, recDur: 40 * time.Second}
	for i, want := range []time.Duration{0, 10 * time.Second, 20 * time.Second, 30 * time.Second} {
		if got := s.replayOffset(i); got != want {
			t.Errorf("replayer %d offset %v, want %v", i, got, want)
		}
	}
	s.burst = true
	if got := s.replayOffset(3); got != 0 {
		t.Errorf("burst offset %v, want 0 for every replayer", got)
	}
}

// The structural no-claude guard: the generated config passes, and every way
// a role could start something else is refused.
func TestCheckRoles_GeneratedConfigPassesAndEverythingElseIsRefused(t *testing.T) {
	cfg := genConfig(rigSpec{root: "/tmp/r", bin: "/tmp/w/initech", rec: "/tmp/w/s.irec", panes: 4, idle: 60 * time.Second, replay: 120 * time.Second, recDur: 60 * time.Second})
	if err := checkRoles(cfg); err != nil {
		t.Fatalf("the rig's own config was refused: %v\n%s", err, cfg)
	}
	if got := strings.Count(cfg, "agent_type: generic"); got != 4 {
		t.Errorf("%d generic roles, want 4 (1 shell + 3 replayers)", got)
	}
	for name, bad := range map[string]string{
		"claude command":  strings.Replace(cfg, "command: [bash,", "command: [claude,", 1),
		"claude anywhere": cfg + "# claude\n",
		"not generic":     strings.Replace(cfg, "agent_type: generic", "agent_type: claude-code", 1),
		"another command": strings.Replace(cfg, "command: [bash,", "command: [node,", 1),
	} {
		if err := checkRoles(bad); err == nil {
			t.Errorf("%s: checkRoles accepted it", name)
		}
	}
}

func TestSyntheticRecording_RoundTripsThroughTheRealReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.irec")
	if err := writeSynthetic(path, 10*time.Second, 80, 24); err != nil {
		t.Fatal(err)
	}
	got, err := recordingLength(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != 10*time.Second {
		t.Errorf("recording length %v, want 10s", got)
	}
}

func TestParseLog_PerfLinesAndMarkerStamps(t *testing.T) {
	log := strings.Join([]string{
		`time=2026-10-10T09:55:23.322-04:00 level=INFO msg="[perf] minute" mode=full window_s=60 frames=1834 p50_us=2262 p95_us=18101 p99_us=36203 max_us=77514 missed_ticks=11 read_bytes=190247 pane_io="a=1/2,b=3/4"`,
		`time=2026-10-10T09:55:24.000-04:00 level=INFO msg="[perf] marker accepted" pane=shell marker=IQPERF-1`,
		`time=2026-10-10T09:55:24.012-04:00 level=INFO msg="[perf] marker written" pane=shell marker=IQPERF-1`,
		`time=2026-10-10T09:55:24.030-04:00 level=INFO msg="[perf] marker echoed" pane=shell marker=IQPERF-1`,
		`time=2026-10-10T09:55:25.000-04:00 level=INFO msg="[render] enter" frame=150`,
	}, "\n")
	path := filepath.Join(t.TempDir(), "initech.log")
	os.WriteFile(path, []byte(log), 0o600)

	mins, marks, err := parseLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(mins) != 1 || mins[0].frames != 1834 || mins[0].p99 != 36203*time.Microsecond || mins[0].missed != 11 || mins[0].readBytes != 190247 || mins[0].activePanes != 2 {
		t.Errorf("perf minutes = %+v", mins)
	}
	m := marks["IQPERF-1"]
	if m == nil || m.written.Sub(m.accepted) != 12*time.Millisecond || m.echoed.Sub(m.written) != 18*time.Millisecond {
		t.Errorf("marker stamps = %+v", m)
	}
}

// The census reads process NAMES, matching the last path element exactly.
func TestCountNamed_MatchesTheNameNotASubstring(t *testing.T) {
	ps := "/usr/local/bin/claude\nclaude\n/opt/x/claude-helper\nzsh\n  claude  \n"
	if got := countNamed(ps, "claude"); got != 3 {
		t.Errorf("countNamed = %d, want 3 (claude-helper is not claude)", got)
	}
}

func TestRenderTable_NamesShaHostRecordingAndLimits(t *testing.T) {
	out := renderTable(tableMeta{sha: "abc1234", host: "lab (darwin/arm64)", recSet: "synthetic", idle: time.Minute, replay: 2 * time.Minute, cols: 200, rows: 60},
		[]tableRow{{n: 10, mode: "staggered", phase: "idle", verdict: "OK", census: "0/0/0"}})
	for _, want := range []string{"abc1234", "lab (darwin/arm64)", "synthetic", "1 focused bash shell", "| 10 | staggered | idle |", "0/0/0"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}
