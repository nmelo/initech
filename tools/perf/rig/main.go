//go:build !windows

// The rig's driver: one initech in a PTY the rig owns, per run. See lib.go.
//
//	go run ./tools/perf/rig -n 10,25,50,100 [-rec FILE.irec] [-out table.md]
package main

import (
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

var (
	flagN      = flag.String("n", "10", "comma-separated pane counts (each: 1 focused shell + N-1 replayers)")
	flagIdle   = flag.Duration("idle", 60*time.Second, "idle phase before the replayers start")
	flagReplay = flag.Duration("replay", 120*time.Second, "replay phase")
	flagRec    = flag.String("rec", "", "shape-only recording to replay; empty = a rig-generated synthetic one")
	flagModes  = flag.String("modes", "staggered,burst", "staggered offsets, aligned burst, or both")
	flagOut    = flag.String("out", "", "also write the table to this file")
	flagRoot   = flag.String("root", "", "run root (short: a Unix socket path over ~100 chars fails); default /tmp/iq-rig-<user>")
	flagCols   = flag.Int("cols", 200, "TUI width")
	flagRows   = flag.Int("rows", 60, "TUI height")
	flagLocal  = flag.Bool("local", false, "a dev Mac that runs other claude sessions: record the host-wide claude count instead of failing on it; claude under the rig's TUI still fails the run")
)

func main() {
	flag.Parse()
	if *flagRoot == "" {
		*flagRoot = "/tmp/iq-rig-" + os.Getenv("USER")
	}
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	must(err, "find the repo root")
	repo := strings.TrimSpace(string(top))
	shaB, err := exec.Command("git", "-C", repo, "rev-parse", "--short", "HEAD").Output()
	must(err, "read the sha")
	sha := strings.TrimSpace(string(shaB))
	dirty := ""
	if st, _ := exec.Command("git", "-C", repo, "status", "--porcelain", "--untracked-files=no").Output(); len(st) > 0 {
		dirty = "+uncommitted"
	}

	work, err := os.MkdirTemp("", "iq-rig-work-")
	must(err, "make a work dir")
	bin := filepath.Join(work, "initech")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = repo
	if out, err := build.CombinedOutput(); err != nil {
		fail("build initech at %s: %v\n%s", sha, err, out)
	}

	rec, recSet := *flagRec, ""
	if rec == "" {
		rec = filepath.Join(work, "synthetic.irec")
		must(writeSynthetic(rec, 60*time.Second, 80, 24), "write the synthetic recording")
		recSet = "synthetic (rig-generated: 20 lines/s, a line redraw every 10th, shape-only)"
	} else {
		recSet = "shape-only " + filepath.Base(rec)
	}
	recDur, err := recordingLength(rec)
	must(err, "read the recording")

	host, _ := os.Hostname()
	meta := tableMeta{sha: sha + dirty, host: host + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")", recSet: recSet,
		idle: *flagIdle, replay: *flagReplay, cols: *flagCols, rows: *flagRows, profiles: work}
	var rows []tableRow
	failed := false
	for _, ns := range strings.Split(*flagN, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(ns))
		if err != nil || n < 2 {
			fail("bad -n entry %q (need at least 2: the shell and one replayer)", ns)
		}
		for _, mode := range strings.Split(*flagModes, ",") {
			mode = strings.TrimSpace(mode)
			spec := rigSpec{root: *flagRoot, bin: bin, rec: rec, panes: n, idle: *flagIdle, replay: *flagReplay, recDur: recDur, burst: mode == "burst"}
			fmt.Fprintf(os.Stderr, "rig: N=%d %s ...\n", n, mode)
			r := runOne(spec, mode, work)
			if r.verdict != "OK" {
				failed = true
			}
			rows = append(rows, r.rows()...)
		}
	}
	table := renderTable(meta, rows)
	fmt.Print(table)
	if *flagOut != "" {
		must(os.WriteFile(*flagOut, []byte(table), 0o644), "write the table")
	}
	if failed {
		os.Exit(1)
	}
}

// runResult is one run (one N, one mode), both phases.
type runResult struct {
	n                                    int
	mode, verdict                        string
	hostBefore, hostMax, hostAfter       int // claude anywhere on the host
	keys, ext, accW, wEcho               map[string][]time.Duration
	keyMiss, extMiss                     map[string]int
	perf                                 map[string][]perfMinute
	claudeBefore, claudeMax, claudeAfter int
	runnerBusy                           bool
	leftovers                            int
	profile                              string
}

