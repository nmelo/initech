package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
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

// Server is an HTTP server that serves the SPA and pane API.
type Server struct {
	addr   string
	srv    *http.Server
	logger *slog.Logger
	lister PaneLister
}

// NewServer creates a Server bound to 127.0.0.1 on the given port.
// If port is 0, the OS assigns a free port.
func NewServer(port int, lister PaneLister, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	s := &Server{
		addr:   addr,
		logger: logger,
		lister: lister,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/panes", s.handlePanes)

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
