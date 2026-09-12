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

// flattenWhitespace collapses every run of whitespace to one space so a
// prose assertion survives re-wrapping. A sentence that moved across a line
// break is the same sentence; a sentence that was DELETED is what these
// tests are for, and that still reds.
func flattenWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// containsPhrase reports whether haystack contains phrase, both flattened.
func containsPhrase(haystack, phrase string) bool {
	return strings.Contains(flattenWhitespace(haystack), flattenWhitespace(phrase))
}

// communicationSection returns the rendered template's "## Communication"
// section: from its heading to the next top-level heading, or to the end.
func communicationSection(t *testing.T, role, out string) string {
	t.Helper()
	const heading = "## Communication"
	i := strings.Index(out, heading)
	if i < 0 {
		t.Fatalf("%s: no Communication section", role)
	}
	rest := out[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// AC 1: the rendered template documents initech post with all six flags.
func TestOperatorInboxGuide_EveryRoleDocumentsPostAndItsFlags(t *testing.T) {
	flags := []string{"--default", "--chime", "--check", "--mine", "--withdraw", "--stdin", "-f "}
	for role, tmpl := range allRoleTemplates() {
		t.Run(role, func(t *testing.T) {
			out := renderForRole(t, tmpl)
			if !strings.Contains(out, "initech post") {
				t.Fatalf("%s's template never mentions initech post — an undocumented channel is an unused one", role)
			}
			for _, f := range flags {
				if !strings.Contains(out, f) {
					t.Errorf("%s: flag %q missing from the rendered template", role, f)
				}
			}
		})
	}
}

// AC 2: each of the five agent-side rules, asserted separately.
func TestOperatorInboxGuide_EveryRoleCarriesAllFiveRules(t *testing.T) {
	rules := []struct{ name, phrase string }{
		{"post with a default and keep going", "Post with a default and keep going"},
		{"what belongs here", "Only what the operator alone can decide or know"},
		{"dismissed means no", "Dismissed means no"},
		{"when to chime", "Chime only when your work is stopped on the answer"},
		{"never poll", "Never poll"},
	}
	for role, tmpl := range allRoleTemplates() {
		out := renderForRole(t, tmpl)
		for _, r := range rules {
			t.Run(role+"/"+r.name, func(t *testing.T) {
				if !containsPhrase(out, r.phrase) {
					t.Errorf("%s: rule %q missing (looked for %q). These five rules are the only thing enforcing non-blocking behaviour; a trimmed one is a guard that stopped existing.", role, r.name, r.phrase)
				}
			})
		}
	}
}

// AC 3: the worked example, and BOTH of its shapes. Agents copy examples, and
// one shape teaches one shape (pm grooming): a question that proceeds on its
// default with the reply arriving later, AND a heads-up expecting nothing.
func TestOperatorInboxGuide_WorkedExampleCarriesBothShapes(t *testing.T) {
	for role, tmpl := range allRoleTemplates() {
		t.Run(role, func(t *testing.T) {
			out := renderForRole(t, tmpl)
			if !containsPhrase(out, "Worked example") {
				t.Fatalf("%s: no worked example — a rule without one is advice", role)
			}
			// Shape (a): posts with a default, proceeds immediately, and the
			// reply arrives later while the agent is on other work.
			for _, marker := range []string{
				`--default "update both"`,
				"You do not wait",
				"the reply lands in your pane",
				"the operator's answer replaces your default",
			} {
				if !containsPhrase(out, marker) {
					t.Errorf("%s: the question-with-a-default shape is missing %q", role, marker)
				}
			}
			// Shape (b): a heads-up that expects nothing back.
			for _, marker := range []string{
				"A heads-up that expects nothing back",
				"You do not check it and you do not wait for a reply",
			} {
				if !containsPhrase(out, marker) {
					t.Errorf("%s: the heads-up shape is missing %q", role, marker)
				}
			}
		})
	}
}

// The guide sits WITH the other communication tools, where an agent reading
// about messaging will meet it (spec: "document initech post beside initech
// send"). Scoped to the section: most templates mention initech send in an
// earlier section too, so a document-wide check would pass for a guide
// appended to the end of the file.
func TestOperatorInboxGuide_SitsWithTheOtherCommunicationTools(t *testing.T) {
	for role, tmpl := range allRoleTemplates() {
		t.Run(role, func(t *testing.T) {
			section := communicationSection(t, role, renderForRole(t, tmpl))
			if !strings.Contains(section, "initech post") {
				t.Errorf("%s: the inbox guide is not in the Communication section", role)
			}
			if !strings.Contains(section, "initech send") {
				t.Errorf("%s: the Communication section does not mention initech send; the guide is meant to sit beside it", role)
			}
		})
	}
}
