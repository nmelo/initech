package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/spf13/cobra"
)

func TestConfigShow_BasicOutput(t *testing.T) {
	dir := t.TempDir()
	yaml := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - super\n  - eng1\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "super"), 0755)
	os.WriteFile(filepath.Join(dir, "super", "CLAUDE.md"), []byte("# super"), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)
	os.WriteFile(filepath.Join(dir, "eng1", "CLAUDE.md"), []byte("# eng1"), 0644)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runConfigShow(cmd, nil); err != nil {
		t.Fatalf("runConfigShow: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"project",
		"testproj",
		"(yaml)",
		"roles",
		"[super, eng1]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestConfigShow_SecretMasking(t *testing.T) {
	dir := t.TempDir()
	yaml := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - eng1\ntoken: mysecret123\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)
	os.WriteFile(filepath.Join(dir, "eng1", "CLAUDE.md"), []byte("# eng1"), 0644)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	// Without --reveal: secret should be masked.
	configShowReveal = false
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runConfigShow(cmd, nil); err != nil {
		t.Fatalf("runConfigShow: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "mysecret123") {
		t.Fatalf("secret should be masked:\n%s", got)
	}
	if !strings.Contains(got, "****") {
		t.Fatalf("masked value should show ****:\n%s", got)
	}
}

func TestConfigShow_RevealFlag(t *testing.T) {
	dir := t.TempDir()
	yaml := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - eng1\ntoken: mysecret123\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)
	os.WriteFile(filepath.Join(dir, "eng1", "CLAUDE.md"), []byte("# eng1"), 0644)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	configShowReveal = true
	defer func() { configShowReveal = false }()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runConfigShow(cmd, nil); err != nil {
		t.Fatalf("runConfigShow: %v", err)
	}

	if !strings.Contains(out.String(), "mysecret123") {
		t.Fatalf("--reveal should show actual secret:\n%s", out.String())
	}
}

func TestConfigShow_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	yaml := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - eng1\nmcp_token: fromyaml\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)
	os.WriteFile(filepath.Join(dir, "eng1", "CLAUDE.md"), []byte("# eng1"), 0644)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	t.Setenv("INITECH_MCP_TOKEN", "fromenv")

	configShowReveal = true
	defer func() { configShowReveal = false }()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runConfigShow(cmd, nil); err != nil {
		t.Fatalf("runConfigShow: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "fromenv") {
		t.Fatalf("env override value should appear:\n%s", got)
	}
	if !strings.Contains(got, "(env: INITECH_MCP_TOKEN)") {
		t.Fatalf("source should show env var name:\n%s", got)
	}
}

