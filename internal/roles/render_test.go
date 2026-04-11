package roles

import (
	"strings"
	"testing"
)

func TestRender_FullSubstitution(t *testing.T) {
	tmpl := "Project: {{project_name}}, Root: {{project_root}}"
	vars := RenderVars{
		ProjectName: "testapp",
		ProjectRoot: "/home/user/testapp",
	}

	got := Render(tmpl, vars)
	want := "Project: testapp, Root: /home/user/testapp"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRender_PartialVars(t *testing.T) {
	tmpl := "Stack: {{tech_stack}}, Build: {{build_cmd}}, Test: {{test_cmd}}"
	vars := RenderVars{
		TechStack: "Go 1.23",
		// BuildCmd and TestCmd intentionally empty
	}

	got := Render(tmpl, vars)
	if !strings.Contains(got, "Stack: Go 1.23") {
		t.Errorf("known var not substituted: %q", got)
	}
	if !strings.Contains(got, "{{build_cmd}}") {
		t.Errorf("empty var should be preserved: %q", got)
	}
	if !strings.Contains(got, "{{test_cmd}}") {
		t.Errorf("empty var should be preserved: %q", got)
	}
}

func TestRender_UnknownVarsPreserved(t *testing.T) {
	tmpl := "Hello {{project_name}}, your {{custom_field}} is ready"
	vars := RenderVars{ProjectName: "myapp"}

	got := Render(tmpl, vars)
	if !strings.Contains(got, "Hello myapp") {
		t.Errorf("known var not substituted: %q", got)
	}
	if !strings.Contains(got, "{{custom_field}}") {
		t.Errorf("unknown var should be preserved: %q", got)
	}
}

func TestRender_NoVars(t *testing.T) {
	tmpl := "No variables here, just plain text."
	got := Render(tmpl, RenderVars{})
	if got != tmpl {
		t.Errorf("got %q, want %q", got, tmpl)
	}
}

func TestRender_AllVars(t *testing.T) {
	vars := RenderVars{
		ProjectName: "p",
		ProjectRoot: "r",
		TechStack:   "t",
		BuildCmd:    "b",
		TestCmd:     "x",
	}

	tmpl := "{{project_name}}|{{project_root}}|{{tech_stack}}|{{build_cmd}}|{{test_cmd}}"
	got := Render(tmpl, vars)
	want := "p|r|t|b|x"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRender_MultipleOccurrences(t *testing.T) {
	tmpl := "{{project_name}} is great. I love {{project_name}}."
	vars := RenderVars{ProjectName: "initech"}

	got := Render(tmpl, vars)
	want := "initech is great. I love initech."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderString(t *testing.T) {
	tmpl := "Hello {{name}}, welcome to {{place}}"
	got := RenderString(tmpl, "name", "Nelson")
	if got != "Hello Nelson, welcome to {{place}}" {
		t.Errorf("got %q", got)
	}
}
