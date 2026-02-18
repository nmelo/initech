package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
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

	runner := &iexec.DefaultRunner{}
	_, err = runner.Run("tmuxinator", "start", p.Name)
	if err != nil {
		return fmt.Errorf("tmuxinator start: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Session '%s' started with %d agents.\n", p.Name, len(p.Roles))
	return nil
}
