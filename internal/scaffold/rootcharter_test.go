package scaffold

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
)

// ini-vby5.1: the root CLAUDE.md is the shared rulebook; a charter carries
// only its role's delta, under a generated header.

// everyRole is every catalog role plus a custom one, so the caps and the
// header hold for the fallback template too.
func everyRole() []string {
	var out []string
	for name := range roles.Catalog {
		out = append(out, name)
	}
	return append(out, "datasci")
}

func sampleProject() *config.Project {
	return &config.Project{Name: "acme", Root: "/work/acme", Roles: everyRole()}
}

func TestRenderRootCLAUDE_CarriesEverySharedSectionOnceInOrder(t *testing.T) {
	root := RenderRootCLAUDE(sampleProject())
	order := []string{
		"## Project Documents", "## Roles", "## How we work",
		"## Escalation\n", "## Sensitive changes\n", "## Docs gate\n", "## Evidence\n",
		"## QA pairs\n", "## Filing and closing\n", "## Messaging\n", "### The operator's inbox",
		"## Budget\n", "## Shared machine\n", "## Fleet files\n", "## Beads\n", "## Learning protocol\n",
	}
	last := -1
	for _, h := range order {
		if n := strings.Count(root, h); n != 1 {
			t.Fatalf("root carries %q %d times, want exactly once", h, n)
		}
		at := strings.Index(root, h)
		if at < last {
			t.Fatalf("%q is out of order in the root", h)
		}
		last = at
	}
	for _, rule := range []string{"Post with a default and keep going", "Say what happened, not which bead", "Never `pkill -f`", "Push before marking ready_for_qa"} {
		if strings.Count(root, rule) != 1 {
			t.Errorf("root carries %q %d times, want once", rule, strings.Count(root, rule))
		}
	}
}

func TestRenderRootCLAUDE_ListsEachRoleWithWhatItOwns(t *testing.T) {
	root := RenderRootCLAUDE(&config.Project{Name: "acme", Roles: []string{"super", "eng2", "datasci"}})
	for _, want := range []string{
		"- `super/` - " + roles.RoleSummary("super"),
		"- `eng2/` - " + roles.RoleSummary("eng"),
		"- `datasci/` - Custom role",
	} {
		if !strings.Contains(root, want+"\n") {
			t.Errorf("root Roles list missing %q", want)
		}
	}
}

func TestRenderRootCLAUDE_StaysUnderItsSizeCap(t *testing.T) {
	if n := strings.Count(RenderRootCLAUDE(sampleProject()), "\n"); n >= 300 {
		t.Errorf("root is %d lines with every catalog role, want under 300", n)
	}
}

func TestRenderCharter_EveryRoleStartsWithAValidGeneratedHeader(t *testing.T) {
	p := sampleProject()
	for _, r := range p.Roles {
		c := RenderCharter(p, p.Root, r)
		if !strings.HasPrefix(c, "<!-- generated from the "+TemplateNameForRole(r)+" template.") {
			t.Errorf("%s: charter does not open with the generated header:\n%.200s", r, c)
		}
		body, hasHeader, unedited := roles.ParseCharter(c)
		if !hasHeader || !unedited {
			t.Errorf("%s: hasHeader=%v unedited=%v, want a valid hash line", r, hasHeader, unedited)
		}
		if !strings.HasPrefix(body, "# CLAUDE.md\n\nThe root CLAUDE.md applies to you; this file adds only what "+r+" does differently.\n") {
			t.Errorf("%s: body does not open with the pointer to the root", r)
		}
	}
}

func TestRenderCharter_NoRoleCarriesTheSharedSections(t *testing.T) {
	p := sampleProject()
	for _, r := range p.Roles {
		c := RenderCharter(p, p.Root, r)
		for _, s := range []string{"## Communication", "## Announcement Rule", "initech post", "### The operator's inbox", "## Shared machine"} {
			if strings.Contains(c, s) {
				t.Errorf("%s's charter carries %q; it belongs in the root", r, s)
			}
		}
	}
}

func TestRenderCharter_EveryRoleStaysUnderItsSizeCap(t *testing.T) {
	p := sampleProject()
	for _, r := range p.Roles {
		if n := strings.Count(RenderCharter(p, p.Root, r), "\n"); n >= 100 {
			t.Errorf("%s's charter is %d lines, want under 100", r, n)
		}
	}
}
