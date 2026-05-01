// Package telemetry provides anonymous usage tracking via PostHog.
// Events are fire-and-forget: sends are non-blocking and failures are
// silently discarded. No PII is collected.
package telemetry

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	posthogEndpoint = "https://us.i.posthog.com/capture"
	posthogAPIKey   = "phc_Hlm2RtsGAMCBcpmBvjFBuECVCHhfbE5kzdIa8a33qgD"
	bufferSize      = 16
	sendTimeout     = 3 * time.Second
)

// Client sends anonymous telemetry events to PostHog.
// Zero value is not usable; call Init to create a client.
type Client struct {
	apiKey    string
	deviceID  string
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
	APIKey     string         `json:"api_key"`
	Event      string         `json:"event"`
	Properties map[string]any `json:"properties"`
	Timestamp  string         `json:"timestamp"`
	DistinctID string         `json:"distinct_id"`
}

// Init creates a telemetry client and starts the background sender.
// Pass the initech version string.
func Init(version string) *Client {
	c := &Client{
		apiKey:    posthogAPIKey,
		deviceID:  loadOrCreateDeviceID(),
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

// DeviceID returns the stable device identifier used as PostHog distinct_id.
func (c *Client) DeviceID() string {
	if c == nil {
		return ""
	}
	return c.deviceID
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
		DistinctID: c.deviceID,
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

// deviceIDDir is overridable for testing.
var deviceIDDir = defaultDeviceIDDir

func defaultDeviceIDDir() string {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "initech")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "initech")
}

// loadOrCreateDeviceID reads the stable device UUID from disk, creating it
// on first run. Falls back to a random session-scoped ID if disk I/O fails.
func loadOrCreateDeviceID() string {
	dir := deviceIDDir()
	if dir == "" {
		return newDeviceUUID()
	}
	path := filepath.Join(dir, "device_id")

	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if len(id) >= 32 {
			return id
		}
	}

	id := newDeviceUUID()
	os.MkdirAll(dir, 0700)
	os.WriteFile(path, []byte(id+"\n"), 0600)
	return id
}

func newDeviceUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	// RFC 4122 v4: set version and variant bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
