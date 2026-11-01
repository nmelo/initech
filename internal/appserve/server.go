package appserve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nmelo/initech/internal/progress"
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
	// SessionFile is the pane's newest transcript, "" for none. Not part of
	// what the feed compares: a pane's state is its state and banner.
	SessionFile string
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
	// TokenDir is the folder holding the token digest, read on every
	// request, so a token issued while the service runs takes effect at once
	// and revokes the one before; no digest there refuses every request.
	// Empty: Digest and HasToken stand instead.
	TokenDir string
	// Digest is the token's SHA-256; a request carries the token itself.
	// With HasToken false every request is refused.
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
	// StuckAfter is how long a rig scan runs before OnStuck is told;
	// DefaultStuckAfter when zero. OnStuck runs once per scan that sticks.
	StuckAfter time.Duration
	OnStuck    func(string)
}

// DefaultCacheFor is the status cache window.
const DefaultCacheFor = 5 * time.Second

// DefaultStuckAfter is how long a rig scan runs before it is reported stuck.
const DefaultStuckAfter = 30 * time.Second

// ScreenLines is how many lines of a pane's screen ride with its status
// while it shows a dialog, a banner or a prompt.
const ScreenLines = 20

// Server is initech app-serve's HTTP service.
type Server struct {
	cfg Config
	mux *http.ServeMux

	feed    *Feed
	pinsMu  sync.Mutex // one PUT /v1/pins at a time
	devices *Devices   // nil when the state folder's devices file cannot be read

	mu       sync.Mutex
	rigs     []Rig
	rigsAt   time.Time
	statuses map[string]cachedStatus
	panes    map[string]map[string]PaneState // by rig root, then pane: the last poll
	found    []Rig                           // every rig the last poll's scan found, running or not
	polling  bool                            // Run is going: requests read its results and never scan
	polled   bool                            // a poll has finished

	progress      []progress.Report         // the latest progress run
	progressAsked map[string]cachedProgress // reports read on request, by rig root and prefix

	tails map[string]tailPos // by rig root and pane; only RunTail's goroutine touches it

	blockersMu   sync.Mutex
	lastBlockers []byte                  // the list last compared, as JSON; nil before the first
	lastWaiting  map[string]WaitingEvent // the waiting items on it
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
	if cfg.StuckAfter <= 0 {
		cfg.StuckAfter = DefaultStuckAfter
	}
	s := &Server{cfg: cfg, mux: http.NewServeMux(), statuses: map[string]cachedStatus{}, progressAsked: map[string]cachedProgress{}, tails: map[string]tailPos{}, feed: NewFeed(cfg.Now)}
	if cfg.StateDir != "" {
		if d, err := LoadDevices(cfg.StateDir, cfg.Now); err == nil {
			s.devices = d
		} else if cfg.OnStuck != nil {
			cfg.OnStuck("initech app-serve: devices not read, no pushes: " + err.Error())
		}
	}
	s.mux.HandleFunc("GET /v1/rigs", s.handleRigs)
	s.mux.HandleFunc("GET /v1/status", s.handleStatus)
	s.mux.HandleFunc("GET /v1/status/{rig}", s.handleRigStatus)
	s.mux.HandleFunc("GET /v1/conversation/{rig}/{pane}", s.handleConversation)
	s.mux.HandleFunc("POST /v1/keys/{rig}/{pane}", s.handleKeys)
	s.mux.HandleFunc("POST /v1/send/{rig}/{pane}", s.handleSend)
	s.mux.HandleFunc("GET /v1/blockers", s.handleBlockers)
	s.mux.HandleFunc("GET /v1/progress", s.handleProgress)
	s.mux.HandleFunc("GET /v1/progress/{rig}", s.handleRigProgress)
	s.mux.HandleFunc("PUT /v1/devices", s.handlePutDevice)
	s.mux.HandleFunc("DELETE /v1/devices/{token}", s.handleDeleteDevice)
	s.mux.HandleFunc("GET /v1/pins", s.handleGetPins)
	s.mux.HandleFunc("PUT /v1/pins", s.handlePutPins)
	s.mux.HandleFunc("GET /v1/feed", func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		serveFeed(s.feed, w, r, func() bool {
			digest, ok := s.digest()
			return ok && tokenMatches(header, digest)
		})
	})
	return s
}

