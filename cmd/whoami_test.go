package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWhoami_BasicOutput(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	// Create a minimal initech.yaml with peer_name.
	cfgContent := fmt.Sprintf("project: testproject\nroot: %s\npeer_name: laptop\nroles:\n  - eng1\n", dir)
	if err := os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}
	// Create a CLAUDE.md in the dir.
	claudePath := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte("# test"), 0644); err != nil {
		t.Fatal(err)
	}
	// Create agent dir so validation passes.
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	t.Setenv("INITECH_ROLE", "eng1")
	t.Setenv("INITECH_AGENT", "")

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"whoami"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "role:      eng1 (from INITECH_ROLE)") {
		t.Errorf("missing role with source in output: %s", out)
	}
	if !strings.Contains(out, "peer:      laptop") {
		t.Errorf("missing peer in output: %s", out)
	}
	if !strings.Contains(out, "directory: "+dir) {
		t.Errorf("missing directory in output: %s", out)
	}
	if !strings.Contains(out, "claude.md: "+claudePath) {
		t.Errorf("missing claude.md path in output: %s", out)
	}
}

func TestWhoami_NoRoleEnv(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	t.Setenv("INITECH_ROLE", "")
	t.Setenv("INITECH_AGENT", "")

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"whoami"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "role:      (not set)") {
		t.Errorf("expected '(not set)' for role: %s", out)
	}
}

func TestWhoami_NoConfig(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	t.Setenv("INITECH_ROLE", "qa1")
	t.Setenv("INITECH_AGENT", "")

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"whoami"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "peer:      (not set)") {
		t.Errorf("expected '(not set)' for peer: %s", out)
	}
	if !strings.Contains(out, "claude.md: (none)") {
		t.Errorf("expected '(none)' for claude.md: %s", out)
	}
}

// --- detectRole tests ---

func TestDetectRole_EnvRole(t *testing.T) {
	t.Setenv("INITECH_ROLE", "eng1")
	t.Setenv("INITECH_AGENT", "qa1")

	display, raw := detectRole("/tmp/whatever", nil)
	if raw != "eng1" {
		t.Errorf("raw = %q, want eng1", raw)
	}
	if display != "eng1 (from INITECH_ROLE)" {
		t.Errorf("display = %q, want %q", display, "eng1 (from INITECH_ROLE)")
	}
}

func TestDetectRole_EnvAgent(t *testing.T) {
	t.Setenv("INITECH_ROLE", "")
	t.Setenv("INITECH_AGENT", "qa2")

	display, raw := detectRole("/tmp/whatever", nil)
	if raw != "qa2" {
		t.Errorf("raw = %q, want qa2", raw)
	}
	if display != "qa2 (from INITECH_AGENT)" {
		t.Errorf("display = %q, want %q", display, "qa2 (from INITECH_AGENT)")
	}
}

func TestDetectRole_DirFallback_CatalogRole(t *testing.T) {
	t.Setenv("INITECH_ROLE", "")
	t.Setenv("INITECH_AGENT", "")

	// "eng2" is in the roles catalog.
	dir := filepath.Join(t.TempDir(), "eng2")
	os.MkdirAll(dir, 0755)

	display, raw := detectRole(dir, nil)
	if raw != "eng2" {
		t.Errorf("raw = %q, want eng2", raw)
	}
	if display != "eng2 (from directory name)" {
		t.Errorf("display = %q, want %q", display, "eng2 (from directory name)")
	}
}

func TestDetectRole_DirFallback_ConfigRole(t *testing.T) {
	t.Setenv("INITECH_ROLE", "")
	t.Setenv("INITECH_AGENT", "")

	// "designer" is NOT in the catalog but IS in the config roles list.
	dir := filepath.Join(t.TempDir(), "designer")
	os.MkdirAll(dir, 0755)

	display, raw := detectRole(dir, []string{"designer", "dba"})
	if raw != "designer" {
		t.Errorf("raw = %q, want designer", raw)
	}
	if display != "designer (from directory name)" {
		t.Errorf("display = %q, want %q", display, "designer (from directory name)")
	}
}

func TestDetectRole_NotSet(t *testing.T) {
	t.Setenv("INITECH_ROLE", "")
	t.Setenv("INITECH_AGENT", "")

	// Temp dir name won't match any role.
	dir := t.TempDir()

	display, raw := detectRole(dir, nil)
	if raw != "" {
		t.Errorf("raw = %q, want empty", raw)
	}
	if display != "(not set)" {
		t.Errorf("display = %q, want %q", display, "(not set)")
	}
}

// --- detectRoleFromDir tests ---

func TestDetectRoleFromDir_WalksUp(t *testing.T) {
	// Create /tmp/.../eng1/sub/deep, should walk up and find "eng1".
	root := filepath.Join(t.TempDir(), "eng1", "sub", "deep")
	os.MkdirAll(root, 0755)

	got := detectRoleFromDir(root, nil)
	if got != "eng1" {
		t.Errorf("detectRoleFromDir = %q, want eng1", got)
	}
}

func TestDetectRoleFromDir_ConfigRoleWalksUp(t *testing.T) {
	// Custom role "myagent" not in catalog but in config roles.
	root := filepath.Join(t.TempDir(), "myagent", "src")
	os.MkdirAll(root, 0755)

	got := detectRoleFromDir(root, []string{"myagent"})
	if got != "myagent" {
		t.Errorf("detectRoleFromDir = %q, want myagent", got)
	}
}

func TestDetectRoleFromDir_NoMatch(t *testing.T) {
	dir := t.TempDir()
	got := detectRoleFromDir(dir, []string{"eng1", "qa1"})
	if got != "" {
		t.Errorf("detectRoleFromDir = %q, want empty", got)
	}
}

func TestDetectRole_EnvRoleTakesPriority(t *testing.T) {
	t.Setenv("INITECH_ROLE", "super")
	t.Setenv("INITECH_AGENT", "eng1")

	// Even though dir name is "eng2" (catalog match), INITECH_ROLE wins.
	dir := filepath.Join(t.TempDir(), "eng2")
	os.MkdirAll(dir, 0755)

	_, raw := detectRole(dir, []string{"eng2"})
	if raw != "super" {
		t.Errorf("INITECH_ROLE should take priority, got raw = %q", raw)
	}
}

func TestDetectRole_EnvAgentBeforeDir(t *testing.T) {
	t.Setenv("INITECH_ROLE", "")
	t.Setenv("INITECH_AGENT", "pm")

	// Dir name is "eng1" (catalog match), but INITECH_AGENT wins.
	dir := filepath.Join(t.TempDir(), "eng1")
	os.MkdirAll(dir, 0755)

	_, raw := detectRole(dir, nil)
	if raw != "pm" {
		t.Errorf("INITECH_AGENT should take priority over dir, got raw = %q", raw)
	}
}

func TestFindClaudeMD_WalksUp(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b", "c")
	os.MkdirAll(sub, 0755)

	// Place CLAUDE.md at root level.
	claudePath := filepath.Join(root, "CLAUDE.md")
	os.WriteFile(claudePath, []byte("# root"), 0644)

	got := findClaudeMD(sub)
	if got != claudePath {
		t.Errorf("got %q, want %q", got, claudePath)
	}
}

func TestFindClaudeMD_NotFound(t *testing.T) {
	dir := t.TempDir()
	got := findClaudeMD(dir)
	if got != "(none)" {
		t.Errorf("got %q, want %q", got, "(none)")
	}
}
