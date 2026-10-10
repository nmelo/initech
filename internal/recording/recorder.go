package recording

import (
	"bufio"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// recorderQueue bounds the chunks waiting for the disk. At 32 KiB per PTY
// read that is at most 8 MiB per pane, and only while the disk is behind.
const recorderQueue = 256

type pending struct {
	kind       byte
	at         time.Duration
	data       []byte
	cols, rows int
}

// Recorder records one pane. Output and Resize are called from the pane's
// read loop and never block it: each copies its input onto a bounded queue
// that one goroutine drains into a buffered Writer. A full queue or a write
// error drops the chunk and counts it; the count goes in the trailer, and
// the session keeps running either way.
type Recorder struct {
	start   time.Time // Monotonic origin for every record's time.
	queue   chan pending
	done    chan struct{}
	dropped atomic.Uint64

	mu         sync.RWMutex // Guards closed against a send on a closed queue.
	closed     bool
	cols, rows int // Last size recorded, so Resize writes only real changes.

	out   *bufio.Writer
	w     *Writer
	sink  io.WriteCloser
	close sync.Once
	err   error
}

// NewRecorder writes h to sink and starts the writer goroutine. h.Start is
// set to now; the record times count from the same instant.
func NewRecorder(sink io.WriteCloser, h Header) (*Recorder, error) {
	now := time.Now()
	h.Start = now
	out := bufio.NewWriterSize(sink, 64*1024)
	w, err := NewWriter(out, h)
	if err != nil {
		return nil, err
	}
	if err := out.Flush(); err != nil {
		return nil, err
	}
	r := &Recorder{
		start: now,
		queue: make(chan pending, recorderQueue),
		done:  make(chan struct{}),
		cols:  h.Cols,
		rows:  h.Rows,
		out:   out,
		w:     w,
		sink:  sink,
	}
	go r.run()
	return r, nil
}

// Output records one chunk of child output. data is copied; the caller may
// reuse its buffer as soon as Output returns.
func (r *Recorder) Output(data []byte) {
	if r == nil || len(data) == 0 {
		return
	}
	r.enqueue(pending{kind: KindOutput, data: append([]byte(nil), data...)})
}

// Resize records a new PTY size. A size equal to the last one recorded is
// not written, so callers can report the size on every read without cost.
func (r *Recorder) Resize(cols, rows int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed || (cols == r.cols && rows == r.rows) {
		r.mu.Unlock()
		return
	}
	r.cols, r.rows = cols, rows
	r.mu.Unlock()
	r.enqueue(pending{kind: KindResize, cols: cols, rows: rows})
}

// Dropped reports how many chunks were dropped so far.
func (r *Recorder) Dropped() uint64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

func (r *Recorder) enqueue(p pending) {
	p.at = time.Since(r.start)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return
	}
	select {
	case r.queue <- p:
	default:
		r.dropped.Add(1)
	}
}

func (r *Recorder) run() {
	defer close(r.done)
	for p := range r.queue {
		var err error
		switch p.kind {
		case KindOutput:
			err = r.w.Output(p.at, p.data)
		case KindResize:
			err = r.w.Resize(p.at, p.cols, p.rows)
		}
		// Flush whenever the queue is empty, so the file on disk keeps up
		// with a quiet pane and a crash loses little.
		if err == nil && len(r.queue) == 0 {
			err = r.out.Flush()
		}
		if err != nil {
			r.dropped.Add(1)
		}
	}
}

// Close stops recording, waits for queued chunks to reach the writer, writes
// the trailer with the dropped count, and closes the file. Safe to call more
// than once; later calls return the first call's error.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.close.Do(func() {
		r.mu.Lock()
		r.closed = true
		close(r.queue)
		r.mu.Unlock()
		<-r.done
		err := r.w.Trailer(time.Since(r.start), r.dropped.Load())
		if ferr := r.out.Flush(); err == nil {
			err = ferr
		}
		if cerr := r.sink.Close(); err == nil {
			err = cerr
		}
		r.err = err
	})
	return r.err
}
