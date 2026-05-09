package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// PaneInfo describes a single managed pane. Mirrors tui.PaneInfo so the web
// package does not import tui directly.
type PaneInfo struct {
	Name     string `json:"name"`
	Host     string `json:"host,omitempty"`
	Activity string `json:"activity"`
	Alive    bool   `json:"alive"`
	Visible  bool   `json:"visible"`
}

// PaneLister returns the current set of panes. The bool is false if the
// underlying system is shutting down and the data is unavailable.
type PaneLister interface {
	AllPanes() ([]PaneInfo, bool)
}

// PaneSubscriber provides fan-out subscription to a pane's PTY byte stream.
// The returned channel receives copies of all bytes read from the pane's PTY.
// Callers must call UnsubscribePane when done.
type PaneSubscriber interface {
	// SubscribePane registers a subscriber for the named pane's PTY output.
	// Returns a channel of byte slices and true if the pane exists, or
	// nil and false if the pane is not found.
	SubscribePane(paneName, subscriberID string) (chan []byte, bool)

	// UnsubscribePane removes a subscriber. Safe to call if the pane or
	// subscriber does not exist.
	UnsubscribePane(paneName, subscriberID string)
}

// StateSnapshot is the JSON payload sent over /ws/state. It combines layout
// information with per-agent status for the web companion SPA.
type StateSnapshot struct {
	Project string      `json:"project,omitempty"`
	Layout  LayoutInfo  `json:"layout"`
	Panes   []PaneState `json:"panes"`
	Live    *LiveInfo   `json:"live,omitempty"` // Non-nil only in live mode.
}

// LiveInfo describes live mode state: which agents are pinned and the current
// slot assignments. Sent only when layout mode is "live".
type LiveInfo struct {
	Pinned map[string]int `json:"pinned"` // Agent name -> slot index (0-based).
	Slots  []string       `json:"slots"`  // Current agent name per slot.
}

// LayoutInfo describes the current TUI layout.
type LayoutInfo struct {
	Mode    string `json:"mode"`    // "focus", "grid", "2col", or "live"
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
	Focused string `json:"focused"` // Pane name.
}

// PaneState describes one agent's state for the web companion.
type PaneState struct {
	Name     string `json:"name"`
	Activity string `json:"activity"`
	Alive    bool   `json:"alive"`
	Visible  bool   `json:"visible"`
	Pinned   bool   `json:"pinned,omitempty"` // True if pinned in live mode.
	BeadID   string `json:"bead_id,omitempty"`
	Order    int    `json:"order"`
	Cols     int    `json:"cols"`     // Terminal width in columns (from VT emulator).
	Rows     int    `json:"rows"`     // Terminal height in rows (from VT emulator).
}

// StateProvider returns the current TUI state snapshot. Called on a timer
// by the state broadcaster.
type StateProvider interface {
	CurrentState() (StateSnapshot, bool)
}

// PaneWriter writes raw bytes to a pane's PTY input. Used by the WebSocket
// handler to relay keyboard input from the browser to the agent terminal.
// Implementations must serialize writes with other PTY writers (e.g. IPC send).
type PaneWriter interface {
	// WriteToPTY writes data to the named pane's PTY master. Returns an error
	// if the pane is not found or dead. Safe for concurrent use.
	WriteToPTY(paneName string, data []byte) error
}

// PinToggler toggles the pinned state of a pane in live mode.
// Returns the new pinned state and true on success, or false if the pane
// is not found or live mode is not active.
type PinToggler interface {
	TogglePin(paneName string) (pinned bool, ok bool)
}

// AgentEventInfo is a serializable agent event for the web companion.
type AgentEventInfo struct {
	Kind   string `json:"kind"`
	Pane   string `json:"pane"`
	BeadID string `json:"bead_id,omitempty"`
	Detail string `json:"detail"`
	Time   string `json:"time"` // RFC 3339
}

// EventProvider provides event subscription for the /ws/state stream.
// Subscribe returns a channel that receives agent events. Unsubscribe
// removes the subscription and closes the channel.
type EventProvider interface {
	SubscribeEvents(id string) chan AgentEventInfo
	UnsubscribeEvents(id string)
}

// wsMessage is the envelope for all /ws/state messages.
type wsMessage struct {
	Type  string       `json:"type"`
	State *StateSnapshot `json:"state,omitempty"`
	Event *AgentEventInfo `json:"event,omitempty"`
}

