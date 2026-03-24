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

var startBead string

var startCmd = &cobra.Command{
	Use:   "start <role> [role...]",
	Short: "Bring stopped agents back into the session",
	Long: `Creates new tmux windows for the specified agent(s) and starts Claude
with the appropriate permission level. Use --bead to dispatch a bead after startup.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runStart,
}

func init() {
	startCmd.Flags().StringVar(&startBead, "bead", "", "Bead ID to dispatch after agent starts")
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf("session '%s' is not running. Use 'initech up' first", p.Name)
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
		// Create new window, wait for shell to be ready, then send the
		// startup command via send-keys. We don't pass the command to
		// new-window directly because tmux closes the window when that
		// command exits (e.g., if claude fails to start).
		if err := tmux.NewWindow(runner, p.Name, roleName); err != nil {
			fmt.Fprintf(out, "Warning: could not start %s: %v\n", roleName, err)
			continue
		}

		// Brief pause for the shell to initialize in the new window
		time.Sleep(500 * time.Millisecond)

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
			fmt.Fprintf(out, "Warning: could not send startup command to %s: %v\n", roleName, err)
			continue
		}

		fmt.Fprintf(out, "Started %s in session '%s'.\n", roleName, p.Name)

		// Dispatch bead if requested
		if startBead != "" {
			time.Sleep(5 * time.Second)
			dispatch := fmt.Sprintf("[from initech] Resume %s.", startBead)
			if err := tmux.SendKeys(runner, target, dispatch); err != nil {
				fmt.Fprintf(out, "Warning: could not dispatch bead to %s: %v\n", roleName, err)
			} else {
				fmt.Fprintf(out, "Dispatched: \"%s\"\n", dispatch)
			}
		}
	}

	return nil
}
