// ringbuf.go implements a fixed-size circular byte buffer for PTY output
// replay on reconnect. When the buffer fills, new writes overwrite the
// oldest data. Snapshot returns the buffered content in chronological order.
package tui

import "sync"

// DefaultRingBufSize is the per-pane ring buffer capacity. 256KB holds ~12
// full screen rewrites of terminal output, enough to reconstruct the current
// screen state on reconnect.
const DefaultRingBufSize = 256 * 1024

// RingBuf is a fixed-size circular byte buffer. It implements io.Writer.
// All methods are safe for concurrent use.
type RingBuf struct {
	buf  []byte
	w    int  // next write position
	full bool // true once the buffer has wrapped at least once
	mu   sync.Mutex
}

// NewRingBuf creates a ring buffer with the given capacity in bytes.
func NewRingBuf(size int) *RingBuf {
	if size <= 0 {
		size = DefaultRingBufSize
	}
	return &RingBuf{buf: make([]byte, size)}
}

// Write appends p to the buffer, overwriting oldest data when full.
// Always returns len(p), nil (io.Writer contract).
func (r *RingBuf) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := len(p)
	cap := len(r.buf)

	// If the write is larger than the buffer, keep only the tail.
	if n >= cap {
		copy(r.buf, p[n-cap:])
		r.w = 0
		r.full = true
		return n, nil
	}

	// How much fits before wrap?
	space := cap - r.w
	if n <= space {
		copy(r.buf[r.w:], p)
		r.w += n
		if r.w == cap {
			r.w = 0
			r.full = true
		}
	} else {
		// Split write across the wrap boundary.
		copy(r.buf[r.w:], p[:space])
		copy(r.buf, p[space:])
		r.w = n - space
		r.full = true
	}

	return n, nil
}

// Snapshot returns the buffered content in chronological order (oldest first).
// Returns nil if the buffer is empty.
func (r *RingBuf) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.full && r.w == 0 {
		return nil // Empty.
	}

	if !r.full {
		// Haven't wrapped yet. Data is buf[0:w].
		out := make([]byte, r.w)
		copy(out, r.buf[:r.w])
		return out
	}

	// Wrapped. Data is buf[w:] + buf[:w].
	cap := len(r.buf)
	out := make([]byte, cap)
	copy(out, r.buf[r.w:])
	copy(out[cap-r.w:], r.buf[:r.w])
	return out
}

// Len returns the number of bytes currently stored.
func (r *RingBuf) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.full {
		return len(r.buf)
	}
	return r.w
}
