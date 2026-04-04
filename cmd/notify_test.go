package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/nmelo/initech/internal/webhook"
)

func TestNotifyCommand_Success(t *testing.T) {
	var received webhook.Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := fmt.Sprintf("project: testproject\nroot: %s\nwebhook_url: %s\nroles:\n  - eng1\n", dir, srv.URL)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	rootCmd.SetArgs([]string{"notify", "--kind", "deploy", "--agent", "shipper", "v1.9.1 deployed"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
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
	if received.Project != "testproject" {
		t.Errorf("project = %q, want %q", received.Project, "testproject")
	}
}

func TestNotifyCommand_MissingWebhook(t *testing.T) {
	dir := t.TempDir()
	cfg := fmt.Sprintf("project: testproject\nroot: %s\nroles:\n  - eng1\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	rootCmd.SetArgs([]string{"notify", "test message"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing webhook_url")
	}
}

func TestNotifyCommand_AgentFromEnv(t *testing.T) {
	var received webhook.Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := fmt.Sprintf("project: testproject\nroot: %s\nwebhook_url: %s\nroles:\n  - eng1\n", dir, srv.URL)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	os.Setenv("INITECH_AGENT", "eng1")
	defer os.Unsetenv("INITECH_AGENT")

	rootCmd.SetArgs([]string{"notify", "--agent", "", "task done"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if received.Agent != "eng1" {
		t.Errorf("agent = %q, want %q (from INITECH_AGENT)", received.Agent, "eng1")
	}
}
