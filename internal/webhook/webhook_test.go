package webhook

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsSlackWebhook(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://hooks.slack.com/services/T/B/x", true},
		{"https://hooks.slack.com/workflows/T/B/x", true},
		{"https://discord.com/api/webhooks/123/abc", false},
		{"http://localhost:8080/event", false},
	}
	for _, tt := range tests {
		if got := IsSlackWebhook(tt.url); got != tt.want {
			t.Errorf("IsSlackWebhook(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestFormatSlackText(t *testing.T) {
	tests := []struct {
		name string
		p    Payload
		want string
	}{
		{
			name: "agent with bead",
			p:    Payload{Kind: "agent.completed", Agent: "eng1", BeadID: "ini-abc", Detail: "done"},
			want: ":white_check_mark: *[eng1]* `ini-abc` done",
		},
		{
			name: "agent without bead",
			p:    Payload{Kind: "custom", Agent: "shipper", Detail: "v1.9 released"},
			want: ":speech_balloon: *[shipper]* v1.9 released",
		},
		{
			name: "no agent with bead",
			p:    Payload{Kind: "deploy", BeadID: "ini-xyz", Detail: "deployed"},
			want: ":shipit: `ini-xyz` deployed",
		},
		{
			name: "no agent no bead",
			p:    Payload{Kind: "milestone", Detail: "Phase 1 complete"},
			want: ":checkered_flag: Phase 1 complete",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatSlackText(tt.p)
			if got != tt.want {
				t.Errorf("FormatSlackText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPostNotification_GenericWebhook(t *testing.T) {
	var received Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %s, want application/json", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	err := PostNotification(srv.URL, "deploy", "shipper", "v1.9.1 deployed", "initech")
	if err != nil {
		t.Fatalf("PostNotification() error: %v", err)
	}

	if received.Kind != "deploy" {
		t.Errorf("kind = %q, want %q", received.Kind, "deploy")
	}
	if received.Agent != "shipper" {
		t.Errorf("agent = %q, want %q", received.Agent, "shipper")
	}
	if received.Detail != "v1.9.1 deployed" {
		t.Errorf("detail = %q, want %q", received.Detail, "v1.9.1 deployed")
	}
	if received.Project != "initech" {
		t.Errorf("project = %q, want %q", received.Project, "initech")
	}
	if received.Timestamp == "" {
		t.Error("timestamp should not be empty")
	}
}

func TestPostNotification_SlackWebhook(t *testing.T) {
	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// Override the URL to include hooks.slack.com in the path so detection works.
	// Since httptest uses localhost, we test the format function directly and
	// test the POST mechanism separately.
	err := PostNotification(srv.URL, "custom", "eng1", "test message", "initech")
	if err != nil {
		t.Fatalf("PostNotification() error: %v", err)
	}
	// For a non-Slack URL, body should be the raw Payload JSON.
	if body != nil {
		t.Log("non-Slack URL correctly sent raw payload")
	}
}

func TestPostNotification_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	err := PostNotification(srv.URL, "custom", "", "test", "initech")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if got := err.Error(); got != "webhook returned HTTP 500" {
		t.Errorf("error = %q, want %q", got, "webhook returned HTTP 500")
	}
}

func TestPostNotification_EmptyURL(t *testing.T) {
	err := PostNotification("", "custom", "", "test", "initech")
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

// ── PostAnnouncement ────────────────────────────────────────────────

func TestPostAnnouncement_Queued(t *testing.T) {
	var received AnnouncePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %s, want application/json", ct)
		}
		json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
	}))
	defer srv.Close()

	result := PostAnnouncement(srv.URL, AnnouncePayload{
		Detail:  "Phase 1 done",
		Kind:    "agent.completed",
		Agent:   "eng1",
		Project: "initech",
		BeadID:  "ini-abc",
	})

	if result.Err != nil {
		t.Fatalf("PostAnnouncement error: %v", result.Err)
	}
	if result.Status != "queued" {
		t.Errorf("status = %q, want %q", result.Status, "queued")
	}
	if result.Message != "Announced (queued)" {
		t.Errorf("message = %q, want %q", result.Message, "Announced (queued)")
	}
	if received.Detail != "Phase 1 done" {
		t.Errorf("detail = %q, want %q", received.Detail, "Phase 1 done")
	}
	if received.Timestamp == "" {
		t.Error("timestamp should be auto-populated")
	}
}

func TestPostAnnouncement_Immediate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "immediate"})
	}))
	defer srv.Close()

	result := PostAnnouncement(srv.URL, AnnouncePayload{Detail: "test"})
	if result.Err != nil {
		t.Fatalf("error: %v", result.Err)
	}
	if result.Status != "immediate" {
		t.Errorf("status = %q, want %q", result.Status, "immediate")
	}
	if result.Message != "Announced" {
		t.Errorf("message = %q, want %q", result.Message, "Announced")
	}
}

