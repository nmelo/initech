package replay

import (
	"errors"
	"io"

	"github.com/nmelo/initech/internal/recording"
)

// FileOpener plays a recording file written by internal/recording (ini-pqdy.2).
// Each pass reopens the file, so a looping replay reads it from the start.
//
// Only output reaches the replayer. Resize records are ignored on purpose
// (pqdy.3 behaviour 2): the pane's real size rules, so replay at a different
// width wraps differently, while the byte volume and escape mix -- what costs
// frames -- are unchanged. The trailer, like the end of the file, ends a pass.
func FileOpener(path string) Opener {
	return func() (Source, error) {
		r, err := recording.Open(path)
		if err != nil {
			return nil, err
		}
		return &fileSource{r: r}, nil
	}
}

type fileSource struct{ r *recording.Reader }

func (s *fileSource) Next() (Chunk, error) {
	for {
		rec, err := s.r.Next()
		if err != nil {
			s.r.Close()
			if errors.Is(err, io.EOF) {
				return Chunk{}, io.EOF
			}
			return Chunk{}, err
		}
		switch rec.Kind {
		case recording.KindOutput:
			return Chunk{At: rec.At, Data: rec.Data}, nil
		case recording.KindTrailer:
			s.r.Close()
			return Chunk{}, io.EOF
		}
	}
}
