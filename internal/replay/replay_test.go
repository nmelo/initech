package replay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
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

// earlyTolerance is the clock resolution the never-early half allows, and
// lateAllowance what the late half allows beyond the host's own worst miss.
const (
	earlyTolerance = time.Millisecond
	lateAllowance  = 25 * time.Millisecond
)

// judgeRealClock is the verdict on one real-clock play: offsets is each
// chunk's write time minus its recorded time, and worstMiss the host's worst
// sleep miss over the same window, from controls bare sleeps.
//
// Two halves, because they are two different claims. Early is the replay's
// alone: no host makes a timer fire ahead of time, so a chunk written before
// its recorded time is a defect, and that bound does not move with load. Late
// is mostly the host's: a shared runner wakes every sleeper late, so lateness
// is judged against what a bare sleep suffered in the same window, plus
// lateAllowance for the replay's own work. The fake-clock tests above prove
// the schedule; this one proves the real clock is wired to it.
func judgeRealClock(offsets []time.Duration, worstMiss time.Duration, controls int) error {
	for i, d := range offsets {
		if d < -earlyTolerance {
			return fmt.Errorf("chunk %d written %s before its recorded time: a replay defect at any load", i, -d)
		}
		if limit := worstMiss + lateAllowance; d > limit {
			return fmt.Errorf("chunk %d late by %s, over the host's worst sleep miss %s (of %d control sleeps) plus %s", i, d, worstMiss, controls, lateAllowance)
		}
	}
	return nil
}

// measureSleepMisses runs bare sleeps of spacing until stop closes and at
// least min have run, and returns the worst miss and how many ran.
func measureSleepMisses(spacing time.Duration, min int, stop <-chan struct{}) (worst time.Duration, n int) {
	for {
		start := time.Now()
		time.Sleep(spacing)
		if miss := time.Since(start) - spacing; miss > worst {
			worst = miss
		}
		n++
		select {
		case <-stop:
			if n >= min {
				return worst, n
			}
		default:
		}
	}
}

// The real clock, on the real sleep: a chunk is never early, and late only by
// what the host did to a bare sleep in the same window (judgeRealClock). The
// control runs alongside the play, not before or after it, so a load spike
// during the play is in the control too. Skipped under -short, which runs no
// wall-clock timing; the verdict's own tests below run everywhere.
func TestPlay_RealClockIsNeverEarlyAndLateOnlyByTheHost(t *testing.T) {
	if testing.Short() {
		t.Skip("real-clock timing depends on host load; runs in the full suite")
	}
	const spacing = 7 * time.Millisecond // tenChunks' mean gap
	type control struct {
		worst time.Duration
		n     int
	}
	stop := make(chan struct{})
	done := make(chan control, 1)
	go func() {
		worst, n := measureSleepMisses(spacing, 10, stop)
		done <- control{worst, n}
	}()
	clk := Real()
	w := &stampWriter{clk: clk, start: time.Now()}
	chunks := tenChunks()
	err := Play(context.Background(), opener(chunks), w, Options{}, clk)
	close(stop)
	c := <-done
	if err != nil {
		t.Fatal(err)
	}
	if len(w.at) != len(chunks) {
		t.Fatalf("wrote %d chunks, want %d", len(w.at), len(chunks))
	}
	offsets := make([]time.Duration, len(chunks))
	var latest time.Duration
	for i, ch := range chunks {
		offsets[i] = w.at[i] - ch.At
		latest = max(latest, offsets[i])
	}
	t.Logf("worst lateness %s, host's worst sleep miss %s over %d sleeps", latest, c.worst, c.n)
	if err := judgeRealClock(offsets, c.worst, c.n); err != nil {
		t.Fatalf("%v (writes at %v)", err, w.at)
	}
}

// The verdict, on planted offsets: early fails at any host jitter, even a
// huge one; late passes inside the host's worst miss plus the allowance and
// fails past it, naming the control.
func TestJudgeRealClock_EarlyIsADefectAndLateIsMeasuredAgainstTheHost(t *testing.T) {
	cases := []struct {
		name      string
		offset    time.Duration
		worstMiss time.Duration
		wantErr   string
	}{
		{"on time", 0, 0, ""},
		{"inside the clock's resolution", -time.Millisecond, 0, ""},
		{"early, quiet host", -2 * time.Millisecond, 0, "before its recorded time"},
		{"early, host a second behind", -2 * time.Millisecond, time.Second, "before its recorded time"},
		{"late under the allowance, quiet host", 24 * time.Millisecond, 0, ""},
		{"late past the allowance, quiet host", 26 * time.Millisecond, 0, "plus 25ms"},
		{"late, but less than a loaded host's miss plus the allowance", 90 * time.Millisecond, 70 * time.Millisecond, ""},
		{"late past a loaded host's miss plus the allowance", 96 * time.Millisecond, 70 * time.Millisecond, "worst sleep miss 70ms (of 12 control sleeps)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := judgeRealClock([]time.Duration{0, c.offset}, c.worstMiss, 12)
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("got %v, want a pass", err)
			case c.wantErr != "" && err == nil:
				t.Fatalf("passed, want an error with %q", c.wantErr)
			case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
				t.Fatalf("got %v, want it to name %q", err, c.wantErr)
			}
		})
	}
}

// The control runs its minimum even when told to stop at once, and reports a
// miss: a real sleep always wakes some time after its deadline, so zero would
// mean it measured nothing, and that would quietly tighten the late half.
func TestMeasureSleepMisses_RunsItsMinimumAndRecordsAMiss(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	worst, n := measureSleepMisses(time.Millisecond, 3, stop)
	if n != 3 {
		t.Errorf("ran %d sleeps, want 3", n)
	}
	if worst <= 0 {
		t.Errorf("worst miss %s over %d sleeps: the control measured nothing", worst, n)
	}
}

// earlyClock wakes every sleep 5ms ahead of time: a planted early replay.
type earlyClock struct{ fakeClock }

func (c *earlyClock) Sleep(ctx context.Context, d time.Duration) error {
	return c.fakeClock.Sleep(ctx, d-5*time.Millisecond)
}

// A replay that writes early, through the real Play, fails the never-early
// half however late the host's sleeps ran.
func TestJudgeRealClock_APlantedEarlyPlayFails(t *testing.T) {
	clk := &earlyClock{fakeClock{now: time.Unix(1000, 0)}}
	w := &stampWriter{clk: clk, start: clk.now}
	chunks := tenChunks()
	if err := Play(context.Background(), opener(chunks), w, Options{}, clk); err != nil {
		t.Fatal(err)
	}
	offsets := make([]time.Duration, len(chunks))
	for i, ch := range chunks {
		offsets[i] = w.at[i] - ch.At
	}
	err := judgeRealClock(offsets, time.Second, 10)
	if err == nil || !strings.Contains(err.Error(), "before its recorded time") {
		t.Fatalf("a play that wrote early got %v (offsets %v)", err, offsets)
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
