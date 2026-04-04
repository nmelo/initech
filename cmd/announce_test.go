package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/webhook"
	"github.com/spf13/cobra"
)

func writeAnnounceConfig(t *testing.T, dir, announceURL string) {
	t.Helper()
	cfg := fmt.Sprintf("project: testproject\nroot: %s\nannounce_url: %s\nroles:\n  - eng1\n", dir, announceURL)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)
}

func TestAnnounceCommand_Queued(t *testing.T) {
	var received webhook.AnnouncePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeAnnounceConfig(t, dir, srv.URL)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	cmd := &cobra.Command{}
	cmd.SetArgs(nil)
	announceKind = "agent.completed"
	announceAgent = "eng1"
	announceBead = "ini-abc"
	announceProject = ""
	defer func() {
		announceKind = "custom"
		announceAgent = ""
		announceBead = ""
		announceProject = ""
	}()

	err := runAnnounce(cmd, []string{"Auth refactor done"})
	if err != nil {
		t.Fatalf("runAnnounce: %v", err)
	}

	if received.Detail != "Auth refactor done" {
		t.Errorf("detail = %q, want %q", received.Detail, "Auth refactor done")
	}
	if received.Kind != "agent.completed" {
		t.Errorf("kind = %q, want %q", received.Kind, "agent.completed")
	}
	if received.Agent != "eng1" {
		t.Errorf("agent = %q, want %q", received.Agent, "eng1")
	}
	if received.BeadID != "ini-abc" {
		t.Errorf("bead_id = %q, want %q", received.BeadID, "ini-abc")
	}
	if received.Project != "testproject" {
		t.Errorf("project = %q, want %q", received.Project, "testproject")
	}
	if received.Timestamp == "" {
		t.Error("timestamp should not be empty")
	}
}

func TestAnnounceCommand_Immediate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "immediate"})
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeAnnounceConfig(t, dir, srv.URL)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	cmd := &cobra.Command{}
	announceKind = "custom"
	announceAgent = ""
	announceBead = ""
	announceProject = ""

	err := runAnnounce(cmd, []string{"test"})
	if err != nil {
		t.Fatalf("runAnnounce: %v", err)
	}
}

func TestAnnounceCommand_Suppressed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "suppressed"})
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeAnnounceConfig(t, dir, srv.URL)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	cmd := &cobra.Command{}
	announceKind = "custom"
	announceAgent = ""
	announceBead = ""
	announceProject = ""

	err := runAnnounce(cmd, []string{"test"})
	if err != nil {
		t.Fatalf("runAnnounce: %v", err)
	}
}

func TestAnnounceCommand_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeAnnounceConfig(t, dir, srv.URL)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	cmd := &cobra.Command{}
	announceKind = "custom"
	announceAgent = ""
	announceBead = ""
	announceProject = ""

	// 429 should return nil (exit 0), not an error.
	err := runAnnounce(cmd, []string{"test"})
	if err != nil {
		t.Fatalf("429 should exit 0, got error: %v", err)
	}
}

func TestAnnounceCommand_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeAnnounceConfig(t, dir, srv.URL)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	cmd := &cobra.Command{}
	announceKind = "custom"
	announceAgent = ""
	announceBead = ""
	announceProject = ""

	err := runAnnounce(cmd, []string{"test"})
	if err == nil {
		t.Fatal("500 should return error")
	}
}

func TestAnnounceCommand_MissingConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := fmt.Sprintf("project: testproject\nroot: %s\nroles:\n  - eng1\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	cmd := &cobra.Command{}
	announceKind = "custom"
	announceAgent = ""
	announceBead = ""
	announceProject = ""

	err := runAnnounce(cmd, []string{"test"})
	if err == nil {
		t.Fatal("missing announce_url should return error")
	}
	if !strings.Contains(err.Error(), "announce_url not configured") {
		t.Errorf("error = %q, want mention of announce_url", err.Error())
	}
}

func TestAnnounceCommand_EmptyMessage(t *testing.T) {
	cmd := &cobra.Command{}
	announceKind = "custom"
	announceAgent = ""
	announceBead = ""
	announceProject = ""

	err := runAnnounce(cmd, []string{""})
	if err == nil {
		t.Fatal("empty message should return error")
	}
	if !strings.Contains(err.Error(), "message required") {
		t.Errorf("error = %q, want 'message required'", err.Error())
	}
}

func TestAnnounceCommand_AgentFromEnv(t *testing.T) {
	var received webhook.AnnouncePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeAnnounceConfig(t, dir, srv.URL)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	os.Setenv("INITECH_AGENT", "qa1")
	defer os.Unsetenv("INITECH_AGENT")

	cmd := &cobra.Command{}
	announceKind = "custom"
	announceAgent = ""
	announceBead = ""
	announceProject = ""

	err := runAnnounce(cmd, []string{"tests passed"})
	if err != nil {
		t.Fatalf("runAnnounce: %v", err)
	}

	if received.Agent != "qa1" {
		t.Errorf("agent = %q, want %q (from INITECH_AGENT)", received.Agent, "qa1")
	}
}

func TestAnnounceCommand_OmitsEmptyFields(t *testing.T) {
	var rawBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&rawBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeAnnounceConfig(t, dir, srv.URL)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	cmd := &cobra.Command{}
	announceKind = ""
	announceAgent = ""
	announceBead = ""
	announceProject = ""

	err := runAnnounce(cmd, []string{"just a message"})
	if err != nil {
		t.Fatalf("runAnnounce: %v", err)
	}

	// bead_id and agent should not appear in JSON when empty.
	if _, ok := rawBody["bead_id"]; ok {
		t.Error("bead_id should be omitted when empty")
	}
	if _, ok := rawBody["agent"]; ok {
		t.Error("agent should be omitted when empty")
	}
}

func TestAnnounceCommand_ProjectOverride(t *testing.T) {
	var received webhook.AnnouncePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeAnnounceConfig(t, dir, srv.URL)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	cmd := &cobra.Command{}
	announceKind = "custom"
	announceAgent = ""
	announceBead = ""
	announceProject = "override-project"
	defer func() { announceProject = "" }()

	err := runAnnounce(cmd, []string{"test"})
	if err != nil {
		t.Fatalf("runAnnounce: %v", err)
	}

	if received.Project != "override-project" {
		t.Errorf("project = %q, want %q", received.Project, "override-project")
	}
}
