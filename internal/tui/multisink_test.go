package tui

import (
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/hashicorp/yamux"
)

type testWriter struct {
	mu   sync.Mutex
	data []byte
}

func (tw *testWriter) Write(p []byte) (int, error) {
	tw.mu.Lock()
	tw.data = append(tw.data, p...)
	tw.mu.Unlock()
	return len(p), nil
}

func (tw *testWriter) String() string {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return string(tw.data)
}

type failWriter struct{}

func (fw *failWriter) Write(p []byte) (int, error) {
	return 0, errors.New("dead")
}

func TestMultiSink_FanOut(t *testing.T) {
	ms := NewMultiSink()
	w1 := &testWriter{}
	w2 := &testWriter{}
	ms.Add(w1)
	ms.Add(w2)

	ms.Write([]byte("hello"))

	if w1.String() != "hello" {
		t.Errorf("w1 = %q, want hello", w1.String())
	}
	if w2.String() != "hello" {
		t.Errorf("w2 = %q, want hello", w2.String())
	}
}

func TestMultiSink_DeadWriterRemoved(t *testing.T) {
	ms := NewMultiSink()
	good := &testWriter{}
	bad := &failWriter{}
	ms.Add(good)
	ms.Add(bad)

	if ms.Len() != 2 {
		t.Fatalf("before write: len = %d, want 2", ms.Len())
	}

	ms.Write([]byte("test"))

	if ms.Len() != 1 {
		t.Errorf("after write: len = %d, want 1 (dead writer removed)", ms.Len())
	}
	if good.String() != "test" {
		t.Errorf("good writer = %q, want test", good.String())
	}
}

func TestMultiSink_Remove(t *testing.T) {
	ms := NewMultiSink()
	w1 := &testWriter{}
	w2 := &testWriter{}
	ms.Add(w1)
	ms.Add(w2)
	ms.Remove(w1)

	ms.Write([]byte("data"))

	if w1.String() != "" {
		t.Errorf("removed writer should not receive data, got %q", w1.String())
	}
	if w2.String() != "data" {
		t.Errorf("w2 = %q, want data", w2.String())
	}
}

func TestMultiSink_Empty(t *testing.T) {
	ms := NewMultiSink()
	n, err := ms.Write([]byte("noop"))
	if n != 4 || err != nil {
		t.Errorf("empty Write = (%d, %v), want (4, nil)", n, err)
	}
}

func TestMultiSink_ConcurrentWrites(t *testing.T) {
	ms := NewMultiSink()
	w := &testWriter{}
	ms.Add(w)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms.Write([]byte("x"))
		}()
	}
	wg.Wait()

	if len(w.String()) != 100 {
		t.Errorf("len = %d, want 100", len(w.String()))
	}
}

// ── window 2 panes silently stop updating (operator, 2026-08-22) ──────
//
// MultiSink removed a writer on ANY error, permanently, and nothing ever
// re-adds one — streamAgentLive registers a window's stream once, at attach.
// But the writers here are yamux streams, and yamux returns TRANSIENT write
// timeouts that leave the stream perfectly usable: ErrTimeout (a *NetError,
// Timeout() true) from a stream deadline, and ErrConnectionWriteTimeout (a
// plain sentinel) when the session's send window stays full past
// ConnectionWriteTimeout — 7s for window sessions. One slow moment in window
// 2 (a burst from a chatty agent, a render hitch) and that pane's stream is
// dropped for the life of the attach: window 1 shows the agent running,
// window 2's panel never updates again, and only reattaching fixes it.
//
// Policing genuinely wedged windows is NOT this type's job: the window
// server's keepalive already detects and drops them (ini-z8o, <=15s). A
// fan-out writer that cannot tell "this client is finished" from "this
// client was briefly busy" must keep the client.
type flakyWriter struct {
	writes int
	failOn int
	err    error
	got    []string
}

func (f *flakyWriter) Write(p []byte) (int, error) {
	f.writes++
	if f.writes == f.failOn {
		return 0, f.err
	}
	f.got = append(f.got, string(p))
	return len(p), nil
}