func TestConfigShow_MapExpansion(t *testing.T) {
	dir := t.TempDir()
	yaml := fmt.Sprintf(`project: testproj
root: %s
roles:
  - eng1
remotes:
  workbench:
    addr: 192.168.1.100:7391
role_overrides:
  eng1:
    agent_type: codex
    command: [codex]
`, dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)
	os.WriteFile(filepath.Join(dir, "eng1", "CLAUDE.md"), []byte("# eng1"), 0644)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	configShowReveal = false
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runConfigShow(cmd, nil); err != nil {
		t.Fatalf("runConfigShow: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"remotes.workbench.addr",
		"192.168.1.100:7391",
		"role_overrides.eng1.agent_type",
		"codex",
		"role_overrides.eng1.command",
		"[codex]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestConfigShow_DefaultSource(t *testing.T) {
	dir := t.TempDir()
	yaml := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - eng1\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)
	os.WriteFile(filepath.Join(dir, "eng1", "CLAUDE.md"), []byte("# eng1"), 0644)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runConfigShow(cmd, nil); err != nil {
		t.Fatalf("runConfigShow: %v", err)
	}

	got := out.String()
	// mcp_bind has default "0.0.0.0" and is not in yaml, so source should be (default).
	if !strings.Contains(got, "(default)") {
		t.Errorf("should show (default) for fields with default values:\n%s", got)
	}
	// webhook_url is empty and not in yaml.
	if !strings.Contains(got, "(not set)") {
		t.Errorf("should show (not set) for empty unconfigured fields:\n%s", got)
	}
}

func TestResolveFieldValue_AllKeys(t *testing.T) {
	bTrue := true
	port := 9100
	proj := &config.Project{
		Name:        "test",
		Root:        "/tmp/test",
		Roles:       []string{"eng1"},
		ClaudeCommand: []string{"claude"},
		PeerName:    "laptop",
		WebPort:     &port,
		Beads:       config.BeadsConfig{Enabled: &bTrue, Prefix: "ini"},
	}

	cases := []struct {
		key  string
		want string
	}{
		{"project", "test"},
		{"root", "/tmp/test"},
		{"roles", "[eng1]"},
		{"claude_command", "[claude]"},
		{"peer_name", "laptop"},
		{"web_port", "9100"},
		{"beads.enabled", "true"},
		{"beads.prefix", "ini"},
		{"nonexistent", ""},
	}

	for _, tc := range cases {
		got := resolveFieldValue(proj, tc.key)
		if got != tc.want {
			t.Errorf("resolveFieldValue(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestExpandRoleOverride_AllFields(t *testing.T) {
	auto := true
	ov := config.RoleOverride{
		Command:          []string{"claude", "--fast"},
		ClaudeArgs:       []string{"--continue"},
		AgentType:        "claude_code",
		Dir:              "/custom",
		AutoApprove:      &auto,
		SubmitKey:        "enter",
		NoBracketedPaste: true,
		TechStack:        "Go",
		BuildCmd:         "make build",
		TestCmd:          "make test",
		RepoName:         "initech",
	}
	lines := expandRoleOverride("eng1", ov)
	if len(lines) != 11 {
		t.Errorf("expected 11 lines, got %d", len(lines))
	}
	keys := make(map[string]bool)
	for _, l := range lines {
		keys[l.Key] = true
	}
	for _, k := range []string{
		"role_overrides.eng1.command",
		"role_overrides.eng1.agent_type",
		"role_overrides.eng1.auto_approve",
		"role_overrides.eng1.tech_stack",
		"role_overrides.eng1.repo_name",
	} {
		if !keys[k] {
			t.Errorf("missing key %q in output", k)
		}
	}
}

func TestExpandRoleOverride_EmptyOverride(t *testing.T) {
	lines := expandRoleOverride("eng1", config.RoleOverride{})
	if len(lines) != 0 {
		t.Errorf("empty override should produce 0 lines, got %d", len(lines))
	}
}

func TestRunConfigShow_ViaCommand(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "initech.yaml")
	cfg := fmt.Sprintf("project: showcmd\nroot: %s\nroles:\n  - eng1\n", dir)
	os.WriteFile(cfgPath, []byte(cfg), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "show"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "showcmd") {
		t.Errorf("output should contain project name, got: %s", buf.String())
	}
}

func TestRunConfigShow_WithReveal(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "initech.yaml")
	cfg := fmt.Sprintf("project: revealpj\nroot: %s\nroles:\n  - eng1\ntoken: secret123\n", dir)
	os.WriteFile(cfgPath, []byte(cfg), 0600)

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "show", "--reveal"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "secret123") {
		t.Errorf("--reveal should show token value, got: %s", buf.String())
	}
}

func TestParseYAMLKeys(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "initech.yaml")
	yaml := "project: test\nroot: /tmp\nroles:\n  - eng1\nremotes:\n  workbench:\n    addr: 192.168.1.100\n"
	os.WriteFile(cfgPath, []byte(yaml), 0644)

	keys, err := parseYAMLKeys(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, k := range []string{"project", "root", "roles", "remotes.workbench.addr"} {
		if !keys[k] {
			t.Errorf("missing key %q", k)
		}
	}
}

func TestResolveAllFields(t *testing.T) {
	proj := &config.Project{
		Name:  "test",
		Root:  "/tmp",
		Roles: []string{"eng1"},
		RoleOverrides: map[string]config.RoleOverride{
			"eng1": {AgentType: "claude_code"},
		},
		Remotes: map[string]config.Remote{
			"wb": {Addr: "192.168.1.100"},
		},
	}
	yamlKeys := map[string]bool{"project": true, "root": true, "roles": true}
	lines := resolveAllFields(proj, yamlKeys)
	if len(lines) == 0 {
		t.Error("expected config lines")
	}
	found := false
	for _, l := range lines {
		if l.Key == "project" && l.Value == "test" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'project' key with value 'test'")
	}
}
