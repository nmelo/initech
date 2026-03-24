package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/tmux"
	"github.com/spf13/cobra"
)

var restartBead string

var restartCmd = &cobra.Command{
	Use:   "restart <role>",
	Short: "Kill and restart a specific agent",
	Long: `Kills the agent's tmux window and creates a new one with Claude running.
Uses --continue to resume prior conversation state when available.
Use --bead to dispatch a bead after the agent restarts.`,
	Args: cobra.ExactArgs(1),
	RunE: runRestart,
}

func init() {
	restartCmd.Flags().StringVar(&restartBead, "bead", "", "Bead ID to dispatch after restart")
	rootCmd.AddCommand(restartCmd)
}

func runRestart(cmd *cobra.Command, args []string) error {
	runner := &iexec.DefaultRunner{}
	out := cmd.OutOrStdout()
	roleName := args[0]

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

	// Validate role exists in config
	found := false
	for _, r := range p.Roles {
		if r == roleName {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("role %q is not in this project's config", roleName)
	}

	if !tmux.SessionExists(runner, p.Name) {
		return fmt.Errorf("session '%s' is not running", p.Name)
	}

	// Kill existing window (ignore error if window doesn't exist)
	tmux.KillWindow(runner, p.Name, roleName)

	// Create new window
	if err := tmux.NewWindow(runner, p.Name, roleName); err != nil {
		return fmt.Errorf("create window: %w", err)
	}

	// Wait for shell to initialize
	time.Sleep(500 * time.Millisecond)

	// Build startup command with --continue fallback
	def := roles.LookupRole(roleName)
	startupCmd := fmt.Sprintf("cd %s/%s && (claude --continue", p.Root, roleName)
	if def.Permission == roles.Autonomous {
		startupCmd += " --dangerously-skip-permissions"
	}
	startupCmd += " || claude"
	if def.Permission == roles.Autonomous {
		startupCmd += " --dangerously-skip-permissions"
	}
	startupCmd += ")"

	target := p.Name + ":" + roleName
	if err := tmux.SendKeys(runner, target, startupCmd); err != nil {
		return fmt.Errorf("send startup command: %w", err)
	}

	fmt.Fprintf(out, "Restarted %s in session '%s'.\n", roleName, p.Name)

	// Dispatch bead if requested
	if restartBead != "" {
		time.Sleep(5 * time.Second)
		dispatch := fmt.Sprintf("[from initech] Restarted. Resume %s.", restartBead)
		if err := tmux.SendKeys(runner, target, dispatch); err != nil {
			fmt.Fprintf(out, "Warning: could not dispatch bead to %s: %v\n", roleName, err)
		} else {
			fmt.Fprintf(out, "Dispatched: \"%s\"\n", dispatch)
		}
	}

	return nil
}
