package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestConfigList_ContainsExpectedKeys(t *testing.T) {
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "list"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, key := range []string{
		"project", "root", "roles", "mcp_port", "mcp_token",
		"beads.enabled", "beads.prefix", "webhook_url",
		"slack.app_token", "remotes.<name>.addr",
		"role_overrides.<role>.agent_type",
	} {
		if !strings.Contains(out, key) {
			t.Errorf("output missing key %q", key)
		}
	}
}

func TestConfigList_HasHeader(t *testing.T) {
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "list"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(buf.String(), "\n")
	if len(lines) == 0 || !strings.Contains(lines[0], "KEY") {
		t.Error("expected header row with KEY column")
	}
	if !strings.Contains(lines[0], "TYPE") {
		t.Error("expected header row with TYPE column")
	}
}

func TestConfigList_EnvVarAnnotation(t *testing.T) {
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "list"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "(env: INITECH_MCP_TOKEN)") {
		t.Error("missing env var annotation for mcp_token")
	}
	if !strings.Contains(out, "(env: INITECH_SLACK_APP_TOKEN)") {
		t.Error("missing env var annotation for slack.app_token")
	}
}

func TestConfigList_NoInitechYamlRequired(t *testing.T) {
	// Run from a temp dir with no config.
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"config", "list"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("config list should work without initech.yaml: %v", err)
	}
	if !strings.Contains(buf.String(), "project") {
		t.Error("expected output even without config file")
	}
}
