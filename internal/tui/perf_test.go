package tui

import (
	"reflect"
	"testing"
	"time"
)

// boundFor is the bucket upper bound a duration falls in.
func boundFor(d time.Duration) time.Duration {
	for _, b := range perfBounds {
		if b >= d {
			return b
		}
	}
	return -1
}

func TestPerfHist_PercentilesOnKnownInputs(t *testing.T) {
	h := newPerfHist()
	for i := 0; i < 90; i++ {
		h.add(time.Millisecond)
	}
	for i := 0; i < 9; i++ {
		h.add(10 * time.Millisecond)
	}
	h.add(100 * time.Millisecond)

	if got, want := h.quantile(0.50), boundFor(time.Millisecond); got != want {
		t.Errorf("p50 = %v, want the 1ms bucket's bound %v", got, want)
	}
	// Nearest rank: p95 is the 95th sample and p99 the 99th, both 10ms.
	if got, want := h.quantile(0.95), boundFor(10*time.Millisecond); got != want {
		t.Errorf("p95 = %v, want %v", got, want)
	}
	if got, want := h.quantile(0.99), boundFor(10*time.Millisecond); got != want {
		t.Errorf("p99 = %v, want %v (rank 99 of 100 is the last 10ms sample)", got, want)
	}
	if h.max != 100*time.Millisecond {
		t.Errorf("max = %v, want 100ms exactly", h.max)
	}
}

func TestPerfHist_EmptySingleAndOverflow(t *testing.T) {
	h := newPerfHist()
	if got := h.quantile(0.99); got != 0 {
		t.Errorf("empty p99 = %v, want 0", got)
	}

	h.add(60 * time.Microsecond)
	if got := h.quantile(0.99); got != 60*time.Microsecond {
		t.Errorf("single 60us sample: p99 = %v, want 60us -- a percentile never exceeds the observed max", got)
	}

	o := newPerfHist()
	o.add(5 * time.Second) // past the last bucket
	if got := o.quantile(0.5); got != 5*time.Second {
		t.Errorf("overflow sample: p50 = %v, want the exact max 5s", got)
	}
}

func TestPerfRecorder_RollsOverOncePerMinute(t *testing.T) {
	var r perfRecorder
	t0 := time.Now()
	if _, ok := r.roll(t0); ok {
		t.Fatal("the first roll only starts the clock; it emitted")
	}
	for i := 0; i < 5; i++ {
		r.observeFrame(2*time.Millisecond, 100, 4)
	}
	if _, ok := r.roll(t0.Add(59 * time.Second)); ok {
		t.Fatal("emitted before the minute was up")
	}

	s, ok := r.roll(t0.Add(60 * time.Second))
	if !ok {
		t.Fatal("no snapshot at the minute")
	}
	if s.frames != 5 || s.cellsSum != 500 || s.cellsMax != 100 || s.panesSum != 20 || s.panesMax != 4 {
		t.Errorf("snapshot = %+v, want frames 5, cells 500/100, panes 20/4", s)
	}
	if s.window != 60*time.Second {
		t.Errorf("window = %v, want 60s", s.window)
	}

	r.observeFrame(3*time.Millisecond, 7, 1)
	if _, ok := r.roll(t0.Add(119 * time.Second)); ok {
		t.Fatal("emitted twice in one minute")
	}
	s2, ok := r.roll(t0.Add(120 * time.Second))
	if !ok || s2.frames != 1 || s2.cellsSum != 7 {
		t.Errorf("next minute = %+v ok=%v, want only the one frame observed after the roll", s2, ok)
	}
}

func TestPerfRecorder_MissedTicksAndBacklog(t *testing.T) {
	var r perfRecorder
	t0 := time.Now()
	for i := 0; i < 3; i++ {
		r.observeTick(t0.Add(time.Duration(i)*perfTickInterval), 0)
	}
	if r.missed != 0 {
		t.Fatalf("ticks on time counted %d missed", r.missed)
	}
	// Next tick four intervals after the last: three were dropped.
	r.observeTick(t0.Add(6*perfTickInterval), 3)
	r.observeTick(t0.Add(7*perfTickInterval), 1)
	if r.missed != 3 {
		t.Errorf("missed = %d, want 3", r.missed)
	}
	if r.ipcMax != 3 {
		t.Errorf("ipc depth max = %d, want 3", r.ipcMax)
	}
}

// The line's keys are exactly the documented field list, in order.
func TestPerfLine_KeysMatchTheDocumentedFields(t *testing.T) {
	kv := perfLine(perfSnapshot{window: time.Minute, panes: []perfPaneIO{{"b", 10, 5000}, {"a", 0, 0}}}, 1, 2)
	var keys []string
	for i := 0; i < len(kv); i += 2 {
		keys = append(keys, kv[i].(string))
	}
	if !reflect.DeepEqual(keys, perfFields) {
		t.Errorf("keys = %v\nwant  %v", keys, perfFields)
	}
	for i := 0; i < len(kv); i += 2 {
		if kv[i] == "pane_io" && kv[i+1] != "b=10/5" {
			t.Errorf("pane_io = %q, want only panes that read bytes, as name=bytes/us: %q", kv[i+1], "b=10/5")
		}
	}
}

// At the minute, per-pane counters are summed into the snapshot and reset.
func TestPerfMaybeRoll_SwapsPaneCountersToZero(t *testing.T) {
	a, b := testPane("a"), testPane("b")
	tui := newTestTUI(a, b)
	var got []perfSnapshot
	tui.perfEmit = func(s perfSnapshot) { got = append(got, s) }
	t0 := time.Now()
	tui.perfMaybeRoll(t0)
	a.perfBytes.Add(100)
	a.perfEmuNs.Add(int64(2 * time.Millisecond))
	b.perfBytes.Add(50)

	tui.perfMaybeRoll(t0.Add(perfWindow))

	if len(got) != 1 {
		t.Fatalf("emitted %d snapshots, want 1", len(got))
	}
	if got[0].readBytes != 150 || got[0].emuNs != int64(2*time.Millisecond) || got[0].paneCount != 2 {
		t.Errorf("snapshot = %+v, want read 150 bytes, emu 2ms, 2 panes", got[0])
	}
	if a.perfBytes.Load() != 0 || b.perfBytes.Load() != 0 || a.perfEmuNs.Load() != 0 {
		t.Error("the pane counters were not reset at the minute; the next minute would double-count")
	}
}

// The per-frame and per-chunk hook cost, for AC 3.
func BenchmarkPerfHooks_PerFrame(b *testing.B) {
	var r perfRecorder
	t0 := time.Now()
	r.roll(t0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		r.observeFrame(time.Since(start), 120, 6)
		r.roll(t0)
	}
}

func BenchmarkPerfHooks_PerReadChunk(b *testing.B) {
	p := testPane("a")
	for i := 0; i < b.N; i++ {
		p.perfBytes.Add(4096)
		w0 := time.Now()
		p.perfEmuNs.Add(int64(time.Since(w0)))
	}
}
