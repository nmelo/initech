package appserve

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tailServer(t *testing.T, panes []PaneState) (*Server, *fakeBackend) {
	t.Helper()
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	b := &fakeBackend{rigs: []Rig{{Name: "alpha", Root: "/r/alpha"}}, panes: map[string][]PaneState{"/r/alpha": panes}}
	s := New(Config{Backend: b, Digest: shaToken(), HasToken: true, Now: clk.Now})
	s.Poll()
	return s, b
}

func eventsOf(s *Server, after int64, typ string) []Event {
	evs, _, _ := s.Feed().Since(after)
	var out []Event
	for _, e := range evs {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// AC4: a new transcript entry reaches a subscriber within a second through
// the running tail, rendered as the conversation renders it.
func TestTail_ANewEntryReachesASubscriberWithinASecond(t *testing.T) {
	if TailEvery > 500*time.Millisecond {
		t.Fatalf("TailEvery %s leaves no room inside a second", TailEvery)
	}
	path := writeTranscript(t, []string{tline("user", "old", t0, "before the service")})
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	b := &fakeBackend{rigs: []Rig{{Name: "alpha", Root: "/r/alpha"}},
		panes: map[string][]PaneState{"/r/alpha": {{Name: "eng1", State: "idle", SessionFile: path}}}}
	s, hs := feedServer(t, b, clk)
	s.Poll()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunTail(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	ch := subscribe(t, hs.URL, "")
	time.Sleep(2 * TailEvery) // the tail has taken the file's end

	appendLine(t, path, tline("assistant", "a1", t0, []map[string]any{{"type": "text", "text": "a live reply"}}))
	e := next(t, ch, 2*time.Second) // a second, with margin for a loaded test machine
	var te TranscriptEvent
	if err := json.Unmarshal([]byte(e.Data), &te); err != nil || e.Type != "transcript" {
		t.Fatalf("event %+v (%v)", e, err)
	}
	if te.Rig != "alpha" || te.Pane != "eng1" || te.Message.Kind != KindAgent || te.Message.Text != "a live reply" {
		t.Errorf("transcript event = %+v", te)
	}
}

// The tail starts a pane at the end of its file, so history is not replayed;
// a half-written line waits for its newline; a pane moved to a new session
// file reads that file from its start; a shrunken file reads nothing back.
func TestTail_StartsAtTheEndAndReadsWholeLines(t *testing.T) {
	path := writeTranscript(t, []string{tline("user", "old", t0, "history")})
	ps := PaneState{Name: "eng1", State: "idle", SessionFile: path}
	s, b := tailServer(t, []PaneState{ps})
	s.tailOnce()
	if got := eventsOf(s, 0, "transcript"); len(got) != 0 {
		t.Fatalf("first look replayed history: %+v", got)
	}

	line := tline("user", "n1", t0, "new words")
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(line[:10])
	f.Sync()
	s.tailOnce()
	if got := eventsOf(s, 0, "transcript"); len(got) != 0 {
		t.Fatalf("a half-written line was read: %+v", got)
	}
	f.WriteString(line[10:] + "\n")
	f.Close()
	s.tailOnce()
	got := eventsOf(s, 0, "transcript")
	if len(got) != 1 || got[0].Data.(TranscriptEvent).Message.Text != "new words" {
		t.Fatalf("after its newline: %+v", got)
	}
	s.tailOnce()
	if n := len(eventsOf(s, 0, "transcript")); n != 1 {
		t.Errorf("a look with nothing new sent %d events in all, want 1", n)
	}

	next := filepath.Join(t.TempDir(), "sess-2.jsonl")
	os.WriteFile(next, []byte(tline("user", "s2", t0, "new session")+"\n"), 0o600)
	b.mu.Lock()
	b.panes["/r/alpha"] = []PaneState{{Name: "eng1", State: "idle", SessionFile: next}}
	b.mu.Unlock()
	s.Poll()
	s.tailOnce()
	got = eventsOf(s, 0, "transcript")
	if len(got) != 2 || got[1].Data.(TranscriptEvent).Message.Text != "new session" {
		t.Errorf("a new session file: %+v", got)
	}

	os.WriteFile(next, nil, 0o600)
	s.tailOnce()
	if n := len(eventsOf(s, 0, "transcript")); n != 2 {
		t.Errorf("a shrunken file sent events: %d", n)
	}
	appendLine(t, next, tline("user", "s3", t0, "after the shrink"))
	s.tailOnce()
	if n := len(eventsOf(s, 0, "transcript")); n != 3 {
		t.Errorf("after a shrink, new lines: %d events, want 3", n)
	}
}

// AC6 feed-it-nothing: a pane with no transcript, and one whose file has
// gone, send nothing; a malformed new line is left out and the look goes on.
func TestTail_NoTranscriptAndAMalformedLineSendNothing(t *testing.T) {
	path := writeTranscript(t, []string{tline("user", "old", t0, "history")})
	gone := filepath.Join(t.TempDir(), "gone.jsonl")
	s, _ := tailServer(t, []PaneState{{Name: "none", State: "idle"}, {Name: "gone", State: "idle", SessionFile: gone}, {Name: "eng1", State: "idle", SessionFile: path}})
	s.tailOnce()
	appendLine(t, path, `{"type":"user","uuid":`)
	appendLine(t, path, tline("user", "n1", t0, "still here"))
	s.tailOnce()
	got := eventsOf(s, 0, "transcript")
	if len(got) != 1 || got[0].Data.(TranscriptEvent).Message.Text != "still here" {
		t.Errorf("events = %+v, want only the good line", got)
	}
}
