package appserve

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// APNs hosts, by environment.
const (
	APNsProduction = "https://api.push.apple.com"
	APNsSandbox    = "https://api.sandbox.push.apple.com"
)

// Timing of the sender.
const (
	jwtLifetime   = 40 * time.Minute // Apple takes a token 20 to 60 minutes old
	pushTimeout   = 10 * time.Second
	errorLogEvery = time.Hour
)

// APNsConfig is the apns block of initech.yaml: where the key is and what it
// signs for. Environment is production unless it says sandbox.
type APNsConfig struct {
	KeyPath     string `yaml:"key_path"`
	KeyID       string `yaml:"key_id"`
	TeamID      string `yaml:"team_id"`
	Topic       string `yaml:"topic"`
	Environment string `yaml:"environment"`
	KeyName     string `yaml:"key_name"`
}

// Host is the APNs host for the block's environment.
func (c APNsConfig) Host() string {
	if strings.EqualFold(strings.TrimSpace(c.Environment), "sandbox") {
		return APNsSandbox
	}
	return APNsProduction
}

// LoadAPNsKey reads the signing key at c.KeyPath: it must be mode 0600 and a
// PKCS#8 P-256 key. The error never carries the file's bytes.
func LoadAPNsKey(c APNsConfig) (*ecdsa.PrivateKey, error) {
	for name, v := range map[string]string{"key_path": c.KeyPath, "key_id": c.KeyID, "team_id": c.TeamID, "topic": c.Topic} {
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("apns %s is not set", name)
		}
	}
	st, err := os.Stat(c.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("apns key: %w", err)
	}
	if st.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("apns key %s is mode %#o; it must be 0600", c.KeyPath, st.Mode().Perm())
	}
	raw, err := os.ReadFile(c.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("apns key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("apns key %s is not a PEM file", c.KeyPath)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apns key %s is not a PKCS#8 key", c.KeyPath)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve.Params().Name != "P-256" {
		return nil, fmt.Errorf("apns key %s is not a P-256 key", c.KeyPath)
	}
	return key, nil
}

// Payload is one push as APNs receives it: a title, the pane, and the rig
// and pane for the tap. There is no field for anything else.
type Payload struct {
	APS struct {
		Alert struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"alert"`
		Sound    string `json:"sound"`
		ThreadID string `json:"thread-id"`
	} `json:"aps"`
	Data struct {
		Rig    string `json:"rig"`
		Pane   string `json:"pane"`
		Reason string `json:"reason"` // waiting or stuck
	} `json:"data"`
}

// NewPayload is the push for pane in rig, for reason.
func NewPayload(rig, pane, reason string) Payload {
	var p Payload
	p.APS.Alert.Title = "initech: " + rig
	p.APS.Alert.Body = pane
	p.APS.Sound = "default"
	p.APS.ThreadID = rig
	p.Data.Rig, p.Data.Pane, p.Data.Reason = rig, pane, reason
	return p
}

// Sender sends pushes to every registered device through APNs. It never
// retries: a token APNs rejects is unregistered, any other failure is logged
// once an hour per device.
type Sender struct {
	cfg     APNsConfig
	key     *ecdsa.PrivateKey
	host    string
	client  *http.Client
	devices *Devices
	now     func() time.Time
	log     func(string)

	mu        sync.Mutex
	jwt       string
	jwtAt     time.Time
	lastError map[string]time.Time // by device, for once-an-hour logging
	noDevice  time.Time
	busy      map[string]bool // devices with a push in flight
}

// NewSender is a sender over key. host and client may be a test's.
func NewSender(cfg APNsConfig, key *ecdsa.PrivateKey, host string, client *http.Client, devices *Devices, now func() time.Time, log func(string)) *Sender {
	return &Sender{cfg: cfg, key: key, host: host, client: client, devices: devices, now: now, log: log,
		lastError: map[string]time.Time{}, busy: map[string]bool{}}
}

// token is the provider token, made in memory and remade before it is
// jwtLifetime old. It is never written or logged.
func (s *Sender) token() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.jwt != "" && now.Sub(s.jwtAt) < jwtLifetime {
		return s.jwt, nil
	}
	enc := base64.RawURLEncoding
	head, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": s.cfg.KeyID})
	claims, _ := json.Marshal(map[string]any{"iss": s.cfg.TeamID, "iat": now.Unix()})
	signing := enc.EncodeToString(head) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, sv, err := ecdsa.Sign(rand.Reader, s.key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	sv.FillBytes(sig[32:])
	s.jwt, s.jwtAt = signing+"."+enc.EncodeToString(sig), now
	return s.jwt, nil
}

// Push sends p to every registered device, each in its own goroutine, and
// returns at once; done, when not nil, is called when all have finished.
func (s *Sender) Push(p Payload, done func()) {
	body, err := json.Marshal(p)
	if err != nil {
		return
	}
	tokens := s.devices.Tokens()
	if len(tokens) == 0 {
		s.mu.Lock()
		if s.now().Sub(s.noDevice) >= errorLogEvery {
			s.noDevice = s.now()
			s.mu.Unlock()
			s.log("initech app-serve: no device registered; pushes are not sent")
		} else {
			s.mu.Unlock()
		}
		if done != nil {
			done()
		}
		return
	}
	var wg sync.WaitGroup
	for _, t := range tokens {
		s.mu.Lock()
		if s.busy[t] {
			s.mu.Unlock()
			continue // one push in flight per device; this one is dropped, not queued
		}
		s.busy[t] = true
		s.mu.Unlock()
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			defer func() { s.mu.Lock(); delete(s.busy, t); s.mu.Unlock() }()
			s.send(t, body)
		}(t)
	}
	if done != nil {
		go func() { wg.Wait(); done() }()
	}
}

// apnsReason is the reason APNs gives with an error.
type apnsReason struct {
	Reason string `json:"reason"`
}

// send posts one push to one device and acts on the answer.
func (s *Sender) send(device string, body []byte) {
	jwt, err := s.token()
	if err != nil {
		s.failed(device, "cannot sign the request")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", s.host+"/3/device/"+device, bytes.NewReader(body))
	if err != nil {
		s.failed(device, "bad request")
		return
	}
	req.Header.Set("authorization", "bearer "+jwt)
	req.Header.Set("apns-topic", s.cfg.Topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-expiration", fmt.Sprint(s.now().Add(time.Hour).Unix()))
	resp, err := s.client.Do(req)
	if err != nil {
		s.failed(device, "APNs unreachable")
		return
	}
	defer resp.Body.Close()
	var why apnsReason
	json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&why)
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusGone, resp.StatusCode == http.StatusBadRequest && why.Reason == "BadDeviceToken":
		if _, err := s.devices.Remove(device); err == nil {
			s.log(fmt.Sprintf("initech app-serve: APNs rejected device %s (%d %s); it is unregistered", short(device), resp.StatusCode, why.Reason))
		}
	default:
		s.failed(device, fmt.Sprintf("APNs answered %d %s", resp.StatusCode, why.Reason))
	}
}

// failed logs a push that did not go, once an hour per device. Nothing is
// retried.
func (s *Sender) failed(device, why string) {
	s.mu.Lock()
	last, seen := s.lastError[device]
	now := s.now()
	if seen && now.Sub(last) < errorLogEvery {
		s.mu.Unlock()
		return
	}
	s.lastError[device] = now
	s.mu.Unlock()
	s.log(fmt.Sprintf("initech app-serve: a push to device %s did not go: %s", short(device), why))
}

// short is a device token as the log names it: its first 8 characters.
func short(device string) string {
	if len(device) > 8 {
		return device[:8] + "…"
	}
	return device
}
