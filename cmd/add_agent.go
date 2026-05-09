package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/git"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/scaffold"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var newAddAgentRunner = func() iexec.Runner { return &iexec.DefaultRunner{} }

var addAgentList bool

var addAgentCmd = &cobra.Command{
	Use:     "add-agent <name>",
	Aliases: []string{"hire"},
	Short:   "Add a new agent workspace to the project",
	Args:    cobra.MaximumNArgs(1),
	RunE:    runAddAgent,
}

func init() {
	addAgentCmd.Long = fmt.Sprintf(`Scaffolds a new agent workspace directory and registers it in initech.yaml.

The agent name must be a known role or a member of a numbered family
(eng1..N, qa1..N). The role must not already exist in the project.

Known roles: %s.
Numbered families: eng1..N, qa1..N (e.g. eng7, qa10).

Restart initech (or run 'initech' in a new session) to activate the new agent.`, knownRoleNames())
	addAgentCmd.Flags().BoolVarP(&addAgentList, "list", "l", false, "List all agents and their install status")
	addAgentCmd.ValidArgsFunction = completeAddAgent
	rootCmd.AddCommand(addAgentCmd)
}

// completeAddAgent returns catalog roles not yet installed in the project.
func completeAddAgent(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	installed := map[string]bool{}
	if wd, err := os.Getwd(); err == nil {
		if cfgPath, err := config.Discover(wd); err == nil {
			if p, err := config.Load(cfgPath); err == nil {
				for _, r := range p.Roles {
					installed[r] = true
				}
			}
		}
	}

	var candidates []string
	for _, spec := range selectorOrder {
		if !installed[spec.name] {
			candidates = append(candidates, spec.name+"\t"+spec.desc)
		}
	}
	return candidates, cobra.ShellCompDirectiveNoFileComp
}

func runAddAgent(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	if addAgentList {
		return runAddAgentList(cmd)
	}

	if len(args) == 0 {
		return fmt.Errorf("agent name required. Use --list to see available agents")
	}

	roleName := args[0]
	runner := newAddAgentRunner()

	if !roles.IsValidRoleName(roleName) {
		return fmt.Errorf("unknown agent %q. Known agents: %s. Numbered families also accepted: eng1..N, qa1..N", roleName, knownRoleNames())
	}

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	cfgPath, err := config.Discover(wd)
	if err != nil {
		return fmt.Errorf("no initech.yaml found. Run 'initech init' first")
	}
	p, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	for _, r := range p.Roles {
		if r == roleName {
			return fmt.Errorf("agent %q already exists in this project", roleName)
		}
	}

	// Scaffold with the full role list (existing + new) so root-level files
	// like CLAUDE.md are rendered with all roles if they happen to be missing.
	// Root-level files are idempotent and skipped when they already exist.
	singleRole := *p
	singleRole.Roles = append(append([]string{}, p.Roles...), roleName)
	progress := func(msg string) {
		if strings.HasPrefix(msg, "Creating ") {
			path := strings.TrimPrefix(msg, "Creating ")
			fmt.Fprintf(out, "  %s %s\n", color.Green("✓"), color.Blue(path))
		} else {
			fmt.Fprintf(out, "  %s\n", msg)
		}
	}
	if _, err := scaffold.Run(&singleRole, scaffold.Options{Force: false, Progress: progress}); err != nil {
		return fmt.Errorf("scaffold: %w", err)
	}

	// Roles with NeedsSrc require a git submodule clone.
	def := roles.LookupRole(roleName)
	if def.NeedsSrc && len(p.Repos) > 0 {
		repoURL := p.Repos[0].URL
		if ov, ok := p.RoleOverrides[roleName]; ok && ov.RepoName != "" {
			for _, r := range p.Repos {
				if r.Name == ov.RepoName {
					repoURL = r.URL
					break
				}
			}
		}
		subPath := filepath.Join(roleName, "src")
		gitRef := filepath.Join(p.Root, subPath, ".git")
		_, statErr := os.Stat(gitRef)
		if statErr != nil && !os.IsNotExist(statErr) {
			return fmt.Errorf("check %s: %w", gitRef, statErr)
		}
		if os.IsNotExist(statErr) {
			// Record whether role/src pre-existed so we only clean up
			// artifacts we created, not pre-existing user data.
			_, srcExisted := os.Stat(filepath.Join(p.Root, subPath))
			if err := gitAddSubmodule(runner, p.Root, repoURL, subPath); err != nil {
				if srcExisted != nil {
					git.CleanFailedSubmodule(runner, p.Root, subPath)
				}
				if git.IsEmptyRepoError(err) {
					fmt.Fprintf(out, "  %s %s has no commits — push an initial commit then re-run\n",
						color.Yellow("!"), color.Yellow(roleName+" repo"))
				} else {
					return fmt.Errorf("add submodule for %s: %w", roleName, err)
				}
			} else {
				fmt.Fprintf(out, "  %s %s\n", color.Green("✓"), color.Dim("src/ submodule"))
			}
		}
	}

	p.Roles = append(p.Roles, roleName)
	if err := config.Write(cfgPath, p); err != nil {
		return fmt.Errorf("update config: %w", err)
	}
	fmt.Fprintf(out, "  %s Updated %s\n", color.Green("✓"), color.Bold("initech.yaml"))

	if tryHotAdd(out, roleName) {
		fmt.Fprintf(out, "\n%s added and activated.\n", color.CyanBold(roleName))
	} else {
		fmt.Fprintf(out, "\n%s added. Restart initech to activate.\n", color.CyanBold(roleName))
	}
	return nil
}

