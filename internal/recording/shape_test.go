package recording

import (
	"bytes"
	"testing"
	"time"
)

// groundText returns the printable ASCII bytes that sit OUTSIDE escape
// sequences, by an independent walk (regexp-free, one pass, no shared state
// with Shaper) so the test does not grade the shaper with itself.
func groundText(b []byte) []byte {
	var out []byte
	i := 0
	for i < len(b) {
		c := b[i]
		if c != 0x1b {
			if c >= 0x20 && c <= 0x7e {
				out = append(out, c)
			}
			i++
			continue
		}
		i++
		if i >= len(b) {
			break
		}
		switch b[i] {
		case '[':
			i++
			for i < len(b) && !(b[i] >= 0x40 && b[i] <= 0x7e) {
				i++
			}
			i++
		case ']', 'P', 'X', '^', '_':
			for i < len(b) {
				if b[i] == 0x07 {
					i++
					break
				}
				if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default:
			for i < len(b) && b[i] >= 0x20 && b[i] <= 0x2f {
				i++
			}
			i++
		}
	}
	return out
}

const shapeFixture = "secret plan\r\n\x1b[1;32mgreen words\x1b[0m \x1b]0;title text\x07after\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\\x1b(Bdone é ü\r\n"

// AC2: the shape copy has no printable ASCII from the original outside
// escapes, keeps every escape byte for byte, and keeps non-ASCII bytes and
// the byte count.
func TestShaper_StripsTextKeepsEscapesAndNonASCII(t *testing.T) {
	in := []byte(shapeFixture)
	var s Shaper
	out := s.Shape(in)
	if len(out) != len(in) {
		t.Fatalf("length %d -> %d", len(in), len(out))
	}
	for _, c := range groundText(out) {
		if c != ShapeFiller {
			t.Fatalf("printable %q survived outside escapes in %q", c, out)
		}
	}
	if len(groundText(out)) != len(groundText(in)) {
		t.Errorf("ground text length changed: %d -> %d", len(groundText(in)), len(groundText(out)))
	}
	for _, esc := range []string{"\x1b[1;32m", "\x1b[0m", "\x1b]0;title text\x07", "\x1b]8;;http://x\x1b\\", "\x1b(B"} {
		if !bytes.Contains(out, []byte(esc)) {
			t.Errorf("escape %q was altered", esc)
		}
	}
	if !bytes.Contains(out, []byte("é ü")[:2]) || !bytes.Contains(out, []byte("\r\n")) {
		t.Error("non-ASCII or control bytes were not kept")
	}
}

// A sequence split across PTY reads is still recognised: the shaper keeps
// its state between chunks, so every split gives the same bytes.
func TestShaper_SequencesSplitAcrossChunks(t *testing.T) {
	in := []byte(shapeFixture)
	var whole Shaper
	want := whole.Shape(in)
	for cut := 1; cut < len(in); cut++ {
		var s Shaper
		got := append(s.Shape(in[:cut]), s.Shape(in[cut:])...)
		if !bytes.Equal(got, want) {
			t.Fatalf("split at %d gives %q, want %q", cut, got, want)
		}
	}
}

// AC2 on a whole file: same record count, kinds, times and sizes; header
// marked shape_only; output bodies stripped.
func TestShapeCopy_KeepsRecordsTimesAndSizes(t *testing.T) {
	var src bytes.Buffer
	w, _ := NewWriter(&src, Header{Pane: "eng1", Cols: 80, Rows: 24})
	_ = w.Output(5*time.Millisecond, []byte("hello \x1b[3"))
	_ = w.Output(9*time.Millisecond, []byte("1mred\x1b[0m"))
	_ = w.Resize(12*time.Millisecond, 100, 30)
	_ = w.Output(20*time.Millisecond, []byte("bye"))
	_ = w.Trailer(25*time.Millisecond, 0)

	var dst bytes.Buffer
	n, err := ShapeCopy(&dst, bytes.NewReader(src.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	h0, in := readAll(t, src.Bytes())
	h1, out := readAll(t, dst.Bytes())
	if n != len(in) || len(out) != len(in) {
		t.Fatalf("records: source %d, copied %d, read back %d", len(in), n, len(out))
	}
	if !h1.ShapeOnly || h1.Pane != h0.Pane || h1.Cols != h0.Cols || h1.Rows != h0.Rows {
		t.Errorf("header %+v from %+v", h1, h0)
	}
	var all []byte
	for i := range in {
		if out[i].Kind != in[i].Kind || out[i].At != in[i].At || out[i].Cols != in[i].Cols ||
			out[i].Rows != in[i].Rows || len(out[i].Data) != len(in[i].Data) {
			t.Errorf("record %d: %+v from %+v", i, out[i], in[i])
		}
		all = append(all, out[i].Data...)
	}
	if string(all) != "xxxxxx\x1b[31mxxx\x1b[0mxxx" {
		t.Errorf("shaped output = %q", all)
	}
}
