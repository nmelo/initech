package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
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

func TestResolveConfigValue_AllKeys(t *testing.T) {
	webPort := 8080
	mcpPort := 9090
	p := &config.Project{
		Name:         "test",
		Root:         "/tmp/test",
		Roles:        []string{"eng1", "eng2"},
		ClaudeArgs:   []string{"--continue"},
		PeerName:     "laptop",
		Mode:         "grid",
		Listen:       ":9000",
		Token:        "tok",
		WebPort:      &webPort,
		WebhookURL:   "http://hook",
		AnnounceURL:  "http://radio",
		McpPort:      &mcpPort,
		Beads:        config.BeadsConfig{Prefix: "ini"},
	}

	keys := []string{
		"project", "root", "roles", "claude_args", "peer_name",
		"mode", "listen", "token", "web_port", "webhook_url",
		"announce_url", "auto_notify", "idle_with_bead_threshold",
		"telemetry", "mcp_port", "mcp_token", "mcp_bind",
		"beads.enabled", "beads.prefix",
		"resource.auto_suspend", "resource.pressure_threshold",
		"slack.app_token", "slack.bot_token", "slack.allowed_users",
		"slack.response_mode", "slack.thread_context",
	}
	for _, k := range keys {
		_, ok := resolveConfigValue(p, k)
		if !ok {
			t.Errorf("resolveConfigValue(%q) returned !ok", k)
		}
	}
	_, ok := resolveConfigValue(p, "nonexistent_key")
	if ok {
		t.Error("resolveConfigValue(nonexistent_key) should return !ok")
	}
}

func TestResolveRemote(t *testing.T) {
	p := &config.Project{
		Remotes: map[string]config.Remote{
			"workbench": {Addr: "192.168.1.100:9000", Token: "abc"},
		},
	}
	v, ok := resolveRemote(p, "workbench", "addr")
	if !ok || v != "192.168.1.100:9000" {
		t.Errorf("addr = %q, ok=%v", v, ok)
	}
	v, ok = resolveRemote(p, "workbench", "token")
	if !ok || v != "abc" {
		t.Errorf("token = %q, ok=%v", v, ok)
	}
	_, ok = resolveRemote(p, "workbench", "badfield")
	if ok {
		t.Error("bad field should return !ok")
	}
	v, ok = resolveRemote(p, "missing", "addr")
	if !ok {
		t.Error("missing remote should still return ok=true (empty)")
	}
}

func TestResolveRoleOverride(t *testing.T) {
	auto := true
	p := &config.Project{
		RoleOverrides: map[string]config.RoleOverride{
			"eng1": {
				Command:          []string{"claude", "--fast"},
				AgentType:        "claude_code",
				Dir:              "/custom",
				AutoApprove:      &auto,
				SubmitKey:        "enter",
				NoBracketedPaste: true,
				TechStack:        "Go",
				BuildCmd:         "make build",
				TestCmd:          "make test",
				RepoName:         "initech",
			},
		},
	}
	fields := []string{
		"command", "agent_type", "dir", "auto_approve", "submit_key",
		"no_bracketed_paste", "tech_stack", "build_cmd", "test_cmd", "repo_name",
	}
	for _, f := range fields {
		_, ok := resolveRoleOverride(p, "eng1", f)
		if !ok {
			t.Errorf("resolveRoleOverride(eng1, %q) returned !ok", f)
		}
	}
	_, ok := resolveRoleOverride(p, "eng1", "bad_field")
	if ok {
		t.Error("bad field should return !ok")
	}
	v, ok := resolveRoleOverride(p, "missing", "command")
	if !ok || v != "" {
		t.Errorf("missing role should return ok=true, empty; got %q, %v", v, ok)
	}
}

func TestRunConfigGet_NoProject(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"config", "get", "project"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when no project found")
	}
}

func TestRunConfigGet_UnknownKey(t *testing.T) {
	dir := t.TempDir()
	cfg := fmt.Sprintf("project: test\nroot: %s\nroles:\n  - eng1\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0644)

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"config", "get", "nonexistent_key"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestFormatIntPtr(t *testing.T) {
	if got := formatIntPtr(nil); got != "" {
		t.Errorf("formatIntPtr(nil) = %q, want empty", got)
	}
	v := 42
	if got := formatIntPtr(&v); got != "42" {
		t.Errorf("formatIntPtr(42) = %q, want '42'", got)
	}
}
