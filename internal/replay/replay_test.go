package replay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

// fakeClock advances only when Play sleeps, so a schedule is exact.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d > 0 {
		c.now = c.now.Add(d)
	}
	return nil
}

type sliceSource struct {
	chunks []Chunk
	i      int
}

func (s *sliceSource) Next() (Chunk, error) {
	if s.i >= len(s.chunks) {
		return Chunk{}, io.EOF
	}
	c := s.chunks[s.i]
	s.i++
	return c, nil
}

func opener(chunks []Chunk) Opener {
	return func() (Source, error) { return &sliceSource{chunks: chunks}, nil }
}

// tenChunks is the AC's 10-chunk fixture: uneven gaps, as real output has.
func tenChunks() []Chunk {
	gaps := []time.Duration{0, 3, 1, 12, 7, 2, 20, 5, 9, 4}
	var out []Chunk
	var at time.Duration
	for i, g := range gaps {
		at += g * time.Millisecond
		out = append(out, Chunk{At: at, Data: []byte(fmt.Sprintf("c%d;", i))})
	}
	return out
}

// stampWriter records when (on clk) each write happened.
type stampWriter struct {
	clk   Clock
	start time.Time
	at    []time.Duration
	buf   bytes.Buffer
}

func (w *stampWriter) Write(p []byte) (int, error) {
	w.at = append(w.at, w.clk.Now().Sub(w.start))
	return w.buf.Write(p)
}

func TestPlay_WritesEveryChunkAtItsRecordedTime(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	w := &stampWriter{clk: clk, start: clk.now}
	chunks := tenChunks()
	if err := Play(context.Background(), opener(chunks), w, Options{}, clk); err != nil {
		t.Fatal(err)
	}
	if len(w.at) != len(chunks) {
		t.Fatalf("wrote %d chunks, want %d", len(w.at), len(chunks))
	}
	for i, c := range chunks {
		if w.at[i] != c.At {
			t.Fatalf("chunk %d written at %s, recorded at %s", i, w.at[i], c.At)
		}
	}
	if got := w.buf.String(); got != "c0;c1;c2;c3;c4;c5;c6;c7;c8;c9;" {
		t.Fatalf("bytes = %q", got)
	}
}

// overshootClock oversleeps every sleep by exactly 1ms, the way a loaded host
// wakes a timer late. Deterministic, so the cell below is too.
type overshootClock struct{ fakeClock }

func (c *overshootClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d > 0 {
		c.now = c.now.Add(d + time.Millisecond)
	}
	return nil
}

// AC 1's real concern, made deterministic: lateness must not ACCUMULATE. On a
// clock that wakes every sleep 1ms late, an absolute schedule keeps every write
// within 1ms of the recording; sleeping relative to the previous write would
// drift by 1ms per chunk, to 9ms by the tenth. This replaced a real-clock median
// cell that flaked inside make check (errors to 15ms with the suite running in
// parallel): what the host adds is the host's, what accumulates is the
// replayer's, and only the second is tested here.
func TestPlay_LatenessDoesNotAccumulateAcrossChunks(t *testing.T) {
	clk := &overshootClock{fakeClock{now: time.Unix(1000, 0)}}
	w := &stampWriter{clk: clk, start: clk.now}
	chunks := tenChunks()
	if err := Play(context.Background(), opener(chunks), w, Options{}, clk); err != nil {
		t.Fatal(err)
	}
	for i, c := range chunks {
		if late := w.at[i] - c.At; late > time.Millisecond {
			t.Fatalf("chunk %d is %s late on a clock that oversleeps 1ms: lateness accumulated (writes at %v)", i, late, w.at)
		}
	}
}

// Real-clock smoke for AC 1: every chunk within a 25ms ceiling. Not the 2ms
// per-chunk bound: on this shared Mac a bare time.Sleep misses 2ms in ~6% of
// wakes at load 30, and far more under a parallel test run (see the
// DIVERGENCE comment on ini-pqdy.3). Skipped under -short for the same reason;
// the real clock is still exercised in make check by the cancellation cell.
func TestPlay_RealClockWritesEveryChunkWithinA25msCeiling(t *testing.T) {
	if testing.Short() {
		t.Skip("real-clock timing depends on host load; runs in the full suite")
	}
	clk := Real()
	w := &stampWriter{clk: clk, start: time.Now()}
	chunks := tenChunks()
	if err := Play(context.Background(), opener(chunks), w, Options{}, clk); err != nil {
		t.Fatal(err)
	}
	for i, c := range chunks {
		if d := w.at[i] - c.At; d > 25*time.Millisecond || d < -25*time.Millisecond {
			t.Fatalf("chunk %d off by %s (written %s, recorded %s)", i, d, w.at[i], c.At)
		}
	}
}

func TestPlay_OffsetSkipsEarlierChunksAndShiftsTheFirstWrite(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	w := &stampWriter{clk: clk, start: clk.now}
	chunks := tenChunks() // c3 is at 16ms
	if err := Play(context.Background(), opener(chunks), w, Options{Offset: 16 * time.Millisecond}, clk); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(w.buf.Bytes(), []byte("c3;")) || bytes.Contains(w.buf.Bytes(), []byte("c2;")) {
		t.Fatalf("offset playback wrote %q", w.buf.String())
	}
	if w.at[0] != 0 {
		t.Fatalf("first write at %s, want immediately", w.at[0])
	}
	if want := chunks[4].At - 16*time.Millisecond; w.at[1] != want {
		t.Fatalf("second write at %s, want %s", w.at[1], want)
	}
}

