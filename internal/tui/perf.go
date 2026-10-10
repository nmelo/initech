// perf.go is initech's always-on self-measurement (ini-pqdy.1): frame cost,
// work per frame, per-pane input, memory and backlog, summarised into ONE log
// line a minute (component "perf") in .initech/initech.log. Nothing is sent
// anywhere. A real session is the field data for the efficiency sprint, so
// the lab and the operator's own sessions read the same code path.
//
// COST BUDGET: counter increments on the frame path, two atomics per PTY read,
// and one log line a minute. The minute's memory read and process-RSS read run
// on their own goroutine, never on the main loop, so they cannot inflate the
// frames being measured.
package tui

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"time"
)

// perfMode is set only at build time, to measure the instrument's own cost
// (AC 3): -ldflags "-X github.com/nmelo/initech/internal/tui.perfMode=frame-only"
// keeps frame timing (without it nothing can be measured) and compiles every
// other hook out of the hot paths. Empty, the default, is the full instrument.
// Deliberately not a flag or config key: there is no user-facing switch.
var perfMode string

func perfExtras() bool { return perfMode != "frame-only" }

const (
	perfWindow       = time.Minute
	perfTickInterval = 33 * time.Millisecond // the main loop's ticker
)

// perfBounds are the histogram's bucket upper bounds: 50us, then x sqrt(2)
// each step, to 2s. A percentile is reported as its bucket's upper bound
// (never above the observed max), so resolution is about 41% of the value --
// enough to see a frame-cost regression, which is what the line is for.
var perfBounds = func() []time.Duration {
	var b []time.Duration
	for v := 50.0; v <= 2e6; v *= math.Sqrt2 {
		b = append(b, time.Duration(v*float64(time.Microsecond)))
	}
	return b
}()

// perfHist is a fixed-bucket histogram of frame durations.
type perfHist struct {
	counts []uint64 // len(perfBounds)+1; the last bucket is overflow
	n      uint64
	max    time.Duration
}

func newPerfHist() perfHist { return perfHist{counts: make([]uint64, len(perfBounds)+1)} }

func (h *perfHist) add(d time.Duration) {
	i := sort.Search(len(perfBounds), func(i int) bool { return perfBounds[i] >= d })
	h.counts[i]++
	h.n++
	if d > h.max {
		h.max = d
	}
}

// quantile returns the nearest-rank q-quantile as its bucket's upper bound,
// capped at the observed max. Zero when empty.
func (h *perfHist) quantile(q float64) time.Duration {
	if h.n == 0 {
		return 0
	}
	rank := uint64(math.Ceil(q * float64(h.n)))
	if rank < 1 {
		rank = 1
	}
	var cum uint64
	for i, c := range h.counts {
		cum += c
		if cum < rank {
			continue
		}
		if i == len(perfBounds) || perfBounds[i] > h.max {
			return h.max
		}
		return perfBounds[i]
	}
	return h.max
}

// perfRecorder accumulates one minute. Main goroutine only.
type perfRecorder struct {
	start    time.Time
	hist     perfHist
	frames   uint64
	missed   uint64
	cellsSum uint64
	cellsMax int
	panesSum uint64
	panesMax int
	ipcMax   int
	lastTick time.Time
}

func (r *perfRecorder) observeFrame(d time.Duration, cells, panes int) {
	if r.hist.counts == nil {
		r.hist = newPerfHist()
	}
	r.hist.add(d)
	r.frames++
	r.cellsSum += uint64(cells)
	r.panesSum += uint64(panes)
	r.cellsMax = max(r.cellsMax, cells)
	r.panesMax = max(r.panesMax, panes)
}

// observeTick counts ticks the main loop missed: time.Ticker drops ticks while
// the loop is busy, so a gap of k intervals between delivered ticks means k-1
// were dropped. Also records the ipcCh backlog seen at the tick.
func (r *perfRecorder) observeTick(tick time.Time, ipcDepth int) {
	if !r.lastTick.IsZero() {
		if k := int((tick.Sub(r.lastTick)+perfTickInterval/2)/perfTickInterval) - 1; k > 0 {
			r.missed += uint64(k)
		}
	}
	r.lastTick = tick
	r.ipcMax = max(r.ipcMax, ipcDepth)
}

// perfSnapshot is one finished minute.
type perfSnapshot struct {
	window             time.Duration
	frames, missed     uint64
	p50, p95, p99, max time.Duration
	cellsSum, panesSum uint64
	cellsMax, panesMax int
	ipcMax, paneCount  int
	panes              []perfPaneIO
	readBytes, emuNs   int64
}

type perfPaneIO struct {
	name  string
	bytes int64
	emuNs int64
}

// roll closes the minute when it has run its length: it returns the snapshot
// and starts the next minute; otherwise it returns false. The first call only
// starts the clock.
func (r *perfRecorder) roll(now time.Time) (perfSnapshot, bool) {
	if r.start.IsZero() {
		r.start = now
		return perfSnapshot{}, false
	}
	if now.Sub(r.start) < perfWindow {
		return perfSnapshot{}, false
	}
	s := perfSnapshot{
		window: now.Sub(r.start), frames: r.frames, missed: r.missed,
		p50: r.hist.quantile(0.50), p95: r.hist.quantile(0.95), p99: r.hist.quantile(0.99), max: r.hist.max,
		cellsSum: r.cellsSum, cellsMax: r.cellsMax, panesSum: r.panesSum, panesMax: r.panesMax, ipcMax: r.ipcMax,
	}
	lastTick := r.lastTick
	*r = perfRecorder{start: now, hist: newPerfHist(), lastTick: lastTick}
	return s, true
}

