package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/scaffold"
)

// ini-vby5.1: add-agent --regen rewrites a generated charter from its
// template and refuses one an operator edited, unless --force.

// regenProject writes a project with eng1 whose CLAUDE.md is charter, and
// returns the charter path and the project as loaded.
func regenProject(t *testing.T, charter string) (string, *config.Project) {
	t.Helper()
	root := t.TempDir()
	writeTestConfig(t, root, []string{"super", "eng1"})
	path := filepath.Join(root, "eng1", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(charter), 0644); err != nil {
		t.Fatal(err)
	}
	chdirTemp(t, root)
	p, err := config.Load(filepath.Join(root, "initech.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return path, p
}

// runRegen drives the real command path with --regen (and --force) set.
func runRegen(t *testing.T, role string, force bool) error {
	t.Helper()
	addAgentRegen, addAgentForce = true, force
	t.Cleanup(func() { addAgentRegen, addAgentForce = false, false })
	var buf bytes.Buffer
	addAgentCmd.SetOut(&buf)
	t.Cleanup(func() { addAgentCmd.SetOut(nil) })
	return runAddAgent(addAgentCmd, []string{role})
}

// olderCharter is a charter generated from an older template: a valid header
// whose hash matches its (outdated) body.
func olderCharter() string {
	body := "# CLAUDE.md\n\nan older template's text\n"
	return roles.CharterHeader("eng", "eng1", body) + body
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAddAgentRegen_RewritesAnUneditedCharterFromTheCurrentTemplate(t *testing.T) {
	path, p := regenProject(t, olderCharter())
	if err := runRegen(t, "eng1", false); err != nil {
		t.Fatalf("--regen on an unedited charter: %v", err)
	}
	if got, want := readFile(t, path), scaffold.RenderCharter(p, p.Root, "eng1"); got != want {
		t.Fatalf("charter after --regen is not the current template:\n%.300s", got)
	}
}

func TestAddAgentRegen_RefusesAHandEditedCharterWithoutForce(t *testing.T) {
	edited := olderCharter() + "\nMy own rule: always run the slow suite.\n"
	path, _ := regenProject(t, edited)
	err := runRegen(t, "eng1", false)
	if err == nil || !strings.Contains(err.Error(), "edited") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a refusal naming the edit and --force", err)
	}
	if readFile(t, path) != edited {
		t.Fatal("a refused --regen changed the file")
	}
}

func TestAddAgentRegen_RefusesACharterWithNoGeneratedHeaderWithoutForce(t *testing.T) {
	hand := "# CLAUDE.md\n\nwritten by hand\n"
	path, _ := regenProject(t, hand)
	err := runRegen(t, "eng1", false)
	if err == nil || !strings.Contains(err.Error(), "no generated header") {
		t.Fatalf("err = %v, want a refusal for a headerless charter", err)
	}
	if readFile(t, path) != hand {
		t.Fatal("a refused --regen changed the file")
	}
}

func TestAddAgentRegen_ForceOverwritesAHandEditedCharter(t *testing.T) {
	path, p := regenProject(t, olderCharter()+"\nMy own rule.\n")
	if err := runRegen(t, "eng1", true); err != nil {
		t.Fatalf("--regen --force: %v", err)
	}
	if readFile(t, path) != scaffold.RenderCharter(p, p.Root, "eng1") {
		t.Fatal("--force did not rewrite the charter")
	}
}

func TestAddAgentRegen_RefusesARoleNotInTheProject(t *testing.T) {
	regenProject(t, olderCharter())
	err := runRegen(t, "qa1", false)
	if err == nil || !strings.Contains(err.Error(), "not in this project") {
		t.Fatalf("err = %v, want a refusal for a role outside initech.yaml", err)
	}
}
