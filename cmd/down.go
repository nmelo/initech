package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/tmux"
	"github.com/spf13/cobra"
)

var downForce bool

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop the tmux session",
	Long: `Stops the tmuxinator session. Warns about uncommitted changes in agent
source directories before stopping. Use --force to bypass warnings.`,
	RunE: runDown,
}

func init() {
	downCmd.Flags().BoolVar(&downForce, "force", false, "Stop even with uncommitted changes")
	rootCmd.AddCommand(downCmd)
}

func runDown(cmd *cobra.Command, args []string) error {
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
		fmt.Fprintf(out, "Session '%s' is not running.\n", p.Name)
		return nil
	}

	// Check for uncommitted changes in src/ dirs
	var warnings []string
	for _, roleName := range p.Roles {
		def := roles.LookupRole(roleName)
		if !def.NeedsSrc {
			continue
		}
		srcDir := filepath.Join(p.Root, roleName, "src")
		if _, err := os.Stat(srcDir); err != nil {
			continue
		}
		status, err := runner.RunInDir(srcDir, "git", "status", "--porcelain")
		if err != nil {
			continue
		}
		if strings.TrimSpace(status) != "" {
			warnings = append(warnings, roleName)
		}
	}

	if len(warnings) > 0 && !downForce {
		for _, w := range warnings {
			fmt.Fprintf(out, "WARNING: %s has uncommitted changes in src/\n", w)
		}
		fmt.Fprintln(out, "Use --force to stop anyway, or 'initech status' to review.")
		return fmt.Errorf("uncommitted changes detected, use --force to override")
	}

	// Stop main session
	tmuxStop := exec.Command("tmuxinator", "stop", p.Name)
	tmuxStop.Stdin = os.Stdin
	tmuxStop.Stdout = os.Stdout
	tmuxStop.Stderr = os.Stderr
	if err := tmuxStop.Run(); err != nil {
		return fmt.Errorf("tmuxinator stop: %w", err)
	}

	// Stop grid session if running
	gridName := p.Name + "-grid"
	if tmux.SessionExists(runner, gridName) {
		gridStop := exec.Command("tmuxinator", "stop", gridName)
		gridStop.Stdin = os.Stdin
		gridStop.Stdout = os.Stdout
		gridStop.Stderr = os.Stderr
		gridStop.Run() // best effort
	}

	fmt.Fprintf(out, "Session '%s' stopped.\n", p.Name)
	return nil
}
