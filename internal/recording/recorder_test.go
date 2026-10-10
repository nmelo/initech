package recording

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

// memSink is an io.WriteCloser over a buffer. When gate is non-nil every
// Write after the first waits for it, so a test can stall the disk; entered
// (when non-nil) is closed the moment the first such Write begins, so the
// test can KNOW the writer goroutine is parked on the disk rather than hope.
type memSink struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	gate      chan struct{}
	entered   chan struct{}
	enterOnce sync.Once
	writes    int
	fail      bool
}

func (m *memSink) Write(p []byte) (int, error) {
	m.mu.Lock()
	m.writes++
	n := m.writes
	gate, fail := m.gate, m.fail
	m.mu.Unlock()
	if gate != nil && n > 1 {
		if m.entered != nil {
			m.enterOnce.Do(func() { close(m.entered) })
		}
		<-gate
	}
	if fail && n > 1 {
		return 0, errors.New("disk full")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.Write(p)
}
func (m *memSink) Close() error { return nil }
func (m *memSink) bytes() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.buf.Bytes()...)
}

// The recorder writes what the pane read, in order, with nondecreasing
// times, only real resizes, and a trailer on close.
func TestRecorder_RecordsOutputAndRealResizesInOrder(t *testing.T) {
	sink := &memSink{}
	r, err := NewRecorder(sink, Header{Pane: "eng1", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	buf := []byte("one")
	r.Output(buf)
	copy(buf, "XXX") // the read loop reuses its buffer; the record must not change
	r.Resize(80, 24) // same size: not written
	r.Resize(120, 40)
	r.Resize(120, 40) // same again: not written
	r.Output([]byte("two"))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	h, recs := readAll(t, sink.bytes())
	if h.Pane != "eng1" || h.Cols != 80 || h.Rows != 24 || h.Start.IsZero() {
		t.Errorf("header = %+v", h)
	}
	if len(recs) != 4 {
		t.Fatalf("%d records, want O R O T: %+v", len(recs), recs)
	}
	if string(recs[0].Data) != "one" || recs[1].Kind != KindResize || recs[1].Cols != 120 ||
		string(recs[2].Data) != "two" || recs[3].Kind != KindTrailer || recs[3].Dropped != 0 {
		t.Errorf("records = %+v", recs)
	}
	for i := 1; i < len(recs); i++ {
		if recs[i].At < recs[i-1].At {
			t.Errorf("time went backwards at record %d: %v < %v", i, recs[i].At, recs[i-1].At)
		}
	}
}

// A stalled disk never blocks the read loop: Output returns at once, chunks
// beyond the queue are dropped, and the trailer carries the count.
//
// The stall is made certain before the flood (ini-pqdy.6): one chunk goes
// first, and the test waits until the writer has drained it and is parked
// inside the disk's Write. Without that wait the writer can keep pace with
// the sender (it did under -race), buffering everything in memory, and
// correctly drop nothing -- a fixture that never stalled, not a recorder
// that failed to drop.
func TestRecorder_StalledDiskNeverBlocksAndCountsDrops(t *testing.T) {
	sink := &memSink{gate: make(chan struct{}), entered: make(chan struct{})}
	r, err := NewRecorder(sink, Header{Pane: "eng1", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	r.Output([]byte("first"))
	select {
	case <-sink.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the writer never reached the disk with the first chunk")
	}
	const flood = recorderQueue * 3
	const sent = flood + 1
	start := time.Now()
	for i := 0; i < flood; i++ {
		r.Output([]byte("chunk"))
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("%d Output calls against a stalled disk took %v; the read loop would have blocked", flood, d)
	}
	// The writer is parked holding "first"; the queue takes exactly
	// recorderQueue more and every chunk past that is dropped.
	if got, want := r.Dropped(), uint64(flood-recorderQueue); got != want {
		t.Fatalf("dropped %d, want %d (flood %d minus a full queue of %d)", got, want, flood, recorderQueue)
	}
	close(sink.gate)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	_, recs := readAll(t, sink.bytes())
	tr := recs[len(recs)-1]
	if tr.Kind != KindTrailer || tr.Dropped == 0 {
		t.Fatalf("last record = %+v, want a trailer with the drop count", tr)
	}
	if kept := uint64(len(recs) - 1); kept+tr.Dropped != sent {
		t.Errorf("kept %d + dropped %d != sent %d", kept, tr.Dropped, sent)
	}
}

// A failing disk drops and counts rather than stopping the session.
func TestRecorder_WriteErrorsAreCountedNotFatal(t *testing.T) {
	sink := &memSink{fail: true}
	r, err := NewRecorder(sink, Header{Pane: "eng1"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		r.Output([]byte("lost"))
		time.Sleep(2 * time.Millisecond)
	}
	_ = r.Close()
	if r.Dropped() == 0 {
		t.Error("write errors were not counted as drops")
	}
}

// Calls after Close, and a nil recorder, are harmless.
func TestRecorder_AfterCloseAndNilAreInert(t *testing.T) {
	sink := &memSink{}
	r, _ := NewRecorder(sink, Header{Pane: "eng1"})
	_ = r.Close()
	r.Output([]byte("late"))
	r.Resize(1, 1)
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	var nilRec *Recorder
	nilRec.Output([]byte("x"))
	nilRec.Resize(1, 1)
	if err := nilRec.Close(); err != nil || nilRec.Dropped() != 0 {
		t.Error("nil recorder is not inert")
	}
}

type discardCloser struct{}

func (discardCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardCloser) Close() error                { return nil }

// BenchmarkRecorderOutput is the read loop's added cost per PTY chunk with
// recording on: a copy and a channel send (ini-pqdy.2 AC3).
func BenchmarkRecorderOutput(b *testing.B) {
	r, _ := NewRecorder(discardCloser{}, Header{Pane: "bench"})
	defer r.Close()
	chunk := bytes.Repeat([]byte("x"), 4096)
	b.SetBytes(int64(len(chunk)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Output(chunk)
	}
}
