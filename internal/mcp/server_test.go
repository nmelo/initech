package mcp

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

const testToken = "test-token-abc123"

func startTestServer(t *testing.T) (*Server, context.CancelFunc) {
	t.Helper()
	srv := NewServer(0, testToken, nil)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	// Wait for server to be reachable.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", srv.Addr())
		if err == nil {
			conn.Close()
			return srv, cancel
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	t.Fatal("server did not start in time")
	return nil, nil
}

func TestServer_StartsOnRandomPort(t *testing.T) {
	srv, cancel := startTestServer(t)
	defer cancel()

	_, port, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		t.Fatalf("bad addr %q: %v", srv.Addr(), err)
	}
	if port == "0" {
		t.Error("port should be assigned, got 0")
	}
}

func TestServer_UnauthenticatedReturns401(t *testing.T) {
	srv, cancel := startTestServer(t)
	defer cancel()

	// No Authorization header.
	resp, err := http.Post("http://"+srv.Addr()+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestServer_InvalidTokenReturns401(t *testing.T) {
	srv, cancel := startTestServer(t)
	defer cancel()

	req, _ := http.NewRequest("POST", "http://"+srv.Addr()+"/mcp", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer wrong-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestServer_ValidTokenAccepted(t *testing.T) {
	srv, cancel := startTestServer(t)
	defer cancel()

	// Send a valid MCP initialize request.
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`
	req, _ := http.NewRequest("POST", "http://"+srv.Addr()+"/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	// The MCP server should process the request (not reject with 401).
	if resp.StatusCode == http.StatusUnauthorized {
		t.Error("valid token was rejected")
	}
}

func TestServer_ShutdownClean(t *testing.T) {
	srv, cancel := startTestServer(t)

	// Verify server is reachable.
	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("server not reachable: %v", err)
	}
	conn.Close()

	// Cancel context to trigger shutdown.
	cancel()

	// Server should stop accepting connections.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", srv.Addr(), 100*time.Millisecond)
		if err != nil {
			return // Good: connection refused means server stopped.
		}
		conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("server still accepting connections after shutdown")
}

func TestServer_MissingBearerPrefix(t *testing.T) {
	srv, cancel := startTestServer(t)
	defer cancel()

	req, _ := http.NewRequest("POST", "http://"+srv.Addr()+"/mcp", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Basic "+testToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
