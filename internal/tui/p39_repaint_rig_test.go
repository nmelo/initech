package tui

// p39_repaint_rig_test.go measures how much WINDOW 1 repaints when nothing on
// screen is changing (ini-p39).
//
// The bead's evidence came from ini-aqy's dump: panes that print six lines once
// and then sleep, yet window 1's raw PTY output carried those lines ~60 times
// a second. tcell's differential update exists to make a static frame cost
// nothing; this rig is the number that says whether it does. Same shape as the
// aqy rig -- the real binary on a real PTY, sh children whose screen is static
// by construction -- with six panes and no viewer.
//
// Four numbers over a five-second window after boot has settled: sentinel-row
// emissions (the bead's own metric, per pane per second), bytes per second,
// frames per second from the app log's periodic frame stamps, and the TUI
// process's CPU%. The assertion is the acceptance criterion: static content
// emits ~nothing. Skipped under -short (it builds the binary and boots a
// fleet); no environment gate, so CI's full-suite leg runs it as is.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

const (
	p39Panes    = 6
	p39Sentinel = "LINE-1-P39-STATIC"
	p39Window   = 5 * time.Second
)

// p39Measurement is one window's worth of numbers.
type p39Measurement struct {
	sentinelPerPanePerSec float64
	bytesPerSec           float64
	framesPerSec          float64
	cpuPercent            float64
	sentinelCount         int
}

func TestP39_StaticFleetRepaintsNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary and boots a six-pane fleet; run without -short")
	}
	root, err := os.MkdirTemp("", "p39")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer os.RemoveAll(root)
	roles := []string{"super", "eng1", "eng2", "eng3", "qa1", "qa2"}
	for _, role := range roles {
		os.MkdirAll(filepath.Join(root, role), 0o755)
		os.WriteFile(filepath.Join(root, role, "CLAUDE.md"), []byte("# "+role+"\n"), 0o644)
	}
	cfg := "project: p39\nroot: " + root + "\nroles:\n"
	for _, role := range roles {
		cfg += "    - " + role + "\n"
	}
	cfg += "claude_command:\n    - sh\nclaude_args:\n    - \"-c\"\n" +
		"    - \"for i in 1 2 3 4 5 6; do echo LINE-$i-P39-STATIC; done; sleep 300\"\n"
	os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o644)

	bin := filepath.Join(t.TempDir(), "initech-p39")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	w1 := newCmd(bin, root)
	ptmx, err := pty.StartWithSize(w1, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("start window 1: %v", err)
	}
	defer func() { ptmx.Close(); w1.Process.Kill(); w1.Process.Wait() }()

	var rawOut bytes.Buffer
	var rawMu sync.Mutex
	snapshot := func() []byte { rawMu.Lock(); defer rawMu.Unlock(); return append([]byte(nil), rawOut.Bytes()...) }
	stop := make(chan struct{})
	go func() {
		buf := make([]byte, 32*1024)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := ptmx.Read(buf)
			if n > 0 {
				rawMu.Lock()
				rawOut.Write(buf[:n])
				rawMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	defer close(stop)

	// Boot: every pane has painted its block at least once.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if bytes.Count(snapshot(), []byte("LINE-6-P39-STATIC")) >= p39Panes {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fleet did not boot: %d of %d panes painted", bytes.Count(snapshot(), []byte("LINE-6-P39-STATIC")), p39Panes)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Settle: past the activity window, so no pane is "running" any more and
	// its KITT bar is not animating. The screen is now static by construction.
	time.Sleep(6 * time.Second)

	m := p39Measure(t, w1.Process.Pid, filepath.Join(root, ".initech", "initech.log"), snapshot, p39Window)
	t.Logf("ini-p39 static fleet (%d panes, %s window): sentinel row emitted %d times = %.1f per pane per second; "+
		"%.0f bytes/s; %.1f frames/s; TUI CPU %.1f%%",
		p39Panes, p39Window, m.sentinelCount, m.sentinelPerPanePerSec, m.bytesPerSec, m.framesPerSec, m.cpuPercent)

	// The acceptance criterion: static content emits ~nothing. One straggler
	// frame per pane at the window's edge is tolerated; sixty a second is not.
	if m.sentinelCount > p39Panes {
		t.Errorf("window 1 re-emitted static content %d times in %s (%.1f per pane per second); "+
			"tcell's differential update is being defeated every frame", m.sentinelCount, p39Window, m.sentinelPerPanePerSec)
	}
}

// p39Measure takes the four numbers across one window.
func p39Measure(t *testing.T, pid int, logPath string, snapshot func() []byte, window time.Duration) p39Measurement {
	t.Helper()
	startBytes := len(snapshot())
	startCPU := p39CPUSeconds(t, pid)
	startFrame, startAt := p39LastFrameStamp(t, logPath)
	time.Sleep(window)
	endCPU := p39CPUSeconds(t, pid)
	all := snapshot()
	endFrame, endAt := p39LastFrameStamp(t, logPath)
	tail := all[startBytes:]
	if path := os.Getenv("P39_DUMP"); path != "" {
		// The window's raw bytes, for reading which cells were re-emitted.
		if err := os.WriteFile(path, tail, 0o644); err != nil {
			t.Logf("dump: %v", err)
		}
	}

	m := p39Measurement{sentinelCount: bytes.Count(tail, []byte(p39Sentinel))}
	secs := window.Seconds()
	m.sentinelPerPanePerSec = float64(m.sentinelCount) / p39Panes / secs
	m.bytesPerSec = float64(len(tail)) / secs
	m.cpuPercent = (endCPU - startCPU) / secs * 100
	if endFrame > startFrame && endAt.After(startAt) {
		m.framesPerSec = float64(endFrame-startFrame) / endAt.Sub(startAt).Seconds()
	}
	return m
}

// p39CPUSeconds reads the process's cumulative CPU time from ps (portable
// across the darwin runners this suite runs on).
func p39CPUSeconds(t *testing.T, pid int) float64 {
	t.Helper()
	out, err := exec.Command("ps", "-o", "cputime=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	// "MM:SS.ss" or "HH:MM:SS".
	parts := strings.Split(strings.TrimSpace(string(out)), ":")
	secs := 0.0
	for _, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			t.Fatalf("parse cputime %q: %v", out, err)
		}
		secs = secs*60 + v
	}
	return secs
}

var p39FrameLine = regexp.MustCompile(`time=(\S+) level=INFO msg="\[render\] enter" frame=(\d+)`)

// p39LastFrameStamp returns the newest "render enter" frame number and its
// timestamp from the app log. render logs one every 150 frames.
func p39LastFrameStamp(t *testing.T, logPath string) (int, time.Time) {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		return 0, time.Time{}
	}
	ms := p39FrameLine.FindAllSubmatch(data, -1)
	if len(ms) == 0 {
		return 0, time.Time{}
	}
	last := ms[len(ms)-1]
	n, _ := strconv.Atoi(string(last[2]))
	ts, err := time.Parse(time.RFC3339Nano, string(last[1]))
	if err != nil {
		t.Logf("frame stamp %q: %v", last[1], err)
	}
	return n, ts
}
