package recording

import (
	"errors"
	"io"
)

// ShapeFiller replaces every printable ASCII byte outside an escape
// sequence in a shape-only copy.
const ShapeFiller = 'x'

// shapeState is where the escape parser stands between bytes. It is kept
// across chunks: a PTY read can end in the middle of a sequence.
type shapeState int

const (
	shapeGround    shapeState = iota // Plain text.
	shapeEsc                         // After ESC.
	shapeEscInter                    // ESC then intermediate bytes (0x20-0x2F), waiting for the final.
	shapeCSI                         // ESC [ ... until a final byte 0x40-0x7E.
	shapeString                      // OSC, DCS, SOS, PM or APC body, until BEL or ESC \.
	shapeStringEsc                   // ESC seen inside a string: \ ends it.
)

// Shaper rewrites output so it keeps the terminal's work and loses the
// words: escape sequences pass through untouched, printable ASCII (0x20-0x7E)
// outside them becomes ShapeFiller, and every other byte (controls, UTF-8)
// is kept. Byte counts are unchanged.
//
// Known limit: text INSIDE escape sequences is kept, as the bead specifies
// ("leaves escape sequences untouched") -- so window titles and hyperlink
// targets set by OSC survive in a shape-only copy.
type Shaper struct {
	state shapeState
}

// Shape returns data rewritten, continuing from the state the previous call
// left.
func (s *Shaper) Shape(data []byte) []byte {
	out := make([]byte, len(data))
	for i, b := range data {
		out[i] = b
		switch s.state {
		case shapeGround:
			if b == 0x1b {
				s.state = shapeEsc
			} else if b >= 0x20 && b <= 0x7e {
				out[i] = ShapeFiller
			}
		case shapeEsc:
			switch {
			case b == '[':
				s.state = shapeCSI
			case b == ']' || b == 'P' || b == 'X' || b == '^' || b == '_':
				s.state = shapeString
			case b == 0x1b:
				s.state = shapeEsc
			case b >= 0x20 && b <= 0x2f:
				s.state = shapeEscInter
			default:
				s.state = shapeGround
			}
		case shapeEscInter:
			if b == 0x1b {
				s.state = shapeEsc
			} else if b >= 0x30 && b <= 0x7e {
				s.state = shapeGround
			}
		case shapeCSI:
			if b == 0x1b {
				s.state = shapeEsc
			} else if b >= 0x40 && b <= 0x7e {
				s.state = shapeGround
			}
		case shapeString:
			if b == 0x07 {
				s.state = shapeGround
			} else if b == 0x1b {
				s.state = shapeStringEsc
			}
		case shapeStringEsc:
			if b == '\\' {
				s.state = shapeGround
			} else if b != 0x1b {
				s.state = shapeString
			}
		}
	}
	return out
}

// ShapeCopy reads a recording from r and writes its shape-only copy to w:
// the same records with the same times and sizes, output bodies rewritten
// by a Shaper, and the header marked shape_only. Returns the number of
// records copied after the header.
func ShapeCopy(w io.Writer, r io.Reader) (int, error) {
	rd, err := NewReader(r)
	if err != nil {
		return 0, err
	}
	h := rd.Header()
	h.ShapeOnly = true
	wr, err := NewWriter(w, h)
	if err != nil {
		return 0, err
	}
	var sh Shaper
	n := 0
	for {
		rec, err := rd.Next()
		if errors.Is(err, io.EOF) {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		if rec.Kind == KindOutput {
			rec.Data = sh.Shape(rec.Data)
		}
		if err := wr.Write(rec); err != nil {
			return n, err
		}
		n++
	}
}
