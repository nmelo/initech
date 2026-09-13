package cmd

import (
	"fmt"
	"os"
	"path"
	"strings"

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

	// NO notification-channel injection here, deliberately (ini-2fd).
	//
	// The local pane builder injects preferredNotifChannel only after
	// confirming the operator's own settings resolution specifies none --
	// which requires READING their settings files. Those files live on the
	// REMOTE host, and this builder cannot see them. Injecting blind would
	// override an explicit choice made on that machine, which is the one
	// outcome the operator decision forbids; skipping leaves remote agents on
	// the pre-fix behaviour, which is the status quo rather than a regression.
	//
	// Closing this needs a way to read the remote's settings over the control
	// channel, tracked separately rather than decided here.
	root := remote.EffectiveRoot(project.Name)
	dir := path.Join(root, roleName)
	if hasOverride && ov.Dir != "" {
		dir = ov.Dir
	}

	agentType, noBracketedPaste, submitKey := resolvePaneBehavior(ov)

	// Shared with scaffold.Run so a remote agent's CLAUDE.md resolves exactly
	// as the same role's local one does; root differs because the remote has
	// its own directory layout, which is why it is a parameter (ini-rg12).
	roleVars := scaffold.RenderVarsFor(project, root, roleName)
	claudeMD := roles.Render(scaffold.TemplateForRole(roleName), roleVars)
	claudeMD = roles.RenderString(claudeMD, "role_name", roleName)
	rootMD := scaffold.RenderRootCLAUDE(project)
	// This writer does not go through scaffold's writeFile, so it repeats the
	// refusal here. Pushing a placeholder to another machine is the same defect
	// as writing one locally, and harder to notice from this side. Both
	// documents are checked rather than only the role one: the root document
	// interpolates the project name, and deciding by hand which strings "cannot"
	// contain a placeholder is how the original three writers each convinced
	// themselves they did not need a check.
	for _, doc := range []struct{ what, text string }{
		{roleName + "/CLAUDE.md", claudeMD},
		{"CLAUDE.md", rootMD},
	} {
		if left := roles.UnrenderedPlaceholders(doc.text); len(left) > 0 {
			return tui.ConfigureAgentCmd{}, fmt.Errorf("refusing to push %s with unrendered template variables:\n  %s",
				doc.what, strings.Join(left, "\n  "))
		}
	}

	return tui.ConfigureAgentCmd{
		Command:          argv,
		Dir:              dir,
		AgentType:        agentType,
		NoBracketedPaste: noBracketedPaste,
		SubmitKey:        submitKey,
		ClaudeMD:         claudeMD,
		RootClaudeMD:     rootMD,
	}, nil
}
