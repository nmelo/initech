package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/git"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/scaffold"
	"github.com/spf13/cobra"
)

var newAddAgentRunner = func() iexec.Runner { return &iexec.DefaultRunner{} }

var addAgentList bool

var addAgentCmd = &cobra.Command{
	Use:   "add-agent <name>",
	Short: "Add a new agent workspace to the project",
	Long: `Scaffolds a new agent workspace directory and registers it in initech.yaml.

The agent name must be a known role supported by initech. The role must not
already exist in the project.

Known roles: super, pm, pmm, arch, eng1, eng2, eng3, qa1, qa2, shipper, sec,
ops, writer, growth.

Restart initech (or run 'initech' in a new session) to activate the new agent.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAddAgent,
}

func init() {
	addAgentCmd.Flags().BoolVarP(&addAgentList, "list", "l", false, "List all agents and their install status")
	rootCmd.AddCommand(addAgentCmd)
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

	if _, ok := roles.Catalog[roleName]; !ok {
		return fmt.Errorf("unknown agent %q. Known agents: %s", roleName, knownRoleNames())
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

	// Scaffold only the new role. Root-level files (CLAUDE.md, docs/) are
	// idempotent and will be skipped since they already exist (Force: false).
	singleRole := *p
	singleRole.Roles = []string{roleName}
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
		if _, statErr := os.Stat(gitRef); statErr != nil {
			if err := gitAddSubmodule(runner, p.Root, repoURL, subPath); err != nil {
				if git.IsEmptyRepoError(err) {
					fmt.Fprintf(out, "  %s %s has no commits — push an initial commit then re-run\n",
						color.Yellow("!"), color.Yellow(roleName+" repo"))
				} else {
					git.CleanFailedSubmodule(runner, p.Root, subPath)
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
	fmt.Fprintf(out, "\n%s added. Restart initech to activate.\n", color.CyanBold(roleName))
	return nil
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
