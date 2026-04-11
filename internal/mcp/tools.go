package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nmelo/initech/internal/webhook"
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

// AddInput is the input schema for the initech_add tool.
type AddInput struct {
	Role string `json:"role" jsonschema:"role name for the new agent (e.g. eng3)"`
}

// AddOutput is the output schema for the initech_add tool.
type AddOutput struct {
	Status string `json:"status"`
	Agent  string `json:"agent"`
}

// RemoveInput is the input schema for the initech_remove tool.
type RemoveInput struct {
	Agent string `json:"agent" jsonschema:"agent name to remove"`
}

// RemoveOutput is the output schema for the initech_remove tool.
type RemoveOutput struct {
	Status string `json:"status"`
}

// AtInput is the input schema for the initech_at tool.
type AtInput struct {
	Agent   string `json:"agent" jsonschema:"target agent name (e.g. eng1)"`
	Message string `json:"message" jsonschema:"text to send to the agent terminal"`
	Delay   string `json:"delay" jsonschema:"delay before sending, as a Go duration (e.g. 5m, 30s, 1h)"`
}

// AtOutput is the output schema for the initech_at tool.
type AtOutput struct {
	Status  string `json:"status"`
	TimerID string `json:"timer_id"`
}

// PatrolInput is the input schema for the initech_patrol tool.
type PatrolInput struct {
	Lines int `json:"lines,omitempty" jsonschema:"number of lines per agent (default 20)"`
}

// PatrolOutput is the output schema for the initech_patrol tool.
type PatrolOutput struct {
	Content string `json:"content"`
}

// NotifyInput is the input schema for the initech_notify tool.
type NotifyInput struct {
	Message string `json:"message" jsonschema:"notification message text"`
	Kind    string `json:"kind,omitempty" jsonschema:"event kind (default custom). Dot-notation encouraged: deploy, release, milestone"`
	Agent   string `json:"agent,omitempty" jsonschema:"agent name to attribute the notification to"`
}

// NotifyOutput is the output schema for the initech_notify tool.
type NotifyOutput struct {
	Status string `json:"status"`
}

// InterruptInput is the input schema for the initech_interrupt tool.
type InterruptInput struct {
	Agent string `json:"agent" jsonschema:"target agent name (e.g. eng1)"`
	Hard  bool   `json:"hard,omitempty" jsonschema:"send Ctrl+C instead of Escape (default false)"`
}

// InterruptOutput is the output schema for the initech_interrupt tool.
type InterruptOutput struct {
	Status string `json:"status"`
}

// AssignInput is the input schema for the initech_assign tool.
type AssignInput struct {
	Agent   string   `json:"agent" jsonschema:"target agent name (e.g. eng1)"`
	BeadID  string   `json:"bead_id,omitempty" jsonschema:"single bead ID (backwards compat)"`
	BeadIDs []string `json:"bead_ids,omitempty" jsonschema:"one or more bead IDs to assign"`
	Message string   `json:"message,omitempty" jsonschema:"custom instructions appended to the dispatch message"`
}

// AssignOutput is the output schema for the initech_assign tool.
type AssignOutput struct {
	Status string `json:"status"`
}

// DeliverInput is the input schema for the initech_deliver tool.
type DeliverInput struct {
	BeadID  string `json:"bead_id" jsonschema:"bead ID to deliver (e.g. ini-abc)"`
	Pass    *bool  `json:"pass,omitempty" jsonschema:"mark ready_for_qa (default true)"`
	Reason  string `json:"reason,omitempty" jsonschema:"failure reason (used when pass=false)"`
	To      string `json:"to,omitempty" jsonschema:"agent to report to (default super)"`
	Message string `json:"message,omitempty" jsonschema:"custom note appended to the report"`
}

// DeliverOutput is the output schema for the initech_deliver tool.
type DeliverOutput struct {
	Status string `json:"status"`
}