func runOne(s rigSpec, mode, work string) *runResult {
	r := &runResult{n: s.panes, mode: mode, verdict: "OK",
		keys: map[string][]time.Duration{}, ext: map[string][]time.Duration{}, accW: map[string][]time.Duration{}, wEcho: map[string][]time.Duration{},
		keyMiss: map[string]int{}, extMiss: map[string]int{}, perf: map[string][]perfMinute{}}

	waitForLoad(30)
	r.runnerBusy = countNamed(psComm(), "Runner.Worker") > 0
	if r.runnerBusy {
		r.verdict = "SKIPPED: a CI runner job is active"
		return r
	}
	// The bead's host rule: no claude process anywhere before a run. A dev Mac
	// (-local) runs the fleet's own claude sessions, so there the host count is
	// only recorded; claude UNDER the rig's TUI fails the run everywhere.
	if r.hostBefore = countNamed(psComm(), "claude"); r.hostBefore > 0 && !*flagLocal {
		r.verdict = "FAILED: a claude process existed on the host before the run"
		return r
	}

	os.RemoveAll(s.root)
	defer os.RemoveAll(s.root)
	must(os.MkdirAll(filepath.Join(s.root, "shell"), 0o755), "make the root")
	for i := 1; i < s.panes; i++ {
		must(os.MkdirAll(filepath.Join(s.root, fmt.Sprintf("r%03d", i)), 0o755), "make a role dir")
	}
	yaml := genConfig(s)
	if err := checkRoles(yaml); err != nil {
		fail("%v", err)
	}
	must(os.WriteFile(filepath.Join(s.root, "initech.yaml"), []byte(yaml), 0o600), "write initech.yaml")

	port := freePort()
	cmd := exec.Command(s.bin, "--pprof", fmt.Sprintf("localhost:%d", port))
	cmd.Dir = s.root
	cmd.Env = rigEnv()
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(*flagCols), Rows: uint16(*flagRows)})
	must(err, "start initech in a PTY")
	tStart := time.Now()
	pid := cmd.Process.Pid

	scr := newScreen(ptmx, *flagCols, *flagRows)
	var censusMu sync.Mutex
	stopCensus := make(chan struct{})
	censusDone := make(chan struct{})
	go func() {
		defer close(censusDone)
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopCensus:
				return
			case <-t.C:
				tree := len(descendantsNamed(pid, "claude"))
				host := countNamed(psComm(), "claude")
				censusMu.Lock()
				r.claudeMax = max(r.claudeMax, tree)
				r.hostMax = max(r.hostMax, host)
				censusMu.Unlock()
			}
		}
	}()

	if !scr.waitFor("shell", 30*time.Second) {
		r.verdict = "FAILED: the TUI never drew the shell pane"
	} else {
		time.Sleep(3 * time.Second)
		ptmx.Write([]byte("n")) // decline a first-run consent overlay, if any
		time.Sleep(time.Second)
		ptmx.Write([]byte{0x1b}) // and dismiss the welcome
		time.Sleep(time.Second)
		ptmx.Write([]byte{0x15}) // clear anything that reached the shell instead

		tIdleEnd := tStart.Add(s.idle)
		tEnd := tIdleEnd.Add(s.replay)
		profile := filepath.Join(work, fmt.Sprintf("cpu-n%d-%s.pprof", s.panes, mode))
		profDone := make(chan struct{})
		go func() {
			defer close(profDone)
			time.Sleep(time.Until(tIdleEnd))
			secs := int((s.replay - 10*time.Second).Seconds())
			if secs < 5 {
				return
			}
			resp, err := http.Get(fmt.Sprintf("http://localhost:%d/debug/pprof/profile?seconds=%d", port, secs))
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if f, err := os.Create(profile); err == nil {
				io.Copy(f, resp.Body)
				f.Close()
				r.profile = profile
			}
		}()

		rng := rand.New(rand.NewSource(int64(s.panes)))
		const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
		token, marker := "", 0
		nextSend := time.Now().Add(3 * time.Second)
		markerPhase := map[string]string{}
		for time.Now().Before(tEnd) {
			phase := "idle"
			if time.Now().After(tIdleEnd) {
				phase = "replay"
			}
			if time.Now().After(nextSend) {
				ptmx.Write([]byte{0x15})
				token = ""
				time.Sleep(150 * time.Millisecond)
				marker++
				tok := fmt.Sprintf("IQPERF-%d", marker)
				markerPhase[tok] = phase
				issued := time.Now()
				send := exec.Command(s.bin, "send", "--allow-dev-delivery", "shell", "echo "+tok)
				send.Dir = s.root
				send.Env = append(rigEnv(), "INITECH_SOCKET="+filepath.Join(s.root, ".initech", "initech.sock"))
				send.Run()
				if d, ok := scr.waitLatency(tok, issued, 5*time.Second); ok {
					r.ext[phase] = append(r.ext[phase], d)
				} else {
					r.extMiss[phase]++
				}
				nextSend = time.Now().Add(3 * time.Second)
				time.Sleep(300 * time.Millisecond)
				continue
			}
			c := alphabet[rng.Intn(len(alphabet))]
			token += string(c)
			wrote := time.Now()
			ptmx.Write([]byte{c})
			if d, ok := scr.waitLatency(token, wrote, 3*time.Second); ok {
				r.keys[phase] = append(r.keys[phase], d)
			} else {
				r.keyMiss[phase]++
			}
			if len(token) >= 8 {
				ptmx.Write([]byte{0x15})
				token = ""
			}
			time.Sleep(250 * time.Millisecond)
		}
		<-profDone
		// Let the minute that ends at the phase boundary be written: perf
		// windows count from the TUI's first frame, a second or so after
		// start, so without this the last minute is kept or lost by timing.
		time.Sleep(time.Until(tEnd.Add(5 * time.Second)))

		// In-app stamps for every marker sent.
		if mins, marks, err := parseLog(filepath.Join(s.root, ".initech", "initech.log")); err == nil {
			for _, m := range mins {
				ph := "replay"
				if !m.at.After(tIdleEnd.Add(5 * time.Second)) {
					ph = "idle"
				}
				r.perf[ph] = append(r.perf[ph], m)
			}
			for tok, st := range marks {
				ph := markerPhase[tok]
				if ph == "" || st.accepted.IsZero() || st.written.IsZero() {
					continue
				}
				r.accW[ph] = append(r.accW[ph], st.written.Sub(st.accepted))
				if !st.echoed.IsZero() {
					r.wEcho[ph] = append(r.wEcho[ph], st.echoed.Sub(st.written))
				}
			}
		}
	}

	// Teardown: every descendant of the TUI, not just its session -- each
	// pane child gets a session of its own, so a group kill cannot reach it.
	desc := descendants(pid)
	syscall.Kill(-pid, syscall.SIGTERM)
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		syscall.Kill(-pid, syscall.SIGKILL)
		<-exited
	}
	ptmx.Close()
	close(stopCensus)
	<-censusDone
	time.Sleep(time.Second)
	for _, d := range desc {
		if syscall.Kill(d, 0) == nil {
			r.leftovers++
			syscall.Kill(d, syscall.SIGKILL)
		}
	}
	// After teardown the TUI's tree is gone, so "after" counts claude among the
	// processes it had that are still alive, plus the host rule when not -local.
	for _, d := range desc {
		if commOf(d) == "claude" {
			r.claudeAfter++
		}
	}
	r.hostAfter = countNamed(psComm(), "claude")
	switch {
	case r.claudeMax > 0 || r.claudeAfter > 0:
		r.verdict = "FAILED: a claude process ran under the rig's TUI"
	case !*flagLocal && (r.hostMax > 0 || r.hostAfter > 0):
		r.verdict = "FAILED: a claude process appeared on the host during or after the run"
	case r.leftovers > 0 && r.verdict == "OK":
		r.verdict = fmt.Sprintf("FAILED: %d process(es) outlived teardown (killed)", r.leftovers)
	}
	return r
}

