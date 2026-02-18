package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

func testProject(root string) *config.Project {
	return &config.Project{
		Name:  "testproject",
		Root:  root,
		Repos: []config.Repo{{URL: "git@github.com:test/repo.git", Name: "repo"}},
		Beads: config.BeadsConfig{Prefix: "tp"},
		Roles: []string{"super", "eng1", "qa1"},
		Grid:  []string{"super", "eng1"},
		RoleOverrides: map[string]config.RoleOverride{
			"eng1": {TechStack: "Go 1.23", BuildCmd: "go build ./...", TestCmd: "go test ./..."},
		},
	}
}

func TestRun_CreatesDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	p := testProject(root)

	_, err := Run(p, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	dirs := []string{
		"",
		"docs",
		"super",
		"super/.claude",
		"eng1",
		"eng1/.claude",
		"qa1",
		"qa1/.claude",
		"qa1/playbooks",
	}
	for _, d := range dirs {
		path := filepath.Join(root, d)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("directory %q not created: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", d)
		}
	}
}

func TestRun_CreatesFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	p := testProject(root)

	created, err := Run(p, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	files := []string{
		".gitignore",
		"CLAUDE.md",
		"AGENTS.md",
		"docs/prd.md",
		"docs/spec.md",
		"docs/systemdesign.md",
		"docs/roadmap.md",
		"super/CLAUDE.md",
		"eng1/CLAUDE.md",
		"qa1/CLAUDE.md",
	}
	for _, f := range files {
		path := filepath.Join(root, f)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("file %q not created: %v", f, err)
		}
	}

	// Verify created list contains expected entries
	if len(created) != len(files) {
		t.Errorf("created list has %d entries, want %d: %v", len(created), len(files), created)
	}
}

func TestRun_RootCLAUDE(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	p := testProject(root)
	Run(p, Options{})

	data, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	content := string(data)

	if !strings.Contains(content, "testproject") {
		t.Error("root CLAUDE.md should contain project name")
	}
	if !strings.Contains(content, "docs/prd.md") {
		t.Error("root CLAUDE.md should reference project documents")
	}
	if !strings.Contains(content, "super") {
		t.Error("root CLAUDE.md should list roles")
	}
}

func TestRun_RoleCLAUDE_SuperTemplate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	p := testProject(root)
	Run(p, Options{})

	data, _ := os.ReadFile(filepath.Join(root, "super", "CLAUDE.md"))
	content := string(data)

	if !strings.Contains(content, "Supervisor") {
		t.Error("super should use SuperTemplate")
	}
	if !strings.Contains(content, "testproject") {
		t.Error("project_name not substituted in super template")
	}
}

func TestRun_RoleCLAUDE_EngTemplate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	p := testProject(root)
	Run(p, Options{})

	data, _ := os.ReadFile(filepath.Join(root, "eng1", "CLAUDE.md"))
	content := string(data)

	if !strings.Contains(content, "Engineer") {
		t.Error("eng1 should use EngTemplate")
	}
	if !strings.Contains(content, "Go 1.23") {
		t.Error("tech_stack override not applied")
	}
	if strings.Contains(content, "{{role_name}}") {
		t.Error("role_name should be substituted with eng1")
	}
	if !strings.Contains(content, "eng1") {
		t.Error("role_name substitution missing")
	}
}

func TestRun_RoleCLAUDE_QATemplate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	p := testProject(root)
	Run(p, Options{})

	data, _ := os.ReadFile(filepath.Join(root, "qa1", "CLAUDE.md"))
	content := string(data)

	if !strings.Contains(content, "QA") {
		t.Error("qa1 should use QATemplate")
	}
}

func TestRun_DocTemplates(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	p := testProject(root)
	Run(p, Options{})

	tests := []struct {
		file    string
		contain string
	}{
		{"docs/prd.md", "Problem Statement"},
		{"docs/spec.md", "Core Model"},
		{"docs/systemdesign.md", "Module Structure"},
		{"docs/roadmap.md", "Phase 0: Discovery and Design"},
	}

	for _, tt := range tests {
		data, err := os.ReadFile(filepath.Join(root, tt.file))
		if err != nil {
			t.Errorf("read %s: %v", tt.file, err)
			continue
		}
		if !strings.Contains(string(data), tt.contain) {
			t.Errorf("%s should contain %q", tt.file, tt.contain)
		}
		if strings.Contains(string(data), "{{project_name}}") {
			t.Errorf("%s should have project_name substituted", tt.file)
		}
	}
}

func TestRun_Idempotent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	p := testProject(root)

	// First run
	Run(p, Options{})

	// Modify a file
	customContent := "# Custom content that should survive"
	os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(customContent), 0644)

	// Second run without force
	created, err := Run(p, Options{Force: false})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	// CLAUDE.md should not be in created list (it was skipped)
	for _, c := range created {
		if c == "CLAUDE.md" {
			t.Error("CLAUDE.md should not be overwritten without force")
		}
	}

	// Verify content preserved
	data, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if string(data) != customContent {
		t.Error("existing file was overwritten without force")
	}
}

func TestRun_Force(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	p := testProject(root)

	// First run
	Run(p, Options{})

	// Modify a file
	os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("custom"), 0644)

	// Second run with force
	_, err := Run(p, Options{Force: true})
	if err != nil {
		t.Fatalf("force Run: %v", err)
	}

	// Verify content was overwritten
	data, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if string(data) == "custom" {
		t.Error("force should overwrite existing files")
	}
}
