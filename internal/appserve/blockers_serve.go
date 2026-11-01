package appserve

import (
	"encoding/json"
	"net/http"
	"sort"
)

// Limits on reading a super's replies for its footer: pages of messages read
// back from the newest, until a reply with a footer is found.
const (
	repliesPageLimit = 200
	repliesPagesMax  = 5
)

// superReplies are the agent replies of the transcript at path, oldest first,
// read back from the newest only as far as the latest reply with a footer: a
// reply with no footer clears nothing, so that is the one Blockers reads. A
// transcript that cannot be read has none.
func superReplies(path string) []Reply {
	if path == "" {
		return nil
	}
	var replies []Reply
	before := int64(0)
	for i := 0; i < repliesPagesMax; i++ {
		pg, err := ReadPage(path, before, repliesPageLimit)
		if err != nil {
			return replies
		}
		var page []Reply
		found := false
		for _, m := range pg.Messages {
			if m.Kind != KindAgent {
				continue
			}
			page = append(page, Reply{Text: m.Text, Footer: m.Footer})
			found = found || len(m.Footer) > 0
		}
		replies = append(page, replies...)
		if found || pg.StartReached {
			return replies
		}
		before = pg.Before
	}
	return replies
}

// snapshot is what Blockers reads, from what the service already holds: the
// last poll's rigs and panes, the super pane's transcript, and the latest
// progress run's beads. It makes no bd or git call and asks no TUI.
func (s *Server) snapshot() Snapshot {
	s.mu.Lock()
	rigs := append([]Rig(nil), s.rigs...)
	polled := s.panes
	reports := s.progress
	s.mu.Unlock()

	snap := Snapshot{Now: s.cfg.Now(), Rigs: make([]RigSnapshot, 0, len(rigs))}
	for _, rg := range rigs {
		row := RigSnapshot{Rig: rg.Name}
		names := make([]string, 0, len(polled[rg.Root]))
		for name := range polled[rg.Root] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			ps := polled[rg.Root][name]
			p := PaneSnapshot{Name: ps.Name, State: ps.State, Banner: ps.Banner}
			if ps.Name == superPaneName {
				p.Replies = superReplies(ps.SessionFile)
			}
			row.Panes = append(row.Panes, p)
		}
		for _, rep := range reports {
			if rep.Rig != rg.Name {
				continue
			}
			for _, b := range rep.Beads {
				owner := b.Owner
				if owner == "-" {
					owner = ""
				}
				row.Beads = append(row.Beads, BeadSnapshot{ID: b.ID, Status: b.Stage, Assignee: owner, LastMove: b.LastMove})
			}
		}
		snap.Rigs = append(snap.Rigs, row)
	}
	return snap
}

// handleBlockers is GET /v1/blockers: the blockers list as of now, read
// fresh from the transcript, so a reload on a transcript event sees that
// reply.
func (s *Server) handleBlockers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, Blockers(s.snapshot()))
}

// publishBlockers sends a blockers event when the list changed since the
// last one, and a waiting_item event for each item text not on the list
// before. The first call sets the baseline and sends nothing.
func (s *Server) publishBlockers() {
	list := Blockers(s.snapshot())
	raw, err := json.Marshal(list)
	if err != nil {
		return
	}
	waiting := map[string]WaitingEvent{}
	for _, rb := range list {
		for _, it := range rb.Waiting {
			e := WaitingEvent{Rig: rb.Rig, Pane: it.Pane, Text: it.Text}
			waiting[rb.Rig+"\x00"+it.Pane+"\x00"+it.Text] = e
		}
	}
	s.blockersMu.Lock()
	defer s.blockersMu.Unlock()
	first := s.lastBlockers == nil
	changed := string(raw) != string(s.lastBlockers)
	seen := s.lastWaiting
	s.lastBlockers, s.lastWaiting = raw, waiting
	if first || !changed {
		return
	}
	keys := make([]string, 0, len(waiting))
	for k := range waiting {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, had := seen[k]; !had {
			s.feed.Publish("waiting_item", waiting[k])
		}
	}
	s.feed.Publish("blockers", list)
}

// WaitingEvent is the data of a waiting_item event: an item a super's footer
// added, sent once for each new text.
type WaitingEvent struct {
	Rig  string `json:"rig"`
	Pane string `json:"pane"`
	Text string `json:"text"`
}
