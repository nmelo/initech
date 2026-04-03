package mcp

import (
	"context"
	"encoding/json"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// statusResourceURI is the canonical URI for the agent fleet status resource.
const statusResourceURI = "initech://status"

// registerResources adds MCP resources to the server. Re-calling this with
// the same server replaces existing resources and triggers list_changed.
func registerResources(s *gomcp.Server, host PaneHost) {
	s.AddResource(&gomcp.Resource{
		URI:         statusResourceURI,
		Name:        "Agent Fleet Status",
		Description: "Current status of all initech agents including activity, health, and assigned beads.",
		MIMEType:    "application/json",
	}, func(_ context.Context, req *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
		return handleReadStatus(host, req)
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