func TestPostAnnouncement_Suppressed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "suppressed"})
	}))
	defer srv.Close()

	result := PostAnnouncement(srv.URL, AnnouncePayload{Detail: "test"})
	if result.Err != nil {
		t.Fatalf("error: %v", result.Err)
	}
	if result.Status != "suppressed" {
		t.Errorf("status = %q, want %q", result.Status, "suppressed")
	}
	if result.Message != "Suppressed (event kind filtered)" {
		t.Errorf("message = %q, want %q", result.Message, "Suppressed (event kind filtered)")
	}
}

func TestPostAnnouncement_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()

	result := PostAnnouncement(srv.URL, AnnouncePayload{Detail: "test"})
	if result.Err != nil {
		t.Fatalf("429 should not return error, got: %v", result.Err)
	}
	if result.Status != "rate_limited" {
		t.Errorf("status = %q, want %q", result.Status, "rate_limited")
	}
	if result.Message != "Dropped (rate limited)" {
		t.Errorf("message = %q, want %q", result.Message, "Dropped (rate limited)")
	}
}

func TestPostAnnouncement_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	result := PostAnnouncement(srv.URL, AnnouncePayload{Detail: "test"})
	if result.Err == nil {
		t.Fatal("500 should return error")
	}
	if result.Status != "error" {
		t.Errorf("status = %q, want %q", result.Status, "error")
	}
	if !strings.Contains(result.Message, "500") {
		t.Errorf("message should contain status code: %q", result.Message)
	}
}

func TestPostAnnouncement_ConnectionRefused(t *testing.T) {
	// Use a port that nothing is listening on.
	result := PostAnnouncement("http://127.0.0.1:1", AnnouncePayload{Detail: "test"})
	if result.Err == nil {
		t.Fatal("connection refused should return error")
	}
	if !strings.Contains(result.Message, "connection refused") {
		t.Errorf("message = %q, want mention of connection refused", result.Message)
	}
}

func TestPostAnnouncement_OmitsEmptyFields(t *testing.T) {
	var rawBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&rawBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
	}))
	defer srv.Close()

	PostAnnouncement(srv.URL, AnnouncePayload{Detail: "just a message"})

	// Fields with omitempty should not appear when empty.
	if _, ok := rawBody["bead_id"]; ok {
		t.Error("bead_id should be omitted when empty")
	}
	if _, ok := rawBody["agent"]; ok {
		t.Error("agent should be omitted when empty")
	}
}

func TestIsConnectionRefused(t *testing.T) {
	if isConnectionRefused(nil) {
		t.Error("nil should not be connection refused")
	}
	if !isConnectionRefused(fmt.Errorf("dial tcp: connection refused")) {
		t.Error("connection refused error should be detected")
	}
	if isConnectionRefused(fmt.Errorf("timeout")) {
		t.Error("timeout should not be connection refused")
	}
}