func TestPlay_ScaleDividesEveryInterval(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	w := &stampWriter{clk: clk, start: clk.now}
	chunks := tenChunks()
	if err := Play(context.Background(), opener(chunks), w, Options{Scale: 2}, clk); err != nil {
		t.Fatal(err)
	}
	for i, c := range chunks {
		if w.at[i] != c.At/2 {
			t.Fatalf("chunk %d at %s, want %s at scale 2", i, w.at[i], c.At/2)
		}
	}
}

func TestPlay_LoopRestartsAndStopAfterEndsIt(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	w := &stampWriter{clk: clk, start: clk.now}
	chunks := tenChunks() // one pass is 63ms
	err := Play(context.Background(), opener(chunks), w, Options{Loop: true, StopAfter: 150 * time.Millisecond}, clk)
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(w.buf.Bytes(), []byte("c0;")); n < 2 {
		t.Fatalf("loop played %d pass(es), want at least 2: %q", n, w.buf.String())
	}
	if last := w.at[len(w.at)-1]; last > 150*time.Millisecond {
		t.Fatalf("wrote at %s, after stop-after 150ms", last)
	}
	if clk.now.Sub(w.start) != 150*time.Millisecond {
		t.Fatalf("returned at %s, want exactly at stop-after", clk.now.Sub(w.start))
	}
}

func TestPlay_StopAfterEndsAPassEarly(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	w := &stampWriter{clk: clk, start: clk.now}
	if err := Play(context.Background(), opener(tenChunks()), w, Options{StopAfter: 20 * time.Millisecond}, clk); err != nil {
		t.Fatal(err)
	}
	if got := w.buf.String(); got != "c0;c1;c2;c3;" {
		t.Fatalf("stop-after 20ms wrote %q, want the chunks recorded by 20ms", got)
	}
}

// The SIGTERM / stdin-EOF path: the command cancels ctx, Play exits clean.
func TestPlay_CancellationEndsPlaybackCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	chunks := []Chunk{{At: 0, Data: []byte("a")}, {At: time.Hour, Data: []byte("never")}}
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- Play(ctx, opener(chunks), &out, Options{}, Real()) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled playback returned %v, want a clean nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("playback did not stop on cancel")
	}
	if out.String() != "a" {
		t.Fatalf("wrote %q", out.String())
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("pane closed") }

func TestPlay_AWriteErrorEndsPlaybackWithThatError(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	err := Play(context.Background(), opener(tenChunks()), failWriter{}, Options{}, clk)
	if err == nil || err.Error() != "pane closed" {
		t.Fatalf("err = %v, want the writer's error", err)
	}
}

// countingOpener fails the test instead of hanging if Play keeps reopening the
// recording without the fake clock moving: that is the spin qa2 measured.
func countingOpener(t *testing.T, chunks []Chunk, limit int) (Opener, *int) {
	n := 0
	return func() (Source, error) {
		n++
		if n > limit {
			t.Fatalf("reopened the recording %d times: a looping pass that writes nothing is spinning", n)
		}
		return &sliceSource{chunks: chunks}, nil
	}, &n
}

// qa2's FAIL, case 1: --loop over a recording with no output stops with an
// error after one empty pass, instead of spinning at ~80% of a core.
func TestPlay_LoopingARecordingWithNoOutputStopsInsteadOfSpinning(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	open, n := countingOpener(t, nil, 50)
	err := Play(context.Background(), open, &bytes.Buffer{}, Options{Loop: true}, clk)
	if !errors.Is(err, ErrNothingToLoop) {
		t.Fatalf("err = %v, want ErrNothingToLoop", err)
	}
	if *n != 1 {
		t.Fatalf("opened %d times, want 1", *n)
	}
}

// qa2's FAIL, case 2 -- the stagger the rig uses: an offset past the end is a
// late start, not an empty loop. Pass 1 plays nothing; every later pass plays
// the WHOLE recording, so the pane still carries the full load profile.
func TestPlay_AnOffsetPastTheEndIsALateStartNotAnEmptyLoop(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	open, _ := countingOpener(t, tenChunks(), 50)
	var out bytes.Buffer
	err := Play(context.Background(), open, &out, Options{Loop: true, Offset: time.Hour, StopAfter: 400 * time.Millisecond}, clk)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out.Bytes(), []byte("c0;c1;")) {
		t.Fatalf("after an empty first pass, the loop must play the whole recording; wrote %q", out.String())
	}
}

// --offset is a phase shift: pass 1 starts partway in, pass 2 starts at 0.
func TestPlay_OffsetAppliesToTheFirstPassOnly(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	var out bytes.Buffer
	err := Play(context.Background(), opener(tenChunks()), &out, Options{Loop: true, Offset: 45 * time.Millisecond, StopAfter: 160 * time.Millisecond}, clk)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out.Bytes(), []byte("c6;c7;c8;c9;c0;c1;")) {
		t.Fatalf("wrote %q: pass 1 from the offset (c6), pass 2 from the start (c0)", out.String())
	}
}

// Every chunk at one timestamp: a pass takes no recorded time, so without a
// floor a loop would rewrite it as fast as the CPU allows. Held to minLoopPass.
func TestPlay_AZeroLengthPassIsHeldToTheFloor(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	chunks := []Chunk{{At: 0, Data: []byte("x")}}
	open, n := countingOpener(t, chunks, 50)
	if err := Play(context.Background(), open, &bytes.Buffer{}, Options{Loop: true, StopAfter: time.Second}, clk); err != nil {
		t.Fatal(err)
	}
	if want := int(time.Second/minLoopPass) + 1; *n > want {
		t.Fatalf("%d passes in 1s, want at most %d (one per %s)", *n, want, minLoopPass)
	}
}
