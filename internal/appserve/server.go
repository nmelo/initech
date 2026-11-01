package appserve

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Rig is one initech rig the scan found: its project name, root and IPC
// socket.
type Rig struct {
	Name   string `json:"rig"`
	Root   string `json:"root"`
	Socket string `json:"-"`
}

// PaneState is one pane as the TUI's pane list gives it, with no bd or git
// read: what the poll and the feed watch.
type PaneState struct {
	Name   string
	State  string // running, idle, waiting, pending, background, suspended, stopped
	Banner string // as status --json prints it; "" when none was read
}

// Backend is what the service reads from initech; cmd implements it over
// the same code initech status, send and progress run.
type Backend interface {
	// FindRigs is every rig the scan folders hold, running or not: a glob
	// and a config read, no bd or git.
	FindRigs() []Rig
	// Panes is the TUI's pane list for r over its socket, no bd or git; an
	// error means no TUI answers there.
	Panes(r Rig) ([]PaneState, error)
	// Status is initech status --json's objects for r, bd's bead answer
	// included.
	Status(r Rig) ([]map[string]any, error)
	// Screen is the last lines of a pane's screen, as initech peek reads
	// them.
	Screen(r Rig, pane string, lines int) ([]string, error)
}

// Config is the service's setup.
type Config struct {
	Backend Backend
	// Digest is the stored token's SHA-256; a request carries the token
	// itself. With HasToken false every request is refused.
	Digest   [32]byte
	HasToken bool
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// CacheFor is how long a status read is served before it is read again.
	CacheFor time.Duration
	// After is the poll's timer; time.After when nil.
	After func(time.Duration) <-chan time.Time
	// StateDir is the service's state folder (token digest, certificate,
	// pinned list).
	StateDir string
}

// DefaultCacheFor is the status cache window.
const DefaultCacheFor = 5 * time.Second

// ScreenLines is how many lines of a pane's screen ride with its status
// while it shows a dialog, a banner or a prompt.
const ScreenLines = 20

// Server is initech app-serve's HTTP service.
type Server struct {
	cfg Config
	mux *http.ServeMux

	feed   *Feed
	pinsMu sync.Mutex // one PUT /v1/pins at a time

	mu       sync.Mutex
	rigs     []Rig
	rigsAt   time.Time
	statuses map[string]cachedStatus
	panes    map[string]map[string]PaneState // by rig root, then pane: the last poll
}

type cachedStatus struct {
	at  time.Time
	rig RigStatus
}

// RigStatus is one rig in GET /v1/status.
type RigStatus struct {
	Rig    string           `json:"rig"`
	Root   string           `json:"root"`
	ReadAt time.Time        `json:"read_at"`
	Agents []map[string]any `json:"agents"`
	Error  string           `json:"error,omitempty"`
}

// RigEntry is one rig in GET /v1/rigs.
type RigEntry struct {
	Rig     string `json:"rig"`
	Root    string `json:"root"`
	Running bool   `json:"running"`
}

// New is a server over cfg.
func New(cfg Config) *Server {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.CacheFor <= 0 {
		cfg.CacheFor = DefaultCacheFor
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	s := &Server{cfg: cfg, mux: http.NewServeMux(), statuses: map[string]cachedStatus{}, feed: NewFeed(cfg.Now)}
	s.mux.HandleFunc("GET /v1/rigs", s.handleRigs)
	s.mux.HandleFunc("GET /v1/status", s.handleStatus)
	s.mux.HandleFunc("GET /v1/status/{rig}", s.handleRigStatus)
	s.mux.HandleFunc("GET /v1/pins", s.handleGetPins)
	s.mux.HandleFunc("PUT /v1/pins", s.handlePutPins)
	s.mux.HandleFunc("GET /v1/feed", func(w http.ResponseWriter, r *http.Request) { serveFeed(s.feed, w, r) })
	return s
}

// Handler is the service with the token gate in front of every route.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.HasToken || !tokenMatches(r.Header.Get("Authorization"), s.cfg.Digest) {
			// No body, no hint: a wrong token and a missing one look alike.
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

// Feed is the service's change feed.
func (s *Server) Feed() *Feed { return s.feed }

// running is the rigs whose TUI answers, read again once the cache window
// has passed.
func (s *Server) running() []Rig {
	s.mu.Lock()
	if !s.rigsAt.IsZero() && s.cfg.Now().Sub(s.rigsAt) < s.cfg.CacheFor {
		rigs := s.rigs
		s.mu.Unlock()
		return rigs
	}
	s.mu.Unlock()
	var rigs []Rig
	for _, rg := range s.cfg.Backend.FindRigs() {
		if _, err := s.cfg.Backend.Panes(rg); err == nil {
			rigs = append(rigs, rg)
		}
	}
	s.setRunning(rigs)
	return rigs
}

func (s *Server) setRunning(rigs []Rig) {
	sort.Slice(rigs, func(i, j int) bool { return rigs[i].Name < rigs[j].Name })
	s.mu.Lock()
	s.rigs, s.rigsAt = rigs, s.cfg.Now()
	s.mu.Unlock()
}

func (s *Server) handleRigs(w http.ResponseWriter, r *http.Request) {
	up := map[string]bool{}
	for _, rg := range s.running() {
		up[rg.Root] = true
	}
	out := []RigEntry{}
	for _, rg := range s.cfg.Backend.FindRigs() {
		out = append(out, RigEntry{Rig: rg.Name, Root: rg.Root, Running: up[rg.Root]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rig < out[j].Rig })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	out := []RigStatus{}
	for _, rg := range s.running() {
		out = append(out, s.status(rg))
	}
	writeJSON(w, http.StatusOK, map[string]any{"rigs": out})
}

func (s *Server) handleRigStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("rig")
	for _, rg := range s.running() {
		if rg.Name == name {
			writeJSON(w, http.StatusOK, s.status(rg))
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no running rig " + name})
}

// status is rg's status, read through the backend at most once per cache
// window, each agent carrying its screen lines while it shows a dialog, a
// banner or a prompt.
func (s *Server) status(rg Rig) RigStatus {
	now := s.cfg.Now()
	s.mu.Lock()
	if c, ok := s.statuses[rg.Root]; ok && now.Sub(c.at) < s.cfg.CacheFor {
		s.mu.Unlock()
		return c.rig
	}
	s.mu.Unlock()
	st := RigStatus{Rig: rg.Name, Root: rg.Root, ReadAt: now, Agents: []map[string]any{}}
	agents, err := s.cfg.Backend.Status(rg)
	if err != nil {
		st.Error = err.Error()
	} else {
		for _, a := range agents {
			a["screen"] = nil
			if needsScreen(a) {
				if role, _ := a["role"].(string); role != "" {
					if lines, err := s.cfg.Backend.Screen(rg, role, ScreenLines); err == nil {
						a["screen"] = lines
					}
				}
			}
			st.Agents = append(st.Agents, a)
		}
	}
	s.mu.Lock()
	s.statuses[rg.Root] = cachedStatus{at: now, rig: st}
	s.mu.Unlock()
	return st
}

// needsScreen reports an agent whose status says a dialog or banner is open
// (banner set and not "none") or that waits at a prompt or on input.
func needsScreen(a map[string]any) bool {
	if b, ok := a["banner"].(string); ok && b != "" && b != "none" {
		return true
	}
	state, _ := a["state"].(string)
	return state == "waiting" || state == "pending"
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

// SplitScreen is screen text as lines, trailing blank lines dropped.
func SplitScreen(text string) []string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if lines == nil {
		lines = []string{}
	}
	return lines
}
