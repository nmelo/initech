// multisink.go implements a fan-out io.Writer that replicates writes to
// multiple downstream writers. Used by the daemon to stream PTY bytes to
// all connected clients plus the ring buffer simultaneously.
package tui

import (
	"io"
	"sync"
)

// MultiSink writes to all registered writers. Dead writers (those that
// return errors) are automatically removed. All methods are safe for
// concurrent use.
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
		if _, err := w.Write(p); err != nil {
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
