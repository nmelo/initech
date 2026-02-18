package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/nmelo/initech/internal/config"
	"github.com/spf13/cobra"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start the tmux session with all agents",
	Long: `Starts the tmuxinator session defined by the project's initech.yaml.
Loads the config, verifies the tmuxinator YAML exists, and runs tmuxinator start.`,
	RunE: runUp,
}

func init() {
	rootCmd.AddCommand(upCmd)
}

func runUp(cmd *cobra.Command, args []string) error {
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
		return err
	}

	// Verify tmuxinator config exists
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}
	tmuxYAML := filepath.Join(home, ".config", "tmuxinator", p.Name+".yml")
	if _, err := os.Stat(tmuxYAML); os.IsNotExist(err) {
		return fmt.Errorf("tmuxinator config not found at %s. Run 'initech init' first", tmuxYAML)
	}

	// tmuxinator needs the real terminal (stdin/stdout/stderr) to create
	// tmux sessions. Using exec.Runner's CombinedOutput() would capture
	// the terminal and cause "not a terminal" errors.
	tmux := exec.Command("tmuxinator", "start", p.Name)
	tmux.Stdin = os.Stdin
	tmux.Stdout = os.Stdout
	tmux.Stderr = os.Stderr

	if err := tmux.Run(); err != nil {
		return fmt.Errorf("tmuxinator start: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Session '%s' started with %d agents.\n", p.Name, len(p.Roles))
	return nil
}
