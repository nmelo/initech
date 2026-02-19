package cmd

import (
	"fmt"
	"os"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/tmux"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop <role> [role...]",
	Short: "Stop individual agents to free memory",
	Long:  `Kills the tmux window for the specified agent(s). The session stays running.`,
	Args:  cobra.MinimumNArgs(1),
	RunE:  runStop,
}

func init() {
	rootCmd.AddCommand(stopCmd)
}

func runStop(cmd *cobra.Command, args []string) error {
	runner := &iexec.DefaultRunner{}
	out := cmd.OutOrStdout()

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	cfgPath, err := config.Discover(wd)
	if err != nil {
		return fmt.Errorf("no initech.yaml found")
	}

	p, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	if !tmux.SessionExists(runner, p.Name) {
		return fmt.Errorf("session '%s' is not running", p.Name)
	}

	// Validate all roles exist in config
	roleSet := make(map[string]bool, len(p.Roles))
	for _, r := range p.Roles {
		roleSet[r] = true
	}
	for _, roleName := range args {
		if !roleSet[roleName] {
			return fmt.Errorf("role %q is not in this project's config", roleName)
		}
	}

	for _, roleName := range args {
		err := tmux.KillWindow(runner, p.Name, roleName)
		if err != nil {
			fmt.Fprintf(out, "Warning: could not stop %s: %v\n", roleName, err)
			continue
		}
		fmt.Fprintf(out, "Stopped %s in session '%s'.\n", roleName, p.Name)
	}

	return nil
}
