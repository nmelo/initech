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

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHandleReadStatus_ReturnsJSON(t *testing.T) {
	eng1 := &fakePaneHandle{
		name: "eng1", activity: "running", alive: true, visible: true,
		beadID: "ini-abc", memoryRSSKB: 102400,
	}
	qa1 := &fakePaneHandle{
		name: "qa1", activity: "idle", alive: false, visible: false,
		memoryRSSKB: 51200,
	}
	host := newFakeHost(eng1, qa1)

	result, err := handleReadStatus(host, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Contents))
	}

	content := result.Contents[0]
	if content.URI != statusResourceURI {
		t.Errorf("URI = %q, want %q", content.URI, statusResourceURI)
	}
	if content.MIMEType != "application/json" {
		t.Errorf("MIMEType = %q, want application/json", content.MIMEType)
	}

	var entries []statusEntry
	if err := json.Unmarshal([]byte(content.Text), &entries); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Find entries (map iteration order in AllPanes is non-deterministic).
	var eng1Entry, qa1Entry *statusEntry
	for i := range entries {
		switch entries[i].Name {
		case "eng1":
			eng1Entry = &entries[i]
		case "qa1":
			qa1Entry = &entries[i]
		}
	}
	if eng1Entry == nil || qa1Entry == nil {
		t.Fatalf("missing entries: eng1=%v qa1=%v", eng1Entry, qa1Entry)
	}
	if eng1Entry.Activity != "running" || !eng1Entry.Alive || !eng1Entry.Visible {
		t.Errorf("eng1 fields wrong: %+v", eng1Entry)
	}
	if eng1Entry.BeadID != "ini-abc" {
		t.Errorf("eng1 bead_id = %q", eng1Entry.BeadID)
	}
	if qa1Entry.Activity != "idle" || qa1Entry.Alive || qa1Entry.Visible {
		t.Errorf("qa1 fields wrong: %+v", qa1Entry)
	}
}

func TestHandleReadStatus_EmptyPaneList(t *testing.T) {
	host := newFakeHost()

	result, err := handleReadStatus(host, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Contents))
	}

	var entries []statusEntry
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &entries); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty array, got %d entries", len(entries))
	}
}

func TestHandleReadStatus_ShuttingDown(t *testing.T) {
	host := newFakeHost()
	host.shuttingDown = true

	_, err := handleReadStatus(host, nil)
	if err == nil {
		t.Fatal("expected error when shutting down")
	}
}

func TestRegisterResources_ResourceRegistered(t *testing.T) {
	// Verify registerResources doesn't panic and the resource is accessible
	// through a full server. We test via an HTTP request with a valid token.
	// Bind to 127.0.0.1 to avoid IPv6 Host header issues with the SDK.
	host := newFakeHost(
		&fakePaneHandle{name: "eng1", activity: "running", alive: true, visible: true},
	)
	srv, cancel := startTestServerOnLocalhost(t, host)
	defer cancel()

	// The resource should be registered. We can verify by calling the MCP
	// resources/list method via JSON-RPC over a proper session.
	// First initialize the session and capture the session ID.
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`
	initResp := doMCPSessionRequest(t, srv, initBody, "")
	sessionID := initResp.sessionID

	// Send initialized notification with session ID.
	notifyBody := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	doMCPSessionRequest(t, srv, notifyBody, sessionID)

	// List resources.
	listBody := `{"jsonrpc":"2.0","id":2,"method":"resources/list","params":{}}`
	listResp := doMCPSessionRequest(t, srv, listBody, sessionID)
	respBody := listResp.body

	// Should contain our resource.
	if !json.Valid([]byte(respBody)) {
		t.Fatalf("invalid JSON response: %s", respBody)
	}
	var resp struct {
		Result struct {
			Resources []struct {
				URI  string `json:"uri"`
				Name string `json:"name"`
			} `json:"resources"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(respBody), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, respBody)
	}
	found := false
	for _, r := range resp.Result.Resources {
		if r.URI == statusResourceURI {
			found = true
			if r.Name != "Agent Fleet Status" {
				t.Errorf("name = %q, want %q", r.Name, "Agent Fleet Status")
			}
		}
	}
	if !found {
		t.Errorf("resource %q not found in list: %s", statusResourceURI, respBody)
	}
}

// ── Per-agent resource template tests ──

func makeReadReq(uri string) *gomcp.ReadResourceRequest {
	return &gomcp.ReadResourceRequest{
		Params: &gomcp.ReadResourceParams{URI: uri},
	}
}

