// Package webhook provides a standalone HTTP POST client for sending
// notifications to a configured webhook URL. It handles Slack auto-detection
// and formatting so that callers (CLI, IPC, MCP) share a single code path.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Payload is the JSON body POSTed to the webhook URL.
type Payload struct {
	Kind      string `json:"kind"`
	Agent     string `json:"agent"`
	BeadID    string `json:"bead_id,omitempty"`
	Detail    string `json:"detail"`
	Timestamp string `json:"timestamp"`
	Project   string `json:"project"`
}

// PostNotification sends a single notification to the webhook URL. It
// auto-detects Slack webhooks and formats accordingly. Returns an error
// if the POST fails or the server returns a non-2xx status.
func PostNotification(url, kind, agent, message, project string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload := Payload{
		Kind:      kind,
		Agent:     agent,
		Detail:    message,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Project:   project,
	}

	var body []byte
	var err error

	if IsSlackWebhook(url) {
		text := FormatSlackText(payload)
		body, err = json.Marshal(map[string]string{"text": text})
	} else {
		body, err = json.Marshal(payload)
	}
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST failed: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// AnnouncePayload is the JSON body POSTed to an Agent Radio announce endpoint.
// Fields use omitempty so only provided values are sent.
type AnnouncePayload struct {
	Detail    string `json:"detail"`
	Kind      string `json:"kind,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Project   string `json:"project,omitempty"`
	BeadID    string `json:"bead_id,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// AnnounceResult describes the outcome of a PostAnnouncement call.
type AnnounceResult struct {
	// Status is the server-reported status: "queued", "immediate", "suppressed",
	// or "rate_limited" (429), or "error" for failures.
	Status string
	// Message is the human-readable CLI output line.
	Message string
	// Err is non-nil for hard failures (4xx/5xx, timeout, connection refused).
	Err error
}

// PostAnnouncement sends a TTS announcement to an Agent Radio endpoint.
// Unlike PostNotification, it parses the server's JSON response to determine
// announcement disposition, and treats 429 as a soft success (the server
// queues rate-limited announcements automatically).
func PostAnnouncement(url string, p AnnouncePayload) AnnounceResult {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.Timestamp = time.Now().Format(time.RFC3339)

	body, err := json.Marshal(p)
	if err != nil {
		return AnnounceResult{Status: "error", Message: "Error: marshal payload", Err: err}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return AnnounceResult{Status: "error", Message: "Error: create request", Err: err}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return AnnounceResult{
				Status:  "error",
				Message: "Error: connection timed out",
				Err:     err,
			}
		}
		if isConnectionRefused(err) {
			return AnnounceResult{
				Status:  "error",
				Message: "Error: connection refused (is Agent Radio running?)",
				Err:     err,
			}
		}
		return AnnounceResult{Status: "error", Message: fmt.Sprintf("Error: %v", err), Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return AnnounceResult{Status: "rate_limited", Message: "Dropped (rate limited)"}
	}

	if resp.StatusCode >= 400 {
		var respBody bytes.Buffer
		respBody.ReadFrom(resp.Body)
		return AnnounceResult{
			Status:  "error",
			Message: fmt.Sprintf("Error: %d %s", resp.StatusCode, strings.TrimSpace(respBody.String())),
			Err:     fmt.Errorf("announce returned HTTP %d", resp.StatusCode),
		}
	}

	// Parse 200 response for status field.
	var result struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		// Server returned 200 but no parseable JSON; treat as success.
		return AnnounceResult{Status: "queued", Message: "Announced (queued)"}
	}

	switch result.Status {
	case "immediate":
		return AnnounceResult{Status: "immediate", Message: "Announced"}
	case "suppressed":
		return AnnounceResult{Status: "suppressed", Message: "Suppressed (event kind filtered)"}
	default:
		return AnnounceResult{Status: "queued", Message: "Announced (queued)"}
	}
}

// isConnectionRefused checks whether an error chain contains a "connection refused"
// indication, which means the target server is not running.
func isConnectionRefused(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "connection refused")
}

// IsSlackWebhook returns true if the URL is a Slack incoming webhook.
func IsSlackWebhook(url string) bool {
	return strings.Contains(url, "hooks.slack.com/")
}

// FormatSlackText produces a human-readable Slack mrkdwn message from a payload.
func FormatSlackText(p Payload) string {
	icon := slackIcon(p.Kind)
	if p.Agent != "" {
		if p.BeadID != "" {
			return fmt.Sprintf("%s *[%s]* `%s` %s", icon, p.Agent, p.BeadID, p.Detail)
		}
		return fmt.Sprintf("%s *[%s]* %s", icon, p.Agent, p.Detail)
	}
	if p.BeadID != "" {
		return fmt.Sprintf("%s `%s` %s", icon, p.BeadID, p.Detail)
	}
	return fmt.Sprintf("%s %s", icon, p.Detail)
}

func slackIcon(kind string) string {
	switch kind {
	case "agent.completed":
		return ":white_check_mark:"
	case "agent.claimed":
		return ":arrow_forward:"
	case "agent.failed":
		return ":x:"
	case "agent.stalled":
		return ":warning:"
	case "agent.stuck":
		return ":rotating_light:"
	case "agent.suspended":
		return ":pause_button:"
	case "agent.resumed":
		return ":arrow_forward:"
	case "agent.started":
		return ":rocket:"
	case "agent.stopped":
		return ":stop_button:"
	case "agent.restarted":
		return ":arrows_counterclockwise:"
	case "agent.added":
		return ":heavy_plus_sign:"
	case "agent.removed":
		return ":heavy_minus_sign:"
	case "deploy":
		return ":shipit:"
	case "release":
		return ":package:"
	case "milestone":
		return ":checkered_flag:"
	default:
		return ":speech_balloon:"
	}
}
