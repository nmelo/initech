package appserve

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/progress"
)

func super(path string) PaneState {
	return PaneState{Name: "super", State: "idle", Banner: "none", SessionFile: path}
}

func replyLine(uuid, text string) string {
	return tline("assistant", uuid, t0, []map[string]any{{"type": "text", "text": text}})
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// AC11: GET /v1/blockers is Blockers over what the service holds: the super's
// latest footer with its recommendation, a stuck pane from the poll, a quiet
// rig's row, behind the token, and read fresh, so a reply already in the
// transcript is on the list before any event about it (eng7's reload).
func TestBlockersEndpoint_ReadsTheFooterAndThePoll(t *testing.T) {
	path := writeTranscript(t, []string{replyLine("a1", "Ship it?\n\nRecommendation: ship.\n\n- ▶ Approve the release?\n- Fleet: idle")})
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	b := &fakeBackend{rigs: []Rig{{Name: "alpha", Root: "/r/alpha"}, {Name: "beta", Root: "/r/beta"}},
		panes: map[string][]PaneState{
			"/r/alpha": {super(path), {Name: "eng1", State: "idle", Banner: "permission_prompt"}},
			"/r/beta":  {{Name: "eng1", State: "running", Banner: "none"}},
		}}
	s := New(Config{Backend: b, Digest: shaToken(), HasToken: true, Now: clk.Now})
	h := s.Handler()
	auth := "Bearer " + testToken

	if code, body := get(t, h, "/v1/blockers", auth); code != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("before a poll: %d %s, want []", code, body)
	}
	s.Poll()
	reads := b.statusCalls
	_, body := get(t, h, "/v1/blockers", auth)
	var list []RigBlockers
	if err := json.Unmarshal([]byte(body), &list); err != nil || len(list) != 2 {
		t.Fatalf("list = %s (%v)", body, err)
	}
	alpha, beta := list[0], list[1]
	if len(alpha.Waiting) != 1 || alpha.Waiting[0].Text != "Approve the release?" || alpha.Waiting[0].Recommendation != "ship." || alpha.Waiting[0].Pane != "super" {
		t.Errorf("waiting = %+v", alpha.Waiting)
	}
	if len(alpha.Stuck) != 1 || alpha.Stuck[0].Pane != "eng1" || alpha.Quiet {
		t.Errorf("stuck = %+v quiet %v", alpha.Stuck, alpha.Quiet)
	}
	if !beta.Quiet || len(beta.Waiting)+len(beta.Stuck) != 0 {
		t.Errorf("beta = %+v, want a quiet row", beta)
	}
	if b.statusCalls != reads {
		t.Errorf("a blockers read asked for status (bd)")
	}

	// A new footer is on the list as soon as it is in the transcript, with no
	// poll between; a later reply with no footer clears nothing.
	appendLine(t, path, replyLine("a2", "Done.\n\n▶ nothing"))
	_, body = get(t, h, "/v1/blockers", auth)
	json.Unmarshal([]byte(body), &list)
	if len(list[0].Waiting) != 0 {
		t.Errorf("after a nothing footer: %+v", list[0].Waiting)
	}
	appendLine(t, path, replyLine("a3", "- ▶ Restart hover?"))
	appendLine(t, path, replyLine("a4", "A reply with no footer."))
	_, body = get(t, h, "/v1/blockers", auth)
	json.Unmarshal([]byte(body), &list)
	if len(list[0].Waiting) != 1 || list[0].Waiting[0].Text != "Restart hover?" {
		t.Errorf("a footerless reply cleared or a new footer missed: %+v", list[0].Waiting)
	}

	if code, body := get(t, h, "/v1/blockers", ""); code != 401 || body != "" {
		t.Errorf("no token: %d %q", code, body)
	}
}