// Server is an HTTP server that serves the SPA and pane API.
type Server struct {
	// addrMu protects addr against the read/write race between Start
	// (which overwrites addr with the actual bound address once the
	// listener is up — including resolving port 0) and Addr (called by
	// callers polling for readiness). Initialized by NewServer; rewritten
	// once during Start; read by Addr from any goroutine.
	addrMu        sync.Mutex
	addr          string
	srv           *http.Server
	logger        *slog.Logger
	lister        PaneLister
	subscriber    PaneSubscriber // Optional: enables /ws/pane/{name} endpoint.
	stateProvider StateProvider  // Optional: enables /ws/state endpoint.
	eventProvider EventProvider  // Optional: enables events on /ws/state.
	paneWriter    PaneWriter     // Optional: enables PTY input via /ws/pane/{name}. Nil = read-only.
	pinToggler    PinToggler     // Optional: enables POST /api/pin/{name}. Nil = 501.
	subIDSeq      atomic.Uint64  // Monotonic counter for generating unique subscriber IDs.
}

// NewServer creates a Server bound to 0.0.0.0 on the given port, accessible
// from other machines on the network. The operator explicitly enables this
// with --web-port, so we bind all interfaces by default.
// If port is 0, the OS assigns a free port. The subscriber parameter is
// optional; if nil, the /ws/pane/{name} endpoint returns 501.
func NewServer(port int, lister PaneLister, subscriber PaneSubscriber, stateProvider StateProvider, eventProvider EventProvider, paneWriter PaneWriter, pinToggler PinToggler, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	addr := fmt.Sprintf("0.0.0.0:%d", port)

	s := &Server{
		addr:          addr,
		logger:        logger,
		lister:        lister,
		subscriber:    subscriber,
		stateProvider: stateProvider,
		eventProvider: eventProvider,
		paneWriter:    paneWriter,
		pinToggler:    pinToggler,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/panes", s.handlePanes)
	mux.HandleFunc("GET /ws/pane/{name}", s.handlePaneWS)
	mux.HandleFunc("GET /ws/state", s.handleStateWS)
	mux.HandleFunc("POST /api/pin/{name}", s.handlePin)

	// Serve embedded SPA files. The embed.FS has a "static" prefix that we
	// strip so that requests to "/" resolve to static/index.html.
	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /", http.FileServer(http.FS(staticSub)))

	s.srv = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return s
}

// Start begins listening and serving. It blocks until the server is shut down
// or a fatal listen error occurs. The returned error is nil on clean shutdown
// (via Shutdown) and non-nil on listen failure.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("web server listen: %w", err)
	}
	bound := ln.Addr().String() // capture actual address (port 0 case)
	s.addrMu.Lock()
	s.addr = bound
	s.addrMu.Unlock()
	s.logger.Info("web server listening", "addr", bound)

	go func() {
		<-ctx.Done()
		s.srv.Shutdown(context.Background())
	}()

	err = s.srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Addr returns the address the server is bound to. Only meaningful after
