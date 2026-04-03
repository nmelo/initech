package mcp

import (
	"context"
	"fmt"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// PeekInput is the input schema for the initech_peek tool.
// Fields without omitempty are required in the JSON Schema.
type PeekInput struct {
	Agent string `json:"agent" jsonschema:"target agent name (e.g. eng1)"`
	Lines int    `json:"lines,omitempty" jsonschema:"number of lines to return (default 50)"`
}

// PeekOutput is the output schema for the initech_peek tool.
type PeekOutput struct {
	Content string `json:"content"`
}

// SendInput is the input schema for the initech_send tool.
type SendInput struct {
	Agent   string `json:"agent" jsonschema:"target agent name (e.g. eng1)"`
	Message string `json:"message" jsonschema:"text to send to the agent terminal"`
	Enter   *bool  `json:"enter,omitempty" jsonschema:"press Enter after the message (default true)"`
}

// SendOutput is the output schema for the initech_send tool.
type SendOutput struct {
	Status string `json:"status"`
}

// AgentInput is the input schema for tools that take only an agent name.
type AgentInput struct {
	Agent string `json:"agent" jsonschema:"target agent name (e.g. eng1)"`
}

// AgentOutput is the output schema for lifecycle tools.
type AgentOutput struct {
	Status string `json:"status"`
}

// registerTools adds MCP tools to the server.
func registerTools(s *gomcp.Server, host PaneHost) {
	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_peek",
		Description: "Read recent terminal output from an agent pane. Call initech_status first to discover available agent names.",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input PeekInput) (*gomcp.CallToolResult, PeekOutput, error) {
		return handlePeek(host, input)
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_send",
		Description: "Send a message to an agent's terminal. The text is injected into the agent's PTY input. Call initech_status first to discover available agent names.",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input SendInput) (*gomcp.CallToolResult, SendOutput, error) {
		return handleSend(host, input)
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_restart",
		Description: "Restart an agent's process. Stops the current process and starts a fresh one with the same configuration.",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input AgentInput) (*gomcp.CallToolResult, AgentOutput, error) {
		return handleLifecycle(host, input, "restart")
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_stop",
		Description: "Stop an agent's process. The agent pane remains but the process exits.",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input AgentInput) (*gomcp.CallToolResult, AgentOutput, error) {
		return handleLifecycle(host, input, "stop")
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_start",
		Description: "Start a previously stopped agent. No-op if the agent is already running.",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input AgentInput) (*gomcp.CallToolResult, AgentOutput, error) {
		return handleLifecycle(host, input, "start")
	})
}

func handlePeek(host PaneHost, input PeekInput) (*gomcp.CallToolResult, PeekOutput, error) {
	if input.Agent == "" {
		return nil, PeekOutput{}, fmt.Errorf("agent is required")
	}

	lines := input.Lines
	if lines <= 0 {
		lines = 50
	}

	pane, ok := host.FindPane(input.Agent)
	if !ok {
		return nil, PeekOutput{}, fmt.Errorf("host is shutting down")
	}
	if pane == nil {
		return nil, PeekOutput{}, fmt.Errorf("agent %q not found", input.Agent)
	}

	content := pane.PeekContent(lines)
	return nil, PeekOutput{Content: content}, nil
}

func handleLifecycle(host PaneHost, input AgentInput, action string) (*gomcp.CallToolResult, AgentOutput, error) {
	if input.Agent == "" {
		return nil, AgentOutput{}, fmt.Errorf("agent is required")
	}

	var err error
	switch action {
	case "restart":
		err = host.RestartAgent(input.Agent)
	case "stop":
		err = host.StopAgent(input.Agent)
	case "start":
		err = host.StartAgent(input.Agent)
	}
	if err != nil {
		return nil, AgentOutput{}, err
	}

	statusMap := map[string]string{
		"restart": "restarted",
		"stop":    "stopped",
		"start":   "started",
	}
	return nil, AgentOutput{Status: statusMap[action]}, nil
}

func handleSend(host PaneHost, input SendInput) (*gomcp.CallToolResult, SendOutput, error) {
	if input.Agent == "" {
		return nil, SendOutput{}, fmt.Errorf("agent is required")
	}
	if input.Message == "" {
		return nil, SendOutput{}, fmt.Errorf("message is required")
	}

	pane, ok := host.FindPane(input.Agent)
	if !ok {
		return nil, SendOutput{}, fmt.Errorf("host is shutting down")
	}
	if pane == nil {
		return nil, SendOutput{}, fmt.Errorf("agent %q not found", input.Agent)
	}

	enter := true
	if input.Enter != nil {
		enter = *input.Enter
	}
	pane.SendText(input.Message, enter)

	return nil, SendOutput{Status: "sent"}, nil
}
