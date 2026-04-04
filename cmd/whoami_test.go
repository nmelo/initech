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
	if !strings.Contains(out, "role:      eng1") {
		t.Errorf("missing role in output: %s", out)
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