// StatusInput is the input schema for the initech_status tool (no params).
type StatusInput struct{}

// StatusOutput is the output schema for the initech_status tool.
type StatusOutput struct {
	Content string `json:"content"`
}

// statusEntry is one agent's status for JSON serialization.
type statusEntry struct {
	Name        string `json:"name"`
	Activity    string `json:"activity"`
	Alive       bool   `json:"alive"`
	Visible     bool   `json:"visible"`
	BeadID      string `json:"bead_id,omitempty"`
	MemoryRSSKB int64  `json:"memory_rss_kb"`
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

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_add",
		Description: "Hot-add a new agent pane to the running session. The workspace directory must already exist.",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input AddInput) (*gomcp.CallToolResult, AddOutput, error) {
		return handleAdd(host, input)
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_remove",
		Description: "Remove an agent pane from the running session. Cannot remove the last agent.",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input RemoveInput) (*gomcp.CallToolResult, RemoveOutput, error) {
		return handleRemove(host, input)
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_at",
		Description: "Schedule a deferred message to an agent. The message is sent after the specified delay (e.g. \"5m\", \"30s\").",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input AtInput) (*gomcp.CallToolResult, AtOutput, error) {
		return handleAt(host, input)
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_patrol",
		Description: "Read recent terminal output from all agent panes. Returns a JSON object keyed by agent name with terminal output as values.",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input PatrolInput) (*gomcp.CallToolResult, PatrolOutput, error) {
		return handlePatrol(host, input)
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_status",
		Description: "Get the status of all agents. Returns a JSON array with name, activity, alive, visible, bead_id, and memory_rss_kb for each agent.",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input StatusInput) (*gomcp.CallToolResult, StatusOutput, error) {
		return handleStatus(host)
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_notify",
		Description: "Post a notification to the configured webhook (Slack, Discord, or generic). Use for milestones, deployments, status updates, and custom announcements.",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input NotifyInput) (*gomcp.CallToolResult, NotifyOutput, error) {
		return handleNotify(host, input)
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_interrupt",
		Description: "Send Escape or Ctrl+C to an agent's terminal. Escape stops Claude Code's current action. Ctrl+C (hard=true) kills a running shell command.",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input InterruptInput) (*gomcp.CallToolResult, InterruptOutput, error) {
		return handleInterrupt(host, input)
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_assign",
		Description: "Atomic bead dispatch: claims a bead, registers it in the TUI, and sends a dispatch message to the agent. Requires bd CLI.",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input AssignInput) (*gomcp.CallToolResult, AssignOutput, error) {
		return handleAssign(host, input)
	})

	gomcp.AddTool(s, &gomcp.Tool{
		Name:        "initech_deliver",
		Description: "Atomic bead completion: updates status, clears TUI, reports to super, announces on radio/webhook. Counterpart to initech_assign. Requires bd CLI.",
	}, func(_ context.Context, _ *gomcp.CallToolRequest, input DeliverInput) (*gomcp.CallToolResult, DeliverOutput, error) {
		return handleDeliver(host, input)
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

func handleAdd(host PaneHost, input AddInput) (*gomcp.CallToolResult, AddOutput, error) {
	if input.Role == "" {
		return nil, AddOutput{}, fmt.Errorf("role is required")
	}
	if err := host.AddAgent(input.Role); err != nil {
		return nil, AddOutput{}, err
	}
	return nil, AddOutput{Status: "added", Agent: input.Role}, nil
}

func handleRemove(host PaneHost, input RemoveInput) (*gomcp.CallToolResult, RemoveOutput, error) {
	if input.Agent == "" {
		return nil, RemoveOutput{}, fmt.Errorf("agent is required")
	}
	if err := host.RemoveAgent(input.Agent); err != nil {
		return nil, RemoveOutput{}, err
	}
	return nil, RemoveOutput{Status: "removed"}, nil
}

func handleAt(host PaneHost, input AtInput) (*gomcp.CallToolResult, AtOutput, error) {
	if input.Agent == "" {
		return nil, AtOutput{}, fmt.Errorf("agent is required")
	}
	if input.Message == "" {
		return nil, AtOutput{}, fmt.Errorf("message is required")
	}
	if input.Delay == "" {
		return nil, AtOutput{}, fmt.Errorf("delay is required")
	}
	if _, err := time.ParseDuration(input.Delay); err != nil {
		return nil, AtOutput{}, fmt.Errorf("invalid delay %q: %w", input.Delay, err)
	}
	timerID, err := host.ScheduleSend(input.Agent, input.Message, input.Delay)
	if err != nil {
		return nil, AtOutput{}, err
	}
	return nil, AtOutput{Status: "scheduled", TimerID: timerID}, nil
}

func handlePatrol(host PaneHost, input PatrolInput) (*gomcp.CallToolResult, PatrolOutput, error) {
	lines := input.Lines
	if lines <= 0 {
		lines = 20
	}

	panes, ok := host.AllPanes()
	if !ok {
		return nil, PatrolOutput{}, fmt.Errorf("host is shutting down")
	}

	result := make(map[string]string, len(panes))
	for _, p := range panes {
		result[p.Name()] = p.PeekContent(lines)
	}

	data, _ := json.Marshal(result)
	return nil, PatrolOutput{Content: string(data)}, nil
}

func handleStatus(host PaneHost) (*gomcp.CallToolResult, StatusOutput, error) {
	panes, ok := host.AllPanes()
	if !ok {
		return nil, StatusOutput{}, fmt.Errorf("host is shutting down")
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
	return nil, StatusOutput{Content: string(data)}, nil
}

func handleAssign(host PaneHost, input AssignInput) (*gomcp.CallToolResult, AssignOutput, error) {
	if input.Agent == "" {
		return nil, AssignOutput{}, fmt.Errorf("agent is required")
	}

	// Merge bead_id (string, backward compat) and bead_ids (array).
	beadIDs := input.BeadIDs
	if len(beadIDs) == 0 && input.BeadID != "" {
		beadIDs = []string{input.BeadID}
	}
	if len(beadIDs) == 0 {
		return nil, AssignOutput{}, fmt.Errorf("bead_id or bead_ids is required")
	}

	type result struct {
		id    string
		title string
	}
	var successes []result
	var failures []string

	// Process each bead: show + claim.
	for _, id := range beadIDs {
		out, err := exec.Command("bd", "show", id, "--json").CombinedOutput()
		if err != nil {
			failures = append(failures, id)
			continue
		}
		var beads []struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal(out, &beads); err != nil || len(beads) == 0 || beads[0].Title == "" {
			failures = append(failures, id)
			continue
		}
		title := beads[0].Title
		if len(title) > 80 {
			title = title[:77] + "..."
		}

		claimOut, err := exec.Command("bd", "update", id, "--status", "in_progress", "--assignee", input.Agent).CombinedOutput()
		if err != nil {
			_ = claimOut
			failures = append(failures, id)
			continue
		}
		successes = append(successes, result{id: id, title: title})
	}

	if len(successes) == 0 {
		return nil, AssignOutput{}, fmt.Errorf("no beads could be assigned (failed: %s)", strings.Join(failures, ", "))
	}

	// Register beads in TUI (first bead for backward compat).
	_ = host.SetBead(input.Agent, successes[0].id)

	// Build and send dispatch message.
	var dispatch string
	if len(successes) == 1 {
		s := successes[0]
		dispatch = fmt.Sprintf("[from super] %s: %s. Read bd show %s for full AC.", s.id, s.title, s.id)
	} else {
		var b strings.Builder
		fmt.Fprintf(&b, "[from super] Assigned %d beads:", len(successes))
		showCount := len(successes)
		if showCount > 5 {
			showCount = 5
		}
		for i := 0; i < showCount; i++ {
			fmt.Fprintf(&b, "\n- %s: %s", successes[i].id, successes[i].title)
		}
		if len(successes) > 5 {
			fmt.Fprintf(&b, "\n... and %d more. Run bd list --assignee %s for full list.", len(successes)-5, input.Agent)
		} else {
			b.WriteString("\nRead bd show <id> for full AC on each.")
		}
		dispatch = b.String()
	}
	if input.Message != "" {
		dispatch += " " + input.Message
	}

	pane, ok := host.FindPane(input.Agent)
	if !ok {
		return nil, AssignOutput{}, fmt.Errorf("host is shutting down")
	}
	if pane == nil {
		return nil, AssignOutput{}, fmt.Errorf("beads claimed but agent %q not found for dispatch", input.Agent)
	}
	pane.SendText(dispatch, true)

	// Announce (fire and forget).
	if announceURL, project := host.AnnounceConfig(); announceURL != "" {
		ids := make([]string, len(successes))
		for i, s := range successes {
			ids[i] = s.id
		}
		var detail string
		if len(successes) == 1 {
			detail = fmt.Sprintf("%s picking up: %s", input.Agent, successes[0].title)
		} else {
			detail = fmt.Sprintf("%s assigned %d beads", input.Agent, len(successes))
		}
		webhook.PostAnnouncement(announceURL, webhook.AnnouncePayload{
			Detail:  detail,
			Kind:    "agent.started",
			Agent:   input.Agent,
			Project: project,
			BeadID:  successes[0].id,
		})
	}

	ids := make([]string, len(successes))
	for i, s := range successes {
		ids[i] = s.id
	}
	status := fmt.Sprintf("assigned %d bead(s) to %s: %s", len(successes), input.Agent, strings.Join(ids, ", "))
	if len(failures) > 0 {
		status += fmt.Sprintf(" (failed: %s)", strings.Join(failures, ", "))
	}
	return nil, AssignOutput{Status: status}, nil
}

func handleNotify(host PaneHost, input NotifyInput) (*gomcp.CallToolResult, NotifyOutput, error) {
	if input.Message == "" {
		return nil, NotifyOutput{}, fmt.Errorf("message is required")
	}

	webhookURL, project := host.NotifyConfig()
	if webhookURL == "" {
		return nil, NotifyOutput{}, fmt.Errorf("no webhook_url configured in initech.yaml")
	}

	kind := input.Kind
	if kind == "" {
		kind = "custom"
	}

	if err := webhook.PostNotification(webhookURL, kind, input.Agent, input.Message, project); err != nil {
		return nil, NotifyOutput{}, err
	}

	return nil, NotifyOutput{Status: "sent"}, nil
}

func handleInterrupt(host PaneHost, input InterruptInput) (*gomcp.CallToolResult, InterruptOutput, error) {
	if input.Agent == "" {
		return nil, InterruptOutput{}, fmt.Errorf("agent is required")
	}
	if err := host.InterruptAgent(input.Agent, input.Hard); err != nil {
		return nil, InterruptOutput{}, err
	}
	status := "interrupted (Escape)"
	if input.Hard {
		status = "interrupted (Ctrl+C)"
	}
	return nil, InterruptOutput{Status: status}, nil
}

func handleDeliver(host PaneHost, input DeliverInput) (*gomcp.CallToolResult, DeliverOutput, error) {
	if input.BeadID == "" {
		return nil, DeliverOutput{}, fmt.Errorf("bead_id is required")
	}

	isFail := input.Pass != nil && !*input.Pass

	// Step 1: Read bead info.
	out, err := exec.Command("bd", "show", input.BeadID, "--json").CombinedOutput()
	if err != nil {
		return nil, DeliverOutput{}, fmt.Errorf("bead %s not found: %s", input.BeadID, strings.TrimSpace(string(out)))
	}
	var beads []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &beads); err != nil {
		return nil, DeliverOutput{}, fmt.Errorf("parse bd output: %w", err)
	}
	title := input.BeadID
	if len(beads) > 0 && beads[0].Title != "" {
		title = beads[0].Title
	}
	if len(title) > 80 {
		title = title[:77] + "..."
	}

	// Step 2: Update bead status.
	if isFail {
		reason := input.Reason
		if reason == "" {
			reason = "no reason provided"
		}
		commentArgs := []string{"comments", "add", input.BeadID, "FAILED: " + reason}
		if commentOut, err := exec.Command("bd", commentArgs...).CombinedOutput(); err != nil {
			return nil, DeliverOutput{}, fmt.Errorf("bd comment failed: %s", strings.TrimSpace(string(commentOut)))
		}
	} else {
		statusOut, err := exec.Command("bd", "update", input.BeadID, "--status", "ready_for_qa").CombinedOutput()
		if err != nil {
			return nil, DeliverOutput{}, fmt.Errorf("bd update failed: %s", strings.TrimSpace(string(statusOut)))
		}
	}

	// Step 3: Clear TUI bead on the caller (best effort).
	callerAgent := os.Getenv("INITECH_AGENT")
	if callerAgent != "" {
		_ = host.SetBead(callerAgent, "")
	}

	// Step 4: Send report.
	recipient := input.To
	if recipient == "" {
		recipient = "super"
	}
	var report string
	if isFail {
		reason := input.Reason
		if reason == "" {
			reason = "no reason provided"
		}
		report = fmt.Sprintf("[from %s] %s: %s FAILED: %s", agentOrUnknownMCP(callerAgent), input.BeadID, title, reason)
	} else {
		report = fmt.Sprintf("[from %s] %s: %s ready for QA", agentOrUnknownMCP(callerAgent), input.BeadID, title)
	}
	if input.Message != "" {
		report += ". " + input.Message
	}
	pane, ok := host.FindPane(recipient)
	if ok && pane != nil {
		pane.SendText(report, true)
	}

	// Step 5: Announce (fire and forget).
	if announceURL, project := host.AnnounceConfig(); announceURL != "" {
		var detail, kind string
		if isFail {
			kind = "agent.failed"
			if input.Reason != "" {
				detail = fmt.Sprintf("%s hit a wall: %s", agentOrUnknownMCP(callerAgent), input.Reason)
			} else {
				detail = fmt.Sprintf("%s hit a wall", agentOrUnknownMCP(callerAgent))
			}
		} else {
			kind = "agent.completed"
			detail = fmt.Sprintf("%s finished: %s", agentOrUnknownMCP(callerAgent), title)
		}
		webhook.PostAnnouncement(announceURL, webhook.AnnouncePayload{
			Detail:  detail,
			Kind:    kind,
			Agent:   agentOrUnknownMCP(callerAgent),
			Project: project,
			BeadID:  input.BeadID,
		})
	}

	// Step 6: Webhook (fire and forget).
	if webhookURL, project := host.NotifyConfig(); webhookURL != "" {
		var kind, message string
		if isFail {
			kind = "agent.failed"
			message = fmt.Sprintf("%s FAILED", title)
			if input.Reason != "" {
				message += ": " + input.Reason
			}
		} else {
			kind = "agent.completed"
			message = fmt.Sprintf("%s ready for QA", title)
		}
		webhook.PostNotification(webhookURL, kind, agentOrUnknownMCP(callerAgent), message, project) //nolint:errcheck
	}

	if isFail {
		return nil, DeliverOutput{Status: fmt.Sprintf("delivered %s (FAILED) -> %s", input.BeadID, recipient)}, nil
	}
	return nil, DeliverOutput{Status: fmt.Sprintf("delivered %s (ready for QA) -> %s", input.BeadID, recipient)}, nil
}

func agentOrUnknownMCP(agent string) string {
	if agent == "" {
		return "unknown"
	}
	return agent
}
