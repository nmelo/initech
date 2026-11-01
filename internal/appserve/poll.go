package appserve

import (
	"context"
	"sort"
	"time"
)

// PollEvery is how often the service reads every rig's pane list for the
// feed: the pane states initech status reports, no faster.
const PollEvery = 30 * time.Second

// PaneEvent is the data of a pane_state event: a pane's state or banner
// changed between two polls. From is "" for a pane that was not there.
type PaneEvent struct {
	Rig    string `json:"rig"`
	Pane   string `json:"pane"`
	From   string `json:"from"`
	To     string `json:"to"`
	Banner string `json:"banner"`
}

// StuckEvent is the data of a stuck event: a pane that cannot act on its
// own, at a dialog or banner (Reason is the banner kind) or waiting or
// pending (Reason is the state). Sent once when the pane gets stuck.
type StuckEvent struct {
	Rig    string `json:"rig"`
	Pane   string `json:"pane"`
	Reason string `json:"reason"`
}

// stuckReason is why ps cannot act on its own, "" when it can.
func stuckReason(ps PaneState) string {
	if ps.Banner != "" && ps.Banner != "none" {
		return ps.Banner
	}
	if ps.State == "waiting" || ps.State == "pending" {
		return ps.State
	}
	return ""
}

// ScanStuckNotice is what OnStuck is told. It names no pane content.
const ScanStuckNotice = "initech app-serve cannot finish reading its rig folders; run under launchd, it may need macOS permission for them"

// Run polls every rig every PollEvery until ctx ends, publishing what
// changed on the feed. The first poll runs at once. From here on requests
// read the poll's results instead of scanning, so a poll that blocks holds no
// request; it is reported through OnStuck.
func (s *Server) Run(ctx context.Context) {
	s.mu.Lock()
	s.polling = true
	s.mu.Unlock()
	for {
		done := make(chan struct{})
		go func() {
			s.Poll()
			close(done)
		}()
		stuck := time.NewTimer(s.cfg.StuckAfter)
		select {
		case <-done:
		case <-stuck.C:
			if s.cfg.OnStuck != nil {
				s.cfg.OnStuck(ScanStuckNotice)
			}
			select {
			case <-done:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			stuck.Stop()
			return
		}
		stuck.Stop()
		select {
		case <-ctx.Done():
			return
		case <-s.cfg.After(PollEvery):
		}
	}
}

// Poll reads every rig's pane list once (the TUI's list, no bd or git),
// records which rigs are running, and publishes a pane_state event for each
// pane whose state or banner changed since the last poll and a stuck event
// for each that became stuck. A rig seen for the first time sets the
// baseline and publishes nothing.
func (s *Server) Poll() {
	var running []Rig
	seen := map[string]map[string]PaneState{}
	found := s.cfg.Backend.FindRigs()
	for _, rg := range found {
		panes, err := s.cfg.Backend.Panes(rg)
		if err != nil {
			continue
		}
		running = append(running, rg)
		cur := make(map[string]PaneState, len(panes))
		for _, ps := range panes {
			cur[ps.Name] = ps
		}
		seen[rg.Root] = cur
		s.mu.Lock()
		prev, known := s.panes[rg.Root]
		s.mu.Unlock()
		if !known {
			continue
		}
		for _, ps := range panes {
			old, had := prev[ps.Name]
			if had && old.State == ps.State && old.Banner == ps.Banner {
				continue
			}
			s.feed.Publish("pane_state", PaneEvent{Rig: rg.Name, Pane: ps.Name, From: old.State, To: ps.State, Banner: ps.Banner})
			if r := stuckReason(ps); r != "" && (!had || stuckReason(old) == "") {
				s.feed.Publish("stuck", StuckEvent{Rig: rg.Name, Pane: ps.Name, Reason: r})
			}
		}
	}
	// All at once: a request must not see the poll finished with the running
	// list still the old one.
	sort.Slice(running, func(i, j int) bool { return running[i].Name < running[j].Name })
	s.mu.Lock()
	first := !s.polled
	s.panes, s.found, s.polled = seen, found, true
	s.rigs, s.rigsAt = running, s.cfg.Now()
	s.mu.Unlock()
	if first {
		close(s.firstPoll)
	}
	s.publishBlockers()
}
