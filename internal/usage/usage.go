// Package usage sends initech's one anonymous usage event: usage_ping, at
// most once per 24 hours per install, carrying a random install id, the
// initech version, the OS and the CPU architecture, and nothing else.
//
// The caller decides whether to ping at all (Enabled) and runs Ping off the
// UI path; Ping itself never prints. Every failure is returned so tests can
// see it, and the caller drops it.
package usage

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	// EventName is the only event this package sends.
	EventName = "usage_ping"

	// DefaultEndpoint is the PostHog capture URL, the same project the old
	// telemetry client used. The project key is public (it is on the site).
	DefaultEndpoint = "https://us.i.posthog.com/capture"
	posthogAPIKey   = "phc_Hlm2RtsGAMCBcpmBvjFBuECVCHhfbE5kzdIa8a33qgD"

	// EndpointEnv overrides DefaultEndpoint; tests and QA point it at a local
	// fake server.
	EndpointEnv = "INITECH_USAGE_ENDPOINT"

	// Interval is the shortest gap between two pings from one install.
	Interval = 24 * time.Hour

	sendTimeout  = 3 * time.Second
	idFile       = "install_id"
	lastPingFile = "last_ping"
)

// ErrTooSoon means the last ping was less than Interval ago; nothing was sent.
var ErrTooSoon = errors.New("usage: last ping under 24h ago")

// errRealEndpointInTest refuses a send to PostHog from a test binary.
var errRealEndpointInTest = errors.New("usage: refusing to send to the real endpoint from a test")

// Enabled reports whether the usage ping may run. telemetry is the
// initech.yaml value (nil = key absent = on). DO_NOT_TRACK set to anything but
// "", "0" or "false" turns it off and wins over the file.
func Enabled(telemetry *bool, getenv func(string) string) bool {
	if telemetry != nil && !*telemetry {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(getenv("DO_NOT_TRACK"))) {
	case "", "0", "false":
		return true
	}
	return false
}

// Options configures one Ping. Zero values pick the real defaults.
type Options struct {
	Version  string                        // initech version string.
	Endpoint string                        // Capture URL; "" = EndpointEnv, then DefaultEndpoint.
	Dir      string                        // Where install_id and last_ping live; "" = DefaultDir().
	Now      func() time.Time              // Clock; nil = time.Now.
	Client   *http.Client                  // nil = a client with a 3 second timeout.
	Debug    func(msg string, args ...any) // Optional debug log line; nil = silent.
}

// payload is the exact capture body. Adding a field here changes what leaves
// the user's machine, which needs its own operator yes; the golden test in
// usage_test.go fails until it is updated.
type payload struct {
	APIKey     string     `json:"api_key"`
	Event      string     `json:"event"`
	DistinctID string     `json:"distinct_id"`
	Timestamp  string     `json:"timestamp"`
	Properties properties `json:"properties"`
}

type properties struct {
	Version      string `json:"version"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	GeoIPDisable bool   `json:"$geoip_disable"` // PostHog derives no location from the request.
}

// Ping sends one usage_ping unless one was sent less than Interval ago. It
// creates the install id on first use. last_ping is written only after a
// successful response, so a failed send is tried again at the next start.
func Ping(o Options) error {
	err := ping(o)
	if err != nil && o.Debug != nil {
		o.Debug("usage ping not sent", "err", err)
	}
	return err
}

func ping(o Options) error {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	dir := o.Dir
	if dir == "" {
		dir = DefaultDir()
	}
	if dir == "" {
		return errors.New("usage: no config directory")
	}
	// A last ping in the future (a clock set back, a hand-edited file) does
	// not count: honouring it would silence the ping until that date.
	if last, err := readLastPing(dir); err == nil && !last.After(now()) && now().Sub(last) < Interval {
		return ErrTooSoon
	}
	endpoint := o.Endpoint
	if endpoint == "" {
		endpoint = os.Getenv(EndpointEnv)
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	if endpoint == DefaultEndpoint && testing.Testing() {
		return errRealEndpointInTest
	}
	id, err := loadOrCreateID(dir)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload{
		APIKey:     posthogAPIKey,
		Event:      EventName,
		DistinctID: id,
		Timestamp:  now().UTC().Format(time.RFC3339),
		Properties: properties{Version: o.Version, OS: runtime.GOOS, Arch: runtime.GOARCH, GeoIPDisable: true},
	})
	if err != nil {
		return err
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: sendTimeout}
	}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("usage: endpoint answered %d", resp.StatusCode)
	}
	return os.WriteFile(filepath.Join(dir, lastPingFile), []byte(now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}

// DefaultDir is where the install id lives: %LOCALAPPDATA%\initech on
// Windows, ~/.config/initech elsewhere. "" when no home directory is known.
func DefaultDir() string {
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

func readLastPing(dir string) (time.Time, error) {
	data, err := os.ReadFile(filepath.Join(dir, lastPingFile))
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
}

// loadOrCreateID returns the install id, making it (mode 0600) on first use.
// An unwritable directory is an error: an id that is not stored would count
// one install as many.
func loadOrCreateID(dir string) (string, error) {
	path := filepath.Join(dir, idFile)
	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); len(id) >= 32 {
			return id, nil
		}
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// newID returns a random RFC 4122 version 4 UUID.
func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// Notice is the one line that tells the user the ping exists. initech init,
// the generated initech.yaml, the first-launch welcome box and --help all
// print this text.
const Notice = "initech sends one anonymous usage ping a day (version, OS, CPU architecture, a random install id). telemetry: false turns it off."
