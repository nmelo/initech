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

// TestUnrenderedPlaceholders_FindsWhatRenderLeftBehind pins the guard's
// contract: it reports every line still holding a "{{", with the line number,
// and reports nothing for clean text.
//
// The malformed cases are the point of the function existing. Render's
// varPattern is \{\{(\w+)\}\}, so it never even attempts "{{ tech_stack }}" or
// "{{build-cmd}}" — a guard built on varPattern would pass them straight to
// disk, where they are exactly as broken in an agent's instructions as a
// well-formed placeholder (ini-rg12).
func TestUnrenderedPlaceholders_FindsWhatRenderLeftBehind(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"clean", "# CLAUDE.md\n\nBuild: `make build`\n", 0},
		{"empty", "", 0},
		{"single brace is not a placeholder", "func f() { x := 1 }\n", 0},
		{"well formed", "Stack:\n{{tech_stack}}\n", 1},
		{"two lines", "{{tech_stack}}\nBuild: {{build_cmd}}\n", 2},
		{"two on one line", "{{build_cmd}} and {{test_cmd}}\n", 1},
		{"inner spaces", "Build: {{ build_cmd }}\n", 1},
		{"hyphen is not a word char", "Build: {{build-cmd}}\n", 1},
		{"empty braces", "Build: {{}}\n", 1},
		{"unterminated", "Build: {{build_cmd\n", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UnrenderedPlaceholders(tt.text)
			if len(got) != tt.want {
				t.Fatalf("UnrenderedPlaceholders(%q) = %v (%d), want %d", tt.text, got, len(got), tt.want)
			}
		})
	}
}

// TestUnrenderedPlaceholders_ReportsLocatableExcerpts verifies the report is
// usable as a diagnostic: the operator who hits the refusal needs the line
// number and the text, not just a count.
func TestUnrenderedPlaceholders_ReportsLocatableExcerpts(t *testing.T) {
	got := UnrenderedPlaceholders("one\ntwo\nBuild: {{build_cmd}}\n")
	if len(got) != 1 {
		t.Fatalf("got %v, want one excerpt", got)
	}
	if !strings.Contains(got[0], "line 3") {
		t.Errorf("excerpt = %q, want the 1-based line number", got[0])
	}
	if !strings.Contains(got[0], "build_cmd") {
		t.Errorf("excerpt = %q, want the offending text", got[0])
	}
}

// TestUnrenderedPlaceholders_TruncatesLongLines keeps one pathological line
// from burying the rest of the report.
func TestUnrenderedPlaceholders_TruncatesLongLines(t *testing.T) {
	got := UnrenderedPlaceholders("{{x}}" + strings.Repeat("y", 500))
	if len(got) != 1 {
		t.Fatalf("got %v, want one excerpt", got)
	}
	if len(got[0]) > 120 {
		t.Errorf("excerpt is %d chars, want it truncated", len(got[0]))
	}
	if !strings.HasSuffix(got[0], "...") {
		t.Errorf("excerpt = %q, want a truncation marker", got[0])
	}
}
