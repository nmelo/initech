// Package mcp provides an MCP (Model Context Protocol) server for initech,
// exposing agent primitives over streamable HTTP. External LLM clients connect
// to observe and control agents via standard MCP tools.
package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// PaneHandle is a minimal interface for interacting with a single pane.
// The MCP package uses this to avoid importing internal/tui directly.
type PaneHandle interface {
	Name() string
	PeekContent(lines int) string
	SendText(text string, enter bool)
}

// PaneHost provides pane lookup and lifecycle operations for MCP tool handlers.
type PaneHost interface {
	// FindPane returns the pane with the given name, or nil if not found.
	// The bool is false if the host is shutting down.
	FindPane(name string) (PaneHandle, bool)

	// RestartAgent restarts the named agent's process. Returns an error if the
	// agent is not found or the restart fails.
	RestartAgent(name string) error

	// StopAgent stops the named agent's process. Returns an error if the agent
	// is not found. Stopping an already-stopped agent is a no-op.
	StopAgent(name string) error

	// StartAgent starts a previously stopped agent. Returns an error if the
	// agent is not found or the start fails. Starting an already-running agent
	// is a no-op.
	StartAgent(name string) error
}

// Server is an MCP server that exposes initech agent primitives over
// streamable HTTP. It wraps the official go-sdk MCP server with bearer
// token authentication.
type Server struct {
	addr   string
	token  string
	srv    *http.Server
	logger *slog.Logger
}

// NewServer creates an MCP server bound to the given address and port.
// The token is required for bearer auth on every request. If port is 0,
// the OS assigns a free port. If bind is empty, defaults to "0.0.0.0".
// The host provides pane access for tool handlers.
func NewServer(port int, bind, token string, host PaneHost, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if bind == "" {
		bind = "0.0.0.0"
	}
	addr := fmt.Sprintf("%s:%d", bind, port)

	mcpServer := gomcp.NewServer(
		&gomcp.Implementation{
			Name:    "initech",
			Version: "1.0.0",
		},
		nil,
	)

	registerTools(mcpServer, host)

	handler := gomcp.NewStreamableHTTPHandler(
		func(r *http.Request) *gomcp.Server { return mcpServer },
		nil,
	)

	s := &Server{
		addr:   addr,
		token:  token,
		logger: logger,
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", s.requireBearerToken(handler))

	s.srv = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	return s
}

// Start begins listening and serving. It blocks until the server is shut down
// or a fatal listen error occurs. Returns nil on clean shutdown.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("mcp server listen: %w", err)
	}
	s.addr = ln.Addr().String()
	s.logger.Info("mcp server listening", "addr", s.addr)

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
// Start has been called.
func (s *Server) Addr() string {
	return s.addr
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// requireBearerToken is middleware that validates the Authorization header
// against the configured token. Returns 401 on missing or invalid tokens.
func (s *Server) requireBearerToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			http.Error(w, "authorization required", http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "bearer token required", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != s.token {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
