package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
)

func TestRunAddAgent_UnknownRole(t *testing.T) {
	err := runAddAgent(addAgentCmd, []string{"unknownrole"})
	if err == nil {
		t.Fatal("expected error for unknown role")
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("error = %q, want to contain 'unknown agent'", err.Error())
	}
}

func TestRunAddAgent_AlreadyExists(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm", "arch"})

	origWd := chdirTemp(t, root)
	defer os.Chdir(origWd)

	err := runAddAgent(addAgentCmd, []string{"arch"})
	if err == nil {
		t.Fatal("expected error for duplicate role")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want to contain 'already exists'", err.Error())
	}
}

func TestRunAddAgent_NoConfig(t *testing.T) {
	root := t.TempDir()
	origWd := chdirTemp(t, root)
	defer os.Chdir(origWd)

	err := runAddAgent(addAgentCmd, []string{"arch"})
	if err == nil {
		t.Fatal("expected error when no initech.yaml")
	}
	if !strings.Contains(err.Error(), "initech.yaml") {
		t.Errorf("error = %q, want to mention initech.yaml", err.Error())
	}
}

func TestRunAddAgent_Success(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm"})
	os.MkdirAll(filepath.Join(root, "pm"), 0755)

	origWd := chdirTemp(t, root)
	defer os.Chdir(origWd)

	origRunner := newAddAgentRunner
	newAddAgentRunner = func() iexec.Runner { return &iexec.FakeRunner{} }
	defer func() { newAddAgentRunner = origRunner }()

	var buf bytes.Buffer
	addAgentCmd.SetOut(&buf)
	if err := runAddAgent(addAgentCmd, []string{"arch"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "arch")); err != nil {
		t.Error("arch/ directory not created")
	}

	updated, err := config.Load(filepath.Join(root, "initech.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range updated.Roles {
		if r == "arch" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("arch not added to config roles: %v", updated.Roles)
	}

	if !strings.Contains(buf.String(), "arch") {
		t.Errorf("output missing role name: %q", buf.String())
	}
}

func TestRunAddAgent_SuccessOutput(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm"})
	os.MkdirAll(filepath.Join(root, "pm"), 0755)

	origWd := chdirTemp(t, root)
	defer os.Chdir(origWd)

	origRunner := newAddAgentRunner
	newAddAgentRunner = func() iexec.Runner { return &iexec.FakeRunner{} }
	defer func() { newAddAgentRunner = origRunner }()

	var buf bytes.Buffer
	addAgentCmd.SetOut(&buf)
	if err := runAddAgent(addAgentCmd, []string{"sec"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "initech.yaml") {
		t.Errorf("output missing initech.yaml mention: %q", out)
	}
	if !strings.Contains(out, "Restart") {
		t.Errorf("output missing restart instruction: %q", out)
	}
}

func TestRunAddAgentList_ShowsAllRoles(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm", "super"})

	origWd := chdirTemp(t, root)
	defer os.Chdir(origWd)

	var buf bytes.Buffer
	addAgentCmd.SetOut(&buf)
	addAgentList = true
	defer func() { addAgentList = false }()

	if err := runAddAgent(addAgentCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, name := range []string{"super", "pm", "arch", "eng1", "qa1", "shipper"} {
		if !strings.Contains(out, name) {
			t.Errorf("output missing role %q", name)
		}
	}
	if !strings.Contains(out, "✓") {
		t.Error("output missing installed checkmark")
	}
	if !strings.Contains(out, "-") {
		t.Error("output missing uninstalled marker")
	}
}

func writeTestConfig(t *testing.T, root string, roles []string) {
	t.Helper()
	p := &config.Project{
		Name:  "test",
		Root:  root,
		Roles: roles,
		Beads: config.BeadsConfig{Enabled: boolPtr(false)},
	}
	if err := config.Write(filepath.Join(root, "initech.yaml"), p); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func chdirTemp(t *testing.T, dir string) string {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return orig
}