func (r *runResult) rows() []tableRow {
	var out []tableRow
	for _, ph := range []string{"idle", "replay"} {
		row := tableRow{n: r.n, mode: r.mode, phase: ph, verdict: r.verdict,
			keys: summarize(r.keys[ph]), keyMiss: r.keyMiss[ph], ext: summarize(r.ext[ph]), extMiss: r.extMiss[ph],
			accW: summarize(r.accW[ph]), wEcho: summarize(r.wEcho[ph]),
			census: fmt.Sprintf("tree %d/%d/%d, host %d/%d/%d%s", r.claudeBefore, r.claudeMax, r.claudeAfter, r.hostBefore, r.hostMax, r.hostAfter, localNote()), runnerBusy: r.runnerBusy}
		for _, m := range r.perf[ph] {
			row.p99s = append(row.p99s, m.p99)
			row.frameMax = max(row.frameMax, m.max)
			row.missed += m.missed
		}
		out = append(out, row)
	}
	return out
}

// ── the rig's own view of the TUI ───────────────────────────────────

// screen is the rig's emulator over the TUI's output. Probes are checked on
// each output chunk as it arrives, never on a timer.
type screen struct {
	emu        *vt.SafeEmulator
	cols, rows int
	mu         sync.Mutex
	chunk      *sync.Cond
	seq        uint64
}

