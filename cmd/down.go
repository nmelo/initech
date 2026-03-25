package cmd

import (
	"fmt"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop the TUI session",
	Long:  `Sends a quit signal to the running TUI via IPC. The TUI shuts down and all agent panes are closed.`,
	RunE:  runDown,
}

func init() {
	rootCmd.AddCommand(downCmd)
}

func runDown(cmd *cobra.Command, args []string) error {
	sockPath, p, err := discoverSocket()
	if err != nil {
		return err
	}

	resp, err := ipcCallSocket(sockPath, tui.IPCRequest{Action: "quit"})
	if err != nil {
		return fmt.Errorf("send quit: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Session '%s' stopped.\n", p.Name)
	return nil
}
