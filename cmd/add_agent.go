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

var addAgentCmd = &cobra.Command{
	Use:   "add-agent <name>",
	Short: "Add a new agent workspace to the project",
	Long: `Scaffolds a new agent workspace directory and registers it in initech.yaml.

The agent name must be a known role supported by initech. The role must not
already exist in the project.

Known roles: super, pm, pmm, arch, eng1, eng2, eng3, qa1, qa2, shipper, sec,
ops, writer, growth.

Restart initech (or run 'initech' in a new session) to activate the new agent.`,
	Args: cobra.ExactArgs(1),
	RunE: runAddAgent,
}

func init() {
	rootCmd.AddCommand(addAgentCmd)
}

func runAddAgent(cmd *cobra.Command, args []string) error {
	roleName := args[0]
	runner := newAddAgentRunner()
	out := cmd.OutOrStdout()

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

// knownRoleNames returns a human-friendly list of all catalog role names,
// in selectorOrder sequence, for use in error messages.
func knownRoleNames() string {
	names := make([]string, len(selectorOrder))
	for i, spec := range selectorOrder {
		names[i] = spec.name
	}
	return strings.Join(names, ", ")
}
