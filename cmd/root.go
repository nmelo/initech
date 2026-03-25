// Package cmd implements the initech CLI commands using Cobra.
// Each subcommand lives in its own file. Root handles global flags and version.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version is set at build time via ldflags.
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "initech",
	Short: "Bootstrap and manage multi-agent development projects",
	Long: `Initech captures local software development patterns into a reproducible,
bootstrappable system. It manages tmux sessions where each window is an
autonomous Claude Code agent with a defined role.

Running initech with no subcommand launches the TUI.`,
	RunE: runTUI,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the initech version",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("initech %s\n", Version)
		return nil
	},
}
