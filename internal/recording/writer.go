package recording

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"time"
)

// Writer writes one recording to an io.Writer. It is not safe for concurrent
// use and does no buffering of its own; the pane-side Recorder wraps it in a
// bufio.Writer on its own goroutine.
type Writer struct {
	w   io.Writer
	hdr [recordHeaderLen]byte
}

// NewWriter writes the magic and the header record and returns a Writer for
// the records that follow. h.Version is set to Version.
func NewWriter(w io.Writer, h Header) (*Writer, error) {
	if _, err := io.WriteString(w, Magic); err != nil {
		return nil, err
	}
	h.Version = Version
	body, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	wr := &Writer{w: w}
	if err := wr.record(KindHeader, 0, body); err != nil {
		return nil, err
	}
	return wr, nil
}

// Output writes one chunk of child output read at time at.
func (w *Writer) Output(at time.Duration, data []byte) error {
	return w.record(KindOutput, at, data)
}

// Resize records that the child's PTY became cols x rows at time at.
func (w *Writer) Resize(at time.Duration, cols, rows int) error {
	body, _ := json.Marshal(resizeBody{Cols: cols, Rows: rows})
	return w.record(KindResize, at, body)
}

// Trailer writes the clean-close record with the count of dropped chunks.
func (w *Writer) Trailer(at time.Duration, dropped uint64) error {
	body, _ := json.Marshal(trailerBody{Dropped: dropped})
	return w.record(KindTrailer, at, body)
}

// Write writes r as it is, so a tool that rewrites a recording (the shape
// copy) keeps every record's kind and time.
func (w *Writer) Write(r Record) error {
	switch r.Kind {
	case KindOutput:
		return w.Output(r.At, r.Data)
	case KindResize:
		return w.Resize(r.At, r.Cols, r.Rows)
	case KindTrailer:
		return w.Trailer(r.At, r.Dropped)
	}
	return nil
}

func (w *Writer) record(kind byte, at time.Duration, payload []byte) error {
	if at < 0 {
		at = 0
	}
	w.hdr[0] = kind
	binary.BigEndian.PutUint64(w.hdr[1:9], uint64(at))
	binary.BigEndian.PutUint32(w.hdr[9:13], uint32(len(payload)))
	if _, err := w.w.Write(w.hdr[:]); err != nil {
		return err
	}
	_, err := w.w.Write(payload)
	return err
}
