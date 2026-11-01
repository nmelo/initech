package appserve

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/nmelo/initech/internal/progress"
)

// ProgressEvery is how often the service reads progress, per rig, for the
// efforts with a bead in progress.
const ProgressEvery = 5 * time.Minute

// ProgressReader is a backend that can read progress; cmd implements it over
// the code initech progress runs. These are the service's only bd and git
// calls on a timer.
type ProgressReader interface {
	// ActiveEfforts is the bead prefixes (progress.Effort) of r's beads past
	// open and not closed.
	ActiveEfforts(r Rig) ([]string, error)
	// Progress is initech progress --json for r and prefix.
	Progress(r Rig, prefix string) (progress.Report, error)
}

// RunProgress reads progress every ProgressEvery until ctx ends; the first
// read runs at once.
func (s *Server) RunProgress(ctx context.Context) {
	pr, ok := s.cfg.Backend.(ProgressReader)
	if !ok {
		return
	}
	for {
		s.readProgress(pr)
		select {
		case <-ctx.Done():
			return
		case <-s.cfg.After(ProgressEvery):
		}
	}
}

// readProgress replaces the latest run: for every running rig, one report per
// active effort. A rig with no active effort has none; an effort that fails
// to read keeps nothing from the run before, since a stale report would read
// as current.
func (s *Server) readProgress(pr ProgressReader) {
	latest := []progress.Report{}
	for _, rg := range s.cfg.Backend.FindRigs() {
		if _, err := s.cfg.Backend.Panes(rg); err != nil {
			continue
		}
		efforts, err := pr.ActiveEfforts(rg)
		if err != nil {
			continue
		}
		sort.Strings(efforts)
		for _, prefix := range efforts {
			if rep, err := pr.Progress(rg, prefix); err == nil {
				latest = append(latest, rep)
			}
		}
	}
	s.mu.Lock()
	s.progress = latest
	s.mu.Unlock()
}

// handleProgress is GET /v1/progress: the latest run, one report per rig and
// active effort. It never starts a read.
func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	out := s.progress
	s.mu.Unlock()
	if out == nil {
		out = []progress.Report{}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRigProgress is GET /v1/progress/{rig}?prefix=<p>: the rig's reports
// in the latest run, or the one for prefix. A prefix the run did not read is
// read on request, at most once per cache window.
func (s *Server) handleRigProgress(w http.ResponseWriter, r *http.Request) {
	name, prefix := r.PathValue("rig"), r.URL.Query().Get("prefix")
	var rig *Rig
	for _, rg := range s.running() {
		if rg.Name == name {
			c := rg
			rig = &c
		}
	}
	if rig == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no running rig " + name})
		return
	}
	s.mu.Lock()
	reports := append([]progress.Report{}, s.progress...)
	s.mu.Unlock()
	out := []progress.Report{}
	for _, rep := range reports {
		if rep.Rig == name && (prefix == "" || rep.Prefix == prefix) {
			out = append(out, rep)
		}
	}
	if prefix == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	if len(out) > 0 {
		writeJSON(w, http.StatusOK, out[0])
		return
	}
	pr, ok := s.cfg.Backend.(ProgressReader)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "progress is not read here"})
		return
	}
	key := rig.Root + "\x00" + prefix
	now := s.cfg.Now()
	s.mu.Lock()
	c, hit := s.progressAsked[key]
	s.mu.Unlock()
	if hit && now.Sub(c.at) < s.cfg.CacheFor {
		writeJSON(w, http.StatusOK, c.rep)
		return
	}
	rep, err := pr.Progress(*rig, prefix)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	s.mu.Lock()
	if len(s.progressAsked) >= maxProgressAsked {
		s.progressAsked = map[string]cachedProgress{}
	}
	s.progressAsked[key] = cachedProgress{at: now, rep: rep}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, rep)
}

// maxProgressAsked bounds the reports kept from on-request reads.
const maxProgressAsked = 64

type cachedProgress struct {
	at  time.Time
	rep progress.Report
}
