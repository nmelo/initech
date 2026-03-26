package cmd

import (
	"fmt"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an agent from the running TUI session",
	Long: `Kills the named agent's process and removes its pane from the TUI.
The workspace directory is not deleted. initech.yaml is not modified.`,
	Args: cobra.ExactArgs(1),
	RunE: runRemove,
}

func init() {
	rootCmd.AddCommand(removeCmd)
}

func runRemove(cmd *cobra.Command, args []string) error {
	name := args[0]

	sockPath, _, err := discoverSocket()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	resp, err := ipcCallSocket(sockPath, tui.IPCRequest{
		Action: "remove",
		Target: name,
	})
	if err != nil {
		return fmt.Errorf("could not remove %s: %w", name, err)
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	fmt.Fprintf(out, "Removed %s.\n", name)
	return nil
}
