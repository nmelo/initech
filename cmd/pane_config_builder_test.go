package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildReloadingPaneConfigBuilder_ReloadsConfigFromDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "eng1"), 0755); err != nil {
		t.Fatalf("mkdir eng1: %v", err)
	}

	cfgPath := filepath.Join(dir, "initech.yaml")
	writeConfig := func(command string) {
		t.Helper()
		yaml := "project: test\n" +
			"root: " + dir + "\n" +
			"roles:\n" +
			"  - eng1\n" +
			"role_overrides:\n" +
			"  eng1:\n" +
			"    command:\n" +
			"      - /bin/sh\n" +
			"      - -c\n" +
			"      - " + command + "\n"
		if err := os.WriteFile(cfgPath, []byte(yaml), 0600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}

	writeConfig("echo first")
	builder := buildReloadingPaneConfigBuilder(cfgPath, buildAgentPaneConfig)

	cfg1, err := builder("eng1")
	if err != nil {
		t.Fatalf("first builder call: %v", err)
	}
	if got := cfg1.Command[len(cfg1.Command)-1]; got != "echo first" {
		t.Fatalf("first command tail = %q, want %q", got, "echo first")
	}

	writeConfig("echo second")

	cfg2, err := builder("eng1")
	if err != nil {
		t.Fatalf("second builder call: %v", err)
	}
	if got := cfg2.Command[len(cfg2.Command)-1]; got != "echo second" {
		t.Fatalf("second command tail = %q, want %q", got, "echo second")
	}
}

func TestBuildReloadingPaneConfigBuilder_ReturnsReloadError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "initech.yaml")
	if err := os.WriteFile(cfgPath, []byte("project: ["), 0600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}

	builder := buildReloadingPaneConfigBuilder(cfgPath, buildAgentPaneConfig)
	if _, err := builder("eng1"); err == nil {
		t.Fatal("expected config reload error")
	}
}
