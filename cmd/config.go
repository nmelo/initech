package cmd

import "github.com/spf13/cobra"

// configCmd is the parent command for config subcommands.
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "View and manage initech configuration",
	Long:  `Subcommands for viewing, validating, and understanding initech.yaml.`,
}

func init() {
	rootCmd.AddCommand(configCmd)
}
