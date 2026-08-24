// multisink.go implements a fan-out io.Writer that replicates writes to
// multiple downstream writers. Used by the daemon to stream PTY bytes to
// all connected clients plus the ring buffer simultaneously.
package tui

import (
	"errors"
	"io"
	"net"
	"sync"

	"github.com/hashicorp/yamux"
)

// MultiSink writes to all registered writers. Writers that are FINISHED are
// removed automatically; writers that merely timed out are kept. All methods
// are safe for concurrent use.
type MultiSink struct {
	mu      sync.Mutex
	writers []io.Writer
}

// NewMultiSink creates an empty MultiSink. Add writers with Add().
func NewMultiSink() *MultiSink {
	return &MultiSink{}
}

// Add registers a writer to receive future writes.
func (ms *MultiSink) Add(w io.Writer) {
	ms.mu.Lock()
	ms.writers = append(ms.writers, w)
	ms.mu.Unlock()
}

// Remove unregisters a writer. No-op if not found.
func (ms *MultiSink) Remove(w io.Writer) {
	ms.mu.Lock()
	for i, wr := range ms.writers {
		if wr == w {
			ms.writers = append(ms.writers[:i], ms.writers[i+1:]...)
			break
		}
	}
	ms.mu.Unlock()
}

// Len returns the current number of registered writers.
func (ms *MultiSink) Len() int {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return len(ms.writers)
}

// Write sends p to all registered writers. Writers that return errors are
// removed automatically (dead client cleanup). Returns len(p), nil to
// satisfy io.Writer (the caller should not stall on downstream failures).
//
// The writer list is snapshot'd under lock, then writes happen lock-free.
// This prevents a slow/blocked writer from holding the lock and stalling
// Add/Remove or other concurrent Write calls.
func (ms *MultiSink) Write(p []byte) (int, error) {
	ms.mu.Lock()
	snapshot := make([]io.Writer, len(ms.writers))
	copy(snapshot, ms.writers)
	ms.mu.Unlock()

	// Write to all without holding the lock.
	var dead []io.Writer
	for _, w := range snapshot {
		if !writeComplete(w, p) {
			dead = append(dead, w)
		}
	}

	// Remove dead writers under lock.
	if len(dead) > 0 {
		ms.mu.Lock()
		for _, dw := range dead {
			for i, w := range ms.writers {
				if w == dw {
					ms.writers = append(ms.writers[:i], ms.writers[i+1:]...)
					break
				}
			}
		}
		ms.mu.Unlock()
	}
	return len(p), nil
}

// transientWriteErr reports whether a failed write means "this writer was
// busy" rather than "this writer is finished".
//
// THE DISTINCTION IS LOAD-BEARING (operator, 2026-08-22: window 2 panes
// silently stopping while window 1 showed the agent running). These writers
// are yamux streams to attached windows, registered ONCE at attach and never
// re-added — so removing one on a recoverable error freezes that pane for the
// life of the attach. yamux returns two such errors, and they are shaped
// differently on purpose: ErrTimeout is a *NetError (Timeout() true) from a
// stream deadline, while ErrConnectionWriteTimeout is a plain sentinel raised
// when the session's send window stays full past ConnectionWriteTimeout — 7s
// for window sessions, which one chatty agent and one slow render can reach.
//
// Not policing wedged clients here is deliberate, not an omission: the window
// server's keepalive already detects and drops a genuinely wedged window
// within 15s (ini-z8o), and that mechanism can tell "wedged" from "busy"
// because it asks the session rather than inferring from one write.
func transientWriteErr(err error) bool {
	if errors.Is(err, yamux.ErrConnectionWriteTimeout) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// maxTransientRetries bounds the remainder-retry loop. Each yamux write
// attempt already carries the session's ConnectionWriteTimeout (7s for window
// sessions), so 2 retries tolerates ~21s of stall before the writer is
// declared finished — past the z8o keepalive's own 12s wedge-detection, so a
// genuinely wedged window is dead by session teardown before we give up here.
const maxTransientRetries = 2

// writeComplete delivers p to w in full or reports the writer finished.
//
// ALL-OR-DROP, NEVER A GAP (operator's hover garble, 2026-08-22): yamux
// Stream.Write sends in segments and returns (total, err), so a transient
// timeout can fire with part of the chunk already on the wire. v2.11.2 kept
// the writer and moved on to the NEXT chunk — the unsent remainder became a
// byte gap mid-stream, tearing escape sequences and garbling the viewer
// until its next full repaint. Freeze traded for corruption, strictly worse:
// the keepalive can recover a dropped window, nothing recovers a torn
// escape sequence. A transient timeout therefore retries the REMAINDER
// (late but complete); only persistent failure or a terminal error drops.
func writeComplete(w io.Writer, p []byte) bool {
	buf := p
	for attempt := 0; ; attempt++ {
		n, err := w.Write(buf)
		if n > 0 {
			buf = buf[n:]
		}
		if err == nil {
			if len(buf) == 0 {
				return true
			}
			continue // short write without error: keep going (io.Writer contract edge)
		}
		if !transientWriteErr(err) || attempt >= maxTransientRetries {
			return false
		}
	}
}

// syncStream serializes writes to ONE client stream.
//
// MultiSink.Write snapshots its writer list under lock and then writes
// LOCK-FREE, deliberately, so a slow writer cannot stall Add/Remove. Live
// fan-out is therefore serialized only by there being a single producer
// goroutine per pane. A mid-session replay (ini-dr03) is issued from the
// control goroutine and would be a SECOND concurrent writer to the same
// stream, free to interleave mid-chunk -- producing the torn escape sequence
// this bead exists to remove, from a new direction.
//
// At ATTACH this cannot happen: replayToStream runs before streamAgentLive
// adds the stream to the MultiSink. Mid-session there is no such ordering, so
// the ordering is made explicit here instead of assumed.
type syncStream struct {
	mu sync.Mutex
	w  net.Conn
}

func newSyncStream(w net.Conn) *syncStream { return &syncStream{w: w} }

func (s *syncStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
