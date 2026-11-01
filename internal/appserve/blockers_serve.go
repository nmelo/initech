package appserve

import (
	"encoding/json"
	"net/http"
	"sort"
)

// snapshot is what Blockers reads, from what the service already holds: the
// last poll's rigs and panes and the latest progress run's beads. No reply
// is given, so no footer is read. It makes no bd or git call and asks no TUI.
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
			row.Panes = append(row.Panes, PaneSnapshot{Name: ps.Name, State: ps.State, Banner: ps.Banner})
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

// blockers is the list: Blockers over the snapshot for the stuck panes,
// with each rig's waiting entries the beads in waiting_on_nelson at the last
// bd read, not footer lines.
func (s *Server) blockers() []RigBlockers {
	list := Blockers(s.snapshot())
	s.mu.Lock()
	byName := map[string][]WaitingBead{}
	for _, rg := range s.rigs {
		byName[rg.Name] = s.waiting[rg.Root]
	}
	s.mu.Unlock()
	for i := range list {
		items := []WaitingItem{}
		for _, b := range byName[list[i].Rig] {
			items = append(items, WaitingItem{Pane: b.Assignee, Text: b.Title, Bead: b.ID})
		}
		list[i].Waiting = items
		list[i].Quiet = len(items) == 0 && len(list[i].Stuck) == 0
	}
	return list
}

// handleBlockers is GET /v1/blockers: the list as of the last pane and bd
// reads.
func (s *Server) handleBlockers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.blockers())
}

// publishBlockers sends a blockers event when the list changed since the
// last one. The first call sets the baseline and sends nothing. Beads
// entering waiting_on_nelson are announced by readWaiting, not here.
func (s *Server) publishBlockers() {
	list := s.blockers()
	raw, err := json.Marshal(list)
	if err != nil {
		return
	}
	s.blockersMu.Lock()
	defer s.blockersMu.Unlock()
	first := s.lastBlockers == nil
	changed := string(raw) != string(s.lastBlockers)
	s.lastBlockers = raw
	if first || !changed {
		return
	}
	s.feed.Publish("blockers", list)
}