func TestMultiSink_KeepsWriterAfterTransientTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"stream deadline (net.Error timeout)", yamux.ErrTimeout},
		{"session send window full", yamux.ErrConnectionWriteTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &flakyWriter{failOn: 2, err: tc.err}
			ms := NewMultiSink()
			ms.Add(w)

			ms.Write([]byte("before"))
			ms.Write([]byte("during-the-hitch")) // transient failure
			ms.Write([]byte("after"))

			if ms.Len() != 1 {
				t.Fatalf("writer dropped after a TRANSIENT timeout — window 2's pane stops " +
					"updating for the life of the attach while window 1 shows it running")
			}
			// CONTRACT CHANGE with the all-or-drop fix (hover garble): the
			// hitched chunk is now RETRIED and delivered, not dropped — the
			// original assertion encoded lossy-keep, which is the byte gap
			// that tore escape sequences. All three chunks must arrive.
			if len(w.got) != 3 || w.got[1] != "during-the-hitch" || w.got[2] != "after" {
				t.Fatalf("transient hitch lost or reordered data: got %v", w.got)
			}
		})
	}
}

func TestMultiSink_StillDropsFinishedWriter(t *testing.T) {
	w := &flakyWriter{failOn: 1, err: io.ErrClosedPipe}
	ms := NewMultiSink()
	ms.Add(w)
	ms.Write([]byte("x"))
	if ms.Len() != 0 {
		t.Fatal("a writer whose pipe is closed IS finished and must be dropped — " +
			"keeping it would leak a dead client forever")
	}
}

// ── hover window-2 GARBLE (operator, 2026-08-22) — the freeze fix's own gap ──
//
// yamux Stream.Write sends in segments and returns (total, err) — a transient
// timeout can fire AFTER part of the chunk is on the wire. v2.11.2's fix kept
// the writer and continued with the NEXT chunk, so the unsent remainder became
// a byte gap mid-stream: torn escape sequences, garbled viewer panes (the
// screenshot's overstruck chrome). Freeze traded for corruption.
//
// Correct semantics: a transient timeout RETRIES THE REMAINDER — the viewer
// gets everything, late. Only persistent failure drops the writer; a stream
// with a gap in it is worse than no stream, because the z8o keepalive can
// recover a dropped window but nothing recovers a torn escape sequence.
type partialWriter struct {
	fails int // remaining Write calls that fail (after writing half)
	err   error
	got   []byte
	calls int
}

func (p *partialWriter) Write(b []byte) (int, error) {
	p.calls++
	if p.fails > 0 {
		p.fails--
		n := len(b) / 2
		p.got = append(p.got, b[:n]...)
		return n, p.err
	}
	p.got = append(p.got, b...)
	return len(b), nil
}

func TestMultiSink_TransientTimeoutRetriesRemainder_NoByteGap(t *testing.T) {
	w := &partialWriter{fails: 1, err: yamux.ErrConnectionWriteTimeout}
	ms := NewMultiSink()
	ms.Add(w)

	ms.Write([]byte("\x1b[31mRED\x1b[0m")) // torn mid-escape if the gap survives

	if got := string(w.got); got != "\x1b[31mRED\x1b[0m" {
		t.Fatalf("byte gap after transient timeout: writer received %q — a torn escape "+
			"sequence garbles the viewer until the next full repaint", got)
	}
	if ms.Len() != 1 {
		t.Fatal("writer dropped after a recovered transient timeout")
	}
}

func TestMultiSink_PersistentTimeoutDropsWriter(t *testing.T) {
	w := &partialWriter{fails: 99, err: yamux.ErrConnectionWriteTimeout}
	ms := NewMultiSink()
	ms.Add(w)

	ms.Write([]byte("data"))

	if ms.Len() != 0 {
		t.Fatal("writer kept through PERSISTENT timeouts — an unbounded retry loop " +
			"stalls the fan-out for every other window")
	}
}
