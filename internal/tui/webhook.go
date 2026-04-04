// webhook.go implements fire-and-forget HTTP POST of agent events to an
// external webhook URL. Each AgentEvent triggers a JSON POST with kind
// (dot-notation), agent, bead_id, detail, timestamp, and project fields.
package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/nmelo/initech/internal/webhook"
)

// webhookKindMap translates EventType to dot-notation kind strings.
var webhookKindMap = map[EventType]string{
	EventBeadCompleted:     "agent.completed",
	EventBeadClaimed:       "agent.claimed",
	EventBeadFailed:        "agent.failed",
	EventAgentStalled:      "agent.stalled",
	EventAgentStuck:        "agent.stuck",
	EventAgentIdle:         "agent.idle",
	EventAgentIdleWithBead: "agent.idle_with_bead",
	EventAgentSuspended:    "agent.suspended",
	EventAgentResumed:      "agent.resumed",
	EventMessageSent:       "agent.message",
	EventAgentStarted:      "agent.started",
	EventAgentStopped:      "agent.stopped",
	EventAgentRestarted:    "agent.restarted",
	EventAgentAdded:        "agent.added",
	EventAgentRemoved:      "agent.removed",
	EventTimerFired:        "agent.timer_fired",
}

// startWebhookSink reads events from ch and POSTs each one to url as JSON.
// Blocks until ctx is cancelled or ch is closed. Fire-and-forget: failures
// are logged but not retried.
func startWebhookSink(ctx context.Context, url, project string, ch <-chan AgentEvent) {
	client := &http.Client{Timeout: 5 * time.Second}

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			postWebhookEvent(ctx, client, url, project, ev)
		case <-ctx.Done():
			return
		}
	}
}

func postWebhookEvent(ctx context.Context, client *http.Client, url, project string, ev AgentEvent) {
	kind, ok := webhookKindMap[ev.Type]
	if !ok {
		kind = "agent." + ev.Type.String()
	}

	payload := webhook.Payload{
		Kind:      kind,
		Agent:     ev.Pane,
		BeadID:    ev.BeadID,
		Detail:    ev.Detail,
		Timestamp: ev.Time.Format(time.RFC3339),
		Project:   project,
	}

	var body []byte
	var err error

	if webhook.IsSlackWebhook(url) {
		text := webhook.FormatSlackText(payload)
		body, err = json.Marshal(map[string]string{"text": text})
	} else {
		body, err = json.Marshal(payload)
	}
	if err != nil {
		LogWarn("webhook", "marshal failed", "err", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		LogWarn("webhook", "request creation failed", "url", url, "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		LogWarn("webhook", "POST failed", "url", url, "err", err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		LogWarn("webhook", "POST rejected", "url", url, "status", resp.StatusCode)
	}
}
