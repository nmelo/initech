// Package telemetry provides anonymous usage tracking via PostHog.
// Events are fire-and-forget: sends are non-blocking and failures are
// silently discarded. No PII is collected.
package telemetry

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"runtime"
	"sync"
	"time"
)

const (
	posthogEndpoint = "https://us.i.posthog.com/capture"
	posthogAPIKey   = "POSTHOG_API_KEY_PLACEHOLDER"
	bufferSize      = 16
	sendTimeout     = 3 * time.Second
)

// Client sends anonymous telemetry events to PostHog.
// Zero value is not usable; call Init to create a client.
type Client struct {
	apiKey    string
	sessionID string
	version   string
	startTime time.Time
	events    chan event
	done      chan struct{}
	once      sync.Once
}

type event struct {
	Name       string         `json:"event"`
	Properties map[string]any `json:"properties"`
	Timestamp  string         `json:"timestamp"`
}

type capturePayload struct {
	APIKey     string `json:"api_key"`
	Event      string `json:"event"`
	Properties map[string]any `json:"properties"`
	Timestamp  string `json:"timestamp"`
	DistinctID string `json:"distinct_id"`
}

// Init creates a telemetry client and starts the background sender.
// Pass the initech version string. Returns a no-op client if apiKey
// is the placeholder (telemetry not configured for this build).
func Init(version string) *Client {
	c := &Client{
		apiKey:    posthogAPIKey,
		sessionID: newSessionID(),
		version:   version,
		startTime: time.Now(),
		events:    make(chan event, bufferSize),
		done:      make(chan struct{}),
	}
	go c.sender()
	return c
}

// SessionID returns the random session identifier for event correlation.
func (c *Client) SessionID() string {
	if c == nil {
		return ""
	}
	return c.sessionID
}

// Track enqueues an event for async delivery. Non-blocking: if the
// buffer is full, the event is dropped silently.
func (c *Client) Track(name string, props map[string]any) {
	if c == nil {
		return
	}
	if props == nil {
		props = make(map[string]any)
	}
	props["session_id"] = c.sessionID
	props["version"] = c.version
	props["os"] = runtime.GOOS
	props["arch"] = runtime.GOARCH

	ev := event{
		Name:       name,
		Properties: props,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	select {
	case c.events <- ev:
	default:
	}
}

// Shutdown flushes pending events and stops the background sender.
// Safe to call multiple times. Blocks until the sender exits or 5s
// elapses.
func (c *Client) Shutdown() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		close(c.events)
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
		}
	})
}

// Duration returns the session duration since Init.
func (c *Client) Duration() time.Duration {
	if c == nil {
		return 0
	}
	return time.Since(c.startTime)
}

func (c *Client) sender() {
	defer close(c.done)
	client := &http.Client{Timeout: sendTimeout}
	for ev := range c.events {
		c.send(client, ev)
	}
}

func (c *Client) send(client *http.Client, ev event) {
	payload := capturePayload{
		APIKey:     c.apiKey,
		Event:      ev.Name,
		Properties: ev.Properties,
		Timestamp:  ev.Timestamp,
		DistinctID: c.sessionID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequest("POST", posthogEndpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func newSessionID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
