package cmd

import "github.com/spf13/cobra"

// configCmd is the parent command for config subcommands.
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "View and manage initech configuration",
}

func init() {
	rootCmd.AddCommand(configCmd)
}
