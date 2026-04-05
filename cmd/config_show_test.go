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
