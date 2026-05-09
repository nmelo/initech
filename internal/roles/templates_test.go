package roles

import (
	"strings"
	"testing"
)

func TestSuperTemplate_Renders(t *testing.T) {
	vars := RenderVars{ProjectName: "testproject"}
	out := Render(SuperTemplate, vars)

	if strings.Contains(out, "{{project_name}}") {
		t.Error("project_name not substituted")
	}
	if !strings.Contains(out, "testproject") {
		t.Error("project name missing from output")
	}

	// Verify key sections exist
	sections := []string{"Identity", "Critical Failure Modes", "Decision Authority",
		"Dispatching Work", "Monitoring", "Communication", "Bead Lifecycle", "Project Documents", "Tools"}
	for _, s := range sections {
		if !strings.Contains(out, s) {
			t.Errorf("missing section: %s", s)
		}
	}
}

func TestEngTemplate_Renders(t *testing.T) {
	vars := RenderVars{
		ProjectName: "testproject",
		ProjectRoot: "/home/user/testproject",
		TechStack:   "Go 1.23",
		BuildCmd:    "go build ./...",
		TestCmd:     "go test ./...",
	}
	out := Render(EngTemplate, vars)

	if strings.Contains(out, "{{project_name}}") {
		t.Error("project_name not substituted")
	}
	if !strings.Contains(out, "Go 1.23") {
		t.Error("tech_stack not substituted")
	}

	// role_name is not in RenderVars, should be preserved for per-role substitution
	if !strings.Contains(out, "{{role_name}}") {
		t.Error("role_name should be preserved (not in RenderVars)")
	}

	sections := []string{"Identity", "Critical Failure Modes", "Decision Authority",
		"Workflow", "Verification Before Completion", "Code Quality", "Communication", "Tech Stack"}
	for _, s := range sections {
		if !strings.Contains(out, s) {
			t.Errorf("missing section: %s", s)
		}
	}
}

func TestQATemplate_Renders(t *testing.T) {
	vars := RenderVars{ProjectName: "testproject", ProjectRoot: "/tmp/test"}
	out := Render(QATemplate, vars)

	if strings.Contains(out, "{{project_name}}") {
		t.Error("project_name not substituted")
	}

	sections := []string{"Identity", "Critical Failure Modes", "Workflow",
		"Verdict Rules", "Communication"}
	for _, s := range sections {
		if !strings.Contains(out, s) {
			t.Errorf("missing section: %s", s)
		}
	}
}

func TestAllRoleTemplates_Render(t *testing.T) {
	vars := RenderVars{
		ProjectName: "testproject",
		ProjectRoot: "/home/user/testproject",
	}

	tests := []struct {
		name     string
		template string
		contains string // at least one role-specific string to verify identity
	}{
		{"PM", PMTemplate, "Product Manager"},
		{"Arch", ArchTemplate, "Architect"},
		{"Sec", SecTemplate, "Security"},
		{"Shipper", ShipperTemplate, "Shipper"},
		{"PMM", PMMTemplate, "Product Marketing"},
		{"Writer", WriterTemplate, "Technical Writer"},
		{"Ops", OpsTemplate, "Operations"},
		{"Growth", GrowthTemplate, "Growth Engineer"},
		{"Intern", InternTemplate, "Intern"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := Render(tt.template, vars)

			if strings.Contains(out, "{{project_name}}") {
				t.Errorf("%s: project_name not substituted", tt.name)
			}
			if !strings.Contains(out, "testproject") {
				t.Errorf("%s: project name missing", tt.name)
			}
			if !strings.Contains(out, tt.contains) {
				t.Errorf("%s: missing identity string %q", tt.name, tt.contains)
			}
			// All templates should have these core sections
			for _, section := range []string{"Identity", "Communication"} {
				if !strings.Contains(out, section) {
					t.Errorf("%s: missing section %q", tt.name, section)
				}
			}
		})
	}
}

func TestDocTemplates_Render(t *testing.T) {
	vars := RenderVars{ProjectName: "myapp"}

	tests := []struct {
		name     string
		template string
		sections []string
	}{
		{"PRD", PRDTemplate, []string{"Problem Statement", "User", "Success Criteria", "Non-Goals", "User Journeys", "Risks", "Scope Boundaries"}},
		{"Spec", SpecTemplate, []string{"Core Model", "Components", "Behaviors", "Data Model", "Constraints"}},
		{"SystemDesign", SystemDesignTemplate, []string{"Module Structure", "Data Structures", "Core Algorithms", "Command Implementations", "Testing Strategy", "Build Order"}},
		{"Roadmap", RoadmapTemplate, []string{"Phase 0", "Discovery and Design", "Phase 1", "Milestone Summary", "Agent Allocation", "Risk Gates"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := Render(tt.template, vars)

			if strings.Contains(out, "{{project_name}}") {
				t.Error("project_name not substituted")
			}
			if !strings.Contains(out, "myapp") {
				t.Error("project name missing from output")
			}

			for _, s := range tt.sections {
				if !strings.Contains(out, s) {
					t.Errorf("missing section: %s", s)
				}
			}
		})
	}
}

func TestRoadmapTemplate_HasPhaseZero(t *testing.T) {
	out := Render(RoadmapTemplate, RenderVars{ProjectName: "test"})

	if !strings.Contains(out, "Phase 0: Discovery and Design") {
		t.Error("roadmap should have Phase 0 pre-filled")
	}
	if !strings.Contains(out, "PM writes docs/prd.md") {
		t.Error("roadmap Phase 0 should include PM work item")
	}
	if !strings.Contains(out, "Success gate") {
		t.Error("roadmap Phase 0 should include success gate")
	}
}
