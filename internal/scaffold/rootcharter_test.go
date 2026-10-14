package scaffold

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
)

// ini-vby5.1: the root CLAUDE.md is the shared rulebook, one section per
// shared rule.

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
