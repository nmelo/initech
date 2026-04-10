// Package mcp provides an MCP (Model Context Protocol) server for initech,
// exposing agent primitives over streamable HTTP. External LLM clients connect
// to observe and control agents via standard MCP tools.
package mcp

import (
	"context"
	"encoding/json"
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
	Activity() string
	IsAlive() bool
	IsVisible() bool
	BeadID() string
	MemoryRSSKB() int64
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

	// AddAgent hot-adds a new agent pane with the given role name.
	AddAgent(name string) error

	// RemoveAgent removes the named agent pane. Returns an error if the
	// pane is not found or is the last remaining pane.
	RemoveAgent(name string) error

	// InterruptAgent sends a raw control byte to the named agent's PTY.
	// hard=true sends Ctrl+C (0x03), false sends Escape (0x1B).
	InterruptAgent(name string, hard bool) error

	// ScheduleSend schedules a message to be sent to an agent after a delay.
	// The delay is a Go duration string (e.g. "5m", "30s"). Returns the
	// timer ID on success.
	ScheduleSend(agent, message, delay string) (string, error)

	// AllPanes returns all panes. The bool is false if the host is shutting down.
	AllPanes() ([]PaneHandle, bool)

	// NotifyConfig returns the webhook URL and project name for posting
	// notifications. Returns empty strings if not configured.
	NotifyConfig() (webhookURL, project string)

	// AnnounceConfig returns the Agent Radio announce URL and project name.
	// Returns empty strings if not configured.
	AnnounceConfig() (announceURL, project string)

	// SetBead registers a bead ID on a pane for display in the TUI overlay.
	SetBead(agent, beadID string) error
}

// Server is an MCP server that exposes initech agent primitives over
// streamable HTTP. It wraps the official go-sdk MCP server with bearer
// token authentication.
type Server struct {
	addr      string
	token     string
	srv       *http.Server
	logger    *slog.Logger
	mcpServer *gomcp.Server          // Underlying MCP server for resource notifications.
	host      PaneHost               // Pane access for resource re-registration on hot-add.
	tracker   *subscriptionTracker   // Tracks subscriptions and debounces output notifications.
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

	var tracker *subscriptionTracker

	opts := &gomcp.ServerOptions{
		Logger: logger,
	}

	// Pre-create tracker so subscribe/unsubscribe handlers can reference it.
	// The mcpServer pointer is set after creation.
	tracker = &subscriptionTracker{
		subs:       make(map[string]int),
		dirtyPanes: make(map[string]bool),
		stopCh:     make(chan struct{}),
	}

	opts.SubscribeHandler = tracker.Subscribe
	opts.UnsubscribeHandler = tracker.Unsubscribe

	mcpServer := gomcp.NewServer(
		&gomcp.Implementation{
			Name:    "initech",
			Version: "1.0.0",
		},
		opts,
	)
	tracker.mcpServer = mcpServer

	if host != nil {
		registerTools(mcpServer, host)
		registerResources(mcpServer, host)
	}

	tracker.StartDebounce()

	handler := gomcp.NewStreamableHTTPHandler(
		func(r *http.Request) *gomcp.Server { return mcpServer },
		&gomcp.StreamableHTTPOptions{
			// Disable DNS rebinding protection so the server accepts requests
			// from reverse proxies (Tailscale serve, nginx) where the Host
			// header differs from localhost. Bearer token auth is the access
			// control, not host validation.
			DisableLocalhostProtection: true,
		},
	)

	s := &Server{
		addr:      addr,
		token:     token,
		logger:    logger,
		mcpServer: mcpServer,
		host:      host,
		tracker:   tracker,
	}

	// OAuth Protected Resource Metadata (RFC 9728). Tells MCP clients that
	// this server uses bearer tokens with no OAuth authorization server.
	// The resource URL is derived from the incoming request so it matches
	// what the client used to connect (handles reverse proxies, Tailscale
	// serve, etc.). A static URL would break behind any proxy.
	prmHandler := dynamicProtectedResourceHandler()

	mux := http.NewServeMux()
	mux.Handle("/mcp", s.requireBearerToken(handler))
	mux.Handle("/.well-known/oauth-protected-resource", prmHandler)
	mux.Handle("/.well-known/oauth-protected-resource/", prmHandler)

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

// Shutdown gracefully stops the server and the debounce goroutine.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.tracker != nil {
		s.tracker.Stop()
	}
	return s.srv.Shutdown(ctx)
}

// NotifyResourcesChanged triggers a resources/list_changed notification to all
// connected MCP clients. Call this when panes are hot-added or removed so
// clients re-fetch the agent status resource.
func (s *Server) NotifyResourcesChanged() {
	if s.mcpServer == nil || s.host == nil {
		return
	}
	// Re-add the resource with the same handler. AddResource replaces the
	// existing entry and fires the list_changed notification to all sessions.
	registerResources(s.mcpServer, s.host)
}

// MarkPaneDirty signals that a pane's terminal output has changed. The
// notification is debounced (1 per second per pane) to avoid flooding clients.
// No-op if no clients are subscribed to the pane's output resource.
func (s *Server) MarkPaneDirty(name string) {
	if s.tracker != nil {
		s.tracker.MarkPaneDirty(name)
	}
}

// NotifyAgentStateChanged signals that an agent's state has changed (activity
// toggle, bead claimed/cleared, etc.). Fires notifications for both the fleet
// status resource and the per-agent status resource.
func (s *Server) NotifyAgentStateChanged(name string) {
	if s.tracker != nil {
		s.tracker.NotifyAgentStateChanged(name)
	}
}

// dynamicProtectedResourceHandler returns an HTTP handler that serves RFC 9728
// Protected Resource Metadata with the resource URL derived from the incoming
// request. This ensures the resource field matches what the client used to
// connect, which is critical behind reverse proxies (Tailscale serve, nginx).
func dynamicProtectedResourceHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			scheme = proto
		}

		resource := fmt.Sprintf("%s://%s", scheme, r.Host)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"resource":                 resource,
			"authorization_servers":    []string{},
			"bearer_methods_supported": []string{"header"},
		})
	})
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
