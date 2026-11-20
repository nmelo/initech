package roles

import (
	"fmt"
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
	sections := []string{"Identity", "Critical Failure Modes", "What Goes to the Operator",
		"Dispatching Work", "Patrol", "Managing the Agent Roster"}
	for _, s := range sections {
		if !strings.Contains(out, s) {
			t.Errorf("missing section: %s", s)
		}
	}
	// Shared sections live in the root CLAUDE.md now (ini-vby5.1).
	for _, s := range []string{"## Communication", "## Bead Lifecycle", "## Project Documents", "## Tools", "## Learning Protocol"} {
		if strings.Contains(out, s) {
			t.Errorf("super still carries %q; it belongs in the root CLAUDE.md", s)
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
		"Workflow", "Code Quality", "Tech Stack"}
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
		"Verdict Rules", "Adversarial Testing", "Pre-Mortem Review"}
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
			for _, section := range []string{"Identity", "Decision Authority", "Workflow"} {
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

// ── The operator's inbox guide (ini-3wkl.1) ─────────────────────────
//
// Asserted on RENDERED output for EVERY role, not on the template source:
// the guide is one constant spliced into twelve templates, so a splice that
// silently failed to apply, or a rule trimmed in an unrelated edit, must be
// red here. Each rule gets its own assertion — five of them — so dropping
// one is a failure and not a quiet trim (AC 2).
//
// These strings are the product surface for a rule the product's own suite
// can never test: no assertion can observe an agent choosing to keep working
// after posting. The text is the guard; these tests keep it present.

// allRoleTemplates is every template an agent can be rendered from. A new
// role template that forgets the inbox guide fails the tests below, which is
// the point: an undocumented channel is an unused one.
func allRoleTemplates() map[string]string {
	return map[string]string{
		"super": SuperTemplate, "eng": EngTemplate, "qa": QATemplate,
		"pm": PMTemplate, "arch": ArchTemplate, "sec": SecTemplate,
		"shipper": ShipperTemplate, "pmm": PMMTemplate, "writer": WriterTemplate,
		"ops": OpsTemplate, "growth": GrowthTemplate, "intern": InternTemplate,
	}
}

func renderForRole(t *testing.T, tmpl string) string {
	t.Helper()
	return Render(tmpl, RenderVars{ProjectName: "testproject", ProjectRoot: "/tmp/testproject"})
}

// ini-vby5.1: a role template carries only the role's delta. The shared
// sections live in the root CLAUDE.md, so none of their text may reappear in
// any role.
func TestRoleTemplates_CarryNoSharedSections(t *testing.T) {
	shared := []string{
		"## Communication", "## Announcement Rule", "## Tools",
		"initech post", "### The operator's inbox",
		"describe WHAT happened, not WHICH bead", "Say what happened, not which bead",
	}
	for role, tmpl := range allRoleTemplates() {
		for _, s := range shared {
			if strings.Contains(tmpl, s) {
				t.Errorf("%s's template still carries %q; it belongs in the root CLAUDE.md", role, s)
			}
		}
	}
}

func TestRoleTemplates_OpenWithThePointerToTheRoot(t *testing.T) {
	const pointer = "The root CLAUDE.md applies to you; this file adds only what {{role_name}} does differently."
	for role, tmpl := range allRoleTemplates() {
		if !strings.HasPrefix(tmpl, "# CLAUDE.md\n\n"+pointer+"\n") {
			t.Errorf("%s's template does not open with the pointer to the root", role)
		}
	}
}

// ini-1q3x: supers avoided assign for fold-ins and untracked work, so the TUI
// never learned of them. The generated super charter says to use it for
// every hand-off to an engineer.
func TestSuperTemplate_UsesAssignForEveryHandOffToAnEngineer(t *testing.T) {
	const want = "Use `initech assign` for every hand-off to an engineer, including fold-ins, re-dispatches and work with no bead."
	if !strings.Contains(SuperTemplate, want) {
		t.Errorf("SuperTemplate is missing the line %q", want)
	}
}

// The inbox is removed (ini-jxjl.3): the fleet reference and the root's
// shared sections name no initech post and no inbox. The twin, the fleet
// reference's own command list, shows the text was read.
func TestFleetFiles_NameNoInbox(t *testing.T) {
	texts := map[string]string{"fleet/reference.md": renderForRole(t, FleetReferenceTemplate)}
	for i, sec := range RootSections(nil) {
		texts[fmt.Sprintf("root section %d", i)] = sec
	}
	for name, text := range texts {
		low := strings.ToLower(text)
		for _, gone := range []string{"initech post", "inbox"} {
			if strings.Contains(low, gone) {
				t.Errorf("%s still names %q", name, gone)
			}
		}
	}
	if !strings.Contains(texts["fleet/reference.md"], "initech peek <agent>") {
		t.Error("control: the fleet reference lost its command list, so the check read nothing")
	}
}
