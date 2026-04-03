package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

const testToken = "test-token-abc123"

func startTestServer(t *testing.T) (*Server, context.CancelFunc) {
	return startTestServerWithHost(t, nil)
}

func startTestServerWithHost(t *testing.T, host PaneHost) (*Server, context.CancelFunc) {
	t.Helper()
	srv := NewServer(0, "", testToken, host, nil)
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

func TestServer_CustomBindAddress(t *testing.T) {
	srv := NewServer(0, "127.0.0.1", testToken, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", srv.Addr())
		if err == nil {
			conn.Close()
			host, _, _ := net.SplitHostPort(srv.Addr())
			if host != "127.0.0.1" {
				t.Errorf("host = %q, want 127.0.0.1", host)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server did not start in time")
}

func TestServer_DefaultBindAddress(t *testing.T) {
	srv := NewServer(0, "", testToken, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("tcp", srv.Addr())
		if err == nil {
			conn.Close()
			host, _, _ := net.SplitHostPort(srv.Addr())
			// OS may resolve 0.0.0.0 to :: (IPv6 any).
			if host != "0.0.0.0" && host != "::" {
				t.Errorf("host = %q, want 0.0.0.0 or ::", host)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server did not start in time")
}

func TestServer_OAuthDiscovery_ReturnsMetadata(t *testing.T) {
	srv, cancel := startTestServer(t)
	defer cancel()

	// No auth needed for OAuth discovery (it's public metadata).
	resp, err := http.Get("http://" + srv.Addr() + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var meta struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		BearerMethods        []string `json:"bearer_methods_supported"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	// Resource URL should match the request origin (dynamic, not static).
	wantResource := "http://" + srv.Addr()
	if meta.Resource != wantResource {
		t.Errorf("resource = %q, want %q", meta.Resource, wantResource)
	}
	if len(meta.AuthorizationServers) != 0 {
		t.Errorf("authorization_servers should be empty, got %v", meta.AuthorizationServers)
	}
	if len(meta.BearerMethods) != 1 || meta.BearerMethods[0] != "header" {
		t.Errorf("bearer_methods_supported = %v, want [header]", meta.BearerMethods)
	}
}

func TestServer_OAuthDiscovery_SubPath(t *testing.T) {
	srv, cancel := startTestServer(t)
	defer cancel()

	// The SDK also tries /.well-known/oauth-protected-resource/mcp
	resp, err := http.Get("http://" + srv.Addr() + "/.well-known/oauth-protected-resource/mcp")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_OAuthDiscovery_RespectsXForwardedProto(t *testing.T) {
	srv, cancel := startTestServer(t)
	defer cancel()

	// Simulate a reverse proxy setting X-Forwarded-Proto: https
	req, _ := http.NewRequest("GET", "http://"+srv.Addr()+"/.well-known/oauth-protected-resource", nil)
	req.Host = "myhost.tailnet.ts.net:9201"
	req.Header.Set("X-Forwarded-Proto", "https")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var meta struct {
		Resource string `json:"resource"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}

	// Should use the forwarded scheme and host, not the internal bind address.
	want := "https://myhost.tailnet.ts.net:9201"
	if meta.Resource != want {
		t.Errorf("resource = %q, want %q", meta.Resource, want)
	}
}