// tryHotAdd attempts to add the agent to a running TUI session via IPC.
// Returns true if the agent was activated, false if no session or add failed.
func tryHotAdd(out io.Writer, roleName string) bool {
	sockPath, _, err := discoverSocket()
	if err != nil {
		return false
	}
	resp, err := ipcCallSocket(sockPath, tui.IPCRequest{
		Action: "add",
		Target: roleName,
	})
	if err != nil {
		fmt.Fprintf(out, "  %s Could not hot-add to running session: %v\n", color.Yellow("!"), err)
		return false
	}
	if !resp.OK {
		fmt.Fprintf(out, "  %s Hot-add warning: %s\n", color.Yellow("!"), resp.Error)
		return false
	}
	fmt.Fprintf(out, "  %s %s\n", color.Green("✓"), color.Dim("hot-added to running session"))
	return true
}

// runAddAgentList prints all catalog roles with install status for the current project.
func runAddAgentList(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()

	// Load project config if available; missing config is not an error here.
	installed := map[string]bool{}
	wd, err := os.Getwd()
	if err == nil {
		if cfgPath, err := config.Discover(wd); err == nil {
			if p, err := config.Load(cfgPath); err == nil {
				for _, r := range p.Roles {
					installed[r] = true
				}
			}
		}
	}

	var lastGroup string
	for _, spec := range selectorOrder {
		if spec.group != lastGroup {
			fmt.Fprintf(out, "\n%s\n", color.Dim(spec.group))
			lastGroup = spec.group
		}
		if installed[spec.name] {
			fmt.Fprintf(out, "  %s %-10s %s\n", color.Green("✓"), spec.name, color.Dim(spec.desc))
		} else {
			fmt.Fprintf(out, "  %s %-10s %s\n", color.Dim("-"), spec.name, color.Dim(spec.desc))
		}
	}
	fmt.Fprintln(out)
	return nil
}

// knownRoleNames returns a human-friendly list of all catalog role names,
// in selectorOrder sequence, for use in error messages.
func knownRoleNames() string {
	names := make([]string, len(selectorOrder))
	for i, spec := range selectorOrder {
		names[i] = spec.name
	}
	return strings.Join(names, ", ")
}
