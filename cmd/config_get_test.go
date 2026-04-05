package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupConfigGetEnv creates a temp dir with a valid initech.yaml and chdir into it.
// Returns a cleanup func that restores the original cwd.
func setupConfigGetEnv(t *testing.T, yamlExtra string) func() {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	yaml := fmt.Sprintf(`project: myproject
root: %s
peer_name: laptop
roles:
  - eng1
mcp_port: 9200
mcp_token: secret-token-123
webhook_url: https://hooks.example.com/test
beads:
  enabled: true
  prefix: ini
`, dir)
	if yamlExtra != "" {
		yaml += yamlExtra
	}

	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0600)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	return func() { os.Chdir(origDir) }
}

func TestConfigGet_StringField(t *testing.T) {
	cleanup := setupConfigGetEnv(t, "")
	defer cleanup()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "get", "project"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "myproject" {
		t.Errorf("got %q, want %q", got, "myproject")
	}
}

func TestConfigGet_IntField(t *testing.T) {
	cleanup := setupConfigGetEnv(t, "")
	defer cleanup()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "get", "mcp_port"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "9200" {
		t.Errorf("got %q, want %q", got, "9200")
	}
}

func TestConfigGet_BoolField(t *testing.T) {
	cleanup := setupConfigGetEnv(t, "")
	defer cleanup()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "get", "beads.enabled"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "true" {
		t.Errorf("got %q, want %q", got, "true")
	}
}

func TestConfigGet_ArrayField(t *testing.T) {
	cleanup := setupConfigGetEnv(t, "")
	defer cleanup()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "get", "roles"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "eng1" {
		t.Errorf("got %q, want %q", got, "eng1")
	}
}

func TestConfigGet_SecretMasked(t *testing.T) {
	cleanup := setupConfigGetEnv(t, "")
	defer cleanup()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "get", "mcp_token"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	if !strings.Contains(got, "masked") {
		t.Errorf("expected masked output, got %q", got)
	}
}

func TestConfigGet_SecretRevealed(t *testing.T) {
	cleanup := setupConfigGetEnv(t, "")
	defer cleanup()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "get", "mcp_token", "--reveal"})
	defer rootCmd.SetArgs(nil)

	// Reset flag for next test.
	defer func() { configGetReveal = false }()

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "secret-token-123" {
		t.Errorf("got %q, want %q", got, "secret-token-123")
	}
}

func TestConfigGet_NotSet(t *testing.T) {
	cleanup := setupConfigGetEnv(t, "")
	defer cleanup()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "get", "announce_url"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	if got != "(not set)" {
		t.Errorf("got %q, want %q", got, "(not set)")
	}
}

func TestConfigGet_UnknownKey(t *testing.T) {
	cleanup := setupConfigGetEnv(t, "")
	defer cleanup()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "get", "nonexistent.key"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "unknown config key") {
		t.Errorf("error = %q, expected 'unknown config key'", err.Error())
	}
}

func TestConfigGet_NestedField(t *testing.T) {
	cleanup := setupConfigGetEnv(t, "")
	defer cleanup()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "get", "beads.prefix"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "ini" {
		t.Errorf("got %q, want %q", got, "ini")
	}
}

func TestConfigGet_TemplateKey(t *testing.T) {
	extra := `remotes:
  workbench:
    addr: "192.168.1.100:7392"
    token: remote-secret
`
	cleanup := setupConfigGetEnv(t, extra)
	defer cleanup()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "get", "remotes.workbench.addr"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "192.168.1.100:7392" {
		t.Errorf("got %q, want %q", got, "192.168.1.100:7392")
	}
}

func TestConfigGet_RoleOverrideTemplate(t *testing.T) {
	extra := `role_overrides:
  eng1:
    agent_type: codex
`
	cleanup := setupConfigGetEnv(t, extra)
	defer cleanup()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "get", "role_overrides.eng1.agent_type"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "codex" {
		t.Errorf("got %q, want %q", got, "codex")
	}
}

func TestConfigGet_PeerName(t *testing.T) {
	cleanup := setupConfigGetEnv(t, "")
	defer cleanup()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "get", "peer_name"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "laptop" {
		t.Errorf("got %q, want %q", got, "laptop")
	}
}
