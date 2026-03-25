package cmd

import (
	"fmt"
	"time"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var restartBead string

var restartCmd = &cobra.Command{
	Use:   "restart <role>",
	Short: "Kill and restart a specific agent",
	Long: `Kills and respawns the specified agent's process via IPC.
Use --bead to dispatch a bead after the agent restarts.`,
	Args: cobra.ExactArgs(1),
	RunE: runRestart,
}

func init() {
	restartCmd.Flags().StringVar(&restartBead, "bead", "", "Bead ID to dispatch after restart")
	rootCmd.AddCommand(restartCmd)
}

func runRestart(cmd *cobra.Command, args []string) error {
	sockPath, _, err := discoverSocket()
	if err != nil {
		return err
	}

	roleName := args[0]
	out := cmd.OutOrStdout()

	resp, err := ipcCallSocket(sockPath, tui.IPCRequest{
		Action: "restart",
		Target: roleName,
	})
	if err != nil {
		return fmt.Errorf("restart %s: %w", roleName, err)
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	fmt.Fprintf(out, "Restarted %s.\n", roleName)

	// Dispatch bead if requested.
	if restartBead != "" {
		time.Sleep(3 * time.Second)
		dispatch := fmt.Sprintf("[from initech] Restarted. Resume %s.", restartBead)
		dresp, derr := ipcCallSocket(sockPath, tui.IPCRequest{
			Action: "send",
			Target: roleName,
			Text:   dispatch,
			Enter:  true,
		})
		if derr != nil {
			fmt.Fprintf(out, "Warning: could not dispatch bead to %s: %v\n", roleName, derr)
		} else if !dresp.OK {
			fmt.Fprintf(out, "Warning: dispatch to %s: %s\n", roleName, dresp.Error)
		} else {
			fmt.Fprintf(out, "Dispatched: \"%s\"\n", dispatch)
		}
	}

	return nil
}