func TestHandleReadAgentOutput_Valid(t *testing.T) {
	pane := &fakePaneHandle{name: "eng1", content: "line1\nline2\nprompt>"}
	host := newFakeHost(pane)

	result, err := handleReadAgentOutput(host, makeReadReq("initech://agents/eng1/output"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(result.Contents))
	}
	c := result.Contents[0]
	if c.MIMEType != "text/plain" {
		t.Errorf("mimeType = %q, want text/plain", c.MIMEType)
	}
	if c.Text != "line1\nline2\nprompt>" {
		t.Errorf("text = %q", c.Text)
	}
}

func TestHandleReadAgentOutput_EmptyBuffer(t *testing.T) {
	pane := &fakePaneHandle{name: "eng1", content: ""}
	host := newFakeHost(pane)

	result, err := handleReadAgentOutput(host, makeReadReq("initech://agents/eng1/output"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Contents[0].Text != "" {
		t.Errorf("expected empty text, got %q", result.Contents[0].Text)
	}
}

func TestHandleReadAgentOutput_InvalidAgent(t *testing.T) {
	host := newFakeHost()

	_, err := handleReadAgentOutput(host, makeReadReq("initech://agents/nonexistent/output"))
	if err == nil {
		t.Fatal("expected error for invalid agent")
	}
}

func TestHandleReadAgentOutput_BadURI(t *testing.T) {
	host := newFakeHost()

	_, err := handleReadAgentOutput(host, makeReadReq("initech://bogus"))
	if err == nil {
		t.Fatal("expected error for bad URI")
	}
}

func TestHandleReadAgentStatus_Valid(t *testing.T) {
	pane := &fakePaneHandle{
		name: "eng1", activity: "running", alive: true, visible: true,
		beadID: "ini-abc", memoryRSSKB: 102400,
	}
	host := newFakeHost(pane)

	result, err := handleReadAgentStatus(host, makeReadReq("initech://agents/eng1/status"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(result.Contents))
	}
	c := result.Contents[0]
	if c.MIMEType != "application/json" {
		t.Errorf("mimeType = %q, want application/json", c.MIMEType)
	}

	var entry statusEntry
	if err := json.Unmarshal([]byte(c.Text), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry.Name != "eng1" || entry.Activity != "running" || !entry.Alive {
		t.Errorf("entry = %+v", entry)
	}
	if entry.BeadID != "ini-abc" || entry.MemoryRSSKB != 102400 {
		t.Errorf("entry = %+v", entry)
	}
}

func TestHandleReadAgentStatus_InvalidAgent(t *testing.T) {
	host := newFakeHost()

	_, err := handleReadAgentStatus(host, makeReadReq("initech://agents/nonexistent/status"))
	if err == nil {
		t.Fatal("expected error for invalid agent")
	}
}

func TestParseAgentName(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"initech://agents/eng1/output", "eng1"},
		{"initech://agents/eng1/status", "eng1"},
		{"initech://agents/super/output", "super"},
		{"initech://status", ""},
		{"initech://agents//output", ""},
		{"http://other/path", ""},
		{"", ""},
	}
	for _, tc := range tests {
		got := parseAgentName(tc.uri)
		if got != tc.want {
			t.Errorf("parseAgentName(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}

func startTestServerOnLocalhost(t *testing.T, host PaneHost) (*Server, context.CancelFunc) {
	t.Helper()
	srv := NewServer(0, "127.0.0.1", testToken, host, nil)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

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

type mcpResponse struct {
	body      string
	sessionID string
}

// doMCPSessionRequest sends a JSON-RPC request with session tracking.
func doMCPSessionRequest(t *testing.T, srv *Server, body, sessionID string) mcpResponse {
	t.Helper()
	req, _ := http.NewRequest("POST", "http://"+srv.Addr()+"/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)

	// Extract session ID from response header.
	sid := resp.Header.Get("Mcp-Session-Id")
	if sid == "" && sessionID != "" {
		sid = sessionID // keep existing
	}

	// Handle SSE responses: extract data from event stream.
	respStr := string(respBytes)
	if strings.HasPrefix(respStr, "event:") || strings.Contains(respStr, "\ndata:") {
		for _, line := range strings.Split(respStr, "\n") {
			if strings.HasPrefix(line, "data: ") || strings.HasPrefix(line, "data:") {
				respStr = strings.TrimPrefix(line, "data: ")
				respStr = strings.TrimPrefix(respStr, "data:")
				break
			}
		}
	}

	return mcpResponse{body: respStr, sessionID: sid}
}
