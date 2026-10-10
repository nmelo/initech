package recording

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"
)

func writeSample(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, Header{Pane: "eng1", Start: time.Unix(1700000000, 5).UTC(), Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(w.Output(10*time.Millisecond, []byte("hello\r\n")))
	must(w.Resize(20*time.Millisecond, 100, 30))
	must(w.Output(35*time.Millisecond, []byte("\x1b[1mbold\x1b[0m")))
	must(w.Trailer(40*time.Millisecond, 3))
	return buf.Bytes()
}

func readAll(t *testing.T, b []byte) (Header, []Record) {
	t.Helper()
	r, err := NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	var recs []Record
	for {
		rec, err := r.Next()
		if errors.Is(err, io.EOF) {
			return r.Header(), recs
		}
		if err != nil {
			t.Fatal(err)
		}
		recs = append(recs, rec)
	}
}

// The wire layout is what pqdy.3's replayer was agreed against: magic, then
// kind | t uint64 BE | len uint32 BE | payload, header first at t=0.
func TestRecording_WireLayoutIsTheAgreedOne(t *testing.T) {
	b := writeSample(t)
	if string(b[:4]) != "IREC" {
		t.Fatalf("magic = %q", b[:4])
	}
	if b[4] != 'H' || binary.BigEndian.Uint64(b[5:13]) != 0 {
		t.Fatalf("first record kind=%q t=%d, want H at 0", b[4], binary.BigEndian.Uint64(b[5:13]))
	}
	hlen := binary.BigEndian.Uint32(b[13:17])
	o := 17 + int(hlen)
	if b[o] != 'O' || time.Duration(binary.BigEndian.Uint64(b[o+1:o+9])) != 10*time.Millisecond ||
		binary.BigEndian.Uint32(b[o+9:o+13]) != 7 || string(b[o+13:o+20]) != "hello\r\n" {
		t.Errorf("second record is not O at 10ms with the 7 output bytes: % x", b[o:o+20])
	}
}

// Every record round-trips with its kind, time and body.
func TestRecording_WriterAndReaderRoundTrip(t *testing.T) {
	h, recs := readAll(t, writeSample(t))
	if h.Version != Version || h.Pane != "eng1" || h.Cols != 80 || h.Rows != 24 || !h.Start.Equal(time.Unix(1700000000, 5)) {
		t.Errorf("header = %+v", h)
	}
	if len(recs) != 4 {
		t.Fatalf("got %d records, want 4: %+v", len(recs), recs)
	}
	if recs[0].Kind != KindOutput || recs[0].At != 10*time.Millisecond || string(recs[0].Data) != "hello\r\n" {
		t.Errorf("record 0 = %+v", recs[0])
	}
	if recs[1].Kind != KindResize || recs[1].Cols != 100 || recs[1].Rows != 30 || recs[1].At != 20*time.Millisecond {
		t.Errorf("record 1 = %+v", recs[1])
	}
	if recs[2].Kind != KindOutput || string(recs[2].Data) != "\x1b[1mbold\x1b[0m" {
		t.Errorf("record 2 = %+v", recs[2])
	}
	if recs[3].Kind != KindTrailer || recs[3].Dropped != 3 {
		t.Errorf("record 3 = %+v", recs[3])
	}
}

// A record of an unknown kind is skipped, not an error.
func TestRecording_ReaderSkipsUnknownKinds(t *testing.T) {
	b := writeSample(t)
	hlen := binary.BigEndian.Uint32(b[13:17])
	at := 17 + int(hlen)
	unknown := []byte{'Z', 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 3, 'a', 'b', 'c'}
	spliced := append(append(append([]byte{}, b[:at]...), unknown...), b[at:]...)
	_, recs := readAll(t, spliced)
	if len(recs) != 4 || recs[0].Kind != KindOutput {
		t.Errorf("with an unknown record spliced in: %d records, first %q; want the same 4", len(recs), recs[0].Kind)
	}
}

// A file cut off mid-record (a crashed session) reads up to its last whole
// record and then reports io.EOF.
func TestRecording_TruncatedFileReadsToItsLastWholeRecord(t *testing.T) {
	b := writeSample(t)
	for cut := len(b) - 1; cut > len(b)-20; cut-- {
		_, recs := readAll(t, b[:cut])
		if len(recs) != 3 {
			t.Errorf("cut at %d of %d: %d records, want the 3 before the trailer", cut, len(b), len(recs))
		}
	}
}

// Not a recording, or a newer version, is refused at open.
func TestRecording_ReaderRefusesForeignAndNewerFiles(t *testing.T) {
	if _, err := NewReader(bytes.NewReader([]byte("hello world"))); !errors.Is(err, ErrNotRecording) {
		t.Errorf("foreign file: err = %v, want ErrNotRecording", err)
	}
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, Header{Pane: "x"})
	_ = w
	b := buf.Bytes()
	newer := bytes.Replace(b, []byte(`"version":1`), []byte(`"version":9`), 1)
	if bytes.Equal(newer, b) {
		t.Fatal("fixture: version field not found in the header")
	}
	if _, err := NewReader(bytes.NewReader(newer)); err == nil {
		t.Error("a version-9 recording was accepted")
	}
}