func newScreen(ptmx *os.File, cols, rows int) *screen {
	s := &screen{emu: vt.NewSafeEmulator(cols, rows), cols: cols, rows: rows}
	s.chunk = sync.NewCond(&s.mu)
	go func() { io.Copy(ptmx, s.emu) }() // answer the TUI's terminal queries
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				s.emu.Write(buf[:n])
				s.mu.Lock()
				s.seq++
				s.chunk.Broadcast()
				s.mu.Unlock()
			}
			if err != nil {
				s.mu.Lock()
				s.seq = ^uint64(0)
				s.chunk.Broadcast()
				s.mu.Unlock()
				return
			}
		}
	}()
	return s
}

func (s *screen) text() string {
	var b strings.Builder
	for y := 0; y < s.rows; y++ {
		for x := 0; x < s.cols; x++ {
			if c := s.emu.CellAt(x, y); c != nil && c.Content != "" {
				b.WriteString(c.Content)
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// waitLatency returns the time from since to the first output chunk after
// which the screen contains want.
func (s *screen) waitLatency(want string, since time.Time, limit time.Duration) (time.Duration, bool) {
	deadline := time.Now().Add(limit)
	s.mu.Lock()
	seen := s.seq
	s.mu.Unlock()
	for {
		if strings.Contains(s.text(), want) {
			return time.Since(since), true
		}
		if time.Now().After(deadline) {
			return 0, false
		}
		s.mu.Lock()
		for s.seq == seen {
			// Wake at the deadline even if the TUI goes quiet.
			t := time.AfterFunc(time.Until(deadline), func() { s.mu.Lock(); s.chunk.Broadcast(); s.mu.Unlock() })
			s.chunk.Wait()
			t.Stop()
			if time.Now().After(deadline) {
				break
			}
		}
		seen = s.seq
		s.mu.Unlock()
	}
}

func (s *screen) waitFor(want string, limit time.Duration) bool {
	_, ok := s.waitLatency(want, time.Now(), limit)
	return ok
}

// ── host helpers ────────────────────────────────────────────────────

// psComm lists process NAMES only (ps -axo comm=). Never command lines:
// other users' and agents' processes carry secrets in theirs.
func psComm() string {
	out, _ := exec.Command("ps", "-axo", "comm=").Output()
	return string(out)
}

func localNote() string {
	if *flagLocal {
		return " (-local: host count recorded, not gated)"
	}
	return ""
}

// commOf returns a live process's NAME, or "".
func commOf(pid int) string {
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return filepath.Base(strings.TrimSpace(string(out)))
}

// descendantsNamed lists processes below pid whose NAME is name.
func descendantsNamed(pid int, name string) []int {
	var out []int
	for _, d := range descendants(pid) {
		if commOf(d) == name {
			out = append(out, d)
		}
	}
	return out
}

// descendants lists every process below pid, from pid/ppid pairs only.
func descendants(pid int) []int {
	out, _ := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	kids := map[int][]int{}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) != 2 {
			continue
		}
		c, err1 := strconv.Atoi(f[0])
		p, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			kids[p] = append(kids[p], c)
		}
	}
	var all []int
	var walk func(int)
	walk = func(p int) {
		for _, c := range kids[p] {
			all = append(all, c)
			walk(c)
		}
	}
	walk(pid)
	return all
}

// waitForLoad waits (up to 5 min) while the 1-minute load average is above lim.
func waitForLoad(lim float64) {
	for i := 0; i < 30; i++ {
		out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
		if err != nil {
			return
		}
		f := strings.Fields(strings.Trim(string(out), "{} \n"))
		if len(f) == 0 {
			return
		}
		if l, err := strconv.ParseFloat(f[0], 64); err != nil || l <= lim {
			return
		}
		fmt.Fprintf(os.Stderr, "rig: load %s > %.0f, waiting\n", f[0], lim)
		time.Sleep(10 * time.Second)
	}
}

func rigEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "INITECH_SOCKET=") || strings.HasPrefix(kv, "INITECH_AGENT=") || strings.HasPrefix(kv, "TERM=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "TERM=xterm-256color")
}

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	must(err, "find a free port")
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func must(err error, what string) {
	if err != nil {
		fail("%s: %v", what, err)
	}
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "rig: "+format+"\n", a...)
	os.Exit(2)
}
