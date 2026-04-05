package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestConfigValidate_ValidConfig(t *testing.T) {
	restoreColor := disableColor(t)
	defer restoreColor()

	dir := t.TempDir()
	yaml := fmt.Sprintf("project: testproj\nroot: %s\nwebhook_url: https://hooks.test\nannounce_url: https://announce.test\nroles:\n  - eng1\nbeads:\n  prefix: ini\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := runConfigValidate(cmd, nil)
	if err != nil {
		t.Fatalf("runConfigValidate should not error for valid config: %v\noutput:\n%s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "PASS") {
		t.Errorf("valid config should have PASS checks:\n%s", got)
	}
	if strings.Contains(got, "ERROR") {
		t.Errorf("valid config should have no ERRORs:\n%s", got)
	}
	if !strings.Contains(got, "0 error(s)") {
		t.Errorf("summary should show 0 errors:\n%s", got)
	}
}

func TestConfigValidate_MissingProject(t *testing.T) {
	restoreColor := disableColor(t)
	defer restoreColor()

	dir := t.TempDir()
	yaml := fmt.Sprintf("root: %s\nroles:\n  - eng1\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := runConfigValidate(cmd, nil)
	if err == nil {
		t.Fatal("expected error for missing project")
	}

	got := out.String()
	if !strings.Contains(got, "project") && !strings.Contains(got, "required") {
		t.Errorf("should flag missing project field:\n%s", got)
	}
}

func TestConfigValidate_BadRoleOverride(t *testing.T) {
	restoreColor := disableColor(t)
	defer restoreColor()

	dir := t.TempDir()
	yaml := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - eng1\nrole_overrides:\n  eng99:\n    agent_type: codex\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := runConfigValidate(cmd, nil)
	if err == nil {
		t.Fatal("expected error for bad role override")
	}

	got := out.String()
	if !strings.Contains(got, "eng99") {
		t.Errorf("should flag nonexistent role in overrides:\n%s", got)
	}
	if !strings.Contains(got, "not in roles") {
		t.Errorf("should explain the role is not in roles list:\n%s", got)
	}
}

func TestConfigValidate_BadPort(t *testing.T) {
	restoreColor := disableColor(t)
	defer restoreColor()

	dir := t.TempDir()
	yaml := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - eng1\nmcp_port: 99999\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := runConfigValidate(cmd, nil)
	if err == nil {
		t.Fatal("expected error for out of range port")
	}

	got := out.String()
	if !strings.Contains(got, "mcp_port") && !strings.Contains(got, "out of range") {
		t.Errorf("should flag bad port:\n%s", got)
	}
}

func TestConfigValidate_WebhookInfo(t *testing.T) {
	restoreColor := disableColor(t)
	defer restoreColor()

	dir := t.TempDir()
	yaml := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - eng1\nbeads:\n  prefix: ini\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := runConfigValidate(cmd, nil)
	// Should not error (webhook/announce are INFO, not ERROR).
	if err != nil {
		t.Fatalf("INFO notes should not cause error: %v\noutput:\n%s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "webhook_url") {
		t.Errorf("should note missing webhook_url:\n%s", got)
	}
	if !strings.Contains(got, "announce_url") {
		t.Errorf("should note missing announce_url:\n%s", got)
	}
}

func TestConfigValidate_UnknownFields(t *testing.T) {
	restoreColor := disableColor(t)
	defer restoreColor()

	dir := t.TempDir()
	yaml := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - eng1\nbeads:\n  prefix: ini\ntypo_field: oops\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	_ = runConfigValidate(cmd, nil) // May or may not error.

	got := out.String()
	if !strings.Contains(got, "typo_field") {
		t.Errorf("should flag unknown field:\n%s", got)
	}
	if !strings.Contains(got, "unknown field") {
		t.Errorf("should label as unknown field:\n%s", got)
	}
}

func TestConfigValidate_MalformedRemote(t *testing.T) {
	restoreColor := disableColor(t)
	defer restoreColor()

	dir := t.TempDir()
	yaml := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - eng1\nremotes:\n  bad:\n    addr: noporthere\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := runConfigValidate(cmd, nil)
	if err == nil {
		t.Fatal("expected error for malformed remote addr")
	}

	got := out.String()
	if !strings.Contains(got, "remotes.bad") {
		t.Errorf("should flag malformed remote:\n%s", got)
	}
}

func TestConfigValidate_YAMLParseError(t *testing.T) {
	restoreColor := disableColor(t)
	defer restoreColor()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte("{{invalid yaml"), 0644)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := runConfigValidate(cmd, nil)
	if err == nil {
		t.Fatal("expected error for malformed yaml")
	}
}

func TestConfigValidate_BeadsPrefixWarn(t *testing.T) {
	restoreColor := disableColor(t)
	defer restoreColor()

	dir := t.TempDir()
	yaml := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - eng1\nwebhook_url: https://hooks.test\nannounce_url: https://announce.test\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)
	os.MkdirAll(filepath.Join(dir, "eng1"), 0755)

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := runConfigValidate(cmd, nil)
	// Should not error (beads prefix is a warning).
	if err != nil {
		t.Fatalf("beads prefix warning should not cause error: %v\noutput:\n%s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "beads.prefix") {
		t.Errorf("should warn about missing beads prefix:\n%s", got)
	}
}
