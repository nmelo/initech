package appserve

import (
	"bytes"
	"context"
	"io"
	"os"
	"time"
)

// TailEvery is how often the service looks for new transcript lines: a stat
// per pane, no bd or git, so a new entry reaches the feed within a second.
const TailEvery = 500 * time.Millisecond

// tailReadMax bounds what one look reads of one transcript; a jump larger
// than this is skipped, and the app reloads the page when it opens the pane.
const tailReadMax = 32 << 20

// TranscriptEvent is the data of a transcript event: one new message in a
// pane's conversation.
type TranscriptEvent struct {
	Rig     string  `json:"rig"`
	Pane    string  `json:"pane"`
	Message Message `json:"message"`
}

// tailPos is how far the tail has read a pane's transcript.
type tailPos struct {
	file   string
	offset int64
}

// RunTail looks for new transcript lines every TailEvery until ctx ends.
func (s *Server) RunTail(ctx context.Context) {
	for {
		s.tailOnce()
		select {
		case <-ctx.Done():
			return
		case <-s.cfg.After(TailEvery):
		}
	}
}

// tailOnce reads what each polled pane's transcript gained: a transcript
// event per new message, and for a super whose reply is new, the blockers
// list again (on disk already, so anyone reloading on the event reads it). A
// pane seen for the first time starts at the end of its file; a pane that
// moved to a new session file starts that file at its beginning.
func (s *Server) tailOnce() {
	s.mu.Lock()
	rigs := append([]Rig(nil), s.rigs...)
	polled := s.panes
	s.mu.Unlock()

	for _, rg := range rigs {
		for _, ps := range polled[rg.Root] {
			if ps.SessionFile == "" {
				continue
			}
			key := rg.Root + "\x00" + ps.Name
			pos, known := s.tails[key]
			st, err := os.Stat(ps.SessionFile)
			if err != nil {
				continue
			}
			if !known {
				s.tails[key] = tailPos{ps.SessionFile, st.Size()}
				continue
			}
			if pos.file != ps.SessionFile {
				pos = tailPos{ps.SessionFile, 0}
			}
			msgs := s.readNew(&pos, st.Size())
			s.tails[key] = pos
			if ps.Name == superPaneName && hasReply(msgs) {
				s.publishBlockers()
			}
			for _, m := range msgs {
				s.feed.Publish("transcript", TranscriptEvent{Rig: rg.Name, Pane: ps.Name, Message: m})
			}
		}
	}
}

// hasReply reports an agent reply among msgs.
func hasReply(msgs []Message) bool {
	for _, m := range msgs {
		if m.Kind == KindAgent {
			return true
		}
	}
	return false
}

// readNew renders the whole lines between pos and size, moving pos past them;
// a last line with no newline yet waits for the next look.
func (s *Server) readNew(pos *tailPos, size int64) []Message {
	if size < pos.offset { // truncated or replaced: nothing to read back
		pos.offset = size
		return nil
	}
	if size == pos.offset {
		return nil
	}
	if size-pos.offset > tailReadMax {
		pos.offset = size
		return nil
	}
	f, err := os.Open(pos.file)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, size-pos.offset)
	if _, err := f.ReadAt(buf, pos.offset); err != nil && err != io.EOF {
		return nil
	}
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return nil
	}
	pos.offset += int64(end) + 1
	var r renderer
	for _, ln := range bytes.Split(buf[:end], []byte{'\n'}) {
		if ln = bytes.TrimSpace(ln); len(ln) > 0 {
			r.line(ln)
		}
	}
	return r.msgs
}
