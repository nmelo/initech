package scaffold

import (
	"fmt"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
)

// UnsetBuildCmd and UnsetTestCmd are written in place of {{build_cmd}} and
// {{test_cmd}} when nothing configures them. Both placeholders appear INSIDE
// backticks in the role templates ("Build: `{{build_cmd}}`", "cd src &&
// {{test_cmd}}"), so the replacement has to read as a command slot an operator
// still has to fill. Angle brackets are this repo's existing convention for
// exactly that (<bead-id>, <agent-name> throughout the templates), and naming
// the initech.yaml key makes the text self-servicing: the agent reading its own
// CLAUDE.md can tell its operator what to set.
//
// These deliberately do NOT guess a build system. A wrong command an agent
// believes is worse than an obviously absent one: the agent runs it, gets a
// shell error, and has no idea the value was a guess (super's constraint on
// ini-rg12).
const (
	UnsetBuildCmd = "<build_cmd not set in initech.yaml>"
	UnsetTestCmd  = "<test_cmd not set in initech.yaml>"
)

// UnsetTechStack is the {{tech_stack}} text for a project that configures no
// stack. It names both keys because the fix differs by intent: one stack for
// the project, or one agent working in a different language.
func UnsetTechStack(roleName string) string {
	return fmt.Sprintf("Not configured. Set `tech_stack` in initech.yaml, "+
		"or `role_overrides.%s.tech_stack` to set it for this agent only.", roleName)
}

// RenderVarsFor resolves every template variable for one role, in precedence
// order: the role's own role_overrides entry, then the project-level key, then
// text stating the value is unset and naming the key to set.
//
// This is the ONE resolver. Three writers built this by hand before ini-rg12 —
// scaffold.Run (init and hire both land here), the census's coverage check, and
// the remote push builder that ships CLAUDE.md to another machine — and each
// copy read role_overrides only, so all three wrote literal "{{tech_stack}}"
// into agent instructions whenever no override existed. Duplicating the
// precedence rule per writer is a silent-omission machine: the next writer
// inherits nothing and nobody notices until an agent reads a placeholder as its
// tech stack.
//
// root is passed separately rather than read from p.Root because the remote
// builder renders for a DIFFERENT machine's directory layout; that is the only
// legitimate difference between the call sites, so it is the only parameter.
//
// Every field of the result is non-empty, which is the property the write guard
// depends on: roles.Render leaves a placeholder intact for an empty value, so
// "resolved to something" and "no placeholder reaches disk" are the same claim.
func RenderVarsFor(p *config.Project, root, roleName string) roles.RenderVars {
	vars := roles.RenderVars{
		ProjectName: p.Name,
		ProjectRoot: root,
		TechStack:   p.TechStack,
		BuildCmd:    p.BuildCmd,
		TestCmd:     p.TestCmd,
	}
	if ov, ok := p.RoleOverrides[roleName]; ok {
		if ov.TechStack != "" {
			vars.TechStack = ov.TechStack
		}
		if ov.BuildCmd != "" {
			vars.BuildCmd = ov.BuildCmd
		}
		if ov.TestCmd != "" {
			vars.TestCmd = ov.TestCmd
		}
	}
	if vars.TechStack == "" {
		vars.TechStack = UnsetTechStack(roleName)
	}
	if vars.BuildCmd == "" {
		vars.BuildCmd = UnsetBuildCmd
	}
	if vars.TestCmd == "" {
		vars.TestCmd = UnsetTestCmd
	}
	return vars
}
