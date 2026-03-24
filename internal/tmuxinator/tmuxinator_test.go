package tmuxinator

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"gopkg.in/yaml.v3"
)

func minimalProject() *config.Project {
	return &config.Project{
		Name:  "testproject",
		Root:  "/home/user/testproject",
		Roles: []string{"super", "eng1", "qa1"},
		Grid:  []string{"super", "eng1"},
		Beads: config.BeadsConfig{Prefix: "tp"},
	}
}

func TestGenerate_BasicStructure(t *testing.T) {
	p := minimalProject()
	out, err := Generate(p)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Verify it's valid YAML
	var parsed map[string]any
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}

	if parsed["name"] != "testproject" {
		t.Errorf("name = %v, want testproject", parsed["name"])
	}
	if parsed["root"] != "/home/user/testproject" {
		t.Errorf("root = %v", parsed["root"])
	}
}

func TestGenerate_WindowCount(t *testing.T) {
	p := minimalProject()
	out, err := Generate(p)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var parsed map[string]any
	yaml.Unmarshal(out, &parsed)

	windows, ok := parsed["windows"].([]any)
	if !ok {
		t.Fatalf("windows is not a list: %T", parsed["windows"])
	}
	if len(windows) != 3 {
		t.Errorf("window count = %d, want 3", len(windows))
	}
}

func TestGenerate_PermissionTiers(t *testing.T) {
	p := minimalProject()
	out, err := Generate(p)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	s := string(out)

	// super is Supervised, should NOT have --dangerously-skip-permissions
	// eng1 is Autonomous, should have --dangerously-skip-permissions
	// We check by looking at the YAML content
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "super:") {
			if strings.Contains(trimmed, "dangerously-skip-permissions") {
				t.Error("super should not have --dangerously-skip-permissions")
			}
		}
		if strings.HasPrefix(trimmed, "eng1:") {
			if !strings.Contains(trimmed, "dangerously-skip-permissions") {
				t.Error("eng1 should have --dangerously-skip-permissions")
			}
		}
	}
}

func TestGenerate_PreWindow(t *testing.T) {
	p := minimalProject()
	out, err := Generate(p)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "BEADS_DIR") {
		t.Error("pre_window should set BEADS_DIR")
	}
	if !strings.Contains(s, "/home/user/testproject/.beads") {
		t.Error("BEADS_DIR should point to project's .beads directory")
	}
}

func TestGenerate_NoBeadsPrefix(t *testing.T) {
	p := minimalProject()
	p.Beads.Prefix = ""

	out, err := Generate(p)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if strings.Contains(string(out), "pre_window") {
		t.Error("should not set pre_window when beads prefix is empty")
	}
}

func TestGenerateGrid(t *testing.T) {
	p := minimalProject()
	out, err := GenerateGrid(p)
	if err != nil {
		t.Fatalf("GenerateGrid: %v", err)
	}

	var parsed map[string]any
	yaml.Unmarshal(out, &parsed)

	if parsed["name"] != "testproject-grid" {
		t.Errorf("grid name = %v, want testproject-grid", parsed["name"])
	}

	windows, ok := parsed["windows"].([]any)
	if !ok {
		t.Fatalf("windows is not a list")
	}
	if len(windows) != 2 {
		t.Errorf("grid window count = %d, want 2 (super + eng1)", len(windows))
	}
}

func TestGenerateGrid_EmptyGrid(t *testing.T) {
	p := minimalProject()
	p.Grid = nil

	out, err := GenerateGrid(p)
	if err != nil {
		t.Fatalf("GenerateGrid: %v", err)
	}
	if out != nil {
		t.Errorf("empty grid should return nil, got %d bytes", len(out))
	}
}

func TestGenerate_UnknownRole(t *testing.T) {
	p := &config.Project{
		Name:  "custom",
		Root:  "/tmp/custom",
		Roles: []string{"designer"},
	}

	out, err := Generate(p)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	s := string(out)
	// Unknown roles default to Autonomous
	if !strings.Contains(s, "dangerously-skip-permissions") {
		t.Error("unknown role should get Autonomous permissions")
	}
}