// Handler is the service with the token gate in front of every route.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		digest, ok := s.digest()
		if !ok || !tokenMatches(r.Header.Get("Authorization"), digest) {
			// No body, no hint: a wrong token and a missing one look alike.
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

// digest is the token digest to check a request against; false when there
// is none or it cannot be read, which refuses the request.
func (s *Server) digest() ([32]byte, bool) {
	if s.cfg.TokenDir == "" {
		return s.cfg.Digest, s.cfg.HasToken
	}
	d, err := LoadDigest(s.cfg.TokenDir)
	return d, err == nil
}

// Feed is the service's change feed.
func (s *Server) Feed() *Feed { return s.feed }

// notYetRead reports that the poll loop is going and no poll has finished,
// so there is no answer yet, and says so on w: a scan that blocks (macOS
// guarding a folder, say) must not hang the requests behind it.
func (s *Server) notYetRead(w http.ResponseWriter) bool {
	s.mu.Lock()
	waiting := s.polling && !s.polled
	s.mu.Unlock()
	if waiting {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the rig scan has not finished"})
	}
	return waiting
}

// running is the rigs whose TUI answers: the poll's result while the poll
// loop is going, else read again once the cache window has passed.
func (s *Server) running() []Rig {
	s.mu.Lock()
	if s.polling {
		rigs := s.rigs
		s.mu.Unlock()
		return rigs
	}
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
	if s.notYetRead(w) {
		return
	}
	up := map[string]bool{}
	for _, rg := range s.running() {
		up[rg.Root] = true
	}
	s.mu.Lock()
	found, polling := s.found, s.polling
	s.mu.Unlock()
	if !polling {
		found = s.cfg.Backend.FindRigs()
	}
	out := []RigEntry{}
	for _, rg := range found {
		out = append(out, RigEntry{Rig: rg.Name, Root: rg.Root, Running: up[rg.Root]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rig < out[j].Rig })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if s.notYetRead(w) {
		return
	}
	out := []RigStatus{}
	for _, rg := range s.running() {
		out = append(out, s.status(rg))
	}
	writeJSON(w, http.StatusOK, map[string]any{"rigs": out})
}

func (s *Server) handleRigStatus(w http.ResponseWriter, r *http.Request) {
	if s.notYetRead(w) {
		return
	}
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

// MaxPageLimit caps a conversation page's limit.
const MaxPageLimit = 200

// handleConversation is one page of a pane's conversation: the newest one,
// or the one before ?before=<cursor>, with ?limit=<n> (default 50). A pane
// with no transcript is an empty page at the start.
func (s *Server) handleConversation(w http.ResponseWriter, r *http.Request) {
	if s.notYetRead(w) {
		return
	}
	rigName, paneName := r.PathValue("rig"), r.PathValue("pane")
	q := r.URL.Query()
	before, limit := int64(0), 50
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad before"})
			return
		}
		before = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxPageLimit {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("limit is 1 to %d", MaxPageLimit)})
			return
		}
		limit = n
	}
	for _, rg := range s.running() {
		if rg.Name != rigName {
			continue
		}
		panes, err := s.cfg.Backend.Panes(rg)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no running rig " + rigName})
			return
		}
		for _, ps := range panes {
			if ps.Name != paneName {
				continue
			}
			if ps.SessionFile == "" {
				writeJSON(w, http.StatusOK, Page{Messages: []Message{}, StartReached: true})
				return
			}
			pg, err := ReadPage(ps.SessionFile, before, limit)
			if err != nil {
				writeJSON(w, http.StatusOK, Page{Messages: []Message{}, StartReached: true})
				return
			}
			writeJSON(w, http.StatusOK, pg)
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no pane " + paneName + " in " + rigName})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no running rig " + rigName})
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
