package recording

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// ErrNotRecording is returned when the input does not start with Magic and a
// header record.
var ErrNotRecording = errors.New("recording: not an initech recording")

// Reader reads a recording record by record.
type Reader struct {
	r      *bufio.Reader
	header Header
	closer io.Closer
}

// NewReader reads the magic and the header and returns a Reader positioned
// at the first record after the header. A header version above Version is
// refused.
func NewReader(r io.Reader) (*Reader, error) {
	br := bufio.NewReader(r)
	magic := make([]byte, len(Magic))
	if _, err := io.ReadFull(br, magic); err != nil || string(magic) != Magic {
		return nil, ErrNotRecording
	}
	rd := &Reader{r: br}
	kind, _, payload, err := rd.raw()
	if err != nil || kind != KindHeader {
		return nil, ErrNotRecording
	}
	if err := json.Unmarshal(payload, &rd.header); err != nil {
		return nil, fmt.Errorf("recording: bad header: %w", err)
	}
	if rd.header.Version > Version {
		return nil, fmt.Errorf("recording: version %d is newer than this reader (%d)", rd.header.Version, Version)
	}
	return rd, nil
}

// Open opens the recording at path. Close releases the file.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	rd, err := NewReader(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	rd.closer = f
	return rd, nil
}

// Close closes the underlying file when the Reader came from Open.
func (r *Reader) Close() error {
	if r.closer == nil {
		return nil
	}
	return r.closer.Close()
}

// Header returns the recording's header.
func (r *Reader) Header() Header { return r.header }

// Next returns the next record. It returns io.EOF at the end of the file and
// also when the last record is cut short (a session that did not close
// cleanly). Records of a kind this reader does not know are skipped.
func (r *Reader) Next() (Record, error) {
	for {
		kind, at, payload, err := r.raw()
		if err != nil {
			return Record{}, err
		}
		rec := Record{Kind: kind, At: at}
		switch kind {
		case KindOutput:
			rec.Data = payload
		case KindResize:
			var b resizeBody
			if err := json.Unmarshal(payload, &b); err != nil {
				return Record{}, fmt.Errorf("recording: bad resize at %v: %w", at, err)
			}
			rec.Cols, rec.Rows = b.Cols, b.Rows
		case KindTrailer:
			var b trailerBody
			if err := json.Unmarshal(payload, &b); err != nil {
				return Record{}, fmt.Errorf("recording: bad trailer at %v: %w", at, err)
			}
			rec.Dropped = b.Dropped
		default:
			continue
		}
		return rec, nil
	}
}

// raw reads one record. A clean end or a cut-off record both return io.EOF.
func (r *Reader) raw() (byte, time.Duration, []byte, error) {
	var hdr [recordHeaderLen]byte
	if _, err := io.ReadFull(r.r, hdr[:]); err != nil {
		return 0, 0, nil, eofFor(err)
	}
	n := binary.BigEndian.Uint32(hdr[9:13])
	if n > maxPayload {
		return 0, 0, nil, fmt.Errorf("recording: record of %d bytes exceeds the %d limit", n, maxPayload)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r.r, payload); err != nil {
		return 0, 0, nil, eofFor(err)
	}
	return hdr[0], time.Duration(binary.BigEndian.Uint64(hdr[1:9])), payload, nil
}

func eofFor(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return io.EOF
	}
	return err
}
