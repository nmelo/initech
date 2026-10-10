// Package replay plays a recorded agent's output back as if it were the
// agent (ini-pqdy.3), so a lab rig can run N panes of real fleet output
// without N real agents. Each chunk is written at its recorded time; nothing
// is interpreted, so the byte volume and escape mix -- what costs frames --
// are exactly the recording's.
//
// The schedule is ABSOLUTE: every chunk's target is the pass's start plus its
// recorded time, so per-chunk sleep error never accumulates over a long
// recording. There is no busy-waiting: this runs in up to 100 panes on one
// host, and a spin loop would be the very load the rig exists to measure.
package replay

import (
	"context"
	"errors"
	"io"
	"time"
)

// Chunk is one piece of recorded output and when it was written, measured
// from the start of the recording.
type Chunk struct {
	At   time.Duration
	Data []byte
}

// Source yields a recording's output chunks in order, and io.EOF at the end.
// Records that carry no output (header, resize, trailer) never reach here.
type Source interface {
	Next() (Chunk, error)
}

// Opener starts a fresh pass over the recording. Called once per pass, so a
// looping replay re-reads from the beginning.
type Opener func() (Source, error)

// Clock is the time Play runs on. Sleep returns early with ctx's error when
// ctx is done. Real() is the production clock; tests inject their own.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

// Options shape a replay.
type Options struct {
	// Offset starts playback at this recording time. Earlier chunks are
	// skipped, which is how a rig staggers panes over one recording.
	Offset time.Duration
	// Scale divides every interval: 2 plays twice as fast. Zero means 1.
	Scale float64
	// Loop starts again from Offset when a pass ends.
	Loop bool
	// StopAfter ends the replay after this much wall time. Zero means never.
	StopAfter time.Duration
}

// Play writes the recording's chunks to out, each at its scheduled time. It
// returns nil when the recording ends (without Loop), when StopAfter is
// reached, or when ctx is cancelled -- all clean exits. It returns an error
// only when the recording cannot be read or out cannot be written.
func Play(ctx context.Context, open Opener, out io.Writer, o Options, clk Clock) error {
	scale := o.Scale
	if scale <= 0 {
		scale = 1
	}
	began := clk.Now()
	var deadline time.Time
	if o.StopAfter > 0 {
		deadline = began.Add(o.StopAfter)
	}
	// waitUntil sleeps to target; done reports that playback must end there
	// (stop-after reached, or cancelled).
	waitUntil := func(target time.Time) (done bool) {
		if !deadline.IsZero() && target.After(deadline) {
			_ = clk.Sleep(ctx, deadline.Sub(clk.Now()))
			return true
		}
		if d := target.Sub(clk.Now()); d > 0 {
			if clk.Sleep(ctx, d) != nil {
				return true
			}
		}
		return ctx.Err() != nil
	}
	for {
		src, err := open()
		if err != nil {
			return err
		}
		start := clk.Now()
		for {
			c, err := src.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			if c.At < o.Offset {
				continue
			}
			target := start.Add(time.Duration(float64(c.At-o.Offset) / scale))
			if waitUntil(target) {
				return nil
			}
			if _, err := out.Write(c.Data); err != nil {
				return err
			}
		}
		if !o.Loop {
			return nil
		}
		if ctx.Err() != nil || (!deadline.IsZero() && !clk.Now().Before(deadline)) {
			return nil
		}
	}
}

// Real returns the wall clock.
func Real() Clock { return realClock{} }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
