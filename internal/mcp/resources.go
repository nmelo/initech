package mcp

import (
	"context"
	"encoding/json"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// statusResourceURI is the canonical URI for the agent fleet status resource.
const statusResourceURI = "initech://status"

// registerResources adds MCP resources and resource templates to the server.
// Re-calling this with the same server replaces existing entries and triggers
// list_changed.
func registerResources(s *gomcp.Server, host PaneHost) {
	s.AddResource(&gomcp.Resource{
		URI:         statusResourceURI,
		Name:        "Agent Fleet Status",
		Description: "Current status of all initech agents including activity, health, and assigned beads.",
		MIMEType:    "application/json",
	}, func(_ context.Context, req *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
		return handleReadStatus(host, req)
	})

	s.AddResourceTemplate(&gomcp.ResourceTemplate{
		URITemplate: "initech://agents/{name}/output",
		Name:        "Agent Terminal Output",
		Description: "Last 50 lines of terminal output from a specific agent pane.",
		MIMEType:    "text/plain",
	}, func(_ context.Context, req *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
		return handleReadAgentOutput(host, req)
	})

	s.AddResourceTemplate(&gomcp.ResourceTemplate{
		URITemplate: "initech://agents/{name}/status",
		Name:        "Agent Status",
		Description: "Detailed status of a specific agent including activity, health, bead assignment, and memory usage.",
		MIMEType:    "application/json",
	}, func(_ context.Context, req *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
		return handleReadAgentStatus(host, req)
	})
}

func handleReadStatus(host PaneHost, _ *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
	panes, ok := host.AllPanes()
	if !ok {
		return nil, gomcp.ResourceNotFoundError(statusResourceURI)
	}

	entries := make([]statusEntry, len(panes))
	for i, p := range panes {
		entries[i] = statusEntry{
			Name:        p.Name(),
			Activity:    p.Activity(),
			Alive:       p.IsAlive(),
			Visible:     p.IsVisible(),
			BeadID:      p.BeadID(),
			MemoryRSSKB: p.MemoryRSSKB(),
		}
	}

	data, _ := json.Marshal(entries)
	return &gomcp.ReadResourceResult{
		Contents: []*gomcp.ResourceContents{{
			URI:      statusResourceURI,
			MIMEType: "application/json",
			Text:     string(data),
		}},
	}, nil
}

// parseAgentName extracts the agent name from a URI like
// "initech://agents/eng1/output" or "initech://agents/eng1/status".
func parseAgentName(uri string) string {
	const prefix = "initech://agents/"
	if !strings.HasPrefix(uri, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(uri, prefix)
	// rest is "eng1/output" or "eng1/status"
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return parts[0]
}

func handleReadAgentOutput(host PaneHost, req *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
	uri := req.Params.URI
	name := parseAgentName(uri)
	if name == "" {
		return nil, gomcp.ResourceNotFoundError(uri)
	}

	pane, ok := host.FindPane(name)
	if !ok {
		return nil, gomcp.ResourceNotFoundError(uri)
	}
	if pane == nil {
		return nil, gomcp.ResourceNotFoundError(uri)
	}

	content := pane.PeekContent(50)
	return &gomcp.ReadResourceResult{
		Contents: []*gomcp.ResourceContents{{
			URI:      uri,
			MIMEType: "text/plain",
			Text:     content,
		}},
	}, nil
}

func handleReadAgentStatus(host PaneHost, req *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
	uri := req.Params.URI
	name := parseAgentName(uri)
	if name == "" {
		return nil, gomcp.ResourceNotFoundError(uri)
	}

	pane, ok := host.FindPane(name)
	if !ok {
		return nil, gomcp.ResourceNotFoundError(uri)
	}
	if pane == nil {
		return nil, gomcp.ResourceNotFoundError(uri)
	}

	entry := statusEntry{
		Name:        pane.Name(),
		Activity:    pane.Activity(),
		Alive:       pane.IsAlive(),
		Visible:     pane.IsVisible(),
		BeadID:      pane.BeadID(),
		MemoryRSSKB: pane.MemoryRSSKB(),
	}
	data, _ := json.Marshal(entry)
	return &gomcp.ReadResourceResult{
		Contents: []*gomcp.ResourceContents{{
			URI:      uri,
			MIMEType: "application/json",
			Text:     string(data),
		}},
	}, nil
}