// Start has been called (and the listener is active). Safe to call from
// any goroutine while Start is racing to bind — addrMu serializes the
// initial-config read with Start's bound-address write.
func (s *Server) Addr() string {
	s.addrMu.Lock()
	defer s.addrMu.Unlock()
	return s.addr
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) handlePanes(w http.ResponseWriter, r *http.Request) {
	panes, ok := s.lister.AllPanes()
	if !ok {
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(panes)
}

// paneInputRateLimit is the maximum bytes per second accepted from a single
// WebSocket client for PTY input. Excess bytes are silently dropped.
const paneInputRateLimit = 64 * 1024

// handlePaneWS upgrades an HTTP request to a WebSocket connection and runs
// bidirectional relay: PTY output -> browser (write goroutine) and browser
// keyboard input -> PTY (read goroutine). Either goroutine returning cancels
// the other via shared context.
func (s *Server) handlePaneWS(w http.ResponseWriter, r *http.Request) {
	if s.subscriber == nil {
		http.Error(w, "streaming not available", http.StatusNotImplemented)
		return
	}

	paneName := r.PathValue("name")
	if paneName == "" {
		http.Error(w, "pane name required", http.StatusBadRequest)
		return
	}

	subID := fmt.Sprintf("ws-%d", s.subIDSeq.Add(1))
	ch, ok := s.subscriber.SubscribePane(paneName, subID)
	if !ok {
		http.Error(w, "pane not found", http.StatusNotFound)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Allow any origin. The server binds to all interfaces and is
		// explicitly enabled by the operator via --web-port.
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.subscriber.UnsubscribePane(paneName, subID)
		s.logger.Error("websocket accept failed", "pane", paneName, "err", err)
		return
	}
	defer func() {
		s.subscriber.UnsubscribePane(paneName, subID)
		conn.CloseNow()
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Read goroutine: browser -> PTY.
	go func() {
		defer cancel()
		s.paneReadLoop(ctx, conn, paneName)
	}()

	// Write loop: PTY -> browser (runs on this goroutine).
	for {
		select {
		case data, open := <-ch:
			if !open {
				conn.Close(websocket.StatusGoingAway, "pane closed")
				return
			}
			if err := conn.Write(ctx, websocket.MessageBinary, data); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// paneReadLoop reads WebSocket messages from the browser and writes them to the
// pane's PTY via PaneWriter. Rate-limited to paneInputRateLimit bytes/sec.
// Returns when the connection closes or the context is cancelled.
func (s *Server) paneReadLoop(ctx context.Context, conn *websocket.Conn, paneName string) {
	var bytesThisWindow int
	windowStart := time.Now()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}

		// Rate limit: reset window every second, drop excess.
		now := time.Now()
		if now.Sub(windowStart) >= time.Second {
			bytesThisWindow = 0
			windowStart = now
		}
		if bytesThisWindow+len(data) > paneInputRateLimit {
			continue // Drop silently.
		}
		bytesThisWindow += len(data)

		// If no writer configured (read-only mode), discard.
		if s.paneWriter == nil {
			continue
		}

		if err := s.paneWriter.WriteToPTY(paneName, data); err != nil {
			s.logger.Debug("PTY write failed", "pane", paneName, "err", err)
			// Don't close the connection; the pane may restart.
		}
	}
}

const stateDebounceInterval = 500 * time.Millisecond

// handleStateWS upgrades to WebSocket, sends the initial state snapshot, then
// pushes state updates every 500ms when state changes and event messages
// immediately when agent events fire. Both message types use a discriminated
// envelope: {"type":"state",...} or {"type":"event",...}.
func (s *Server) handleStateWS(w http.ResponseWriter, r *http.Request) {
	if s.stateProvider == nil {
		http.Error(w, "state streaming not available", http.StatusNotImplemented)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.logger.Error("websocket accept failed", "endpoint", "/ws/state", "err", err)
		return
	}
	defer conn.CloseNow()

	ctx := conn.CloseRead(r.Context())

	// Subscribe to events if available.
	subID := fmt.Sprintf("ws-state-%d", s.subIDSeq.Add(1))
	var eventCh chan AgentEventInfo
	if s.eventProvider != nil {
		eventCh = s.eventProvider.SubscribeEvents(subID)
		defer s.eventProvider.UnsubscribeEvents(subID)
	}

	// Send initial state snapshot.
	snap, ok := s.stateProvider.CurrentState()
	if !ok {
		conn.Close(websocket.StatusGoingAway, "shutting down")
		return
	}
	msg := wsMessage{Type: "state", State: &snap}
	lastStateJSON, _ := json.Marshal(snap)
	initJSON, _ := json.Marshal(msg)
	if err := conn.Write(ctx, websocket.MessageText, initJSON); err != nil {
		return
	}

	// Poll for state changes on a 500ms ticker, push events immediately.
	ticker := time.NewTicker(stateDebounceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			snap, ok := s.stateProvider.CurrentState()
			if !ok {
				conn.Close(websocket.StatusGoingAway, "shutting down")
				return
			}
			newStateJSON, _ := json.Marshal(snap)
			if string(newStateJSON) == string(lastStateJSON) {
				continue
			}
			lastStateJSON = newStateJSON
			msg := wsMessage{Type: "state", State: &snap}
			data, _ := json.Marshal(msg)
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}

		case evt, open := <-eventCh:
			if !open {
				return
			}
			msg := wsMessage{Type: "event", Event: &evt}
			data, _ := json.Marshal(msg)
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}

		case <-ctx.Done():
			return
		}
	}
}

// pinResponse is the JSON response from POST /api/pin/{name}.
type pinResponse struct {
	Name   string `json:"name"`
	Pinned bool   `json:"pinned"`
}

// handlePin toggles a pane's pinned state in live mode.
func (s *Server) handlePin(w http.ResponseWriter, r *http.Request) {
	if s.pinToggler == nil {
		http.Error(w, "pin not available", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "pane name required", http.StatusBadRequest)
		return
	}
	pinned, ok := s.pinToggler.TogglePin(name)
	if !ok {
		http.Error(w, "pane not found or live mode not active", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pinResponse{Name: name, Pinned: pinned})
}
