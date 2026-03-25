package cmd

import (
	"fmt"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop <role> [role...]",
	Short: "Stop individual agents to free memory",
	Long:  `Kills the process for the specified agent(s) via IPC. The pane stays in the roster for later restart.`,
	Args:  cobra.MinimumNArgs(1),
	RunE:  runStop,
}

func init() {
	rootCmd.AddCommand(stopCmd)
}

func runStop(cmd *cobra.Command, args []string) error {
	sockPath, _, err := discoverSocket()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	for _, roleName := range args {
		resp, err := ipcCallSocket(sockPath, tui.IPCRequest{
			Action: "stop",
			Target: roleName,
		})
		if err != nil {
			fmt.Fprintf(out, "Warning: could not stop %s: %v\n", roleName, err)
			continue
		}
		if !resp.OK {
			fmt.Fprintf(out, "Warning: %s: %s\n", roleName, resp.Error)
			continue
		}
		if resp.Data == "already stopped" {
			fmt.Fprintf(out, "%s is already stopped.\n", roleName)
		} else {
			fmt.Fprintf(out, "Stopped %s.\n", roleName)
		}
	}
	return nil
}
