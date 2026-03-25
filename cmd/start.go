package cmd

import (
	"fmt"
	"time"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var startBead string

var startCmd = &cobra.Command{
	Use:   "start <role> [role...]",
	Short: "Bring stopped agents back into the session",
	Long: `Respawns the process for the specified agent(s) via IPC.
Use --bead to dispatch a bead after the agent restarts.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runStart,
}

func init() {
	startCmd.Flags().StringVar(&startBead, "bead", "", "Bead ID to dispatch after agent starts")
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	sockPath, _, err := discoverSocket()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	for _, roleName := range args {
		resp, err := ipcCallSocket(sockPath, tui.IPCRequest{
			Action: "start",
			Target: roleName,
		})
		if err != nil {
			fmt.Fprintf(out, "Warning: could not start %s: %v\n", roleName, err)
			continue
		}
		if !resp.OK {
			fmt.Fprintf(out, "Warning: %s: %s\n", roleName, resp.Error)
			continue
		}
		if resp.Data == "already running" {
			fmt.Fprintf(out, "%s is already running.\n", roleName)
		} else {
			fmt.Fprintf(out, "Started %s.\n", roleName)
		}

		// Dispatch bead if requested.
		if startBead != "" {
			time.Sleep(3 * time.Second)
			dispatch := fmt.Sprintf("[from initech] Resume %s.", startBead)
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
	}
	return nil
}
