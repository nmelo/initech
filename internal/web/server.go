package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
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
}

// LayoutInfo describes the current TUI layout.
type LayoutInfo struct {
	Mode    string `json:"mode"`    // "focus", "grid", or "2col"
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
	BeadID   string `json:"bead_id,omitempty"`
	Order    int    `json:"order"`
}

// StateProvider returns the current TUI state snapshot. Called on a timer
// by the state broadcaster.
type StateProvider interface {
	CurrentState() (StateSnapshot, bool)
}

// Server is an HTTP server that serves the SPA and pane API.
type Server struct {
	addr          string
	srv           *http.Server
	logger        *slog.Logger
	lister        PaneLister
	subscriber    PaneSubscriber // Optional: enables /ws/pane/{name} endpoint.
	stateProvider StateProvider  // Optional: enables /ws/state endpoint.
	subIDSeq      atomic.Uint64  // Monotonic counter for generating unique subscriber IDs.
}

// NewServer creates a Server bound to 0.0.0.0 on the given port, accessible
// from other machines on the network. The operator explicitly enables this
// with --web-port, so we bind all interfaces by default.
// If port is 0, the OS assigns a free port. The subscriber parameter is
// optional; if nil, the /ws/pane/{name} endpoint returns 501.
func NewServer(port int, lister PaneLister, subscriber PaneSubscriber, stateProvider StateProvider, logger *slog.Logger) *Server {
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
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/panes", s.handlePanes)
	mux.HandleFunc("GET /ws/pane/{name}", s.handlePaneWS)
	mux.HandleFunc("GET /ws/state", s.handleStateWS)

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
	s.addr = ln.Addr().String() // capture actual address (port 0 case)
	s.logger.Info("web server listening", "addr", s.addr)

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
// Start has been called (and the listener is active).
func (s *Server) Addr() string {
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

// handlePaneWS upgrades an HTTP request to a WebSocket connection, subscribes
// to the named pane's PTY byte fan-out, and relays bytes to the browser as
// binary WebSocket messages. The connection is closed when the client
// disconnects or the pane's subscriber channel is closed.
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

	ctx := conn.CloseRead(r.Context())

	for {
		select {
		case data, open := <-ch:
			if !open {
				conn.Close(websocket.StatusGoingAway, "pane closed")
				return
			}
			err := conn.Write(ctx, websocket.MessageBinary, data)
			if err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

const stateDebounceInterval = 500 * time.Millisecond

// handleStateWS upgrades to WebSocket, sends the initial state snapshot, then
// pushes updates every 500ms when state changes. Uses snapshot comparison to
// avoid sending duplicate frames.
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

	// Send initial snapshot.
	snap, ok := s.stateProvider.CurrentState()
	if !ok {
		conn.Close(websocket.StatusGoingAway, "shutting down")
		return
	}
	lastJSON, _ := json.Marshal(snap)
	if err := conn.Write(ctx, websocket.MessageText, lastJSON); err != nil {
		return
	}

	// Poll for changes on a 500ms ticker.
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
			newJSON, _ := json.Marshal(snap)
			// Skip send if snapshot hasn't changed.
			if string(newJSON) == string(lastJSON) {
				continue
			}
			lastJSON = newJSON
			if err := conn.Write(ctx, websocket.MessageText, newJSON); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