// perfFields is the line's key set, in order. Documented on emitPerfLine.
var perfFields = []string{
	"mode", "window_s", "frames", "p50_us", "p95_us", "p99_us", "max_us", "missed_ticks",
	"cells_sum", "cells_max", "panes_drawn_sum", "panes_drawn_max", "ipc_depth_max",
	"pane_count", "heap_inuse_kb", "rss_kb", "read_bytes", "emu_write_us", "pane_io",
}

// perfLine renders a snapshot as the logger's key/value list.
func perfLine(s perfSnapshot, heapKB, rssKB int64) []any {
	us := func(d time.Duration) int64 { return d.Microseconds() }
	mode := "full"
	if !perfExtras() {
		mode = "frame-only"
	}
	sort.Slice(s.panes, func(i, j int) bool { return s.panes[i].bytes > s.panes[j].bytes })
	var io []string
	for _, p := range s.panes {
		if p.bytes > 0 {
			io = append(io, fmt.Sprintf("%s=%d/%d", p.name, p.bytes, p.emuNs/int64(time.Microsecond)))
		}
	}
	return []any{
		"mode", mode, "window_s", int64(s.window.Seconds()), "frames", s.frames,
		"p50_us", us(s.p50), "p95_us", us(s.p95), "p99_us", us(s.p99), "max_us", us(s.max),
		"missed_ticks", s.missed, "cells_sum", s.cellsSum, "cells_max", s.cellsMax,
		"panes_drawn_sum", s.panesSum, "panes_drawn_max", s.panesMax, "ipc_depth_max", s.ipcMax,
		"pane_count", s.paneCount, "heap_inuse_kb", heapKB, "rss_kb", rssKB,
		"read_bytes", s.readBytes, "emu_write_us", s.emuNs / int64(time.Microsecond),
		"pane_io", strings.Join(io, ","),
	}
}

// emitPerfLine writes one minute to the log: component "perf", message
// "minute", then these key=value fields in this order (perfFields):
//
//	mode            full, or frame-only in a cost-measurement build
//	window_s        length of the window, seconds (about 60)
//	frames          frames rendered in the window
//	p50_us p95_us p99_us
//	                frame cost percentiles, microseconds: render entry to after
//	                Show. Reported as the histogram bucket's upper bound
//	                (buckets 50us x sqrt2 steps to 2s), never above max_us
//	max_us          slowest frame, exact
//	missed_ticks    33ms ticks dropped because the loop was busy
//	cells_sum cells_max
//	                cells written to the terminal (changed cells only), summed
//	                over the window and the largest single frame
//	panes_drawn_sum panes_drawn_max
//	                panes drawn per frame, summed and max
//	ipc_depth_max   deepest ipcCh backlog seen at a tick
//	pane_count      panes in this window's process at the end of the window
//	heap_inuse_kb   Go heap in use (runtime/metrics), KiB
//	rss_kb          process resident memory (ps), KiB; 0 if unreadable
//	read_bytes      PTY bytes read by all local panes in the window
//	emu_write_us    time all local panes spent in emu.Write, microseconds
//	pane_io         per local pane that read anything, busiest first:
//	                name=bytes/emu_write_us, comma-separated
//
// Local panes only: remote panes feed their emulators elsewhere and are not
// counted. In a frame-only build the cells, panes, backlog and per-pane fields
// read zero.
func emitPerfLine(s perfSnapshot) {
	LogInfo("perf", "minute", perfLine(s, perfHeapKB(), perfRSSKB())...)
}

// perfHeapKB reads heap in use without stopping the world.
func perfHeapKB() int64 {
	samples := []metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/heap/unused:bytes"},
	}
	metrics.Read(samples)
	var b uint64
	for _, s := range samples {
		if s.Value.Kind() == metrics.KindUint64 {
			b += s.Value.Uint64()
		}
	}
	return int64(b / 1024)
}

// perfRSSKB reads this process's resident memory. Once a minute, off the main
// loop, so a fork is acceptable here.
func perfRSSKB() int64 {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0
	}
	kb, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	return kb
}

// ── TUI hooks ───────────────────────────────────────────────────────

// perfObserveFrame records one frame. Called from render on the main
// goroutine with the time render was entered.
func (t *TUI) perfObserveFrame(start time.Time) {
	d := time.Since(start)
	cells, panes := 0, 0
	if perfExtras() {
		if fs, ok := t.screen.(*frameScreen); ok {
			cells = fs.lastFlushed
		}
		if !t.eventLogM.active {
			panes = len(t.plan.Panes)
		}
	}
	t.perf.observeFrame(d, cells, panes)
	t.perfMaybeRoll(time.Now())
}

// perfObserveTick records a delivered tick and the backlog behind it.
func (t *TUI) perfObserveTick(tick time.Time) {
	depth := 0
	if perfExtras() {
		depth = len(t.ipcCh)
	}
	t.perf.observeTick(tick, depth)
}

// perfMaybeRoll closes a finished minute: the counters are swapped here, on
// the main goroutine, and the memory reads and the log write happen on their
// own goroutine.
func (t *TUI) perfMaybeRoll(now time.Time) {
	s, ok := t.perf.roll(now)
	if !ok {
		return
	}
	s.paneCount = len(t.panes)
	if perfExtras() {
		for _, pv := range t.panes {
			if lp, ok := pv.(*Pane); ok {
				p := perfPaneIO{name: lp.Name(), bytes: lp.perfBytes.Swap(0), emuNs: lp.perfEmuNs.Swap(0)}
				s.readBytes += p.bytes
				s.emuNs += p.emuNs
				s.panes = append(s.panes, p)
			}
		}
	}
	if t.perfEmit != nil {
		t.perfEmit(s)
		return
	}
	t.safeGo(func() { emitPerfLine(s) })
}
