package config

import (
	"os"
	"path/filepath"
	"testing"
)

const validYAML = `project: testproject
root: /tmp/testproject
repos:
  - url: git@github.com:test/repo.git
    name: repo
beads:
  prefix: tp
roles:
  - super
  - eng1
  - qa1
grid:
  - super
  - eng1
role_overrides:
  eng1:
    tech_stack: "Go 1.23"
    build_cmd: "go build ./..."
    test_cmd: "go test ./..."
`

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "initech.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, validYAML)

	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if p.Name != "testproject" {
		t.Errorf("Name = %q, want %q", p.Name, "testproject")
	}
	if p.Root != "/tmp/testproject" {
		t.Errorf("Root = %q, want %q", p.Root, "/tmp/testproject")
	}
	if len(p.Repos) != 1 {
		t.Fatalf("Repos = %d, want 1", len(p.Repos))
	}
	if p.Repos[0].URL != "git@github.com:test/repo.git" {
		t.Errorf("Repo URL = %q", p.Repos[0].URL)
	}
	if p.Beads.Prefix != "tp" {
		t.Errorf("Beads.Prefix = %q, want %q", p.Beads.Prefix, "tp")
	}
	if len(p.Roles) != 3 {
		t.Errorf("Roles = %d, want 3", len(p.Roles))
	}
	if len(p.Grid) != 2 {
		t.Errorf("Grid = %d, want 2", len(p.Grid))
	}
	if ov, ok := p.RoleOverrides["eng1"]; !ok {
		t.Error("missing eng1 override")
	} else if ov.TechStack != "Go 1.23" {
		t.Errorf("eng1 TechStack = %q", ov.TechStack)
	}
}

func TestLoad_ExpandHome(t *testing.T) {
	dir := t.TempDir()
	yaml := `project: test
root: ~/projects/test
roles:
  - super
`
	path := writeConfig(t, dir, yaml)

	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	home, _ := os.UserHomeDir()
	want := filepath.Join(home, "projects/test")
	if p.Root != want {
		t.Errorf("Root = %q, want %q", p.Root, want)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/initech.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "{{invalid yaml}}")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestDiscover(t *testing.T) {
	// Create a nested directory structure with config at the top
	root := t.TempDir()
	writeConfig(t, root, validYAML)

	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := Discover(nested)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	want := filepath.Join(root, "initech.yaml")
	if got != want {
		t.Errorf("Discover = %q, want %q", got, want)
	}
}

func TestDiscover_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := Discover(dir)
	if err == nil {
		t.Fatal("expected error when no initech.yaml exists")
	}
}

func TestValidate_Valid(t *testing.T) {
	p := &Project{
		Name:  "test",
		Root:  "/tmp/test",
		Roles: []string{"super", "eng1"},
		Grid:  []string{"super"},
		RoleOverrides: map[string]RoleOverride{
			"eng1": {TechStack: "Go"},
		},
	}
	if err := Validate(p); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_MissingName(t *testing.T) {
	p := &Project{Root: "/tmp", Roles: []string{"super"}}
	if err := Validate(p); err == nil {
		t.Error("expected error for missing name")
	}
}

func TestValidate_MissingRoot(t *testing.T) {
	p := &Project{Name: "test", Roles: []string{"super"}}
	if err := Validate(p); err == nil {
		t.Error("expected error for missing root")
	}
}

func TestValidate_NoRoles(t *testing.T) {
	p := &Project{Name: "test", Root: "/tmp"}
	if err := Validate(p); err == nil {
		t.Error("expected error for empty roles")
	}
}

func TestValidate_DuplicateRole(t *testing.T) {
	p := &Project{Name: "test", Root: "/tmp", Roles: []string{"eng1", "eng1"}}
	if err := Validate(p); err == nil {
		t.Error("expected error for duplicate role")
	}
}

func TestValidate_GridNotInRoles(t *testing.T) {
	p := &Project{
		Name:  "test",
		Root:  "/tmp",
		Roles: []string{"super"},
		Grid:  []string{"eng1"},
	}
	if err := Validate(p); err == nil {
		t.Error("expected error for grid role not in roles")
	}
}

func TestValidate_OverrideNotInRoles(t *testing.T) {
	p := &Project{
		Name:  "test",
		Root:  "/tmp",
		Roles: []string{"super"},
		RoleOverrides: map[string]RoleOverride{
			"eng1": {TechStack: "Go"},
		},
	}
	if err := Validate(p); err == nil {
		t.Error("expected error for override not in roles")
	}
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")

	p := &Project{
		Name:  "roundtrip",
		Root:  "/tmp/roundtrip",
		Roles: []string{"super", "eng1"},
	}

	if err := Write(path, p); err != nil {
		t.Fatalf("Write: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Write: %v", err)
	}
	if loaded.Name != "roundtrip" {
		t.Errorf("roundtrip Name = %q", loaded.Name)
	}
	if len(loaded.Roles) != 2 {
		t.Errorf("roundtrip Roles = %d", len(loaded.Roles))
	}
}
