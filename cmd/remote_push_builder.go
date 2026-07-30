package cmd

import (
	"os"
	"path"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/scaffold"
	"github.com/nmelo/initech/internal/tui"
)

func init() {
	tui.SetConfigureAgentBuilder(buildRemoteConfigureAgentCmd)
}

// buildRemoteConfigureAgentCmd builds a configure_agent payload for pushing
// roleName to a zero-config remote daemon (ini-om0). Mirrors
// buildAgentPaneConfig's local command/agent-type resolution, but:
//   - targets the remote's workspace root (remote.EffectiveRoot) instead of
//     the local project root, joined with path.Join (not filepath.Join) since
//     the remote is a Unix path regardless of the client's own OS;
//   - does not check directory existence -- the daemon creates the workspace
//     itself from ClaudeMD/RootClaudeMD (writeWorkspace), unlike a local pane
//     whose directory must already exist;
//   - renders and sends CLAUDE.md content directly (ClaudeMD/RootClaudeMD)
//     rather than relying on scaffold to have written files to a local disk
//     the remote host doesn't share.
//
// Registered against tui.SetConfigureAgentBuilder in this file's init(), so
// it runs before any peer connection can be attempted -- previously nothing
// in cmd/ called SetConfigureAgentBuilder at all, so pushRolesToPeer always
// used the placeholder builder (which just errors), and no client could ever
// establish ownership of a remote agent.
func buildRemoteConfigureAgentCmd(roleName string, project *config.Project, remote config.Remote) (tui.ConfigureAgentCmd, error) {
	ov, hasOverride := project.RoleOverrides[roleName]

	var argv []string
	if mock := os.Getenv("INITECH_MOCK_AGENT"); mock != "" {
		argv = []string{mock}
	} else if hasOverride && len(ov.Command) > 0 {
		argv = append(argv, ov.Command...)
	} else {
		if len(project.ClaudeCommand) > 0 {
			argv = append(argv, project.ClaudeCommand...)
		} else {
			argv = []string{"claude"}
		}
		var roleArgs []string
		if hasOverride {
			roleArgs = ov.ClaudeArgs
		}
		if resolved := roles.ResolveClaudeArgs(roleName, project.ClaudeArgs, roleArgs); len(resolved) > 0 {
			argv = append(argv, resolved...)
		}
	}

	root := remote.EffectiveRoot(project.Name)
	dir := path.Join(root, roleName)
	if hasOverride && ov.Dir != "" {
		dir = ov.Dir
	}

	agentType, noBracketedPaste, submitKey := resolvePaneBehavior(ov)

	roleVars := roles.RenderVars{ProjectName: project.Name, ProjectRoot: root}
	if hasOverride {
		if ov.TechStack != "" {
			roleVars.TechStack = ov.TechStack
		}
		if ov.BuildCmd != "" {
			roleVars.BuildCmd = ov.BuildCmd
		}
		if ov.TestCmd != "" {
			roleVars.TestCmd = ov.TestCmd
		}
	}
	claudeMD := roles.Render(scaffold.TemplateForRole(roleName), roleVars)
	claudeMD = roles.RenderString(claudeMD, "role_name", roleName)

	return tui.ConfigureAgentCmd{
		Command:          argv,
		Dir:              dir,
		AgentType:        agentType,
		NoBracketedPaste: noBracketedPaste,
		SubmitKey:        submitKey,
		ClaudeMD:         claudeMD,
		RootClaudeMD:     scaffold.RenderRootCLAUDE(project),
	}, nil
}