// AC11, AC4: a poll that changes the list sends one blockers event carrying
// it and one waiting_item per new text; an unchanged list sends none; the
// first poll is the baseline.
func TestBlockersFeed_EventsOnAChangedList(t *testing.T) {
	path := writeTranscript(t, []string{replyLine("a1", "- ▶ First item")})
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	b := &fakeBackend{rigs: []Rig{{Name: "alpha", Root: "/r/alpha"}}, panes: map[string][]PaneState{"/r/alpha": {super(path)}}}
	s := New(Config{Backend: b, Digest: shaToken(), HasToken: true, Now: clk.Now})
	types := func(after int64) []string {
		evs, _, _ := s.Feed().Since(after)
		var out []string
		for _, e := range evs {
			out = append(out, e.Type)
		}
		return out
	}
	s.Poll()
	if got := types(0); len(got) != 0 {
		t.Fatalf("baseline poll sent %v", got)
	}
	s.Poll()
	if got := types(0); len(got) != 0 {
		t.Fatalf("an unchanged list sent %v", got)
	}
	appendLine(t, path, replyLine("a2", "- ▶ First item\n- ▶ Second item"))
	s.Poll()
	evs, _, _ := s.Feed().Since(0)
	if len(evs) != 2 || evs[0].Type != "waiting_item" || evs[1].Type != "blockers" {
		t.Fatalf("events = %v, want one waiting_item for the new text, then blockers", types(0))
	}
	if w := evs[0].Data.(WaitingEvent); w != (WaitingEvent{Rig: "alpha", Pane: "super", Text: "Second item"}) {
		t.Errorf("waiting_item = %+v", w)
	}
	if list := evs[1].Data.([]RigBlockers); len(list) != 1 || len(list[0].Waiting) != 2 {
		t.Errorf("blockers event = %+v", list)
	}
	last := s.Feed().Last()
	s.Poll()
	if s.Feed().Last() != last {
		t.Errorf("an unchanged poll sent %v", types(last))
	}
}

// AC11: the beads of the latest progress run reach Blockers: a bead quiet an
// hour with its owner idle is stuck on that owner's pane.
func TestBlockersSnapshot_CarriesTheProgressRunsBeads(t *testing.T) {
	clk := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	b := &progressBackend{efforts: map[string][]string{"/r/a": {"ini-x."}}}
	b.rigs = []Rig{{Name: "a", Root: "/r/a"}}
	b.panes = map[string][]PaneState{"/r/a": {{Name: "eng1", State: "idle", Banner: "none"}}}
	s := New(Config{Backend: b, Digest: shaToken(), HasToken: true, Now: clk.Now})
	s.Poll()
	s.mu.Lock()
	s.progress = []progress.Report{{Rig: "a", Prefix: "ini-x.", Beads: []progress.Row{
		{ID: "ini-x.1", Stage: "in_progress", Owner: "eng1", LastMove: clk.Now().Add(-2 * time.Hour)},
		{ID: "ini-x.2", Stage: "in_progress", Owner: "-", LastMove: clk.Now().Add(-2 * time.Hour)},
	}}}
	s.mu.Unlock()
	snap := s.snapshot()
	if len(snap.Rigs) != 1 || len(snap.Rigs[0].Beads) != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if got := snap.Rigs[0].Beads[0]; got.ID != "ini-x.1" || got.Status != "in_progress" || got.Assignee != "eng1" || got.LastMove.IsZero() {
		t.Errorf("bead = %+v", got)
	}
	if got := snap.Rigs[0].Beads[1].Assignee; got != "" {
		t.Errorf("no owner reads %q, want empty", got)
	}
	list := Blockers(snap)
	if len(list[0].Stuck) != 1 || list[0].Stuck[0].Pane != "eng1" {
		t.Errorf("stuck = %+v, want eng1 for the quiet bead", list[0].Stuck)
	}
}

// AC6 feed-it-nothing: a super with no transcript, an unreadable one and an
// empty one have no items and no error.
func TestSuperReplies_NoTranscriptIsNoItems(t *testing.T) {
	if got := superReplies(""); got != nil {
		t.Errorf("no transcript: %v", got)
	}
	if got := superReplies("/no/such/file.jsonl"); got != nil {
		t.Errorf("no file: %v", got)
	}
	if got := superReplies(writeTranscript(t, []string{`{"type":"mode"}`})); len(got) != 0 {
		t.Errorf("no replies: %v", got)
	}
}
