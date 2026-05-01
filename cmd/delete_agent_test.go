package cmd

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

func TestRunDeleteAgent_RemovesFromConfig(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm", "arch", "eng1"})
	os.MkdirAll(filepath.Join(root, "arch"), 0755)

	chdirTemp(t, root)

	var buf bytes.Buffer
	deleteAgentCmd.SetOut(&buf)
	defer deleteAgentCmd.SetOut(nil)

	if err := runDeleteAgent(deleteAgentCmd, []string{"arch"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, err := config.Load(filepath.Join(root, "initech.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range updated.Roles {
		if r == "arch" {
			t.Error("arch should be removed from roles")
		}
	}
	if len(updated.Roles) != 2 {
		t.Errorf("roles count = %d, want 2", len(updated.Roles))
	}
}

func TestRunDeleteAgent_RemovesRoleOverrides(t *testing.T) {
	root := t.TempDir()
	p := &config.Project{
		Name:  "test",
		Root:  root,
		Roles: []string{"pm", "eng1"},
		Beads: config.BeadsConfig{Enabled: boolPtr(false)},
		RoleOverrides: map[string]config.RoleOverride{
			"eng1": {AgentType: "codex"},
		},
	}
	config.Write(filepath.Join(root, "initech.yaml"), p)

	chdirTemp(t, root)

	var buf bytes.Buffer
	deleteAgentCmd.SetOut(&buf)
	defer deleteAgentCmd.SetOut(nil)

	if err := runDeleteAgent(deleteAgentCmd, []string{"eng1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, err := config.Load(filepath.Join(root, "initech.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := updated.RoleOverrides["eng1"]; ok {
		t.Error("eng1 role_overrides should be removed")
	}
}

func TestRunDeleteAgent_NotFound(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm"})

	chdirTemp(t, root)

	err := runDeleteAgent(deleteAgentCmd, []string{"eng1"})
	if err == nil {
		t.Fatal("expected error for missing role")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want to contain 'not found'", err.Error())
	}
}

func TestRunDeleteAgent_PreservesWorkspaceByDefault(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm", "arch"})
	wsDir := filepath.Join(root, "arch")
	os.MkdirAll(wsDir, 0755)
	os.WriteFile(filepath.Join(wsDir, "CLAUDE.md"), []byte("test"), 0644)

	chdirTemp(t, root)

	var buf bytes.Buffer
	deleteAgentCmd.SetOut(&buf)
	defer deleteAgentCmd.SetOut(nil)

	if err := runDeleteAgent(deleteAgentCmd, []string{"arch"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(wsDir); err != nil {
		t.Error("workspace should be preserved without --purge")
	}
}

func TestRunDeleteAgent_PurgeDeletesWorkspace(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm", "arch"})
	wsDir := filepath.Join(root, "arch")
	os.MkdirAll(wsDir, 0755)
	os.WriteFile(filepath.Join(wsDir, "CLAUDE.md"), []byte("test"), 0644)

	chdirTemp(t, root)

	// Simulate "y" confirmation.
	origReader := confirmReader
	confirmReader = func() *bufio.Reader {
		return bufio.NewReader(strings.NewReader("y\n"))
	}
	defer func() { confirmReader = origReader }()

	origPurge := deleteAgentPurge
	deleteAgentPurge = true
	defer func() { deleteAgentPurge = origPurge }()

	var buf bytes.Buffer
	deleteAgentCmd.SetOut(&buf)
	defer deleteAgentCmd.SetOut(nil)

	if err := runDeleteAgent(deleteAgentCmd, []string{"arch"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(wsDir); !os.IsNotExist(err) {
		t.Error("workspace should be deleted with --purge")
	}
	if !strings.Contains(buf.String(), "Deleted") {
		t.Errorf("output missing delete confirmation: %q", buf.String())
	}
}

func TestRunDeleteAgent_PurgeDenied(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm", "arch"})
	wsDir := filepath.Join(root, "arch")
	os.MkdirAll(wsDir, 0755)

	chdirTemp(t, root)

	origReader := confirmReader
	confirmReader = func() *bufio.Reader {
		return bufio.NewReader(strings.NewReader("n\n"))
	}
	defer func() { confirmReader = origReader }()

	origPurge := deleteAgentPurge
	deleteAgentPurge = true
	defer func() { deleteAgentPurge = origPurge }()

	var buf bytes.Buffer
	deleteAgentCmd.SetOut(&buf)
	defer deleteAgentCmd.SetOut(nil)

	if err := runDeleteAgent(deleteAgentCmd, []string{"arch"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(wsDir); err != nil {
		t.Error("workspace should be preserved when confirmation denied")
	}
	if !strings.Contains(buf.String(), "preserved") {
		t.Errorf("output missing preserved message: %q", buf.String())
	}
}

func TestRunDeleteAgent_NoConfig(t *testing.T) {
	root := t.TempDir()
	chdirTemp(t, root)

	err := runDeleteAgent(deleteAgentCmd, []string{"arch"})
	if err == nil {
		t.Fatal("expected error when no initech.yaml")
	}
	if !strings.Contains(err.Error(), "initech.yaml") {
		t.Errorf("error = %q, want to mention initech.yaml", err.Error())
	}
}

func TestDeleteAgentCmd_HasFireAlias(t *testing.T) {
	found := false
	for _, alias := range deleteAgentCmd.Aliases {
		if alias == "fire" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("delete-agent missing 'fire' alias, got %v", deleteAgentCmd.Aliases)
	}
}
